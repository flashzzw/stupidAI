package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installedRecoveryPlanFixture(t *testing.T) (Scope, Scope, *ClaudeInstalledSettingsRecovery) {
	t.Helper()
	clone := func(value any, into any) {
		body, _ := json.Marshal(value)
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatal(err)
		}
	}
	hash := func(letter string) string { return strings.Repeat(letter, 64) }
	size := int64(2)
	previous := Scope{NativeID: "native", Cwd: "/owned", Route: Route{Model: "fixture", Endpoint: "http://127.0.0.1:1"}, Instance: Instance{Kind: "claude", Executable: "/owned/claude"}, SettingsGuardVersion: 1, ClaudeSettingsSources: []ClaudeSettingsSource{
		{Path: "/owned/settings.json", ResolvedPath: "/owned/settings.json", Kind: "file", Mode: 0600, SHA256: hash("a")},
		{Path: "/owned/settings.local.json", ResolvedPath: "/owned/settings.local.json", Kind: "file", Mode: 0600, SHA256: hash("b")},
	}}
	saved := ClaudeStartupSettings{Version: 1, SDKVersion: "0.3.280", ResolverReturned: true, ReadComplete: true, Source: "claude.sdk.observed_startup_reads", Cwd: "/owned", BoundLocalFiles: previous.ClaudeSettingsSources,
		StartupConflicts: []ClaudeStartupConflict{}, Files: []ClaudeStartupFile{
			{Path: "/owned/settings.json", ResolvedPath: "/owned/settings.json", Status: "read", SHA256: hash("a"), Bytes: &size, IdentitySHA256: hash("c")},
			{Path: "/owned/settings.local.json", ResolvedPath: "/owned/settings.local.json", Status: "read", SHA256: hash("b"), Bytes: &size, IdentitySHA256: hash("d")},
			{Path: "/profile/settings.json", ResolvedPath: "/profile/settings.json", Status: "read", SHA256: hash("e"), Bytes: &size, IdentitySHA256: hash("f")}},
		PathBindings: []ClaudeStartupPathBinding{{Path: "/owned/settings.local.json", ResolvedPath: "/owned/settings.local.json", IdentitySHA256: hash("d")}},
		Sources:      []ClaudeStartupLayer{{Source: "user", Path: "/profile/settings.json", SettingsSHA256: hash("e")}}, Registry: []ClaudeStartupRegistry{{Hive: "hklm", Outcome: "absent"}}, Issues: []ClaudeStartupIssue{}, Limitations: []string{"observed_only"}}
	previous.ClaudeInstalledSettings = &ClaudeInstalledSettings{Version: 1, StartupFiles: saved.Files, StartupSourceVersion: saved.Fingerprint(),
		Artifact: ClaudeDerivedSettingsFile{Original: FileSource{Kind: "absent"}, File: FileSource{SHA256: hash("a")}}}
	var failed Scope
	clone(previous, &failed)
	failed.ClaudeSettingsSources[1].SHA256 = hash("a")
	var current ClaudeStartupSettings
	clone(saved, &current)
	current.Files[1].IdentitySHA256, current.PathBindings[0].IdentitySHA256 = hash("c"), hash("c")
	var binding ClaudeInstalledSettings
	clone(previous.ClaudeInstalledSettings, &binding)
	binding.StartupFiles, binding.StartupSourceVersion = current.Files, current.Fingerprint()
	return previous, failed, &ClaudeInstalledSettingsRecovery{Version: 1, OriginalStartup: saved, Startup: current, Binding: binding}
}

func TestInstalledRecoveryProjectionKeepsNewIdentityAndOriginalFailure(t *testing.T) {
	for _, mode := range []string{"recovered", "missing-new-binding", "missing-original-binding", "missing-failed-local-version", "failure-original-metadata", "failure-restored-metadata", "failure-wrong-operation"} {
		t.Run(mode, func(t *testing.T) {
			previous, failed, plan := installedRecoveryPlanFixture(t)
			in := Intent{ID: "recovery", Kind: "recover_execution", Scope: previous, Permission: "read_only", ConfigurationTarget: &failed, ConfigurationRecoveryOf: "apply", ConfigurationBaseline: strings.Repeat("c", 64), ClaudeInstalledRecovery: plan}
			restored, err := claudeRecoveredScope(in)
			if err != nil {
				t.Fatal(err)
			}
			var inventory claudePluginInventory
			if err := json.Unmarshal([]byte(`{"available":true,"packages":[],"skills":{"available":true,"commands":[],"descriptions":[]},"agents":[],"hooks":{"available":true,"items":[],"policy":{}},"mcp":{"available":true,"servers":[]}}`), &inventory); err != nil {
				t.Fatal(err)
			}
			var snapshot claudeSnapshot
			snapshot.Config.ID, snapshot.Config.Cwd, snapshot.Config.Model, snapshot.Config.Endpoint, snapshot.Config.Permission, snapshot.Config.Executable = previous.NativeID, previous.Cwd, previous.Route.Model, previous.Route.Endpoint, in.Permission, previous.Instance.Executable
			snapshot.Config.SettingsGuardVersion, snapshot.Config.SettingsSources = 1, previous.ClaudeSettingsSources
			snapshot.Config.InstalledSettings, snapshot.Config.SettingsArtifact = &plan.Binding, &plan.Binding.Artifact
			snapshot.Entries = []claudeEntry{{Kind: "initialized"}, {Kind: "closed"}}
			snapshot.HistoryDigest = strings.Repeat("e", 64)
			snapshot.Configuration = &claudeConfigurationChange{Operation: "apply", Baseline: in.ConfigurationBaseline, Status: "recovered", SettingsPrevious: previous.ClaudeSettingsSources, SettingsTarget: failed.ClaudeSettingsSources,
				InstalledPrevious: previous.ClaudeInstalledSettings, InstalledTarget: failed.ClaudeInstalledSettings, OriginalPlugins: &inventory,
				Recovery: &claudeConfigurationRecovery{Operation: in.ID, Status: "verified", At: "2026-10-10T00:00:00Z", HistoryDigest: snapshot.HistoryDigest, InstalledSettings: &plan.Binding, Plugins: inventory, Inventory: inventory.MCP}}
			switch mode {
			case "missing-new-binding":
				snapshot.Configuration.Recovery.InstalledSettings = nil
			case "missing-original-binding":
				snapshot.Configuration.InstalledPrevious = nil
			case "missing-failed-local-version":
				snapshot.Configuration.SettingsTarget = nil
			}
			if strings.HasPrefix(mode, "failure-") {
				snapshot.Configuration.Status, snapshot.Configuration.Recovery.Status = "applying", "recovering"
				snapshot.ConfigurationRecoveryFailure = &ConfigurationFailure{Version: 1, Operation: in.ID, Baseline: in.ConfigurationBaseline, Stage: "open_candidate", Reason: "stage_failed", Cleanup: "confirmed", At: "2026-10-10T00:00:00Z"}
				if mode == "failure-original-metadata" {
					snapshot.Config.InstalledSettings, snapshot.Config.SettingsArtifact = previous.ClaudeInstalledSettings, &previous.ClaudeInstalledSettings.Artifact
				}
				if mode == "failure-wrong-operation" {
					snapshot.ConfigurationRecoveryFailure.Operation = "another"
				}
				if (claudeRecoveryFailure(in, snapshot) != nil) != (mode != "failure-wrong-operation") {
					t.Fatal("recovery failure lost its original operation or new identity version")
				}
				return
			}
			if projectClaude(restored, snapshot).ExecutionClear != (mode == "recovered") {
				t.Fatal("recovery projection accepted missing identity evidence or rejected the verified new version")
			}
		})
	}
}

func TestInstalledRecoveryVersionRetainsAllOtherObservedSources(t *testing.T) {
	for _, mode := range []string{"restored", "local-content", "local-resolution", "unadopted-local-identity", "nonlocal-identity", "registry", "source-layer", "path-resolution", "policy", "missing-local-binding", "missing-identity"} {
		t.Run(mode, func(t *testing.T) {
			previous, failed, plan := installedRecoveryPlanFixture(t)
			switch mode {
			case "local-content":
				plan.Startup.Files[1].SHA256 = strings.Repeat("a", 64)
			case "local-resolution":
				plan.Startup.Files[1].ResolvedPath = "/elsewhere/settings.local.json"
			case "unadopted-local-identity":
				plan.Startup.Files[0].IdentitySHA256 = strings.Repeat("a", 64)
			case "nonlocal-identity":
				plan.Startup.Files[2].IdentitySHA256 = strings.Repeat("a", 64)
			case "registry":
				plan.Startup.Registry[0].Outcome = "present"
			case "source-layer":
				plan.Startup.Sources[0].SettingsSHA256 = strings.Repeat("a", 64)
			case "path-resolution":
				plan.Startup.PathBindings[0].ResolvedPath = "/elsewhere/settings.local.json"
			case "policy":
				plan.Startup.PolicyHelpersPresent = true
			case "missing-local-binding":
				plan.Startup.BoundLocalFiles = nil
			case "missing-identity":
				plan.Startup.Files[1].IdentitySHA256 = ""
			}
			// Keep the proposed fingerprint internally consistent. Rejection must
			// follow the changed source, not an accidentally stale test digest.
			plan.Binding.StartupFiles, plan.Binding.StartupSourceVersion = plan.Startup.Files, plan.Startup.Fingerprint()
			if ValidClaudeInstalledSettingsRecovery(previous, failed, plan) != (mode == "restored") {
				t.Fatal("restoration admitted an unapproved source change")
			}
		})
	}
}

func TestInstalledRecoveryCheckRequiresTheReviewedBinding(t *testing.T) {
	for _, mode := range []string{"reviewed", "missing", "original", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			previous, failed, plan := installedRecoveryPlanFixture(t)
			in := Intent{ID: "failed-apply", Kind: "apply_execution", Scope: failed, PreviousScope: &previous, ConfigurationBaseline: strings.Repeat("c", 64), ClaudeInstalledRecovery: plan}
			out := ConfigurationRecoveryCheck{Version: 1, NativeID: failed.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline,
				At: "2026-10-10T00:00:00Z", Source: "claude.configuration_recovery", ReadOnly: true, InstalledRecovery: &plan.Binding}
			for _, kind := range []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"} {
				out.Checks = append(out.Checks, RecoveryCondition{Kind: kind, Status: "passed"})
			}
			switch mode {
			case "missing":
				out.InstalledRecovery = nil
			case "original":
				out.InstalledRecovery = previous.ClaudeInstalledSettings
			case "unexpected":
				in.ClaudeInstalledRecovery = nil
			}
			if err := ValidateConfigurationRecoveryCheck(in, out); (err == nil) != (mode == "reviewed") {
				t.Fatal("recovery report did not preserve the reviewed binding", err)
			}
		})
	}
}

func TestInstalledRecoveryRejectsUnadvertisedBridgeBeforeRequest(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdio fixture required")
	}
	for _, capability := range []int{-1, 2, 1} {
		t.Run(fmt.Sprint(capability), func(t *testing.T) {
			root := t.TempDir()
			bridge := filepath.Join(root, "bridge.py")
			code := fmt.Sprintf(`import json,sys
from pathlib import Path
calls=Path(sys.argv[1])/'calls.jsonl'
for line in sys.stdin:
 r=json.loads(line)
 with calls.open('a') as f: f.write(json.dumps(r['method'])+'\n')
 result={}
 if r['method']=='hello':
  result={'version':1,'sdk':'0.3.280','installed_settings':1,'settings_file_consumer':1}
  if %d >= 0: result['installed_settings_recovery']=%d
 print(json.dumps({'v':1,'id':r['id'],'result':result}),flush=True)
`, capability, capability)
			if err := os.WriteFile(bridge, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			previous, failed, plan := installedRecoveryPlanFixture(t)
			previous.Instance.ID, previous.Instance.Profile, previous.Instance.Bridge, previous.Instance.NodeExecutable = "fixture", root, bridge, python
			failed.Instance = previous.Instance
			adapter := NewClaude(root)
			t.Cleanup(func() { _ = adapter.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := adapter.ReadConfigurationRecovery(ctx, Intent{ID: "failed-apply", Kind: "apply_execution", Scope: failed, PreviousScope: &previous, ConfigurationBaseline: strings.Repeat("c", 64), ClaudeInstalledRecovery: plan})
			if err == nil {
				t.Fatal("fixture has no native recovery evidence")
			}
			calls, readErr := os.ReadFile(filepath.Join(root, "runtimes/fixture/calls.jsonl"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := "\"hello\"\n"
			if capability == 1 {
				want += "\"configuration_recovery\"\n"
			} else if !strings.Contains(err.Error(), "bridge 不支持安装设置恢复版本") {
				t.Fatal("unadvertised bridge did not reject the new recovery contract", err)
			}
			if string(calls) != want {
				t.Fatal("bridge received an unadvertised recovery request or native action", string(calls))
			}
		})
	}
}
