"""Isolated fixed-version stdio study; synthetic history, no model turns.

This does not exercise the production Unix-socket proxy or shared-daemon attach.
Exit status checks the requested preservation claim, not just RPC completion.
"""
import argparse
import json
import os
from pathlib import Path
import select
import subprocess
import tempfile
import time

from execution_config_probe import environment, SERVER
from console_fixture import stop


FIELDS = (
    "model", "modelProvider", "cwd", "runtimeWorkspaceRoots",
    "approvalPolicy", "sandbox", "activePermissionProfile",
)


class Stdio:
    def __init__(self, env, cwd, label, result):
        self.log = (cwd.parent / f"stderr-{label}.txt").open("w")
        self.proc = subprocess.Popen(
            [SERVER, "app-server", "--stdio"], env=env, cwd=cwd,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.log,
            start_new_session=True,
        )
        self.buffer, self.seq, self.label, self.result = bytearray(), 0, label, result

    def send(self, value):
        self.proc.stdin.write(json.dumps(value).encode() + b"\n")
        self.proc.stdin.flush()

    def call(self, method, params):
        self.seq += 1
        self.send({"id": self.seq, "method": method, "params": params})
        self.result["calls"].append({"process": self.label, "method": method, "params": params})
        deadline = time.monotonic() + 15
        while True:
            while b"\n" not in self.buffer:
                remain = deadline - time.monotonic()
                if remain <= 0 or not select.select([self.proc.stdout], [], [], remain)[0]:
                    raise TimeoutError(f"{method} timeout")
                part = os.read(self.proc.stdout.fileno(), 65536)
                if not part:
                    raise EOFError(f"{method} closed; exit={self.proc.poll()}")
                self.buffer.extend(part)
            line, _, rest = self.buffer.partition(b"\n")
            self.buffer[:] = rest
            value = json.loads(line)
            if value.get("id") == self.seq and "method" not in value:
                return value
            self.result["events"].append({"process": self.label, "method": value.get("method")})

    def rpc(self, method, params):
        value = self.call(method, params)
        if "error" in value:
            raise RuntimeError(f"{method}: {value['error']}")
        return value.get("result")

    def initialize(self):
        value = self.rpc("initialize", {
            "clientInfo": {"name": "netcap_native_resume_probe", "version": "1"},
            "capabilities": {"experimentalApi": True},
        })
        self.send({"method": "initialized"})
        self.result.setdefault("initializations", []).append(value)

    def close(self):
        stop(self.proc)
        self.log.close()
        self.result["processes"].append({
            "label": self.label, "pid": self.proc.pid,
            "exit_code": self.proc.returncode, "exited": self.proc.poll() is not None,
        })


def run(mode):
    root = Path(tempfile.mkdtemp(prefix="netcap-native-resume-"))
    env = environment(root)
    env["NETCAP_FIXTURE_KEY"] = "synthetic-only"
    cwd = root / "work"
    cwd.mkdir()
    (Path(env["CODEX_HOME"]) / "config.toml").write_text('''model="gpt-5.4"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode="read-only"
[model_providers.fixture]
name="No model calls"
base_url="http://127.0.0.1:9/v1"
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
''')
    result = {
        "mode": mode, "root": str(root), "calls": [], "events": [], "processes": [],
        "scope": "real isolated Codex stdio RPC; synthetic history only; no turn/start; no shared-daemon or production proxy",
        "global_settings": {"model": "gpt-5.4", "approval_policy": "on-request", "sandbox_mode": "read-only"},
        "checks": {}, "completed": False,
    }
    peer = None
    try:
        result["version"] = subprocess.check_output([SERVER, "--version"], env=env, text=True).strip()
        if result["version"] != "codex-cli 0.158.0":
            raise RuntimeError("unverified server version")
        peer = Stdio(env, cwd, "initial", result)
        peer.initialize()
        started = peer.rpc("thread/start", {
            "cwd": str(cwd), "model": "gpt-6-astra", "modelProvider": "fixture",
            "sandbox": "workspace-write", "approvalPolicy": "never", "historyMode": "legacy",
        })
        tid = started["thread"]["id"]
        result["thread_id"] = tid
        result["started"] = {k: started.get(k) for k in FIELDS}
        if mode != "unmaterialized":
            peer.rpc("thread/inject_items", {"threadId": tid, "items": [{
                "type": "message", "role": "user", "content": [{
                    "type": "input_text", "text": "SYNTHETIC HISTORY ONLY; no turn execution",
                }],
            }]})
        result["before"] = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        if mode == "unsubscribe":
            result["unsubscribe"] = peer.rpc("thread/unsubscribe", {"threadId": tid})
            result["after_unsubscribe"] = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        if mode == "restart":
            peer.close()
            peer = None
            peer = Stdio(env, cwd, "restarted", result)
            peer.initialize()
            result["before_resume"] = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        response = peer.call("thread/resume", {"threadId": tid, "excludeTurns": True})
        if mode == "unmaterialized":
            error = response.get("error", {})
            result["resume_error"] = error
            result["checks"]["unmaterialized_resume_refused"] = (
                error.get("code") == -32600 and "no rollout found" in error.get("message", "")
            )
        else:
            if "error" in response:
                raise RuntimeError(f"thread/resume: {response['error']}")
            resumed = response["result"]
            result["resume_keys"] = list(resumed)
            result["resumed"] = {k: resumed.get(k) for k in FIELDS}
            result["checks"]["original_id"] = resumed["thread"]["id"] == tid
            result["checks"]["preserved_initial_settings"] = result["resumed"] == result["started"]
            result["after"] = peer.rpc("thread/read", {"threadId": tid, "includeTurns": False})["thread"]
        result["completed"] = True
    except Exception as error:
        result["error"] = str(error)
    finally:
        if peer is not None:
            peer.close()
    result["checks"]["owned_processes_exited"] = bool(result["processes"]) and all(p["exited"] for p in result["processes"])
    result["passed"] = result["completed"] and all(result["checks"].values())
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["unmaterialized", "loaded", "unsubscribe", "restart"], required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = run(args.mode)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({k: result[k] for k in ("mode", "checks", "completed", "passed", "root")}, ensure_ascii=False))
    raise SystemExit(0 if result["passed"] else 1)
