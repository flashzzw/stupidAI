import json
import re
import subprocess
import threading
from urllib.parse import quote
from urllib.request import urlopen

from common import http, isolated_env, stop, wait_for


def probe(root, provider, args):
    env = isolated_env(root)
    config = {
        "model": "fixture/fixture-model", "small_model": "fixture/fixture-model",
        "autoupdate": False, "share": "disabled", "plugin": [], "permission": "deny",
        "provider": {"fixture": {"npm": "@ai-sdk/openai-compatible", "name": "Netcap local fixture",
                     "options": {"baseURL": provider.url + "/v1", "apiKey": "fixture-" + provider.route},
                     "models": {"fixture-model": {"name": "Fixture model", "limit": {"context": 32768, "output": 1024}}}}},
    }
    env.update(OPENCODE_CONFIG_CONTENT=json.dumps(config), OPENCODE_DISABLE_MODELS_FETCH="true",
               OPENCODE_DISABLE_AUTOUPDATE="true", OPENCODE_DISABLE_DEFAULT_PLUGINS="true")
    cwd = root / "work"
    cwd.mkdir()
    suffix = "?directory=" + quote(str(cwd), safe="")
    process = None
    logs = []
    events = []
    streams = []

    def spawn(number):
        nonlocal process
        path = root / f"server-{number}.log"
        log = path.open("w")
        logs.append(log)
        process = subprocess.Popen([args.opencode, "serve", "--port", "0", "--hostname", "127.0.0.1", "--pure"],
                                   env=env, cwd=cwd, stdout=log, stderr=log, start_new_session=True)

        def ready():
            output = path.read_text()
            assert process.poll() is None, output
            match = re.search(r"http://127.0.0.1:\d+", output)
            return match[0] if match else None
        return wait_for(ready, "owned OpenCode server")

    def subscribe(base):
        response = urlopen(base + "/event" + suffix, timeout=10)

        def consume():
            try:
                for line in response:
                    if line.startswith(b"data: "):
                        events.append(json.loads(line[6:]))
            except (OSError, ValueError):
                pass
        thread = threading.Thread(target=consume, daemon=True)
        thread.start()
        streams.append((response, thread))

    try:
        base = spawn(1)
        doc = http(base, "/doc")
        assert "204" in doc["paths"]["/session/{sessionID}/prompt_async"]["post"]["responses"]
        (root / "openapi.json").write_text(json.dumps(doc, indent=2))
        effective = http(base, "/config" + suffix)
        assert effective["provider"]["fixture"]["options"]["baseURL"] == provider.url + "/v1"
        subscribe(base)
        session = http(base, "/session" + suffix, {"title": "Netcap fixture " + provider.route})
        sid = session["id"]
        receipts = []

        def turn(number):
            marker = f"NETCAP_{provider.route}_TURN_{number}"
            receipt = http(base, f"/session/{sid}/prompt_async" + suffix,
                           {"model": {"providerID": "fixture", "modelID": "fixture-model"},
                            "parts": [{"type": "text", "text": marker}]})
            assert receipt is None  # Native 204 is admission, not completion.

            def completed():
                history = http(base, f"/session/{sid}/message" + suffix)
                users = [m for m in history if m["info"]["role"] == "user"]
                replies = [m for m in history if m["info"]["role"] == "assistant"]
                if len(users) < number or len(replies) < number:
                    return False
                latest = replies[-1]
                assert not latest["info"].get("error"), latest
                if not latest["info"]["time"].get("completed"):
                    return False
                assert f"fixture {provider.route}: {marker}" in json.dumps(latest)
                receipts.append({"accepted_http_status": 204, "message_id": latest["info"]["id"],
                                 "completed_at": latest["info"]["time"]["completed"]})
                return history
            return wait_for(completed, "OpenCode native message completion")

        turn(1)
        before = turn(2)
        # Ended-session recovery across a server restart, retaining its profile.
        stop(process)
        base = spawn(2)
        restored = http(base, f"/session/{sid}" + suffix)
        assert restored["id"] == sid
        history = http(base, f"/session/{sid}/message" + suffix)
        assert [m["info"]["id"] for m in history] == [m["info"]["id"] for m in before]
        subscribe(base)
        after = turn(3)
        wait_for(lambda: any(e.get("type") == "message.part.updated" for e in events), "SSE message events")
        (root / "history.json").write_text(json.dumps(after, indent=2))
        return {"native_id": sid, "history_messages": len(after), "receipts": receipts,
                "sse_events": len(events), "route": provider.assert_route("fixture-model")}
    finally:
        stop(process)
        for response, thread in streams:
            response.close()
            thread.join(timeout=2)
        for log in logs:
            log.close()
        (root / "events.json").write_text(json.dumps(events, indent=2))
