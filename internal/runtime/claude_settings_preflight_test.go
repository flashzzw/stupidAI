package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeStartupSettingsReaderRejectsUnusableEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "partial", "complete", "sdk", "foreign", "missing-files", "bad-hash", "contradictory", "registry-invalid-json", "registry-invalid-value", "registry-invalid-json-complete", "registry-invalid-value-complete", "file-version", "file-missing-identity", "file-bad-identity", "file-relative-resolution", "binding-version", "binding-bad-identity", "binding-relative-resolution", "policy-flags", "missing-policy-flags", "null-policy-flags", "missing-layer-policy-flags", "null-layer-policy-flags", "conflict-valid", "conflict-missing", "conflict-null", "conflict-kind", "conflict-source", "conflict-path", "conflict-registry", "conflict-limit", "drift", "oversized", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cwd, profile := filepath.Join(root, "work"), filepath.Join(root, "profile")
			for _, p := range []string{cwd, profile} {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			script := fmt.Sprintf(`import json,sys,os
r=json.load(sys.stdin)
mode=%q
d={'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-07T00:00:00Z','resolver_returned':True,'read_complete':True,'complete':False,'files':[],'sources':[],'registry':[],'issues':[],'limitations':['read_observation_not_admission']}
d['policy_helpers_present']=False
d['startup_conflicts']=[]
if mode.startswith('conflict-'):
 d['startup_conflicts']=[{'source':'file','path':r['cwd']+'/policy.json','kind':'state_environment'}]
 if mode=='conflict-missing':d.pop('startup_conflicts')
 if mode=='conflict-null':d['startup_conflicts']=None
 if mode=='conflict-kind':d['startup_conflicts'][0]['kind']='PRIVATE_INVALID_CONFLICT'
 if mode=='conflict-source':d['startup_conflicts'][0]['source']='unknown'
 if mode=='conflict-path':d['startup_conflicts'][0]['path']='relative.json'
 if mode=='conflict-registry':d['startup_conflicts'][0]={'source':'registry','kind':'route_environment'}
 if mode=='conflict-limit':d['startup_conflicts']=d['startup_conflicts']*4097
if mode=='partial':d.update(read_complete=False,issues=[{'path':r['cwd'],'operation':'read','code':'EACCES'}])
if mode=='complete':d['complete']=True
if mode=='sdk':d['sdk_version']='other'
if mode=='foreign':d['cwd']='/foreign'
if mode=='missing-files':d.pop('files')
if mode=='bad-hash':d['sources']=[{'source':'user','settings_sha256':'x'*64}]
if mode=='contradictory':d['registry']=[{'hive':'hklm','outcome':'unavailable'}]
if mode.startswith('file-'):
 d['files']=[{'path':r['cwd']+'/observed.json','resolved_path':r['cwd']+'/observed.json','status':'read','sha256':'b'*64,'identity_sha256':'a'*64,'bytes':2}]
 if mode=='file-missing-identity':d['files'][0].pop('identity_sha256')
 if mode=='file-bad-identity':d['files'][0]['identity_sha256']='bad'
 if mode=='file-relative-resolution':d['files'][0]['resolved_path']='relative.json'
if mode.startswith('binding-'):
 d['path_bindings']=[{'path':r['cwd']+'/alias.json','resolved_path':r['cwd']+'/observed.json','identity_sha256':'a'*64}]
 if mode=='binding-bad-identity':d['path_bindings'][0]['identity_sha256']='bad'
 if mode=='binding-relative-resolution':d['path_bindings'][0]['resolved_path']='relative.json'
if mode.startswith('registry-invalid-'):
 d.update(read_complete=mode.endswith('-complete'),registry=[{'hive':'hklm','outcome':'invalid_json' if mode.startswith('registry-invalid-json') else 'invalid_value'}])
for s in d['sources']:s['has_policy_helpers']=False
if mode=='policy-flags':d.update(policy_helpers_present=True,sources=[{'source':'managed','settings_sha256':'a'*64,'has_policy_helpers':True}])
if mode=='missing-policy-flags':d.pop('policy_helpers_present')
if mode=='null-policy-flags':d['policy_helpers_present']=None
if mode in ['missing-layer-policy-flags','null-layer-policy-flags']:
 d['sources']=[{'source':'managed','settings_sha256':'a'*64}]
 if mode=='null-layer-policy-flags':d['sources'][0]['has_policy_helpers']=None
if mode=='drift':
 os.makedirs(r['cwd']+'/.claude',exist_ok=True)
 open(r['cwd']+'/.claude/settings.json','w').write('{}')
if mode=='oversized':print('x'*(2<<20))
else:print(json.dumps(d))
`, mode)
			if err := os.WriteFile(filepath.Join(root, "settings_preflight.js"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			scope := Scope{Cwd: cwd, Instance: Instance{Kind: "claude", Profile: profile, Bridge: filepath.Join(root, "main.js"), NodeExecutable: "python3"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			result, err := ReadClaudeStartupSettings(ctx, scope)
			partialRegistry := strings.HasPrefix(mode, "registry-invalid-") && !strings.HasSuffix(mode, "-complete")
			versioned := mode == "file-version" || mode == "binding-version"
			valid := mode == "valid" || mode == "partial" || partialRegistry || versioned || mode == "policy-flags" || mode == "conflict-valid"
			if (err == nil) != valid {
				t.Fatalf("mode %s: result=%+v error=%v", mode, result, err)
			}
			if err != nil && strings.Contains(err.Error(), "xxxxxxxx") {
				t.Fatal("raw worker output leaked")
			}
			if valid && (result.Complete || len(result.BoundLocalFiles) != 2 || result.ReadComplete != (mode == "valid" || versioned || mode == "policy-flags" || mode == "conflict-valid")) {
				t.Fatal("unexpected evidence scope")
			}
			if mode == "policy-flags" && (!result.PolicyHelpersPresent || len(result.Sources) != 1 || !result.Sources[0].HasPolicyHelpers) {
				t.Fatal("reader discarded policy helper declarations")
			}
			if mode == "conflict-valid" && (len(result.StartupConflicts) != 1 || result.StartupConflicts[0].Kind != "state_environment" || result.StartupConflicts[0].Path != filepath.Join(cwd, "policy.json")) {
				t.Fatal("reader discarded observed startup binding conflict")
			}
			if partialRegistry && (len(result.Registry) != 1 || result.Registry[0].Outcome != strings.ReplaceAll(strings.TrimPrefix(mode, "registry-"), "-", "_")) {
				t.Fatal("invalid registry value was hidden")
			}
			if versioned {
				body, _ := json.Marshal(result)
				var encoded map[string]any
				if json.Unmarshal(body, &encoded) != nil {
					t.Fatal("invalid serialized metadata")
				}
				key := "files"
				if mode == "binding-version" {
					key = "path_bindings"
				}
				rows, ok := encoded[key].([]any)
				if !ok || len(rows) != 1 {
					t.Fatal("reader discarded file binding evidence")
				}
				row := rows[0].(map[string]any)
				if row["identity_sha256"] != strings.Repeat("a", 64) || row["resolved_path"] != filepath.Join(cwd, "observed.json") {
					t.Fatal("reader discarded file version or resolution")
				}
			}
		})
	}
}
