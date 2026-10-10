package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Real source-file admission and transport; the stdio peer controls native
// responses so missing echoes cannot masquerade as a safely unsent recovery.
func TestClaudeOriginalQueryRecoveryWireModeAndReceipt(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdio fixture required")
	}
	t.Setenv("NETCAP_ORIGINAL_WIRE_KEY", "owned-fixture")
	for _, mode := range []string{"reviewed", "missing-explicit-mode", "stale-explicit-mode", "candidate-phase", "missing-echo", "wrong-echo", "source-drift"} {
		t.Run(mode, func(t *testing.T) {
			m, original := settingsFileFixture(t)
			root := filepath.Dir(original.Cwd)
			original.Instance.NodeExecutable, original.Instance.Bridge = python, filepath.Join(root, "bridge.py")
			original.SettingsGuardVersion = 1
			original.Route = Route{Model: "fixture", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_ORIGINAL_WIRE_KEY"}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			original.ClaudeSettingsSources, err = ReadClaudeLocalSettingsSources(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			worker := `import json,sys,hashlib
from pathlib import Path
r=json.load(sys.stdin);files=[]
def key(s):return ':'.join(str(x) for x in [s.st_dev,s.st_ino,s.st_mode,s.st_uid,s.st_gid,s.st_nlink,s.st_size,s.st_mtime_ns,s.st_ctime_ns])
for path in r['additional_files']:
 p=Path(path)
 if not p.exists():files.append({'path':path,'status':'absent'});continue
 data=p.read_bytes();identity=json.dumps([str(p.resolve()),key(p.stat()),key(p.lstat())],separators=(',',':'))
 files.append({'path':path,'status':'read','sha256':hashlib.sha256(data).hexdigest(),'bytes':len(data),'resolved_path':str(p.resolve()),'identity_sha256':hashlib.sha256(identity.encode()).hexdigest()})
print(json.dumps({'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-10T00:00:00Z','resolver_returned':True,'read_complete':True,'complete':False,'files':files,'sources':[],'registry':[],'issues':[],'limitations':['controlled_native_source_reader'],'policy_helpers_present':False,'startup_conflicts':[]}))
`
			if err := os.WriteFile(filepath.Join(root, "settings_preflight.js"), []byte(worker), 0600); err != nil {
				t.Fatal(err)
			}
			startup, err := ReadClaudeStartupSettings(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			artifact, err := PlanClaudeDerivedSettingsFile(original, []ClaudePluginSetting{{SourceKey: "selected@owned", Enabled: false}}, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			failed := original
			failed.ClaudeInstalledSettings = &ClaudeInstalledSettings{Version: 1, Artifact: artifact, Selections: []PluginSelection{{Name: "selected", NativeSource: "selected@owned", Mode: "disabled", Version: "1", SourcePath: original.Cwd, SourceHash: strings.Repeat("a", 64)}}, SourceFiles: []FileSource{artifact.Original}, StartupFiles: startup.Files, StartupSourceVersion: startup.Fingerprint()}
			in, snapshot, report := originalQueryRecoveryFixture(t, false)
			in.Scope, in.PreviousScope = failed, &original
			report.NativeID = original.NativeID
			snapshot.Config.ID, snapshot.Config.Cwd, snapshot.Config.Model, snapshot.Config.Endpoint = original.NativeID, original.Cwd, original.Route.Model, original.Route.Endpoint
			snapshot.Config.Executable, snapshot.Config.SettingsFile, snapshot.Config.SettingsSources = original.Instance.Executable, original.Instance.SettingsFile, original.ClaudeSettingsSources
			snapshot.Configuration.InstalledTarget = failed.ClaudeInstalledSettings
			recovery := Intent{ID: uuid.NewString(), Kind: "recover_execution", Scope: original, ConfigurationTarget: &failed, ConfigurationRecoveryOf: in.ID, ConfigurationBaseline: in.ConfigurationBaseline, Permission: in.Permission,
				ClaudeOriginalQueryRecovery: &ClaudeOriginalQueryRecovery{Version: 1, Failure: report.OriginalQueryRetry.Failure}}
			if mode == "missing-explicit-mode" {
				recovery.ClaudeOriginalQueryRecovery = nil
			}
			if mode == "stale-explicit-mode" {
				recovery.ClaudeOriginalQueryRecovery.Failure.At = "2026-10-10T01:00:00Z"
			}
			if mode == "candidate-phase" {
				snapshot.Configuration.Progress.Phase = "candidate"
			}
			response, _ := json.Marshal(struct {
				ConfigurationRecoveryCheck
				Snapshot claudeSnapshot `json:"snapshot"`
			}{report, snapshot})
			code := fmt.Sprintf(`import json,sys
from pathlib import Path
calls=Path(sys.argv[1])/'calls.jsonl';mode=%q
report=json.loads(%q)
for line in sys.stdin:
 r=json.loads(line);method=r['method'];params=r['params']
 with calls.open('a') as f:f.write(json.dumps({'method':method,'original_query_retry':params.get('original_query_retry')})+'\n')
 if method=='hello':result={'version':1,'sdk':'0.3.280','settings_file_consumer':1,'installed_settings':1,'original_query_recovery':1}
 elif method=='configuration_recovery':result=report
 elif method=='recover_execution':
  assert params.get('original_query_retry') is True
  result={'native_id':params['native_id'],'original_query_retry':report['original_query_retry']}
  if mode=='missing-echo':result.pop('original_query_retry')
  if mode=='wrong-echo':result['original_query_retry']['failure']['baseline']='d'*64
 else:raise RuntimeError('unexpected native action')
 print(json.dumps({'v':1,'id':r['id'],'result':result}),flush=True)
`, mode, string(response))
			if err := os.WriteFile(original.Instance.Bridge, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "source-drift" {
				if err := os.WriteFile(original.Instance.SettingsFile, []byte(`{"unreviewed":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			adapter := NewClaude(filepath.Dir(m.root))
			t.Cleanup(func() { _ = adapter.Close() })
			p, err := adapter.client(ctx, original.Instance)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := adapter.recoverExecution(ctx, p, recovery)
			recoveryErr := err
			if (err == nil) != (mode == "reviewed") {
				t.Fatalf("wire mode %s: %v", mode, err)
			}
			if mode == "reviewed" && !reflect.DeepEqual(receipt.ClaudeOriginalQueryRecovery, recovery.ClaudeOriginalQueryRecovery) {
				t.Fatal("native echo was not bound to the chosen mode")
			}
			var unsent *NotSent
			afterDispatch := mode == "missing-echo" || mode == "wrong-echo"
			if err != nil && errors.As(err, &unsent) == afterDispatch {
				t.Fatal("missing reply became safely unsent, or rejected request became uncertain", err)
			}
			body, err := os.ReadFile(filepath.Join(filepath.Dir(m.root), "runtimes", original.Instance.ID, "calls.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			calls := []struct {
				Method             string `json:"method"`
				OriginalQueryRetry *bool  `json:"original_query_retry"`
			}{}
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				var call struct {
					Method             string `json:"method"`
					OriginalQueryRetry *bool  `json:"original_query_retry"`
				}
				if json.Unmarshal([]byte(line), &call) != nil {
					t.Fatal("invalid transport observation")
				}
				calls = append(calls, call)
			}
			want := 2
			if mode == "reviewed" || afterDispatch {
				want = 3
			}
			if mode == "source-drift" {
				want = 1
			}
			if len(calls) != want || calls[0].Method != "hello" || want > 1 && calls[1].Method != "configuration_recovery" || want == 3 && (calls[2].Method != "recover_execution" || calls[2].OriginalQueryRetry == nil || !*calls[2].OriginalQueryRetry) {
				t.Fatalf("unexpected native calls: %+v", calls)
			}
			if dir := os.Getenv("NETCAP_ORIGINAL_RECOVERY_WIRE_REPORTS"); dir != "" {
				body, encodeErr := json.MarshalIndent(map[string]any{"mode": mode, "transport": "production Go transport with a controlled stdio peer", "intent": recovery, "check": report, "snapshot": snapshot, "receipt": receipt, "calls": calls, "expected_calls": want, "error": fmt.Sprint(recoveryErr), "not_sent": unsent != nil, "passed": !t.Failed()}, "", "  ")
				if encodeErr != nil || os.WriteFile(filepath.Join(dir, mode+".json"), append(body, '\n'), 0600) != nil {
					t.Error("cannot write owned transport evidence", encodeErr)
				}
			}
		})
	}
}
