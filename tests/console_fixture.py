"""Owned console/native lifecycles for protocol and crash-recovery contracts."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
from urllib.request import Request, urlopen

sys.path.insert(0,str(Path(__file__).resolve().parent/"runtime"))
from common import Provider, isolated_env, stop, wait_for

CLI="/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex"
SERVER_CLI="/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex"


class ConsoleFixture:
    def __init__(self,binary,provider_class=Provider):
        self.binary=binary
        self.root=Path(tempfile.mkdtemp(prefix="netcap-console-"))
        self.provider=provider_class(self.root,"A")
        self.env=isolated_env(self.root)
        self.env["NETCAP_FIXTURE_KEY"]="fixture-A"
        self.cwd=self.root/"work";self.cwd.mkdir()
        self.profile=Path(self.env["CODEX_HOME"])
        (self.profile/"config.toml").write_text(f'''model = "gpt-6-astra"
model_provider = "fixture"
approval_policy = "on-request"
sandbox_mode = "read-only"
[model_providers.fixture]
name = "Fixture"
base_url = "{self.provider.url}/v1"
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
        self.sock=self.root/"codex.sock"
        self.config={"version":1,"state_dir":str(self.root/"console"),"listen":"127.0.0.1:0",
          "instances":[{"id":"codex-local","kind":"codex","executable":CLI,"profile":str(self.profile),"socket":str(self.sock),
                        "version":"0.157.1/0.158.0","policy_revision":"m0-1","env_allow":["NETCAP_FIXTURE_KEY"]}],
          "routes":[{"id":"route-a","instance_id":"codex-local","provider":"fixture","endpoint":self.provider.url+"/v1",
                     "model":"gpt-6-astra","credential_ref":"env:NETCAP_FIXTURE_KEY","credential_revision":"1","config_revision":"1"}],
          "projects":[{"id":"fixture","name":"本地验收项目","cwd":str(self.cwd)}]}
        self.config_path=self.root/"console.json"
        self.native=None;self.console=None;self.info={};self.token=""
        self.native_log=(self.root/"codex.log").open("w")
        self.console_log=(self.root/"console.log").open("w")

    def __enter__(self):
        try:
            self.start_native();self.start_console();return self
        except BaseException:
            self.close();raise

    def __exit__(self,*_):self.close()

    def start_native(self):
        assert self.native is None or self.native.poll() is not None
        if self.sock.exists():self.sock.unlink()
        self.native=subprocess.Popen([SERVER_CLI,"app-server","--listen","unix://"+str(self.sock)],env=self.env,cwd=self.cwd,
                                     stdout=self.native_log,stderr=self.native_log,start_new_session=True)
        wait_for(lambda:self.sock.exists(),"owned native socket")

    def start_console(self):
        assert self.console is None or self.console.poll() is not None
        self.config_path.write_text(json.dumps(self.config))
        connection=self.root/"console/connection.json"
        if connection.exists():connection.unlink()
        self.console=subprocess.Popen([self.binary,"--config",str(self.config_path),"serve"],env=self.env,cwd=self.cwd,
                                      stdout=self.console_log,stderr=self.console_log,start_new_session=True)
        wait_for(connection.exists,"owned console connection")
        self.info=json.loads(connection.read_text())
        self.token=Path(self.info["token_file"]).read_text().strip()

    def restart_console(self,crash=False):
        if crash:os.killpg(self.console.pid,signal.SIGKILL);self.console.wait(timeout=5)
        else:stop(self.console)
        self.start_console()

    def restart_native(self):
        stop(self.native);self.start_native()

    def api(self,path,body=None):
        req=Request(self.info["url"]+path,data=json.dumps(body).encode() if body is not None else None,
                    headers={"Content-Type":"application/json","Authorization":"Bearer "+self.token})
        with urlopen(req,timeout=15) as response:return json.load(response)

    def command(self,key,kind,**values):return self.api("/api/commands",dict(command_id=key,kind=kind,**values))

    def snapshot(self):return self.api("/api/snapshot")

    def session(self,sid):return next(s for s in self.snapshot()["sessions"] if s["id"]==sid)

    def create(self,key):
        r=self.command(key,"create",project_id="fixture",route_id="route-a",title=key,permission="read_only")
        wait_for(lambda:self.session(r["session_id"])["status"]=="idle","native session creation")
        return r["session_id"]

    def close(self):
        stop(self.console);stop(self.native);self.provider.close();self.native_log.close();self.console_log.close()
