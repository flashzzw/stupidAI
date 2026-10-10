import json
from pathlib import Path
import subprocess
import sys

from common import isolated_env, stop, wait_for

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from go_cli_smoke import Proxy


def probe(root, provider, args):
    env = isolated_env(root)
    env["NETCAP_FIXTURE_KEY"] = "fixture-" + provider.route
    cwd = root / "work"
    cwd.mkdir()
    sock = root / "codex.sock"
    config = Path(env["CODEX_HOME"]) / "config.toml"
    config.write_text(f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Netcap isolated fixture"
base_url = "{provider.url}/v1"
env_key = "NETCAP_FIXTURE_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[features]
apps = false
plugins = false
shell_snapshot = false
''')
    with (root / "server.log").open("w") as log:
        server = subprocess.Popen([args.codex_server, "app-server", "--listen", "unix://" + str(sock)],
                                  env=env, cwd=cwd, stdout=log, stderr=log, start_new_session=True)
        client = None
        try:
            wait_for(lambda: sock.exists() or server.poll() is not None, "owned Codex socket")
            assert server.poll() is None, "owned Codex server exited"
            client = Proxy(args.codex, sock, env)
            thread = client.call("thread/start", {"cwd": str(cwd), "model": "gpt-6-astra",
                                                 "modelProvider": "fixture", "historyMode": "legacy",
                                                 "approvalPolicy": "on-request", "sandbox": "read-only"})["thread"]
            tid = thread["id"]
            receipts = []

            def turn(number):
                marker = f"NETCAP_{provider.route}_TURN_{number}"
                result = client.call("turn/start", {"threadId": tid, "input": [{"type": "text", "text": marker}]})
                native_turn = result["turn"]["id"]
                receipts.append({"id": native_turn, "accepted_status": result["turn"]["status"]})

                def completed():
                    history = client.call("thread/read", {"threadId": tid, "includeTurns": True})["thread"]
                    turns = history["turns"]
                    if not turns or turns[-1]["id"] != native_turn or turns[-1]["status"] != "completed":
                        return False
                    assert f"fixture {provider.route}: {marker}" in json.dumps(turns[-1])
                    return history
                return wait_for(completed, "native turn completion")

            turn(1)
            before = turn(2)
            assert len(before["turns"]) == 2
            client.close()
            client = Proxy(args.codex, sock, env)
            history = client.call("thread/read", {"threadId": tid, "includeTurns": True})["thread"]
            assert history["id"] == tid and len(history["turns"]) == 2
            resumed = client.call("thread/resume", {"threadId": tid})["thread"]
            assert resumed["id"] == tid
            after = turn(3)
            (root / "history.json").write_text(json.dumps(after, indent=2))
            assert len(after["turns"]) == 3
            return {"native_id": tid, "turn_receipts": receipts, "history_turns": 3,
                    "route": provider.assert_route("gpt-6-astra")}
        finally:
            if client:
                client.close()
            stop(server)
