#!/usr/bin/env python3
"""Fixed Codex Skill selection/read probes with owned stdio and a local provider.

--metadata-only sends no model input and does not start a listener. It verifies
accepted parameters and identity only, never effective selection or method use.
The full run captures actual model requests; the provider emits text only, so
neither mode claims that a Skill's method has been followed or its Gate passed.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import queue
import subprocess
import tempfile
import threading
import time

from execution_config_probe import SERVER, environment, verify_versions, write
from runtime.common import Provider, stop, wait_for


def selection_overrides(config, path, enabled):
    """Array overrides replace arrays: retain every unrelated native entry."""
    if not Path(path).is_absolute() or not isinstance(enabled, bool):
        raise ValueError("selection requires an absolute path and boolean state")
    entries = copy.deepcopy(config.get("skills", {}).get("config", []))
    if not isinstance(entries, list):
        raise ValueError("native skills.config is not a list")
    seen = set()
    for entry in entries:
        if (not isinstance(entry, dict) or not isinstance(entry.get("path"), str)
                or not Path(entry["path"]).is_absolute()
                or not isinstance(entry.get("enabled"), bool)
                or entry["path"] in seen):
            raise ValueError("native skills.config has an ambiguous entry")
        seen.add(entry["path"])
    entries = [entry for entry in entries if entry["path"] != str(path)]
    entries.append({"path": str(path), "enabled": enabled})
    return {"skills.config": entries}


class Stdio:
    def __init__(self, root, env, label):
        self.seq, self.calls, self.events = 0, [], []
        self.root, self.label = root, label
        self.messages = queue.Queue()
        self.log = (root / (label + ".stderr")).open("w")
        self.proc = subprocess.Popen([SERVER, "app-server", "--stdio"], cwd=root / "work", env=env,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.log,
                                     text=True, start_new_session=True)
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        try:
            result = self.call("initialize", {"clientInfo": {"name": "netcap-skill-probe", "version": "1"},
                                              "capabilities": {"experimentalApi": True}})
            assert result["codexHome"] == env["CODEX_HOME"]
            self.send({"method": "initialized"})
        except BaseException:
            self.close()
            raise

    def _read(self):
        try:
            for line in self.proc.stdout:
                self.messages.put(json.loads(line))
        except BaseException as error:
            self.messages.put(error)
        finally:
            self.messages.put(EOFError("owned app-server stdout closed"))

    def send(self, message):
        self.proc.stdin.write(json.dumps(message) + "\n")
        self.proc.stdin.flush()

    def call(self, method, params):
        self.seq += 1
        call = {"method": method, "params": params}
        self.calls.append(call)
        self.send({"id": self.seq, "method": method, "params": params})
        deadline = time.monotonic() + 20
        while True:
            message = self.messages.get(timeout=max(.01, deadline - time.monotonic()))
            if isinstance(message, BaseException):
                raise message
            if message.get("id") == self.seq and "method" not in message:
                if "error" in message:
                    call["error"] = message["error"]
                    raise RuntimeError(f"{method} rejected; see owned transcript")
                call["result"] = message["result"]
                return message["result"]
            self.events.append(message)

    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                stop(self.proc)
        self.reader.join(timeout=2)
        self.proc.stdout.close()
        self.log.close()
        write(self.root / (self.label + "-protocol.json"), {"calls": self.calls, "events": self.events,
              "exit_code": self.proc.returncode})


def run(root, metadata_only):
    verify_versions(root, ["server"])
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "fixture-A"
    cwd = root / "work"
    cwd.mkdir()
    shared = Path(env["HOME"]) / ".agents/skills/shared/SKILL.md"
    inherited = Path(env["HOME"]) / ".agents/skills/inherited-off/SKILL.md"
    project = cwd / ".agents/skills/project/SKILL.md"
    paths = {"shared": shared, "inherited": inherited, "project": project}
    for name, path in paths.items():
        write(path, f"---\nname: w0207-{name}\ndescription: W0207_{name.upper()}_DESCRIPTION\n---\nW0207_{name.upper()}_BODY\nUse the fixture method only when selected.\n")
    write(cwd / "AGENTS.md", "W0207_PROJECT_RULE: preserve the fixture's method boundaries.\n")
    provider, client = None, None
    checks, samples, sessions, observations = {}, {}, {}, {}
    report = {"root": str(root), "metadata_only": metadata_only, "checks": checks, "samples": samples,
              "sessions": sessions, "observations": observations,
              "scope": "owned native stdio; local text-only provider in full mode; no production Runtime selection or method Gate"}
    try:
        if not metadata_only:
            provider = Provider(root, "A")
        endpoint = provider.url + "/v1" if provider else "http://127.0.0.1:9/v1"
        config = Path(env["CODEX_HOME"]) / "config.toml"
        write(config, f'''model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[model_providers.fixture]
name="Owned fixture"
base_url={json.dumps(endpoint)}
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=false
shell_snapshot=false
[[skills.config]]
path={json.dumps(str(inherited))}
enabled=false
''')
        original = config.read_bytes()
        client = Stdio(root, env, "initial")
        native_config = client.call("config/read", {"cwd": str(cwd), "includeLayers": False})["config"]
        overrides = selection_overrides(native_config, shared, False)
        checks["unrelated_disabled_entry_retained"] = any(e["path"] == str(inherited) and e["enabled"] is False for e in overrides["skills.config"])
        base = {"cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture", "historyMode": "legacy",
                "approvalPolicy": "on-request", "sandbox": "read-only"}

        def turn(label, sid, explicit=False):
            start = len(provider.requests)
            inputs = [{"type": "text", "text": "W0207_RUN_" + label}]
            if explicit:
                inputs.append({"type": "skill", "name": "w0207-shared", "path": str(shared)})
            receipt = client.call("turn/start", {"threadId": sid, "input": inputs})["turn"]
            def completed():
                history = client.call("thread/read", {"threadId": sid, "includeTurns": True})["thread"]
                match = next((t for t in history["turns"] if t["id"] == receipt["id"]), None)
                if match and match["status"] in ("failed", "interrupted"):
                    raise AssertionError("native turn did not complete; see transcript")
                return history if match and match["status"] == "completed" else None
            history = wait_for(completed, label, timeout=45)
            requests = provider.requests[start:]
            assert requests, "native completed without captured model request"
            encoded = json.dumps(requests[0]["body"])
            sample = {"native_id": sid, "turn_id": receipt["id"], "history_turns": len(history["turns"]),
                      "request_count": len(requests), "auth_matches": all(r["auth_matches"] for r in requests),
                      "shared_description": "W0207_SHARED_DESCRIPTION" in encoded,
                      "shared_body": "W0207_SHARED_BODY" in encoded,
                      "inherited_description": "W0207_INHERITED_DESCRIPTION" in encoded,
                      "project_description": "W0207_PROJECT_DESCRIPTION" in encoded,
                      "project_rule": "W0207_PROJECT_RULE" in encoded,
                      "request_sha256": hashlib.sha256(json.dumps(requests[0]["body"], sort_keys=True).encode()).hexdigest()}
            samples[label] = sample
            return sample

        for label, override in (("default", {}), ("disabled", overrides), ("default-again", {})):
            started = client.call("thread/start", {**base, "config": override})
            sid = started["thread"]["id"]
            sessions[label] = sid
            checks[label + "_created_at_bound_cwd"] = started["cwd"] == str(cwd)
            observations[label + "_instruction_sources"] = started.get("instructionSources")
            if metadata_only:
                client.call("thread/inject_items", {"threadId": sid, "items": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "SYNTHETIC HISTORY; no model request"}]}]})
            else:
                sample = turn(label, sid)
                checks[label + "_effective_description"] = sample["shared_description"] == (label != "disabled")
                checks[label + "_other_sources_preserved"] = not sample["inherited_description"] and sample["project_description"] and sample["project_rule"]
                checks[label + "_body_not_eagerly_loaded"] = not sample["shared_body"]
        listing = client.call("skills/list", {"cwds": [str(cwd)], "forceReload": True})
        observations["directory_listing"] = listing
        skills = listing["data"][0]["skills"]
        checks["directory_listing_not_thread_selection"] = any(s["path"] == str(shared) and s["enabled"] for s in skills)
        checks["directory_inherited_disabled_unchanged"] = any(s["path"] == str(inherited) and not s["enabled"] for s in skills)
        client.close()
        client = Stdio(root, env, "restored")
        sid = sessions["disabled"]
        restored = client.call("thread/resume", {**base, "threadId": sid, "config": overrides})
        checks["resume_keeps_original_id"] = restored["thread"]["id"] == sid
        if metadata_only:
            # inject_items persists an item without creating a model turn.
            # An empty turns array is therefore not evidence of history loss.
            rollout = Path(restored["thread"]["path"])
            assert rollout.is_relative_to(Path(env["CODEX_HOME"]))
            checks["resume_keeps_synthetic_item"] = "SYNTHETIC HISTORY; no model request" in rollout.read_text()
        else:
            checks["resume_keeps_history"] = bool(restored["thread"].get("turns"))
        if not metadata_only:
            sample = turn("resumed-disabled", sid)
            checks["resume_keeps_disabled_description"] = not sample["shared_description"]
            checks["resume_preserves_other_sources"] = not sample["inherited_description"] and sample["project_description"] and sample["project_rule"]
            enabled = client.call("thread/resume", {**base, "threadId": sessions["default"], "config": {}})
            sample = turn("explicit-enabled", enabled["thread"]["id"], explicit=True)
            checks["explicit_enabled_loads_body"] = sample["shared_body"]
            # Characterize native explicit input separately. Do not assume a
            # disabled description revokes file access or prevents injection.
            sample = turn("explicit-disabled", sid, explicit=True)
            observations["disabled_explicit_input_loads_body"] = sample["shared_body"]
            checks["local_provider_authentication"] = all(r["auth_matches"] for r in provider.requests)
            checks["no_provider_errors"] = not provider.errors
        checks["profile_unchanged"] = config.read_bytes() == original
        report["metadata_expectations_met"] = all(checks.values())
        report["effective_selection_verified"] = not metadata_only and all(checks.values())
        return report
    finally:
        if client:
            client.close()
        if provider:
            provider.close()
        report["native_calls"] = [c["method"] for path in sorted(root.glob("*-protocol.json")) for c in json.loads(path.read_text())["calls"]]
        write(root / "result.json", report)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--metadata-only", action="store_true")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-w0207-selection-"))
    print("Evidence directory:", root, flush=True)
    try:
        report = run(root, args.metadata_only)
        print(json.dumps({"checks": report["checks"], "metadata_expectations_met": report["metadata_expectations_met"],
                          "effective_selection_verified": report["effective_selection_verified"]}, indent=2))
        return int(not report["metadata_expectations_met"])
    except Exception as error:
        write(root / "failure.json", {"error": type(error).__name__, "detail": str(error)})
        print("Probe failed; see", root / "failure.json", flush=True)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
