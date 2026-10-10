#!/usr/bin/env python3
"""Isolated production console + real Codex + local model fixture.

--hold keeps only these owned processes available for browser acceptance.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

sys.path.insert(0, str(Path(__file__).resolve().parent / "runtime"))
from common import Provider, isolated_env, stop, wait_for


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap",required=True)
    parser.add_argument("--hold",action="store_true")
    parser.add_argument("--info-file")
    args=parser.parse_args()
    root=Path(tempfile.mkdtemp(prefix="netcap-console-"))
    print("Evidence directory:",root,flush=True)
    provider=Provider(root,"A")
    provider.stream_delay=1.2
    env=isolated_env(root)
    env["NETCAP_FIXTURE_KEY"]="fixture-A"
    cwd=root/"work";cwd.mkdir()
    profile=Path(env["CODEX_HOME"])
    (profile/"config.toml").write_text(f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
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
    cli="/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex"
    server_cli="/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex"
    sock=root/"codex.sock"
    cfg={"version":1,"state_dir":str(root/"console"),"listen":"127.0.0.1:0",
         "instances":[{"id":"codex-local","kind":"codex","executable":cli,"profile":str(profile),"socket":str(sock),
                       "version":"0.157.1/0.158.0","policy_revision":"m0-1","env_allow":["NETCAP_FIXTURE_KEY"]}],
         "routes":[{"id":"route-a","instance_id":"codex-local","provider":"fixture","endpoint":provider.url+"/v1",
                    "model":"gpt-6-astra","credential_ref":"env:NETCAP_FIXTURE_KEY","credential_revision":"1","config_revision":"1"}],
         "projects":[{"id":"fixture","name":"本地验收项目","cwd":str(cwd)}]}
    path=root/"console.json";path.write_text(json.dumps(cfg))
    native=console=None
    native_log=(root/"codex.log").open("w");console_log=(root/"console.log").open("w")
    def launch():
        return subprocess.Popen([args.netcap,"--config",str(path),"serve"],env=env,cwd=cwd,stdout=console_log,stderr=console_log,start_new_session=True)
    try:
        native=subprocess.Popen([server_cli,"app-server","--listen","unix://"+str(sock)],env=env,cwd=cwd,stdout=native_log,stderr=native_log,start_new_session=True)
        wait_for(lambda:sock.exists(),"owned native socket")
        console=launch()
        connection=root/"console/connection.json"
        wait_for(connection.exists,"console connection file")
        info=json.loads(connection.read_text());token=Path(info["token_file"]).read_text().strip()
        def api(endpoint,body=None):
            req=Request(info["url"]+endpoint,data=json.dumps(body).encode() if body else None,headers={"Content-Type":"application/json","Authorization":"Bearer "+token})
            with urlopen(req,timeout=10) as response:return json.load(response)
        def command(key,kind,**values):return api("/api/commands",dict(command_id=key,kind=kind,**values))
        receipt=command("create","create",project_id="fixture",route_id="route-a",title="协议验收会话",permission="read_only")
        sid=receipt["session_id"]
        def session():return next(s for s in api("/api/snapshot")["sessions"] if s["id"]==sid)
        wait_for(lambda:session()["status"]=="idle","create receipt")
        native_id=session()["scope"]["native_id"]
        for n in (1,2):
            key="turn-"+str(n)
            sent=command(key,"send",session_id=sid,text=f"NETCAP_A_TURN_{n}")
            assert command(key,"send",session_id=sid,text=f"NETCAP_A_TURN_{n}")==sent
            wait_for(lambda:session()["status"]=="running" and any(m["role"]=="assistant" and m["status"]=="inProgress" for m in session()["view"]["messages"]),"partial native stream before terminal")
            wait_for(lambda:session()["status"]=="idle" and len(session()["view"]["messages"])==n*2,"turn completion")
        print("PASS production HTTP -> durable intent -> real Codex: two streamed turns and duplicate command",flush=True)
        stop(console);connection.unlink();console=launch()
        wait_for(connection.exists,"restarted console")
        info=json.loads(connection.read_text())
        assert session()["scope"]["native_id"]==native_id
        assert len(session()["view"]["messages"])==4
        assert command("turn-2","send",session_id=sid,text="NETCAP_A_TURN_2")["operation_id"]==sent["operation_id"]
        assert len(provider.requests)==2
        print("PASS restart preserves native ID, history and command receipts",flush=True)
        result={"root":str(root),"url":info["url"],"token_file":info["token_file"],"session_id":sid,"native_id":native_id,
                "requests":len(provider.requests),"harness_pid":os.getpid(),"console_pid":console.pid,"native_pid":native.pid}
        (root/"results.json").write_text(json.dumps(result,indent=2))
        if args.info_file:Path(args.info_file).write_text(json.dumps(result,indent=2))
        if args.hold:
            print("READY for browser acceptance:",info["url"],flush=True)
            done=False
            def finish(*_):
                nonlocal done
                done=True
            signal.signal(signal.SIGTERM,finish);signal.signal(signal.SIGINT,finish)
            while not done:
                assert console.poll() is None and native.poll() is None,"owned process exited"
                time.sleep(.3)
    finally:
        stop(console);stop(native);provider.close();native_log.close();console_log.close()


if __name__=="__main__":main()
