#!/usr/bin/env python3
"""Lost wire receipts, incomplete history and crash recovery on a real CLI."""
import argparse
import json
from pathlib import Path
from urllib.error import HTTPError

from console_fixture import ConsoleFixture, wait_for


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--netcap",required=True);args=parser.parse_args()
    fixture=ConsoleFixture(args.netcap)
    faults=fixture.root/"faults";faults.mkdir()
    fixture.env["NETCAP_FAULT_DIR"]=str(faults)
    fixture.config["instances"][0]["env_allow"].append("NETCAP_FAULT_DIR")
    fixture.config["instances"][0]["executable"]=str(Path(__file__).with_name("fault_codex_proxy.py").resolve())
    def arm(method,mode="drop",next_fault=None):
        payload={"method":method,"mode":mode}
        if next_fault:payload["next"]=next_fault
        (faults/"dropped.json").unlink(missing_ok=True)
        (faults/"armed.json").write_text(json.dumps(payload))
    def count(method):
        return sum(json.loads(line)["method"]==method for line in (faults/"calls.jsonl").read_text().splitlines())
    def op(oid):return next(x for x in fixture.snapshot()["operations"] if x["id"]==oid)
    with fixture:
        print("Evidence directory:",fixture.root,flush=True)
        arm("thread/start")
        receipt=fixture.command("lost-create","create",project_id="fixture",route_id="route-a",title="lost-create",permission="read_only")
        sid=receipt["session_id"]
        wait_for(lambda:(faults/"dropped.json").exists(),"dropped real create response")
        expected=json.loads((faults/"dropped.json").read_text())["result"]["thread"]["id"]
        wait_for(lambda:fixture.session(sid)["scope"]["native_id"]==expected,"recover same native thread after lost create")
        assert count("thread/start")==1
        print("PASS lost create receipt: native identity recovered, exactly one create",flush=True)

        arm("turn/start")
        sent=fixture.command("lost-send","send",session_id=sid,text="NETCAP_A_TURN_1")
        wait_for(lambda:op(sent["operation_id"])["status"]=="succeeded","recover lost turn receipt")
        assert count("turn/start")==1
        assert fixture.command("lost-send","send",session_id=sid,text="NETCAP_A_TURN_1")==sent
        print("PASS lost send receipt: original input/turn correlated without resend",flush=True)

        arm("turn/start",next_fault={"method":"thread/read","mode":"partial"})
        partial=fixture.command("partial-send","send",session_id=sid,text="NETCAP_A_TURN_2")
        wait_for(lambda:op(partial["operation_id"])["status"]=="uncertain","partial history stays unknown")
        fixture.command("manual","takeover",session_id=sid)
        for kind in ("resume","send"):
            try:fixture.command("refuse-"+kind,kind,session_id=sid,text="must not execute")
            except HTTPError as error:assert error.code==409
            else:raise AssertionError("unknown was cleared by "+kind)
        fixture.restart_console(crash=True)
        wait_for(lambda:fixture.session(sid)["status"]=="unknown","unknown survives crash")
        assert fixture.session(sid)["paused"] and count("turn/start")==2
        (faults/"armed.json").unlink()
        fixture.command("recheck","reconcile",session_id=sid)
        wait_for(lambda:op(partial["operation_id"])["status"]=="succeeded","original execution resolves after complete evidence")
        assert fixture.session(sid)["control_mode"]=="human_takeover" and fixture.session(sid)["paused"]
        assert count("turn/start")==2
        print("PASS partial history + takeover + crash: occupancy retained; full evidence resolves original operation",flush=True)

        arm("thread/start",next_fault={"mode":"hide_discovery"})
        candidate=fixture.command("associate-create","create",project_id="fixture",route_id="route-a",title="manual-association",permission="read_only")
        wait_for(lambda:(faults/"dropped.json").exists(),"lost create with unavailable discovery")
        expected=json.loads((faults/"dropped.json").read_text())["result"]["thread"]["id"]
        wait_for(lambda:op(candidate["operation_id"])["status"]=="uncertain","discovery cannot identify original")
        associated=fixture.command("verified-association","associate",session_id=candidate["session_id"],operation_id=candidate["operation_id"],native_id=expected)
        wait_for(lambda:op(associated["operation_id"])["status"]=="succeeded","explicit candidate verified against real native source")
        assert fixture.session(candidate["session_id"])["scope"]["native_id"]==expected and count("thread/start")==2
        print("PASS verified native association while discovery is unavailable; no second creation",flush=True)
        (fixture.root/"snapshot.json").write_text(json.dumps(fixture.snapshot(),indent=2))


if __name__=="__main__":main()
