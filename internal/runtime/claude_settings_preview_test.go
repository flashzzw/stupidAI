package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func settingsPreviewFixture(t *testing.T, mode string) (*claudeSettingsFiles, Scope) {
	t.Helper()
	m, scope := settingsFileFixture(t)
	bridge := filepath.Dir(m.root)
	scope.Instance.Bridge = filepath.Join(bridge, "main.js")
	scope.Instance.NodeExecutable = "python3"
	scope.Instance.Executable = "/owned/claude"
	scope.Route.Model = "fixture"
	reader := fmt.Sprintf(`import json,sys
r=json.load(sys.stdin)
mode=%q
out={'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-08T00:00:00Z','resolver_returned':True,'read_complete':True,'complete':False,'files':[],'sources':[],'registry':[],'issues':[],'limitations':['read_observation_not_admission'],'policy_helper_present':mode=='helper'}
out['policy_helpers_present']=mode=='helpers'
out['startup_conflicts']=[]
if mode.startswith('conflict-'):
 kind={'conflict-route':'route_environment','conflict-state':'state_environment','conflict-helper':'api_key_helper','conflict-env':'invalid_environment'}.get(mode)
 if kind:out['startup_conflicts']=[{'source':'managed','policy_origin':'file','kind':kind}]
 else:out.pop('startup_conflicts')
if mode=='missing-helpers':out.pop('policy_helpers_present')
if mode=='null-helpers':out['policy_helpers_present']=None
if mode in ['helper-source','helpers-source']:out['sources']=[{'source':'managed','policy_origin':'file','settings_sha256':'a'*64,'has_policy_helper':mode=='helper-source','has_policy_helpers':mode=='helpers-source'}]
if mode.startswith('registry-'):out.update(read_complete=False,registry=[{'hive':'hklm','outcome':'invalid_json' if mode=='registry-json' else 'invalid_value'}])
if mode in ['identity-drift','resolution-drift']:
 import pathlib
 version=('b' if pathlib.Path(__file__).with_name('native-called').exists() else 'a')*64
 if mode=='identity-drift':out['files']=[{'path':r['cwd']+'/policy.json','resolved_path':r['cwd']+'/policy.json','status':'read','sha256':'c'*64,'identity_sha256':version,'bytes':2}]
 else:out['path_bindings']=[{'path':r['cwd']+'/alias.json','resolved_path':r['cwd']+'/policy.json','identity_sha256':version}]
print(json.dumps(out))
`, mode)
	worker := fmt.Sprintf(`import json,os,sys,fcntl,time
from pathlib import Path
r=json.load(sys.stdin)
mode=%q
assert os.fstat(3).st_ino==os.stat(r['settings_file']).st_ino
with open(r['settings_file']) as f:
 try:fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:pass
 else:raise AssertionError('shared file lock was not held')
Path(__file__).with_name('native-called').write_text('called')
if mode=='cancel':time.sleep(60)
if mode=='worker-dies':os._exit(9)
s=r['plugin_settings'][0]
actual=not s['enabled'] if mode=='mismatch' else (None if mode=='missing-flag' else s['enabled'])
o={'source_key':s['source_key'],'requested_enabled':s['enabled'],'actual_enabled':actual,'matches':actual==s['enabled']}
out={'version':1,'source':'claude.bare.settings_validation','cli_version':'2.1.280','cwd':r['cwd'],'settings_file':r['settings_file'],'schema_valid':True,'errors':[],'effective_sha256':'a'*64,'sources':[],'bare_mode':True,'no_session_persistence':True,'process_stopped':True,'file_holder_observed':mode!='holder-missing','plugin_settings':[o]}
signature={'effective_sha256':'a'*64,'sources_sha256':'b'*64,'applied_sha256':'c'*64,'permissions_sha256':'d'*64}
p={'version':1,'baseline_settings_file':r['baseline_settings_file'],'source_keys':[s['source_key']],'baseline_schema_valid':True,'baseline_process_stopped':True,'baseline':signature,'candidate':dict(signature),'differences':[],'matches':True}
out['preservation']=p
field={'preservation-effective':('effective_sha256','effective_settings'),'preservation-source':('sources_sha256','source_settings'),'preservation-applied':('applied_sha256','applied_settings'),'preservation-permission':('permissions_sha256','permission_rules')}.get(mode)
if field:p['candidate'][field[0]]='e'*64;p.update(differences=[field[1]],matches=False)
if mode=='preservation-missing':out.pop('preservation')
if mode=='preservation-null':out['preservation']=None
if mode=='preservation-baseline':p['baseline_settings_file']='/foreign.json'
if mode=='preservation-keys':p['source_keys']=['foreign@local']
if mode=='preservation-running':p['baseline_process_stopped']=False
if mode=='preservation-hash':p['candidate']['permissions_sha256']='bad'
if mode=='preservation-forged':p['candidate']['permissions_sha256']='e'*64
if mode=='preservation-differences':p['differences']=['permission_rules']
if mode=='preservation-missing-differences':p.pop('differences')
if mode=='preservation-version':p['version']=0
if mode=='preservation-missing-schema':p.pop('baseline_schema_valid')
if mode=='wrong-key':o['source_key']='other@local'
if mode=='schema':
 out.update(schema_valid=False,errors=[{'file':r['settings_file'],'field':'permissions.allow'}])
 p.update(baseline_schema_valid=False,differences=['schema'],matches=False)
if mode=='source-drift':Path(%q).write_text('{}')
if mode=='derived-drift':Path(r['settings_file']).write_text('{"foreign":true}')
print(json.dumps(out))
`, mode, scope.Instance.SettingsFile)
	for name, body := range map[string]string{"settings_preflight.js": reader, "settings_validation.js": worker} {
		if err := os.WriteFile(filepath.Join(bridge, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return m, scope
}

func TestClaudePluginSettingsPreviewBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "mismatch", "missing-flag", "schema", "holder-missing", "wrong-key", "source-drift", "derived-drift", "helper", "helper-source", "helpers", "helpers-source", "conflict-route", "conflict-state", "conflict-helper", "conflict-env", "conflict-missing", "missing-helpers", "null-helpers", "registry-json", "registry-value", "identity-drift", "resolution-drift", "worker-dies"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsPreviewFixture(t, mode)
			out := PreviewClaudePluginSettings(context.Background(), filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local", Enabled: false})
			valid := mode == "valid" || mode == "mismatch" || mode == "missing-flag" || mode == "schema"
			if (len(out.Problems) == 0) != (valid && mode != "schema") || out.Complete || out.SourcesStable != valid {
				t.Fatalf("preview outcome: %+v", out)
			}
			if valid {
				if out.Validation == nil || out.Validation.SchemaValid != (mode != "schema") || out.Validation.PluginSettings[0].Matches != (mode != "mismatch" && mode != "missing-flag") {
					t.Fatal("wrong observation", out.Validation)
				}
			}
			if strings.Contains(mode, "helper") || strings.HasPrefix(mode, "conflict-") || strings.HasPrefix(mode, "registry-") {
				if out.Artifact != nil || out.Cleanup.State != "not_created" {
					t.Fatal("unproven source created a candidate")
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(m.root), "native-called")); !os.IsNotExist(err) {
					t.Fatal("unproven source invoked native worker")
				}
			} else if mode == "derived-drift" {
				if out.Cleanup.State != "retained" || len(out.Cleanup.Issues) == 0 {
					t.Fatal("modified private file removed", out.Cleanup)
				}
			} else if out.Cleanup.State != "released" || len(out.Cleanup.Issues) != 0 {
				t.Fatal("owned candidate not released", out.Cleanup)
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "PRIVATE_SETTINGS_VALUE") {
				t.Fatal("settings value escaped preview")
			}
		})
	}
}

func TestClaudePluginSettingsPreviewRequiresPreservationEvidence(t *testing.T) {
	for _, mode := range []string{"effective", "source", "applied", "permission", "missing", "null", "baseline", "keys", "running", "hash", "forged", "differences", "missing-differences", "version", "missing-schema"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsPreviewFixture(t, "preservation-"+mode)
			out := PreviewClaudePluginSettings(context.Background(), filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local"})
			if len(out.Problems) == 0 || out.Complete || out.Cleanup.State != "released" || len(out.Cleanup.Issues) != 0 {
				t.Fatal("unverified settings preservation was accepted or candidate was retained", out)
			}
			observedDifference := mode == "effective" || mode == "source" || mode == "applied" || mode == "permission"
			if (out.Validation != nil) != observedDifference || out.SourcesStable != observedDifference {
				t.Fatal("invalid evidence and observed differences were not distinguished", out)
			}
			if observedDifference && (out.Validation.Preservation.Matches || !out.Validation.PluginSettings[0].Matches) {
				t.Fatal("matching selected flag masked other setting changes", out.Validation)
			}
		})
	}
}

func TestClaudePluginSettingsPreviewCancellationCleansFile(t *testing.T) {
	m, scope := settingsPreviewFixture(t, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ClaudePluginSettingsPreview, 1)
	go func() {
		done <- PreviewClaudePluginSettings(ctx, filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local"})
	}()
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(filepath.Dir(m.root), "native-called")); err == nil {
			break
		}
		if time.Now().After(end) {
			t.Fatal("worker not started")
		}
	}
	cancel()
	select {
	case out := <-done:
		if len(out.Problems) == 0 || out.Cleanup.State != "released" || len(out.Cleanup.Issues) != 0 {
			t.Fatal("cancelled request did not clean up independently", out)
		}
		if _, err := os.Lstat(out.Artifact.File.Path); !os.IsNotExist(err) {
			t.Fatal("cancelled candidate remains")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled preview did not return")
	}
}

func TestClaudePluginSettingsPreviewIsolatesOldCleanupFailure(t *testing.T) {
	m, scope := settingsPreviewFixture(t, "valid")
	other := scope
	other.NativeID = "other-session"
	old, err := m.create(context.Background(), other, []ClaudePluginSetting{{SourceKey: "selected@local"}})
	if err != nil {
		t.Fatal(err)
	}
	old.file.Close()
	if err := os.WriteFile(old.record.Artifact.File.Path, []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	out := PreviewClaudePluginSettings(context.Background(), filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local"})
	if len(out.Problems) != 0 || out.Validation == nil || !out.Validation.PluginSettings[0].Matches || !out.SourcesStable || out.Cleanup.State != "released" || len(out.Cleanup.Issues) == 0 {
		t.Fatal("old recovery issue blocked or disappeared from an independent preview", out)
	}
	if data, err := os.ReadFile(old.record.Artifact.File.Path); err != nil || string(data) != `{"foreign":true}` {
		t.Fatal("unrelated changed file was removed or changed")
	}
}

func TestClaudePluginSettingsPreviewRejectsCurrentStartupConflicts(t *testing.T) {
	for _, layer := range []string{"custom", "user", "project", "local"} {
		for _, setting := range []string{"api-helper", "route", "state", "backend"} {
			t.Run(layer+"-"+setting, func(t *testing.T) {
				m, scope := settingsPreviewFixture(t, "valid")
				path := scope.Instance.SettingsFile
				switch layer {
				case "user":
					path = filepath.Join(scope.Instance.Profile, "settings.json")
				case "project":
					path = filepath.Join(scope.Cwd, ".claude/settings.json")
				case "local":
					path = filepath.Join(scope.Cwd, ".claude/settings.local.json")
				}
				value := map[string]any{}
				switch setting {
				case "api-helper":
					value["apiKeyHelper"] = "PRIVATE_HELPER_COMMAND"
				case "route":
					value["env"] = map[string]string{"ANTHROPIC_BASE_URL": "https://private.invalid"}
				case "state":
					value["env"] = map[string]string{"HOME": "/private/wrong-home"}
				case "backend":
					value["env"] = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}
				}
				body, _ := json.Marshal(value)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				out := PreviewClaudePluginSettings(context.Background(), filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local"})
				if len(out.Problems) == 0 || out.Validation != nil || out.Artifact != nil {
					t.Errorf("current startup conflict reached native validation: validation=%t, artifact=%t, problems=%v", out.Validation != nil, out.Artifact != nil, out.Problems)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(m.root), "native-called")); !os.IsNotExist(err) {
					t.Error("conflicting current settings launched the native metadata worker")
				}
				after, _ := os.ReadFile(path)
				if string(after) != string(body) {
					t.Error("preview modified the conflicting source")
				}
				encoded, _ := json.Marshal(out)
				if strings.Contains(string(encoded), "PRIVATE_HELPER_COMMAND") || strings.Contains(string(encoded), "private.invalid") || strings.Contains(string(encoded), "/private/wrong-home") {
					t.Error("conflicting values escaped the diagnostic")
				}
			})
		}
	}
}

func TestClaudePluginSettingsPreviewChecksComposedCandidateAgain(t *testing.T) {
	m, scope := settingsPreviewFixture(t, "valid")
	readerPath := filepath.Join(filepath.Dir(m.root), "settings_preflight.js")
	reader, err := os.ReadFile(readerPath)
	if err != nil {
		t.Fatal(err)
	}
	mutation := fmt.Sprintf("from pathlib import Path\nPath(%q).write_text('{\"apiKeyHelper\":\"PRIVATE_LATE_HELPER\"}')\n", scope.Instance.SettingsFile)
	if err := os.WriteFile(readerPath, append([]byte(mutation), reader...), 0600); err != nil {
		t.Fatal(err)
	}
	out := PreviewClaudePluginSettings(context.Background(), filepath.Dir(m.root), scope, ClaudePluginSetting{SourceKey: "selected@local"})
	if len(out.Problems) == 0 || out.Validation != nil || out.Artifact == nil || out.Cleanup.State != "released" {
		t.Fatalf("composed candidate was not rejected and reclaimed: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.root), "native-called")); !os.IsNotExist(err) {
		t.Fatal("late helper reached native validation")
	}
	if _, err := os.Stat(out.Artifact.File.Path); !os.IsNotExist(err) {
		t.Fatal("rejected candidate was not reclaimed")
	}
	if strings.Contains(strings.Join(out.Problems, " "), "PRIVATE_LATE_HELPER") {
		t.Fatal("helper value escaped")
	}
}
