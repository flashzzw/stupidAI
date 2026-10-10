package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func originalQueryRecoveryFixture(t *testing.T, installed bool) (Intent, claudeSnapshot, ConfigurationRecoveryCheck) {
	t.Helper()
	in, s := configurationFailureFixture()
	in.Scope.ClaudeSettingsSources = in.PreviousScope.ClaudeSettingsSources
	in.Scope.ClaudeInstalledSettings = &ClaudeInstalledSettings{Version: 1, Artifact: ClaudeDerivedSettingsFile{ID: "candidate"}}
	if installed {
		in.PreviousScope.ClaudeInstalledSettings = &ClaudeInstalledSettings{Version: 1, Artifact: ClaudeDerivedSettingsFile{ID: "original"}}
		s.Config.InstalledSettings = in.PreviousScope.ClaudeInstalledSettings
		s.Config.SettingsArtifact = &in.PreviousScope.ClaudeInstalledSettings.Artifact
	}
	s.Configuration.SettingsPrevious, s.Configuration.SettingsTarget = nil, nil
	s.Configuration.InstalledTarget, s.Configuration.InstalledPrevious = in.Scope.ClaudeInstalledSettings, in.PreviousScope.ClaudeInstalledSettings
	s.Configuration.Progress = &claudeConfigurationProgress{Version: 1, Phase: "original"}
	s.ConfigurationFailure.Stage, s.ConfigurationFailure.Reason, s.ConfigurationFailure.Detail = "open_original", "stage_failed", ""
	s.Entries = []claudeEntry{{Kind: "initialized"}, {Kind: "closed"}}
	s.HistoryDigest = strings.Repeat("e", 64)
	s.ConfigurationBaseline = in.ConfigurationBaseline
	out := ConfigurationRecoveryCheck{Version: 1, NativeID: in.Scope.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline,
		At: "2026-10-10T00:00:00Z", ReadOnly: true, Source: "claude.configuration_recovery",
		OriginalQueryRetry: &ClaudeOriginalQueryRecovery{Version: 1, Failure: *s.ConfigurationFailure}}
	for _, kind := range []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"} {
		out.Checks = append(out.Checks, RecoveryCondition{Kind: kind, Status: "passed"})
	}
	return in, s, out
}

func TestClaudeOriginalQueryRecoveryRequiresDurablePreCandidateEvidence(t *testing.T) {
	for _, mode := range []string{"inherited", "installed", "components", "missing-phase", "unknown-version", "candidate-started", "unknown-phase", "missing-retry", "retry-version", "failure-operation", "failure-baseline", "failure-time", "different-failure-time", "failure-stage", "failure-reason", "failure-cleanup", "secret-detail", "missing-failure", "already-recovering", "already-recovered", "original-inventory", "unavailable-inventory", "null-inventory", "current-candidate", "wrong-original-mcp", "live-original", "active-input"} {
		t.Run(mode, func(t *testing.T) {
			in, s, out := originalQueryRecoveryFixture(t, mode == "installed")
			c := s.Configuration
			switch mode {
			case "components":
				s.ConfigurationFailure.Stage, out.OriginalQueryRetry.Failure.Stage = "check_original_components", "check_original_components"
			case "missing-phase":
				c.Progress = nil
			case "unknown-version":
				c.Progress.Version = 2
			case "candidate-started":
				c.Progress.Phase = "candidate"
			case "unknown-phase":
				c.Progress.Phase = "maybe"
			case "missing-retry":
				out.OriginalQueryRetry = nil
			case "retry-version":
				out.OriginalQueryRetry.Version = 2
			case "failure-operation":
				out.OriginalQueryRetry.Failure.Operation = "another"
			case "failure-baseline":
				out.OriginalQueryRetry.Failure.Baseline = strings.Repeat("d", 64)
			case "failure-time":
				out.OriginalQueryRetry.Failure.At = "invalid"
			case "different-failure-time":
				out.OriginalQueryRetry.Failure.At = "2026-10-10T01:00:00Z"
			case "failure-stage":
				out.OriginalQueryRetry.Failure.Stage = "open_candidate"
			case "failure-reason":
				out.OriginalQueryRetry.Failure.Reason = "settings_parse_errors"
			case "failure-cleanup":
				out.OriginalQueryRetry.Failure.Cleanup = "unconfirmed"
			case "secret-detail":
				out.OriginalQueryRetry.Failure.Detail = "PRIVATE_NATIVE_ERROR"
			case "missing-failure":
				s.ConfigurationFailure = nil
			case "already-recovering":
				c.Recovery = &claudeConfigurationRecovery{Operation: "recovery", Status: "recovering"}
			case "already-recovered":
				c.Status = "recovered"
			case "original-inventory":
				c.OriginalPlugins = &claudePluginInventory{Available: true}
			case "unavailable-inventory":
				c.OriginalPlugins = &claudePluginInventory{}
			case "null-inventory":
				body, _ := json.Marshal(c)
				body = append(body[:len(body)-1], []byte(`,"original_plugins":null}`)...)
				if err := json.Unmarshal(body, c); err != nil {
					t.Fatal(err)
				}
			case "current-candidate":
				s.Config.InstalledSettings, s.Config.SettingsArtifact = in.Scope.ClaudeInstalledSettings, &in.Scope.ClaudeInstalledSettings.Artifact
			case "wrong-original-mcp":
				c.Previous = []MCPSelection{{Server: "another"}}
			case "live-original":
				s.Live = true
			case "active-input":
				s.Active = "unfinished"
			}
			err := ValidateConfigurationRecoveryCheck(in, out)
			if err == nil {
				err = validateClaudeOriginalQueryRecoveryCheck(in, s, out)
			}
			want := mode == "inherited" || mode == "installed" || mode == "components"
			if (err == nil) != want {
				t.Fatalf("pre-candidate evidence accepted=%v, want=%v: %v", err == nil, want, err)
			}
		})
	}
}

func originalRecoveryInventory(t *testing.T) claudePluginInventory {
	t.Helper()
	var inventory claudePluginInventory
	if err := json.Unmarshal([]byte(`{"available":true,"packages":[{"name":"inherited","path":"/owned/inherited","source":"inherited@owned","version":"1"}],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}}`), &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func TestClaudeOriginalQueryRecoveryProjectionKeepsSeparateInventory(t *testing.T) {
	for _, mode := range []string{"offline", "live", "later-history", "missing-proof", "candidate-phase", "invented-original", "missing-recovery-inventory", "unavailable-recovery-inventory", "changed-package", "missing-current-skills", "changed-policy", "unverified-recovery"} {
		t.Run(mode, func(t *testing.T) {
			in, s, out := originalQueryRecoveryFixture(t, false)
			inventory := originalRecoveryInventory(t)
			s.Configuration.Status = "recovered"
			s.Configuration.Recovery = &claudeConfigurationRecovery{Operation: "recovery", Status: "verified", At: out.At, HistoryDigest: s.HistoryDigest,
				OriginalQueryRetry: out.OriginalQueryRetry, Plugins: inventory, Inventory: inventory.MCP, Skills: inventory.Skills}
			s.Live = mode != "offline"
			s.Plugins, s.MCP, s.Skills = originalRecoveryInventory(t), inventory.MCP, inventory.Skills
			if s.Live {
				s.Entries = append(s.Entries, claudeEntry{Kind: "initialized"})
			}
			switch mode {
			case "later-history":
				s.HistoryDigest = strings.Repeat("f", 64)
			case "missing-proof":
				s.Configuration.Recovery.OriginalQueryRetry = nil
			case "candidate-phase":
				s.Configuration.Progress.Phase = "candidate"
			case "invented-original":
				s.Configuration.OriginalPlugins = &inventory
			case "missing-recovery-inventory":
				s.Configuration.Recovery.Plugins = claudePluginInventory{}
			case "unavailable-recovery-inventory":
				s.Configuration.Recovery.Plugins.Available = false
			case "changed-package":
				s.Plugins.Packages[0].Path = "/changed/cache"
			case "missing-current-skills":
				s.Plugins.Skills.Commands = nil
			case "changed-policy":
				s.Plugins.Hooks.Policy.ManagedOnly = true
			case "unverified-recovery":
				s.Configuration.Recovery.Status = "recovering"
			}
			want := mode == "offline" || mode == "live" || mode == "later-history"
			v := projectClaude(*in.PreviousScope, s)
			if v.ExecutionClear != want {
				t.Fatalf("recovery projection clear=%v, want=%v: %+v", v.ExecutionClear, want, v.Evidence)
			}
		})
	}
}

func TestClaudeOriginalQueryRecoveryRejectsUnadvertisedBridge(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdio fixture required")
	}
	for _, capability := range []int{-1, 2, 1} {
		t.Run(fmt.Sprint(capability), func(t *testing.T) {
			root := t.TempDir()
			in, snapshot, report := originalQueryRecoveryFixture(t, false)
			response, _ := json.Marshal(struct {
				ConfigurationRecoveryCheck
				Snapshot claudeSnapshot `json:"snapshot"`
			}{report, snapshot})
			code := fmt.Sprintf(`import json,sys
from pathlib import Path
calls=Path(sys.argv[1])/'calls.jsonl'
response=json.loads(%q)
for line in sys.stdin:
 r=json.loads(line)
 with calls.open('a') as f: f.write(json.dumps(r['method'])+'\n')
 result=response
 if r['method']=='hello':
  result={'version':1,'sdk':'0.3.280','installed_settings':1,'settings_file_consumer':1}
  if %d >= 0: result['original_query_recovery']=%d
 print(json.dumps({'v':1,'id':r['id'],'result':result}),flush=True)
`, string(response), capability, capability)
			bridge := filepath.Join(root, "bridge.py")
			if err := os.WriteFile(bridge, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			in.Scope.Instance.ID, in.Scope.Instance.Profile, in.Scope.Instance.Bridge, in.Scope.Instance.NodeExecutable = "fixture", root, bridge, python
			in.PreviousScope.Instance = in.Scope.Instance
			adapter := NewClaude(root)
			t.Cleanup(func() { _ = adapter.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, readErr := adapter.ReadConfigurationRecovery(ctx, in)
			if capability == 1 {
				if readErr != nil || out.OriginalQueryRetry == nil {
					t.Fatal("advertised read-only recovery proof was not decoded", readErr)
				}
			} else if readErr == nil || !strings.Contains(readErr.Error(), "没有声明原查询恢复协议") {
				t.Fatal("unadvertised recovery mode was accepted", readErr)
			}
			if capability != 1 {
				p, err := adapter.client(ctx, in.Scope.Instance)
				if err != nil {
					t.Fatal(err)
				}
				recovery := Intent{ID: "recovery", Kind: "recover_execution", Scope: *in.PreviousScope, ConfigurationTarget: &in.Scope, ConfigurationRecoveryOf: in.ID, ConfigurationBaseline: in.ConfigurationBaseline, Permission: in.Permission, ClaudeOriginalQueryRecovery: report.OriginalQueryRetry}
				if _, err := adapter.recoverExecution(ctx, p, recovery); err == nil || !strings.Contains(err.Error(), "bridge 不支持") {
					t.Fatal("explicit original-query recovery reached an old bridge", err)
				}
			}
			calls, err := os.ReadFile(filepath.Join(root, "runtimes/fixture/calls.jsonl"))
			if err != nil || string(calls) != "\"hello\"\n\"configuration_recovery\"\n" {
				t.Fatal("read-only check or rejected dispatch performed another native action", string(calls), err)
			}
		})
	}
}

// Optional cross-language contract: consume production Session reports from
// the SDK test. Those reports use controlled SDK I/O, not a native CLI.
func TestClaudeOriginalQueryRecoverySDKReports(t *testing.T) {
	root := os.Getenv("NETCAP_ORIGINAL_RECOVERY_SDK_REPORTS")
	if root == "" {
		t.Skip("owned SDK reports not requested")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*/result.json"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		var result struct {
			Mode      string                  `json:"mode"`
			Completed bool                    `json:"completed"`
			Config    json.RawMessage         `json:"config"`
			Target    ClaudeInstalledSettings `json:"target"`
			Snapshot  claudeSnapshot          `json:"snapshot"`
			Reports   struct {
				Check struct {
					ConfigurationRecoveryCheck
					Snapshot claudeSnapshot `json:"snapshot"`
				} `json:"check"`
				Receipt Receipt `json:"receipt"`
			} `json:"reports"`
		}
		body, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(body, &result) != nil {
			t.Fatal("invalid SDK report", path, err)
		}
		if !result.Completed || result.Reports.Receipt.ClaudeOriginalQueryRecovery == nil {
			continue
		}
		proof := result.Reports.Receipt.ClaudeOriginalQueryRecovery
		s := result.Reports.Check.Snapshot
		original := Scope{NativeID: s.Config.ID, Cwd: s.Config.Cwd, Instance: Instance{Kind: "claude", Executable: s.Config.Executable}, Route: Route{Model: s.Config.Model, Endpoint: s.Config.Endpoint}, SettingsGuardVersion: s.Config.SettingsGuardVersion, ClaudeSettingsSources: s.Config.SettingsSources, ClaudeInstalledSettings: s.Config.InstalledSettings}
		failed := original
		failed.ClaudeInstalledSettings = &result.Target
		in := Intent{ID: proof.Failure.Operation, Kind: "apply_execution", Scope: failed, PreviousScope: &original, Permission: s.Config.Permission, ConfigurationBaseline: proof.Failure.Baseline}
		if err := ValidateConfigurationRecoveryCheck(in, result.Reports.Check.ConfigurationRecoveryCheck); err != nil {
			t.Fatal(path, err)
		}
		if err := validateClaudeOriginalQueryRecoveryCheck(in, s, result.Reports.Check.ConfigurationRecoveryCheck); err != nil {
			t.Fatal(path, err)
		}
		if !reflect.DeepEqual(proof, result.Reports.Check.OriginalQueryRetry) || !projectClaude(original, result.Snapshot).ExecutionClear || result.Snapshot.Configuration.OriginalPlugins != nil {
			t.Fatal("SDK recovery report lost its separate proof, inventory or original binding", path)
		}
		seen[result.Mode] = true
	}
	if !seen["inherited"] || !seen["installed"] {
		t.Fatal("both complete original binding paths are required", seen)
	}
}
