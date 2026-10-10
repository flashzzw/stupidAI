#!/usr/bin/env python3
"""W01/W02 boundary experiments using real CLIs and synthetic local providers.

No production adapter capabilities are changed. Each invocation owns fresh
profiles, terminals and processes. A passing probe proves only its assertions.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import stat
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
import uuid

from execution_config_probe import (
    CLAUDE, CODEX, Provider, environment, settings, stop, verify_versions, wait_for, write,
)
from console_fixture import ConsoleFixture
from go_cli_smoke import Proxy


class Terminal:
    def __init__(self, argv, env, cwd, path):
        self.path = path
        self.output = bytearray()
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
        self.proc = subprocess.Popen(argv, env=dict(env, TERM="xterm-256color"), cwd=cwd,
                                     stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)

    def drain(self, seconds=.15):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if not select.select([self.master], [], [], .05)[0]:
                continue
            try:
                data = os.read(self.master, 65536)
            except OSError:
                break
            if not data:
                break
            self.output.extend(data)
            # Answer terminal capability queries, without interpreting them as
            # user input or including synthetic answers in the output evidence.
            if b"\x1b[6n" in data:
                os.write(self.master, b"\x1b[1;1R")
            if b"\x1b[c" in data:
                os.write(self.master, b"\x1b[?1;2c")
        write(self.path, self.output.decode(errors="replace"))
        return self.text()

    def text(self):
        return re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", self.output.decode(errors="replace"))

    def send(self, text):
        os.write(self.master, text.encode())
        self.drain()

    def until(self, check, label, timeout=25):
        def poll():
            self.drain()
            return check()
        return wait_for(poll, label, timeout=timeout)

    def close(self):
        stop(self.proc)
        self.drain()
        os.close(self.master)


def is_primary(payload, route, turn=1):
    marker = f"NETCAP_{route}_TURN_{turn}"
    for message in payload.get("messages", []):
        if message.get("role") != "user":
            continue
        content = message.get("content")
        if content == marker or (isinstance(content, list) and any(
                p.get("text") == marker for p in content if isinstance(p, dict))):
            return True
    return False


class HeldProvider(Provider):
    def __init__(self, root, route):
        self.arrived = threading.Event()
        self.release = threading.Event()
        super().__init__(root, route)

    def failure_response(self, path, payload):
        if is_primary(payload, self.route) and not self.arrived.is_set():
            self.arrived.set()
            assert self.release.wait(timeout=100), "fixture release timeout"
        return None


class ClaudeProfile:
    def __init__(self, root, route, held=False, cwd=None, provider_factory=None):
        self.root = root
        root.mkdir(parents=True)
        self.env = environment(root)
        self.cwd = cwd or root / "work"
        self.cwd.mkdir(exist_ok=True)
        self.provider = (provider_factory or (HeldProvider if held else Provider))(root, route)
        self.config = settings(root, self.provider)
        helper = root / "key.py"
        write(helper, f"print('fixture-{route}')\n")
        data = json.loads(self.config.read_text())
        data["env"].pop("ANTHROPIC_API_KEY")
        data["apiKeyHelper"] = shlex.join([sys.executable, str(helper)])
        write(self.config, data)
        write(Path(self.env["CLAUDE_CONFIG_DIR"]) / "settings.json", data)
        self.terminals = []
        self.cleaned = False

    def cli(self, argv, tag, env=None):
        r = subprocess.run([CLAUDE, *argv], env=env or self.env, cwd=self.cwd,
                           capture_output=True, text=True, timeout=35)
        write(self.root / (tag + ".stdout"), r.stdout)
        write(self.root / (tag + ".stderr"), r.stderr)
        assert r.returncode == 0, tag + " exit " + str(r.returncode)
        return r.stdout

    def agents(self, tag):
        return json.loads(self.cli(["agents", "--json"], tag))

    def attach(self, sid, tag):
        terminal = Terminal([CLAUDE, "attach", sid], self.env, self.cwd, self.root / (tag + ".ansi"))
        self.terminals.append(terminal)
        terminal.drain(1)
        return terminal

    def close(self):
        if isinstance(self.provider, HeldProvider):
            self.provider.release.set()
        for terminal in self.terminals:
            terminal.close()
        assert Path(self.env["CLAUDE_CONFIG_DIR"]).is_relative_to(self.root)
        try:
            self.cli(["daemon", "stop", "--any"], "cleanup")
            self.cleaned = True
        finally:
            self.provider.close()


def claude_supervisors(root):
    profiles = []
    result = {"checks": {}, "scope": "two isolated native supervisors; serial TTY detach/reattach and stopped-session recovery"}
    checks = result["checks"]
    try:
        for route in ("A", "B"):
            profiles.append(ClaudeProfile(root / route, route, held=True))
        for p in profiles:
            p.cli(["--bg", f"NETCAP_{p.provider.route}_TURN_1"], "launch")
        for p in profiles:
            wait_for(p.provider.arrived.is_set, "held main request " + p.provider.route)
        checks["both_main_requests_in_flight"] = all(p.provider.arrived.is_set() and not p.provider.release.is_set() for p in profiles)
        jobs = [p.agents("before")[0] for p in profiles]
        checks["distinct_workers_and_jobs"] = jobs[0]["id"] != jobs[1]["id"] and jobs[0]["pid"] != jobs[1]["pid"]
        checks["rosters_do_not_mix"] = all([j["id"] for j in p.agents("roster")] == [job["id"]] for p, job in zip(profiles, jobs))
        for p, job in zip(profiles, jobs):
            terminal = p.attach(job["id"], "attach")
            terminal.until(lambda: f"NETCAP_{p.provider.route}_TURN_1" in terminal.text(), "original prompt rendered")
            checks[p.provider.route + "_same_worker_attached"] = p.agents("attached")[0]["pid"] == job["pid"]
            terminal.send("\x1a")
            terminal.until(lambda: terminal.proc.poll() is not None, "detach completes")
            again = p.attach(job["id"], "reattach")
            again.until(lambda: f"NETCAP_{p.provider.route}_TURN_1" in again.text(), "reattached original prompt")
            checks[p.provider.route + "_same_worker_reattached"] = p.agents("reattached")[0]["pid"] == job["pid"]
            p.provider.release.set()
            again.until(lambda: "fixture" in again.text() and f"NETCAP_{p.provider.route}_TURN_1" in again.text(), "original reply rendered")
            wait_for(lambda: p.agents("settled")[0].get("status") == "idle", "background session settles")
            again.send("\x1a")
            again.until(lambda: again.proc.poll() is not None, "second detach completes")
        a, b = profiles
        a.cli(["daemon", "stop", "--any"], "stop-A")
        checks["stop_A_preserves_B_worker"] = b.agents("B-after-A-stop")[0]["pid"] == jobs[1]["pid"]
        a.cli(["--bg", "--resume", jobs[0]["sessionId"], "NETCAP_A_TURN_2"], "resume-A")
        wait_for(lambda: any(is_primary(r["body"], "A", 2) for r in a.provider.requests), "resumed A request", timeout=35)
        resumed = a.agents("A-resumed")[0]
        checks["A_resumes_same_id_new_worker"] = resumed["sessionId"] == jobs[0]["sessionId"] and resumed["pid"] != jobs[0]["pid"]
        resumed_requests = [r for r in a.provider.requests if is_primary(r["body"], "A", 2)]
        checks["A_history_preserved"] = is_primary(resumed_requests[-1]["body"], "A", 1)
        checks["all_requests_use_bound_credentials"] = all(p.provider.requests and all(r["auth_matches"] for r in p.provider.requests) for p in profiles)
        checks["no_cross_profile_markers"] = all(f"NETCAP_{'B' if p.provider.route == 'A' else 'A'}_TURN_" not in json.dumps(p.provider.requests) for p in profiles)
        result["jobs"] = [{k: j.get(k) for k in ("id", "sessionId", "pid", "status", "state")} for j in jobs]
        result["resumed_job"] = {k: resumed.get(k) for k in ("id", "sessionId", "pid", "status", "state")}
        result["request_counts"] = {p.provider.route: len(p.provider.requests) for p in profiles}
        return result
    finally:
        for p in profiles:
            p.close()
        checks["owned_supervisors_stopped"] = all(p.cleaned for p in profiles)
        write(root / "observations.json", result)


def same_id_namespaces(root):
    root.mkdir(parents=True)
    cwd = root / "shared-project"
    cwd.mkdir()
    profiles = []
    sid = str(uuid.uuid4())
    result = {"native_id": sid, "checks": {}, "scope": "Claude same native ID and cwd in two state namespaces; separate HOME, local providers; no workspace writes"}
    checks = result["checks"]
    try:
        for route in ("A", "B"):
            profiles.append(ClaudeProfile(root / route, route, cwd=cwd))

        def turn(p, number, env=None):
            args = ["-p", f"NETCAP_{p.provider.route}_TURN_{number}", "--output-format", "json", "--tools", ""]
            args += ["--session-id", sid] if number == 1 else ["--resume", sid]
            return json.loads(p.cli(args, "turn-" + str(number), env))

        with ThreadPoolExecutor(max_workers=2) as pool:
            first = list(pool.map(lambda p: turn(p, 1), profiles))
        checks["same_id_created_in_both_namespaces"] = all(r.get("session_id") == sid and not r.get("is_error") for r in first)
        alias = root / "A-state-alias"
        alias.symlink_to(profiles[0].env["CLAUDE_CONFIG_DIR"], target_is_directory=True)
        alias_env = dict(profiles[0].env, CLAUDE_CONFIG_DIR=str(alias))
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(turn, profiles[0], 2, alias_env), pool.submit(turn, profiles[1], 2)]
            resumed = [f.result() for f in futures]
        checks["both_resume_original_id"] = all(r.get("session_id") == sid and not r.get("is_error") for r in resumed)
        checks["alias_is_same_state_directory"] = alias.resolve() == Path(profiles[0].env["CLAUDE_CONFIG_DIR"]).resolve()
        checks["own_history_preserved"] = all(is_primary(p.provider.requests[-1]["body"], p.provider.route) for p in profiles)
        checks["no_cross_profile_history"] = all(f"NETCAP_{'B' if p.provider.route == 'A' else 'A'}_TURN_" not in json.dumps(p.provider.requests) for p in profiles)
        checks["bound_credentials"] = all(p.provider.requests and all(r["auth_matches"] for r in p.provider.requests) for p in profiles)
        result["identities"] = [{"state_dir": p.env["CLAUDE_CONFIG_DIR"], "cwd": str(p.cwd), "native_id": sid} for p in profiles]
        return result
    finally:
        for p in profiles:
            p.close()
        write(root / "observations.json", result)


def codex_tui(root):
    root.mkdir(parents=True)
    fixture = ConsoleFixture("")
    terminal = resumed = client = None
    checks = {}
    result = {"checks": checks, "fixture": str(fixture.root), "scope": "real TUI on pre-shared backend; serial proxy/TUI handoff; draft visibility; no enforced platform arbitration"}
    try:
        fixture.provider.stream_delay = 3
        fixture.start_native()
        terminal = Terminal([CODEX, "--remote", "unix://" + str(fixture.sock), "--no-alt-screen", "NETCAP_A_TURN_1"],
                            fixture.env, fixture.cwd, root / "tui.ansi")
        terminal.until(lambda: bool(fixture.provider.requests), "TUI main request")
        client = Proxy(CODEX, fixture.sock, fixture.env)
        loaded = client.call("thread/loaded/list", {})["data"]
        candidates = []
        for tid in loaded:
            try:
                candidates.append(client.call("thread/read", {"threadId": tid, "includeTurns": True})["thread"])
            except RuntimeError as error:
                if "ephemeral threads do not support includeTurns" not in str(error):
                    raise
                candidates.append({"id": tid, "history_unavailable": str(error)})
        write(root / "loaded-threads.json", candidates)
        matching = [t for t in candidates if "NETCAP_A_TURN_1" in json.dumps(t)]
        assert len(matching) == 1, "expected one thread containing the fixture prompt"
        sid = matching[0]["id"]
        def primary_requests():
            return [r for r in fixture.provider.requests if r["body"].get("client_metadata", {}).get("thread_id") == sid]
        live = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
        checks["observed_TUI_turn_in_flight"] = any(t["status"] == "inProgress" for t in live["turns"])

        def completed(count):
            terminal.drain()
            history = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
            return history if len(history["turns"]) == count and history["turns"][-1]["status"] == "completed" else None

        wait_for(lambda: completed(1), "TUI first turn completed")
        terminal.send("UNSENT_DRAFT_W01")
        terminal.drain(1)
        draft_history = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
        checks["draft_visible_in_terminal"] = "UNSENT_DRAFT_W01" in terminal.text()
        checks["draft_not_in_native_history"] = "UNSENT_DRAFT_W01" not in json.dumps(draft_history)
        checks["draft_did_not_send_primary_request"] = len(primary_requests()) == 1
        terminal.close()
        terminal = None
        fixture.provider.stream_delay = 0
        client.call("thread/resume", {"threadId": sid})
        client.call("turn/start", {"threadId": sid, "input": [{"type": "text", "text": "NETCAP_A_TURN_2"}]})

        def read_completed(count):
            h = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
            return h if len(h["turns"]) == count and h["turns"][-1]["status"] == "completed" else None

        wait_for(lambda: read_completed(2), "proxy second turn")
        resumed = Terminal([CODEX, "resume", "--remote", "unix://" + str(fixture.sock), "--no-alt-screen", sid, "NETCAP_A_TURN_3"],
                           fixture.env, fixture.cwd, root / "resumed-tui.ansi")
        history = resumed.until(lambda: read_completed(3), "TUI returns to original thread", timeout=35)
        checks["same_thread_three_turns"] = history["id"] == sid and len(history["turns"]) == 3
        checks["exactly_three_primary_model_requests"] = len(primary_requests()) == 3
        checks["all_markers_in_original_history"] = all(f"NETCAP_A_TURN_{n}" in json.dumps(history) for n in (1, 2, 3))
        checks["bound_credentials"] = all(r["auth_matches"] for r in fixture.provider.requests)
        result["native_id"] = sid
        result["primary_request_count"] = len(primary_requests())
        result["auxiliary_request_count"] = len(fixture.provider.requests) - len(primary_requests())
        write(root / "history.json", history)
        return result
    finally:
        if terminal:
            terminal.close()
        if resumed:
            resumed.close()
        if client:
            client.close()
        fixture.close()
        write(root / "observations.json", result)


def codex_standalone(root):
    root.mkdir(parents=True)
    fixture = ConsoleFixture("")
    terminal = None
    result = {"checks": {}, "fixture": str(fixture.root),
              "scope": "explicit --no-daemon foreground; discovery observation only, no claim of impossibility",
              "live_adoption": "unverified: no shared control endpoint discovered in this fixture"}
    try:
        fixture.provider.stream_delay = 5
        terminal = Terminal([CODEX, "--no-daemon", "--no-alt-screen", "NETCAP_A_TURN_1"],
                            fixture.env, fixture.cwd, root / "standalone.ansi")
        trusted = False
        def started():
            nonlocal trusted
            if not trusted and "Trust and continue" in terminal.text():
                trusted = True
                terminal.send("\r")
            return bool(fixture.provider.requests)
        terminal.until(started, "standalone TUI request")
        requests = [r for r in fixture.provider.requests if "NETCAP_A_TURN_1" in json.dumps(r["body"])]
        sockets = [str(p) for p in fixture.root.rglob("*") if stat.S_ISSOCK(p.lstat().st_mode)]
        result["discovered_sockets"] = sockets
        result["native_id"] = requests[0]["body"].get("client_metadata", {}).get("session_id")
        result["checks"] = {"foreground_process_alive": terminal.proc.poll() is None,
                            "local_model_request_authenticated": bool(requests) and all(r["auth_matches"] for r in requests),
                            "native_identity_observed": bool(result["native_id"]),
                            "no_shared_socket_in_fixture": not sockets}
        return result
    finally:
        if terminal:
            terminal.close()
        fixture.close()
        write(root / "observations.json", result)


def claude_foreground(root, explicit_id=False):
    profile = ClaudeProfile(root, "A")
    sid = str(uuid.uuid4())
    result = {"checks": {}, "requested_native_id": sid if explicit_id else None,
              "scope": "independent foreground, completed turn, native /background conversion then TTY attach; no in-flight tools"}
    checks = result["checks"]
    try:
        argv = [CLAUDE, *(["--session-id", sid] if explicit_id else []), "NETCAP_A_TURN_1"]
        terminal = Terminal(argv, profile.env, profile.cwd, root / "foreground.ansi")
        profile.terminals.append(terminal)
        # Accept only known first-run prompts in this owned, empty workspace.
        handled = set()
        def started():
            text = re.sub(r"\s+", "", terminal.text())
            for key, phrase in [("theme", "Choosethetextstyle"), ("notes", "PressEntertocontinue"),
                                ("trust", "Yes,Itrustthisfolder")]:
                if key not in handled and phrase in text:
                    handled.add(key)
                    terminal.send("\x1b[B\r" if key == "trust" else "\r")
                    return False
            return any(is_primary(r["body"], "A") for r in profile.provider.requests)
        terminal.until(started, "foreground onboarding and main request", timeout=30)
        first = next(r for r in profile.provider.requests if is_primary(r["body"], "A"))
        sid = json.loads(first["body"]["metadata"]["user_id"])["session_id"]
        result["native_id"] = sid
        terminal.until(lambda: "fixtureA:NETCAP_A_TURN_1" in re.sub(r"\s+", "", terminal.text()), "foreground reply")
        terminal.drain(1)
        before = profile.agents("foreground-roster")
        result["foreground_roster"] = before
        checks["independent_foreground_has_no_background_worker"] = not any(j.get("kind") == "background" for j in before)
        foreground_pid = terminal.proc.pid
        terminal.send("UNSENT_DRAFT_W01")
        terminal.send("\x1b[D")
        terminal.drain(1)
        checks["unsent_draft_blocks_background_switch"] = not any(j.get("kind") == "background" for j in profile.agents("draft-roster"))
        checks["draft_not_sent_to_model"] = "UNSENT_DRAFT_W01" not in json.dumps(profile.provider.requests)
        # Move to the end after the left arrow, then clear the entire draft.
        terminal.send("\x05\x15")
        terminal.send("/background\r")
        def migrated():
            jobs = profile.agents("background-roster")
            return next((j for j in jobs if j.get("cwd") == str(profile.cwd) and j.get("kind") == "background" and j.get("pid")), None)
        job = terminal.until(migrated, "native foreground conversion", timeout=35)
        checks["conversion_uses_new_worker"] = job["pid"] != foreground_pid
        result["foreground_pid"] = foreground_pid
        result["background_job"] = {k: job.get(k) for k in ("id", "sessionId", "pid", "status", "state")}
        result["roster_identity_differs_from_original"] = job["sessionId"] != sid
        attached = profile.attach(job["id"], "converted-attach")
        attached.until(lambda: "NETCAP_A_TURN_1" in attached.text(), "converted history visible")
        attached.send("NETCAP_A_TURN_2\r")
        attached.until(lambda: any(is_primary(r["body"], "A", 2) for r in profile.provider.requests), "input after conversion", timeout=35)
        body = next(r["body"] for r in reversed(profile.provider.requests) if is_primary(r["body"], "A", 2))
        resumed_sid = json.loads(body["metadata"]["user_id"])["session_id"]
        result["followup_native_id"] = resumed_sid
        checks["same_native_id_after_conversion"] = resumed_sid == sid
        checks["original_history_in_followup"] = is_primary(body, "A", 1)
        checks["bound_credentials"] = all(r["auth_matches"] for r in profile.provider.requests)
        return result
    finally:
        profile.close()
        checks["owned_supervisor_stopped"] = profile.cleaned
        write(root / "observations.json", result)


def main():
    cases = {"codex-tui": codex_tui, "codex-standalone": codex_standalone, "claude-foreground": claude_foreground,
             "claude-foreground-explicit-id": lambda root: claude_foreground(root, explicit_id=True),
             "claude-supervisors": claude_supervisors, "same-id": same_id_namespaces}
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["all", *cases], default="all")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-boundary-"))
    print("Evidence directory:", root, flush=True)
    verify_versions(root, ["codex", "server"] if args.case.startswith("codex-") else ["claude"] if args.case != "all" else ["codex", "server", "claude"])
    results = {}
    for name, run in cases.items():
        if args.case not in (name, "all"):
            continue
        print("Running", name, flush=True)
        try:
            observed = run(root / name)
            checks = observed["checks"]
            results[name] = {"execution": "completed", "expectations_met": bool(checks) and all(checks.values()), "observations": observed}
        except Exception as error:
            results[name] = {"execution": "failed", "error": str(error)}
        write(root / "results.json", results)
        print(name, json.dumps(results[name], ensure_ascii=False), flush=True)
    return int(any(r["execution"] != "completed" or not r.get("expectations_met") for r in results.values()))


if __name__ == "__main__":
    raise SystemExit(main())
