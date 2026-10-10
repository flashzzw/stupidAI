#!/usr/bin/env python3
"""Verify per-thread endpoint/env-key overrides on one owned Codex server."""
import argparse
import json

from console_fixture import CLI, ConsoleFixture, Provider, wait_for
from go_cli_smoke import Proxy


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    fixture = ConsoleFixture(args.netcap)
    other = Provider(fixture.root, "B")
    fixture.env["NETCAP_FIXTURE_KEY_B"] = "fixture-B"
    proxy = None
    try:
        fixture.start_native()
        proxy = Proxy(CLI, fixture.sock, fixture.env)
        config = proxy.call("config/read", {"cwd": str(fixture.cwd), "includeLayers": True})
        actual = config["config"]["model_providers"]["fixture"]
        assert actual["env_key"] == "NETCAP_FIXTURE_KEY" and actual["base_url"] == fixture.provider.url + "/v1"
        sessions = {}
        for label, provider, key in (("A", fixture.provider, "NETCAP_FIXTURE_KEY"), ("B", other, "NETCAP_FIXTURE_KEY_B")):
            thread = proxy.call("thread/start", {"cwd": str(fixture.cwd), "model": "gpt-6-astra", "modelProvider": "fixture",
                "approvalPolicy": "on-request", "sandbox": "read-only", "historyMode": "legacy", "config": {
                    "model_providers.fixture.base_url": provider.url + "/v1", "model_providers.fixture.env_key": key,
                    "model_providers.fixture.requires_openai_auth": False,
                }})["thread"]
            sessions[label] = thread["id"]
            proxy.call("turn/start", {"threadId": thread["id"], "input": [{"type": "text", "text": f"NETCAP_{label}_TURN_1"}]})
        for label, provider in (("A", fixture.provider), ("B", other)):
            def finished():
                thread = proxy.call("thread/read", {"threadId": sessions[label], "includeTurns": True})["thread"]
                return thread if thread["turns"] and thread["turns"][-1]["status"] == "completed" else None
            wait_for(finished, label + " per-thread route")
            assert len(provider.requests) == 1 and provider.requests[0]["auth_matches"], label
        after = proxy.call("config/read", {"cwd": str(fixture.cwd), "includeLayers": True})
        assert after["config"]["model_providers"]["fixture"] == actual, "per-thread override mutated global config"
        result = {"same_server": True, "same_model": True, "distinct_endpoints": True, "distinct_env_keys": True,
                  "credentials_verified_by_endpoints": True, "config_read": "global layered config, unchanged by thread overrides"}
        (fixture.root / "codex-route-result.json").write_text(json.dumps(result, indent=2))
        print("Evidence directory:", fixture.root, flush=True)
        print("PASS", json.dumps(result), flush=True)
    finally:
        if proxy:
            proxy.close()
        fixture.close()
        other.close()


if __name__ == "__main__":
    main()
