#!/usr/bin/env python3
"""Exercise production Codex route/credential binding with one owned daemon."""
import argparse
import json
from urllib.error import HTTPError

from console_fixture import ConsoleFixture, Provider, wait_for


def operation(fixture, receipt):
    def terminal():
        current = next(op for op in fixture.snapshot()["operations"] if op["id"] == receipt["operation_id"])
        return current if current["status"] in ("succeeded", "failed", "not_sent", "interrupted") else None
    return wait_for(terminal, "native operation terminal", timeout=130)


def create(fixture, key, route, expected="succeeded"):
    receipt = fixture.command(key, "create", project_id="fixture", route_id=route, permission="read_only")
    result = operation(fixture, receipt)
    assert result["status"] == expected, result.get("error", result["status"])
    return receipt["session_id"]


def send(fixture, key, sid, marker):
    receipt = fixture.command(key, "send", session_id=sid, text=marker)
    result = operation(fixture, receipt)
    assert result["status"] == "succeeded", result.get("error", result["status"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    args = parser.parse_args()
    fixture = ConsoleFixture(args.netcap)
    other = None
    try:
        other = Provider(fixture.root, "B")
        fixture.env["NETCAP_FIXTURE_KEY_B"] = "fixture-B"
        second = dict(fixture.config["routes"][0], id="route-b", endpoint=other.url + "/v1", credential_ref="env:NETCAP_FIXTURE_KEY_B")
        fixture.config["routes"].append(second)
        fixture.start_native()
        fixture.start_console()
        print("Evidence directory:", fixture.root, flush=True)
        sessions = {"A": create(fixture, "create-a", "route-a"), "B": create(fixture, "create-b", "route-b")}
        for label, provider in (("A", fixture.provider), ("B", other)):
            send(fixture, "send-" + label, sessions[label], "NETCAP_" + label + "_TURN_1")
            assert len(provider.requests) == 1 and provider.requests[0]["auth_matches"]
            assert provider.requests[0]["body"]["model"] == "gpt-6-astra"

        # Controller restart preserves the original credential stamps. The
        # native restart then exercises unloaded-thread route restoration.
        fixture.restart_console()
        fixture.restart_native()
        for label, provider in (("A", fixture.provider), ("B", other)):
            sid = sessions[label]
            wait_for(lambda: fixture.session(sid)["connection"] == "ready", label + " route recovery")
            send(fixture, "recovered-" + label, sid, "NETCAP_" + label + "_TURN_2")
            assert len(provider.requests) == 2 and all(r["auth_matches"] for r in provider.requests)

        # Change the controller's key while deliberately leaving its daemon
        # on the original environment. No model call may use that stale key.
        fixture.env["NETCAP_FIXTURE_KEY"] = "fixture-B"
        fixture.config["routes"][0].update(endpoint=other.url + "/v1", credential_revision="2", config_revision="2")
        fixture.restart_console()
        try:
            fixture.command("old-credential-send", "send", session_id=sessions["A"], text="must not send")
        except HTTPError as error:
            assert error.code == 409
        else:
            raise AssertionError("old session adopted rotated credentials")
        create(fixture, "stale-daemon-create", "route-a", "not_sent")
        assert len(fixture.provider.requests) == len(other.requests) == 2

        # Only this fixture-owned daemon is restarted. New threads can now use
        # the rotated key; old session IDs and history remain intact.
        fixture.restart_native()
        rotated = create(fixture, "rotated-create", "route-a")
        send(fixture, "rotated-send", rotated, "NETCAP_B_TURN_3")
        assert len(other.requests) == 3 and other.requests[-1]["auth_matches"]
        assert len(fixture.provider.requests) == 2
        assert len([m for m in fixture.session(sessions["A"])["view"]["messages"] if m["role"] == "assistant"]) >= 2

        # An extra provider auth source must be rejected before thread/start.
        path = fixture.profile / "config.toml"
        text = path.read_text().replace('name = "Fixture"', 'name = "Fixture"\nexperimental_bearer_token = "fixture-extra-key"')
        path.write_text(text)
        create(fixture, "conflicting-auth-create", "route-b", "not_sent")
        assert len(fixture.provider.requests) == 2 and len(other.requests) == 3
        result = {"shared_daemon": True, "same_model": True, "distinct_endpoint_credentials": True,
                  "unloaded_route_recovery": True, "stale_daemon_rejected_without_request": True,
                  "rotated_credential_verified": True, "extra_auth_rejected_without_request": True,
                  "provider_request_counts": {"A": 2, "B": 3}}
        (fixture.root / "production-route-result.json").write_text(json.dumps(result, indent=2) + "\n")
        print("PASS", json.dumps(result), flush=True)
    finally:
        fixture.close()
        if other:
            other.close()


if __name__ == "__main__":
    main()
