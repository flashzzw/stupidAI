#!/usr/bin/env python3
"""Fixed SDK bridge through the real Claude CLI and local-only provider."""
import argparse
import json
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import uuid

sys.path.insert(0, str(Path(__file__).parent / "runtime"))
from common import Provider, isolated_env, stop, wait_for


class Bridge:
    def __init__(self, script, root, env):
        self.seq = 0
        self.log = (root / "bridge.stderr").open("a")
        self.proc = subprocess.Popen(["node", str(script), str(root / "bridge-state")], env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.log, text=True, start_new_session=True)
        hello = self.call("hello", {})
        assert hello.get("version") == 1 and hello.get("sdk") == "0.3.280"

    def call(self, method, params):
        self.seq += 1
        self.proc.stdin.write(json.dumps({"v": 1, "id": str(self.seq), "method": method, "params": params}) + "\n")
        self.proc.stdin.flush()
        assert select.select([self.proc.stdout], [], [], 20)[0], "bridge timeout: " + method
        line = self.proc.stdout.readline()
        assert line, "bridge closed: " + method
        response = json.loads(line)
        assert response["id"] == str(self.seq) and response["v"] == 1
        assert "error" not in response, response.get("error")
        return response["result"]

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=8)
        finally:
            stop(self.proc)
            self.log.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bridge", default="bridges/claude/dist/main.js")
    parser.add_argument("--claude", default="/home/zzw/.local/bin/claude")
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-claude-bridge-"))
    print("Evidence directory:", root, flush=True)
    provider = Provider(root, "A")
    env = isolated_env(root)
    env.update(CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
    cwd = root / "work"
    cwd.mkdir()
    config = {"native_id": str(uuid.uuid4()), "source": str(uuid.uuid4()), "cwd": str(cwd),
              "executable": args.claude, "model": "claude-sonnet-4-6", "endpoint": provider.url,
              "permission": "read_only", "api_key": "fixture-A"}
    client = None
    try:
        client = Bridge(Path(args.bridge).resolve(), root, env)
        result = client.call("create", config)
        assert result["native_id"] == config["native_id"] and not provider.requests
        operations = []

        def turn(number):
            operation = str(uuid.uuid4())
            params = {"native_id": config["native_id"], "operation": operation, "text": f"NETCAP_A_TURN_{number}", "api_key": "fixture-A"}
            sent = client.call("send", params)
            assert client.call("send", params) == sent

            def completed():
                snapshot = client.call("snapshot", {"native_id": config["native_id"]})
                results = [e for e in snapshot["entries"] if e["kind"] == "event" and e["operation"] == operation and e["event"]["type"] == "result"]
                if not results:
                    return False
                assert results[-1]["event"]["subtype"] == "success", results[-1]
                assert f"fixture A: NETCAP_A_TURN_{number}" in results[-1]["event"].get("result", "")
                return snapshot

            snapshot = wait_for(completed, "native SDK terminal result", timeout=40)
            (root / f"turn-{number}.json").write_text(json.dumps(snapshot, indent=2))
            operations.append(operation)
            return snapshot

        turn(1)
        before = turn(2)
        client.close()
        client = Bridge(Path(args.bridge).resolve(), root, env)
        restored = client.call("snapshot", {"native_id": config["native_id"]})
        assert len(restored["history"]) == len(before["history"]) >= 4
        turn(3)
        print("PASS SDK initialization without model call, two turns, duplicate send, native history and fresh bridge resume", flush=True)
        result = provider.assert_route("claude-sonnet-4-6")
        (root / "result.json").write_text(json.dumps({"native_id": config["native_id"], "operations": operations, "route": result}, indent=2))
    finally:
        if client:
            client.close()
        provider.close()


if __name__ == "__main__":
    main()
