package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClaudeCreateSettingsValidationBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "schema", "unreadable", "helper", "helper-source", "helpers", "helpers-source", "conflict-route", "conflict-state", "conflict-helper", "conflict-env", "conflict-missing", "missing-helpers", "null-helpers", "registry-json", "registry-value", "identity-drift", "resolution-drift", "wrong-version", "foreign-cwd", "foreign-settings", "not-bare", "persistent", "not-stopped", "contradictory", "bad-source", "drift", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cwd, profile := filepath.Join(root, "work"), filepath.Join(root, "profile")
			for _, path := range []string{cwd, profile} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			reader := fmt.Sprintf(`import json,sys
r=json.load(sys.stdin)
mode=%q
out={'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-08T00:00:00Z','resolver_returned':True,'read_complete':mode!='unreadable','complete':False,'files':[],'sources':[],'registry':[],'issues':[],'limitations':['read_observation_not_admission'],'policy_helper_present':mode=='helper'}
out['policy_helpers_present']=mode=='helpers'
out['startup_conflicts']=[]
if mode.startswith('conflict-'):
 kind={'conflict-route':'route_environment','conflict-state':'state_environment','conflict-helper':'api_key_helper','conflict-env':'invalid_environment'}.get(mode)
 if kind:out['startup_conflicts']=[{'source':'managed','policy_origin':'file','kind':kind}]
 else:out.pop('startup_conflicts')
if mode=='missing-helpers':out.pop('policy_helpers_present')
if mode=='null-helpers':out['policy_helpers_present']=None
if mode in ['helper-source','helpers-source']:out['sources']=[{'source':'managed','policy_origin':'file','settings_sha256':'a'*64,'has_policy_helper':mode=='helper-source','has_policy_helpers':mode=='helpers-source'}]
if mode=='unreadable':out['issues']=[{'path':r['cwd'],'operation':'read','code':'EACCES'}]
if mode.startswith('registry-'):out.update(read_complete=False,registry=[{'hive':'hklm','outcome':'invalid_json' if mode=='registry-json' else 'invalid_value'}])
if mode in ['identity-drift','resolution-drift']:
 import pathlib
 version=('b' if pathlib.Path(__file__).with_name('native-called').exists() else 'a')*64
 if mode=='identity-drift':out['files']=[{'path':r['cwd']+'/policy.json','resolved_path':r['cwd']+'/policy.json','status':'read','sha256':'c'*64,'identity_sha256':version,'bytes':2}]
 else:out['path_bindings']=[{'path':r['cwd']+'/alias.json','resolved_path':r['cwd']+'/policy.json','identity_sha256':version}]
print(json.dumps(out))
`, mode)
			validator := fmt.Sprintf(`import json,sys,os
from pathlib import Path
r=json.load(sys.stdin)
mode=%q
Path(__file__).with_name('native-called').write_text('called')
out={'version':1,'source':'claude.bare.settings_validation','cli_version':'2.1.280','cwd':r['cwd'],'settings_file':r['settings_file'],'schema_valid':True,'errors':[],'effective_sha256':'a'*64,'sources':[],'bare_mode':True,'no_session_persistence':True,'process_stopped':True}
if mode=='schema':out.update(schema_valid=False,errors=[{'file':r['cwd']+'/settings.json','field':'permissions.allow','message':'PRIVATE_INVALID_SETTINGS_VALUE'}])
if mode=='wrong-version':out['cli_version']='unverified'
if mode=='foreign-cwd':out['cwd']='/foreign'
if mode=='foreign-settings':out['settings_file']='/foreign/settings.json'
if mode=='not-bare':out['bare_mode']=False
if mode=='persistent':out['no_session_persistence']=False
if mode=='not-stopped':out['process_stopped']=False
if mode=='contradictory':out['schema_valid']=False
if mode=='bad-source':out['sources']=[{'source':'userSettings','sha256':'not-a-digest'}]
if mode=='drift':
 os.makedirs(r['cwd']+'/.claude',exist_ok=True)
 Path(r['cwd']+'/.claude/settings.json').write_text('{}')
if mode=='oversized':print('x'*(2<<20))
else:print(json.dumps(out))
`, mode)
			for name, source := range map[string]string{"settings_preflight.js": reader, "settings_validation.js": validator} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			scope := Scope{Cwd: cwd, Route: Route{Model: "fixture"}, Instance: Instance{Kind: "claude", Profile: profile,
				Executable: "/owned/claude", Bridge: filepath.Join(root, "main.js"), NodeExecutable: "python3"}}
			err := validateClaudeCreateSettings(context.Background(), scope)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_INVALID_SETTINGS_VALUE") {
				t.Fatal("native value escaped the validation boundary")
			}
			if mode == "schema" && !strings.Contains(err.Error(), "permissions.allow") {
				t.Fatal("invalid field cannot be located")
			}
			_, stat := os.Stat(filepath.Join(root, "native-called"))
			if (mode == "unreadable" || strings.Contains(mode, "helper") || strings.HasPrefix(mode, "conflict-") || strings.HasPrefix(mode, "registry-")) && !os.IsNotExist(stat) {
				t.Fatal("unproven source invoked native validation")
			}
		})
	}
}

func TestClaudeCreateSettingsValidationCancellationStopsProcessGroup(t *testing.T) {
	root := t.TempDir()
	cwd, profile := filepath.Join(root, "work"), filepath.Join(root, "profile")
	for _, path := range []string{cwd, profile} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	reader := `import json,sys
r=json.load(sys.stdin)
print(json.dumps({'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-08T00:00:00Z','resolver_returned':True,'read_complete':True,'complete':False,'files':[],'sources':[],'registry':[],'issues':[],'limitations':['read_observation_not_admission'],'policy_helpers_present':False,'startup_conflicts':[]}))
`
	worker := `import json,os,signal,subprocess,sys,time
from pathlib import Path
json.load(sys.stdin)
child=subprocess.Popen([sys.executable,'-c','import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(60)'])
Path(__file__).with_name('processes.json').write_text(json.dumps([os.getpid(),child.pid]))
time.sleep(60)
`
	for name, body := range map[string]string{"settings_preflight.js": reader, "settings_validation.js": worker} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scope := Scope{Cwd: cwd, Route: Route{Model: "fixture"}, Instance: Instance{Kind: "claude", Profile: profile,
		Executable: "/owned/claude", Bridge: filepath.Join(root, "main.js"), NodeExecutable: "python3"}}
	done := make(chan error, 1)
	go func() { done <- validateClaudeCreateSettings(ctx, scope) }()
	var pids []int
	for deadline := time.Now().Add(3 * time.Second); len(pids) != 2; time.Sleep(10 * time.Millisecond) {
		data, _ := os.ReadFile(filepath.Join(root, "processes.json"))
		_ = json.Unmarshal(data, &pids)
		if time.Now().After(deadline) {
			t.Fatal("validation worker did not start its owned child")
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(-pids[0], syscall.SIGKILL) })
	for _, pid := range pids {
		if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pids[0] {
			t.Fatalf("metadata processes do not share their own process group: %d, %v", pgid, err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled validation was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled validation did not return promptly")
	}
	for _, pid := range pids {
		// An orphan can briefly await reaping by init. It must no longer execute.
		for deadline := time.Now().Add(time.Second); ; time.Sleep(10 * time.Millisecond) {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if os.IsNotExist(err) || err == nil && strings.HasPrefix(string(data)[strings.LastIndex(string(data), ")")+1:], " Z ") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("cancelled metadata process %d is still running", pid)
			}
		}
	}
}
