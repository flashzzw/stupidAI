//go:build linux && nativeclaude

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type settingsAdoptionReceiptLoss struct {
	runtime.Adapter
	recoveryLoss  bool
	recoveryCalls atomic.Int32
	enabled       bool
	calls         atomic.Int32
	drops         atomic.Int32
}

func (f *settingsAdoptionReceiptLoss) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	out, err := f.Adapter.Execute(ctx, in)
	if in.Kind == "recover_execution" {
		f.recoveryCalls.Add(1)
		if err == nil && f.recoveryLoss {
			return runtime.Receipt{}, fmt.Errorf("fixture lost recovery receipt")
		}
	}
	if in.Kind == "apply_execution" {
		f.calls.Add(1)
		if err == nil && f.enabled {
			f.drops.Add(1)
			return runtime.Receipt{}, fmt.Errorf("fixture lost apply receipt")
		}
	}
	return out, err
}
func (f *settingsAdoptionReceiptLoss) ReadConfigurationRecovery(ctx context.Context, in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
	return f.Adapter.(runtime.ConfigurationRecoveryReader).ReadConfigurationRecovery(ctx, in)
}
func TestClaudeNativeSettingsAdoption(t *testing.T)            { runClaudeSettingsAdoption(t, "") }
func TestClaudeNativeSettingsAdoptionLostReceipt(t *testing.T) { runClaudeSettingsAdoption(t, "lost") }
func TestClaudeNativeSettingsAdoptionColdRestore(t *testing.T) { runClaudeSettingsAdoption(t, "cold") }
func TestClaudeNativeSettingsAdoptionBeforeDispatchDrift(t *testing.T) {
	runClaudeSettingsAdoption(t, "before-dispatch")
}
func TestClaudeNativeSettingsAdoptionAfterStartupDrift(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup")
}
func TestClaudeNativeSettingsAdoptionInvalidSchema(t *testing.T) {
	runClaudeSettingsAdoption(t, "invalid-schema")
}
func TestClaudeNativeSettingsAdoptionInitiallyDisabled(t *testing.T) {
	runClaudeSettingsAdoption(t, "initially-disabled")
}
func TestClaudeNativeSettingsAdoptionDisablesHooks(t *testing.T) {
	runClaudeSettingsAdoption(t, "disable-hooks")
}
func TestClaudeNativeSettingsAdoptionMixedResources(t *testing.T) {
	runClaudeSettingsAdoption(t, "mixed")
}
func TestClaudeNativeSettingsRecoverySchema(t *testing.T) {
	runClaudeSettingsAdoption(t, "invalid-schema", settingsRecoveryCase{Read: true})
}
func TestClaudeNativeSettingsRecoveryStartup(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true})
}
func TestClaudeNativeExplicitRecoverySchema(t *testing.T) {
	runClaudeSettingsAdoption(t, "invalid-schema", settingsRecoveryCase{Read: true, Execute: true})
}
func TestClaudeNativeExplicitRecoveryStartup(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true})
}
func TestClaudeNativeExplicitRecoveryLostReceipt(t *testing.T) {
	runClaudeSettingsAdoption(t, "invalid-schema", settingsRecoveryCase{Read: true, Execute: true, LoseReceipt: true})
}
func TestClaudeNativeExplicitRecoveryStartupFailure(t *testing.T) {
	runClaudeSettingsAdoption(t, "invalid-schema", settingsRecoveryCase{Read: true, Execute: true, FailStartup: true})
}

type settingsRecoveryCase struct {
	Read, Execute, LoseReceipt, FailStartup, ColdOpen bool
	Resources                                         string
	NativeLoss                                        string
}

func TestClaudeNativeMixedRecoveryEnable(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "enable"})
}
func TestClaudeNativeMixedRecoveryDisable(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "disable"})
}
func TestClaudeNativeMixedRecoveryReplace(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "replace"})
}
func TestClaudeNativeMixedRecoveryBridgeLoss(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "replace", NativeLoss: "after_commit"})
}
func TestClaudeNativeMixedRecoveryBeforeDispatchExit(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "replace", NativeLoss: "before_start"})
}
func TestClaudeNativeMixedRecoveryColdOpen(t *testing.T) {
	runClaudeSettingsAdoption(t, "after-startup", settingsRecoveryCase{Read: true, Execute: true, Resources: "replace", NativeLoss: "after_commit", ColdOpen: true})
}
func runClaudeSettingsAdoption(t *testing.T, failure string, options ...settingsRecoveryCase) {
	var recovery settingsRecoveryCase
	if len(options) > 0 {
		recovery = options[0]
	}
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-settings-adoption-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		dir := filepath.Join(root, suffix)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_LOCAL_APP_KEY", "fixture-only")
	repo, cwd, profile := filepath.Join(root, "repo"), filepath.Join(root, "repo/nested"), filepath.Join(root, "profile")
	for _, dir := range []string{cwd, profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	git := exec.Command("git", "init", "--quiet", repo)
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if out, e := git.CombinedOutput(); e != nil {
		t.Fatalf("fixture git: %v %s", e, out)
	}
	path := filepath.Join(repo, ".claude/settings.local.json")
	marker := filepath.Join(root, "hooks.txt")
	settings := func(version string) map[string]any {
		hooks := map[string]any{}
		for _, event := range []string{"SessionStart", "SessionEnd"} {
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf '%s:%s:%%s\\n' \"$NETCAP_GUARD_VERSION\" >> %q", version, event, marker), "timeout": 3}}}}
		}
		return map[string]any{"env": map[string]string{"NETCAP_GUARD_VERSION": version}, "hooks": hooks}
	}
	initialSettings := settings("v1")
	recoveryFails := recovery.FailStartup
	if recoveryFails {
		start := initialSettings["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		start["command"] = start["command"].(string) + fmt.Sprintf("; if [ \"$(grep -c 'v1:SessionStart' %q)\" -gt 1 ]; then printf '{}' > %q; fi", marker, path)
	}
	if failure == "initially-disabled" {
		initialSettings["disableAllHooks"] = true
	}
	writePluginMetadata(t, path, initialSettings)
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Local settings admission", Cwd: cwd}}, Instances: []runtime.Instance{{ID: "claude", Name: "Claude local settings", Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}}, Routes: []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_LOCAL_APP_KEY"}}}
	var picks []CapabilitySelection
	if failure == "mixed" || recovery.Resources != "" {
		skill, plugin := filepath.Join(root, "skill"), filepath.Join(root, "plugin")
		writePluginMetadata(t, filepath.Join(skill, "SKILL.md"), "---\nname: adoption-method\ndescription: standalone candidate method\n---\nTest method.\n")
		writePluginMetadata(t, filepath.Join(plugin, ".claude-plugin/plugin.json"), `{"name":"adoption-pack","version":"1.0.0"}`)
		writePluginMetadata(t, filepath.Join(plugin, "skills/pack-method/SKILL.md"), "---\nname: pack-method\ndescription: plugin candidate method\n---\nTest plugin method.\n")
		writePluginMetadata(t, filepath.Join(plugin, "hooks/hooks.json"), map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf 'plugin-start:%%s\\n' \"$NETCAP_GUARD_VERSION\" >> %q", filepath.Join(root, "plugin-hooks.txt")), "timeout": 3}}}}}})
		c.CapabilityAssets = []CapabilityAsset{{ID: "method", Name: "Method", Kind: "skill", Version: "v1", Source: skill, Backends: []string{"claude"}}, {ID: "pack", Name: "Pack", Kind: "cli_plugin", Version: "v1", Source: plugin, Backends: []string{"claude"}}}
		picks = []CapabilitySelection{{AssetID: "method", Mode: "disabled"}, {AssetID: "pack", Mode: "disabled"}}
		if recovery.Resources == "disable" || recovery.Resources == "replace" {
			for i := range picks {
				picks[i].Mode = "enabled"
			}
		}
	}
	if recovery.NativeLoss != "" {
		wrapper := filepath.Join(root, "bridge-loss.mjs")
		writePluginMetadata(t, wrapper, fmt.Sprintf(`import {Session} from %q;
import {existsSync,writeFileSync,readFileSync} from 'node:fs';
const marker=%q,phase=%q,original=Session.prototype.recoverExecution;
Session.prototype.recoverExecution=async function(params){
 if(phase==='before_start'&&!existsSync(marker)){
  await this.close();
  writeFileSync(marker,JSON.stringify({operation:params.operation,native_id:this.config.id,phase:'before_recovery_start',owned_query_closed:true,bridge_pid:process.pid}));
  process.exit(86);
 }
 const result=await original.call(this,params);
 if(!existsSync(marker)){
  const metadata=JSON.parse(readFileSync(this.metadata,'utf8'));
  if(metadata._configuration.status!=='recovered'||metadata._configuration.recovery.operation!==params.operation)throw Error('fixture wrong commit boundary');
  await this.close();
  writeFileSync(marker,JSON.stringify({operation:params.operation,native_id:result.native_id,phase:'after_durable_commit_before_reply',owned_query_closed:true,bridge_pid:process.pid}));
  process.exit(86);
 }
 return result;
};
await import(%q);
`, filepath.Join(repository, "bridges/claude/dist/session.js"), filepath.Join(root, "bridge-loss.json"), recovery.NativeLoss, filepath.Join(repository, "bridges/claude/dist/main.js")))
		c.Instances[0].Bridge = wrapper
	}
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	mutation := &localSettingsMutationStore{Persistence: db, path: path}
	adapter := &settingsAdoptionReceiptLoss{Adapter: runtime.NewClaude(c.StateDir), enabled: failure == "lost", recoveryLoss: recovery.LoseReceipt}
	a, err := Open(c, mutation, adapter)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	results := map[string]any{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	requireFailure := func(id, stage, label string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		var fields map[string]any
		for {
			read, _ := a.ReadSession(id, "", 100)
			results[label] = read
			body, _ := json.Marshal(read.Session.View)
			var view map[string]any
			_ = json.Unmarshal(body, &view)
			fields, _ = view["configuration_failure"].(map[string]any)
			if fields != nil && fields["cleanup"] == "confirmed" || time.Now().After(deadline) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		check(label+"_diagnostic", fields != nil && fields["version"] == float64(1) && fields["stage"] == stage && fields["cleanup"] == "confirmed")
		read, _ := a.ReadSession(id, "", 100)
		results[label+"_browser"] = map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: read}
		check(label+"_offline_guard", read.Session.View.NativeStatus == "sdk_offline" && read.Session.View.SettingsGuard != nil && read.Session.View.SettingsGuard.State != "registered")
	}
	checkRecovery := func(id string) {
		t.Helper()
		before, _ := a.ReadSession(id, "", 100)
		pending := before.Session.PendingExecutionChange
		request := ExecutionRecoveryRequest{BaseSnapshotID: before.Session.ExecutionSnapshot.ID, PendingChangeID: pending.ID, OperationID: pending.OperationID}
		paths := []string{filepath.Join(c.StateDir, "runtimes/claude", before.Session.Scope.NativeID+".json"), filepath.Join(c.StateDir, "runtimes/claude", before.Session.Scope.NativeID+".jsonl"), marker}
		stamps := func() []string {
			out := []string{}
			for _, p := range paths {
				body, _ := os.ReadFile(p)
				out = append(out, digest(body))
			}
			return out
		}
		originalStamps := stamps()
		contents := func() map[string]string {
			out := map[string]string{}
			for i, p := range paths {
				body, _ := os.ReadFile(p)
				out[[]string{"metadata", "journal", "hooks"}[i]] = string(body)
			}
			return out
		}
		results["recovery_native_before"] = contents()
		sourceBefore, _ := os.ReadFile(path)
		results["recovery_source_before"] = string(sourceBefore)
		blocked, e := a.ReadExecutionRecovery(context.Background(), id, request)
		if e != nil {
			t.Fatal(e)
		}
		results["recovery_before_restore"] = blocked
		check("recovery_original_files_required", !blocked.Satisfied)
		for _, condition := range blocked.Native.Checks {
			check("recovery_before_"+condition.Kind, (condition.Status == "passed") == (condition.Kind != "original_settings"))
		}
		if recovery.Execute {
			_, e := a.Apply(Command{ID: "recover-before-files", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request})
			check("explicit_recovery_rejects_changed_files", e != nil && adapter.recoveryCalls.Load() == 0)
		}
		writePluginMetadata(t, path, initialSettings)
		sourceAfter, _ := os.ReadFile(path)
		results["recovery_source_after"] = string(sourceAfter)
		verified, e := a.ReadExecutionRecovery(context.Background(), id, request)
		if e != nil {
			t.Fatal(e)
		}
		results["recovery_after_restore"] = verified
		check("recovery_recorded_conditions_pass", verified.Satisfied)
		repeated, e := a.ReadExecutionRecovery(context.Background(), id, request)
		check("recovery_repeat_is_read_only", e == nil && repeated.Satisfied && digest(stamps()) == digest(originalStamps))
		results["recovery_native_after"] = contents()
		after, _ := a.ReadSession(id, "", 100)
		results["recovery_session"] = after
		check("recovery_preserves_unknown_snapshot_candidate", after.Session.Status == "unknown" && after.Session.ExecutionHold && after.Session.Paused && digest(after.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && digest(after.Session.PendingExecutionChange) == digest(before.Session.PendingExecutionChange))
		check("recovery_does_not_create_input", len(after.Session.View.Messages) == 0)
		results["recovery_browser"] = map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: after}
		if !recovery.Execute {
			return
		}
		command := Command{ID: "explicit-recovery", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}
		receipt, e := a.Apply(command)
		if e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(25 * time.Second)
		for {
			after, _ = a.ReadSession(id, "", 100)
			if recovery.NativeLoss == "before_start" {
				uncertain := false
				for _, op := range a.Snapshot().Operations {
					if op.ID == receipt.OperationID && op.Status == "uncertain" {
						uncertain = true
					}
				}
				if uncertain {
					break
				}
			}
			if after.Session.PendingExecutionChange == nil || recoveryFails && after.Session.View.ConfigurationFailure != nil && after.Session.View.ConfigurationFailure.Operation == receipt.OperationID && after.Session.View.ConfigurationFailure.Cleanup == "confirmed" {
				break
			}
			if time.Now().After(deadline) {
				results["explicit_timeout"] = after
				t.Fatal("explicit recovery did not settle")
			}
			time.Sleep(25 * time.Millisecond)
		}
		if recovery.NativeLoss == "before_start" {
			body, e := os.ReadFile(filepath.Join(root, "bridge-loss.json"))
			var lost map[string]any
			if e == nil {
				e = json.Unmarshal(body, &lost)
			}
			results["bridge_loss"] = lost
			check("native_exit_before_recovery", e == nil && lost["phase"] == "before_recovery_start" && lost["operation"] == receipt.OperationID)
			check("before_start_stays_unknown", after.Session.Status == "unknown" && after.Session.ExecutionHold && after.Session.Paused && after.Session.PendingExecutionChange.RecoveryOperationID == receipt.OperationID && len(after.Session.ExecutionChangeAttempts) == 0)
			_, e = a.Apply(Command{ID: "retry-unknown", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request})
			check("before_start_rejects_retry", e != nil && adapter.recoveryCalls.Load() == 1)
			results["before_start_session"] = after
			results["before_start_native"] = contents()
			check("before_start_close", a.Close() == nil)
			hooks, _ := os.ReadFile(marker)
			db2, e := OpenStore(c.StateDir)
			if e != nil {
				t.Fatal(e)
			}
			adapter.Adapter = runtime.NewClaude(c.StateDir)
			a, e = Open(c, db2, adapter)
			if e != nil {
				t.Fatal(e)
			}
			time.Sleep(400 * time.Millisecond)
			after, _ = a.ReadSession(id, "", 100)
			currentHooks, _ := os.ReadFile(marker)
			results["before_start_cold"] = after
			check("before_start_restart_no_replay", after.Session.Status == "unknown" && after.Session.ExecutionHold && after.Session.PendingExecutionChange.RecoveryOperationID == receipt.OperationID && string(hooks) == string(currentHooks) && adapter.recoveryCalls.Load() == 1)
			check("before_start_did_not_open", strings.Count(string(currentHooks), "v1:SessionStart:v1") == 1)
			return
		}
		if recoveryFails {
			results["explicit_failed"] = after
			results["explicit_failed_browser"] = map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: after}
			check("explicit_failure_preserves_unknown", after.Session.Status == "unknown" && after.Session.ExecutionHold && after.Session.Paused && after.Session.PendingExecutionChange.Status == "recovering" && len(after.Session.ExecutionChangeAttempts) == 0)
			check("explicit_failure_separate_diagnostics", after.Session.PendingExecutionChange.Failure.Operation == request.OperationID && after.Session.View.ConfigurationFailure.Operation == receipt.OperationID)
			_, e = a.Apply(Command{ID: "retry-unknown-recovery", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request})
			check("explicit_unknown_rejects_retry", e != nil && adapter.recoveryCalls.Load() == 1)
			check("explicit_failure_closed", a.Close() == nil)
			content, _ := os.ReadFile(marker)
			db3, e := OpenStore(c.StateDir)
			if e != nil {
				t.Fatal(e)
			}
			adapter.Adapter = runtime.NewClaude(c.StateDir)
			a, e = Open(c, db3, adapter)
			if e != nil {
				t.Fatal(e)
			}
			deadline = time.Now().Add(4 * time.Second)
			for {
				after, _ = a.ReadSession(id, "", 100)
				if after.Session.View.ConfigurationFailure != nil && after.Session.View.ConfigurationFailure.Operation == receipt.OperationID {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("recovery failure not retained")
				}
				time.Sleep(25 * time.Millisecond)
			}
			results["explicit_failed_cold"] = after
			afterHooks, _ := os.ReadFile(marker)
			check("explicit_unknown_restart_no_replay", string(content) == string(afterHooks) && adapter.recoveryCalls.Load() == 1 && after.Session.Status == "unknown" && after.Session.PendingExecutionChange.RecoveryOperationID == receipt.OperationID)
			return
		}
		check("explicit_original_id_snapshot", after.Session.Scope.NativeID == before.Session.Scope.NativeID && digest(after.Session.Scope) == digest(before.Session.Scope) && digest(after.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot))
		check("explicit_paused_ready", after.Session.Status == "idle" && after.Session.Paused && !after.Session.ExecutionHold && (after.Session.View.SettingsGuard.State == "registered" || recovery.NativeLoss != "" && after.Session.View.NativeStatus == "sdk_offline"))
		attempts := after.Session.ExecutionChangeAttempts
		check("explicit_failed_candidate_archived", len(attempts) == 1 && digest(attempts[0].Change.Snapshot) == digest(pending.Snapshot) && attempts[0].Change.OperationID == request.OperationID && attempts[0].Change.RecoveryOperationID == receipt.OperationID && digest(attempts[0].Change.Failure) == digest(before.Session.View.ConfigurationFailure))
		check("explicit_effective_history_unchanged", digest(after.Session.ExecutionHistory) == digest(before.Session.ExecutionHistory))
		statuses := map[string]string{}
		for _, op := range a.Snapshot().Operations {
			statuses[op.ID] = op.Status
		}
		check("explicit_operations_distinct", statuses[request.OperationID] == "failed" && statuses[receipt.OperationID] == "succeeded")
		repeat, e := a.Apply(command)
		check("explicit_command_idempotent", e == nil && repeat.OperationID == receipt.OperationID && adapter.recoveryCalls.Load() == 1)
		_, e = a.Apply(Command{ID: "another-recovery", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request})
		check("explicit_no_second_recovery", e != nil && adapter.recoveryCalls.Load() == 1)
		content, _ := os.ReadFile(marker)
		check("explicit_one_original_restart", strings.Count(string(content), "v1:SessionStart:v1") == 2)
		check("explicit_no_model_input", len(after.Session.View.Messages) == 0)
		results["explicit_recovered"] = after
		results["recovery_native_recovered"] = contents()
		if recovery.NativeLoss != "" {
			var lost map[string]any
			body, e := os.ReadFile(filepath.Join(root, "bridge-loss.json"))
			if e == nil {
				e = json.Unmarshal(body, &lost)
			}
			results["bridge_loss"] = lost
			check("native_reply_lost_after_commit", e == nil && lost["operation"] == receipt.OperationID && lost["phase"] == "after_durable_commit_before_reply" && lost["owned_query_closed"] == true)
		}
		if recovery.Resources != "" {
			read, e := a.ReadExecution(id)
			results["recovery_resources"] = read
			check("mixed_original_sources", e == nil && read.Status == "unchanged")
			check("mixed_original_skill", len(read.SkillEvidence) == 1 && read.SkillEvidence[0].Verified == (recovery.NativeLoss == "") && read.SkillEvidence[0].Mode == before.Session.Scope.SkillSelections[0].Mode)
			check("mixed_original_plugin", len(read.PluginEvidence) == 1 && read.PluginEvidence[0].Verified == (recovery.NativeLoss == ""))
			hooks, _ := os.ReadFile(filepath.Join(root, "plugin-hooks.txt"))
			results["recovery_plugin_hooks"] = string(hooks)
			want := "plugin-start:v2\n"
			if recovery.Resources == "disable" {
				want = "plugin-start:v1\nplugin-start:v1\n"
			}
			if recovery.Resources == "replace" {
				want = "plugin-start:v1\nplugin-v2-start:v2\nplugin-start:v1\n"
			}
			check("mixed_hooks_use_exact_versions", string(hooks) == want)
			for _, asset := range c.CapabilityAssets {
				item := libraryRead(t, a, asset.ID)
				uses := 0
				for _, use := range item.Uses {
					if use.Phase == "failed_attempt" && use.SnapshotID == pending.Snapshot.ID {
						uses++
					}
				}
				check("mixed_failed_reference_"+asset.ID, uses == 1)
			}
		}
		results["explicit_browser"] = map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: after}
		check("explicit_close", a.Close() == nil)
		if recovery.ColdOpen {
			request := filepath.Join(root, "recovery-cold-request.json")
			output := filepath.Join(root, "recovery-cold-result.json")
			writePluginMetadata(t, request, map[string]any{"directory": filepath.Join(c.StateDir, "runtimes/claude"), "id": after.Session.Scope.NativeID, "sources": after.Session.Scope.ClaudeSettingsSources, "output": output, "recovery": true, "skills": after.Session.Scope.SkillSelections, "plugins": after.Session.Scope.PluginSelections, "retired_skill_name": "replacement-method", "retired_plugin_skill": "replacement-only"})
			cmd := exec.Command("node", filepath.Join(repository, "tests/claude_settings_adoption_restore.mjs"), request)
			cmd.Dir = cwd
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR=" + profile}
			for _, key := range c.Instances[0].EnvAllow {
				cmd.Env = append(cmd.Env, key+"="+os.Getenv(key))
			}
			out, e := cmd.CombinedOutput()
			writePluginMetadata(t, filepath.Join(root, "recovery-cold.log"), string(out))
			check("cold_recovery_query", e == nil)
			var cold map[string]any
			body, _ := os.ReadFile(output)
			e = json.Unmarshal(body, &cold)
			results["recovery_cold_native"] = cold
			check("cold_recovery_result", e == nil && cold["passed"] == true)
		}
		content, _ = os.ReadFile(marker)
		db3, e := OpenStore(c.StateDir)
		if e != nil {
			t.Fatal(e)
		}
		adapter.Adapter = runtime.NewClaude(c.StateDir)
		a, e = Open(c, db3, adapter)
		if e != nil {
			t.Fatal(e)
		}
		time.Sleep(400 * time.Millisecond)
		cold, _ := a.ReadSession(id, "", 100)
		results["explicit_cold"] = cold
		check("explicit_restart_retains_archive", digest(cold.Session.ExecutionChangeAttempts) == digest(attempts) && cold.Session.PendingExecutionChange == nil && cold.Session.Paused && digest(cold.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot))
		afterHooks, _ := os.ReadFile(marker)
		check("explicit_restart_no_replay", string(content) == string(afterHooks) && adapter.recoveryCalls.Load() == 1)
	}
	t.Cleanup(func() {
		checks["close"] = a.Close() == nil
		content, _ := os.ReadFile(marker)
		results["markers"] = string(content)
		if failure == "invalid-schema" || failure == "initially-disabled" {
			check("no_invalid_candidate_start", !strings.Contains(string(content), "v2:SessionStart"))
		} else if failure == "disable-hooks" {
			check("disabled_candidate_has_no_hooks", string(content) == "v1:SessionStart:v1\nv1:SessionEnd:v1\n")
		} else if failure == "before-dispatch" {
			check("only_original_hooks", string(content) == "v1:SessionStart:v1\nv1:SessionEnd:v1\n")
		} else if failure == "after-startup" {
			check("failed_candidate_not_replayed", strings.Count(string(content), "SessionStart:") == (map[bool]int{true: 3, false: 2}[recovery.Execute && recovery.NativeLoss != "before_start"]+map[bool]int{true: 1, false: 0}[recovery.ColdOpen]) && strings.HasPrefix(string(content), "v1:SessionStart:v1\nv1:SessionEnd:v1\nv2:SessionStart:v2\n"))
		} else {
			want := "v1:SessionStart:v1\nv1:SessionEnd:v1\nv2:SessionStart:v2\nv2:SessionEnd:v2\n"
			if failure == "cold" {
				want += "v2:SessionStart:v2\nv2:SessionEnd:v2\n"
			}
			check("v1_then_v2_hooks", string(content) == want)
		}
		writePluginMetadata(t, filepath.Join(root, "result.json"), map[string]any{"root": root, "checks": checks, "results": results, "passed": !t.Failed(), "scope": "real Application/SQLite/Runtime/Session same-ID local file adoption, no model input"})
	})
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "claude", Permission: "read_only", Title: "Settings adoption", CapabilitySelections: picks})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				results["timeout_snapshot"] = a.Snapshot()
				t.Fatal("adoption state did not settle")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	wait(func() bool {
		s := a.Snapshot().Sessions[0]
		return s.Status == "idle" && s.View.SettingsGuard != nil && (s.View.SettingsGuard.State == "registered" || failure == "initially-disabled")
	})
	before, _ := a.ReadSession(created.SessionID, "", 100)
	if recovery.Resources == "replace" {
		for _, old := range c.CapabilityAssets {
			asset := old
			asset.Version = "v2"
			asset.Source = filepath.Join(root, old.ID+"-v2")
			if old.Kind == "skill" {
				writePluginMetadata(t, filepath.Join(asset.Source, "SKILL.md"), "---\nname: replacement-method\ndescription: replacement candidate only\n---\nCandidate version two.\n")
			} else {
				writePluginMetadata(t, filepath.Join(asset.Source, ".claude-plugin/plugin.json"), `{"name":"adoption-pack","version":"2.0.0"}`)
				writePluginMetadata(t, filepath.Join(asset.Source, "skills/replacement-only/SKILL.md"), "---\nname: replacement-only\ndescription: candidate-only plugin method\n---\nCandidate version two.\n")
				writePluginMetadata(t, filepath.Join(asset.Source, "hooks/hooks.json"), map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf 'plugin-v2-start:%%s\\n' \"$NETCAP_GUARD_VERSION\" >> %q", filepath.Join(root, "plugin-hooks.txt")), "timeout": 3}}}}}})
			}
			current := libraryRead(t, a, old.ID)
			libraryChange(t, a, "update-"+old.ID, CapabilityChangeRequest{Action: "update", AssetID: old.ID, BaseRevision: current.Revision, Asset: &asset})
		}
	}
	results["before"] = before
	check("native_sources_recorded", before.Session.ExecutionSnapshot.ClaudeLocalSettings.Version == 2 && len(before.Session.Scope.ClaudeSettingsSources) == 3)
	target := settings("v2")
	if failure == "invalid-schema" {
		target["permissions"] = map[string]any{"allow": 42}
	}
	if failure == "disable-hooks" {
		target["disableAllHooks"] = true
	}
	if failure == "after-startup" {
		body, _ := json.Marshal(settings("v3"))
		script := filepath.Join(root, "mutate.py")
		writePluginMetadata(t, script, fmt.Sprintf("from pathlib import Path\nPath(%q).write_text(%q)\n", path, string(body)))
		target["hooks"].(map[string]any)["SessionStart"] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf 'v2:SessionStart:%%s\\n' \"$NETCAP_GUARD_VERSION\" >> %q; python3 %q", marker, script), "timeout": 3}}}}
	}
	writePluginMetadata(t, path, target)
	if failure == "initially-disabled" || failure == "invalid-schema" {
		time.Sleep(1500 * time.Millisecond)
	} else {
		wait(func() bool { return len(a.Snapshot().Sessions[0].View.SettingsGuard.Changes) > 0 })
	}
	r := ExecutionChangeRequest{BaseSnapshotID: before.Session.ExecutionSnapshot.ID, AdoptLocalSettings: true}
	if failure == "mixed" || recovery.Resources != "" {
		r.Selections = []CapabilitySelection{{AssetID: "method", Mode: "enabled", Version: "v1"}, {AssetID: "pack", Mode: "enabled", Version: "v1"}}
		if recovery.Resources == "disable" {
			for i := range r.Selections {
				r.Selections[i].Mode = "disabled"
			}
		}
		if recovery.Resources == "replace" {
			for i := range r.Selections {
				r.Selections[i].Version = "v2"
			}
		}
	}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil {
		t.Fatal(err)
	}
	results["preview"] = preview
	r.PreviewHash = preview.PreviewHash
	_, err = a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r})
	if err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadSession(created.SessionID, "", 100)
	results["staged"] = staged
	check("save_keeps_original", digest(staged.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && digest(staged.Session.Scope) == digest(before.Session.Scope))
	check("exact_candidate", len(staged.Session.PendingExecutionChange.LocalSettingsChanges) == 1 && staged.Session.PendingExecutionChange.AdoptLocalSettings)
	ref := ExecutionChangeRequest{BaseSnapshotID: r.BaseSnapshotID, PendingChangeID: staged.Session.PendingExecutionChange.ID}
	if failure == "invalid-schema" || failure == "initially-disabled" {
		opTerminal := func(id string) string {
			status := ""
			wait(func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == id {
						status = op.Status
						return status == "succeeded" || status == "not_sent" || status == "uncertain" || status == "failed"
					}
				}
				return false
			})
			return status
		}
		prep, e := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &ref})
		if e != nil {
			results["prepare_error"] = e.Error()
		} else {
			status := opTerminal(prep.OperationID)
			results["prepare_status"] = status
			if status == "succeeded" {
				applied, e := a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref})
				if e != nil {
					results["apply_error"] = e.Error()
				} else {
					results["apply_status"] = opTerminal(applied.OperationID)
				}
			}
		}
		failed, _ := a.ReadSession(created.SessionID, "", 100)
		results["failed"] = failed
		check("invalid_adoption_not_promoted", digest(failed.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && failed.Session.PendingExecutionChange != nil)
		check("no_model_input", len(failed.Session.View.Messages) == 0)
		if failure == "invalid-schema" {
			requireFailure(created.SessionID, "check_settings", "failure_visible")
			check("first_application_closed", a.Close() == nil)
			db2, e := OpenStore(c.StateDir)
			if e != nil {
				t.Fatal(e)
			}
			adapter.Adapter = runtime.NewClaude(c.StateDir)
			a, e = Open(c, db2, adapter)
			if e != nil {
				t.Fatal(e)
			}
			requireFailure(created.SessionID, "check_settings", "failure_restored")
			if recovery.Read {
				checkRecovery(created.SessionID)
			}
		}
		if failure == "initially-disabled" {
			check("unavailable_original_never_applies", adapter.calls.Load() == 0)
		}
		return
	}
	var prep CommandReceipt
	wait(func() bool {
		prep, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &ref})
		return err == nil
	})
	wait(func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == prep.OperationID {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	prepared, _ := a.ReadSession(created.SessionID, "", 100)
	results["prepared"] = prepared
	check("prepare_only_pauses", digest(prepared.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && prepared.Session.ExecutionHold && prepared.Session.PendingExecutionChange.Baseline != "")
	if failure == "before-dispatch" {
		body, _ := json.Marshal(settings("v3"))
		mutation.arm("apply_execution", body)
	}
	var applied CommandReceipt
	wait(func() bool {
		applied, err = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref})
		return err == nil
	})
	if failure == "before-dispatch" || failure == "after-startup" {
		expected := "not_sent"
		if failure == "after-startup" {
			requireFailure(created.SessionID, "open_candidate", "failure_visible")
			expected = "uncertain"
		}
		wait(func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == applied.OperationID {
					return op.Status == expected
				}
			}
			return false
		})
		failed, _ := a.ReadSession(created.SessionID, "", 100)
		results["failed"] = failed
		check("failure_keeps_original", digest(failed.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && digest(failed.Session.Scope) == digest(before.Session.Scope))
		check("failure_keeps_candidate_and_pause", failed.Session.PendingExecutionChange != nil && failed.Session.ExecutionHold && failed.Session.Paused)
		check("dispatch_count", adapter.calls.Load() == map[bool]int32{true: 0, false: 1}[failure == "before-dispatch"])
		if failure == "after-startup" {
			check("first_application_closed", a.Close() == nil)
			db2, e := OpenStore(c.StateDir)
			if e != nil {
				t.Fatal(e)
			}
			adapter.Adapter = runtime.NewClaude(c.StateDir)
			a, e = Open(c, db2, adapter)
			if e != nil {
				t.Fatal(e)
			}
			time.Sleep(400 * time.Millisecond)
			restored, _ := a.ReadSession(created.SessionID, "", 100)
			results["restored"] = restored
			check("restart_keeps_original_and_unknown", digest(restored.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot) && restored.Session.PendingExecutionChange != nil && restored.Session.PendingExecutionChange.Status == "applying")
			requireFailure(created.SessionID, "open_candidate", "failure_restored")
			if recovery.Read {
				checkRecovery(created.SessionID)
			}
		}
		return
	}
	wait(func() bool {
		if a.Snapshot().Sessions[0].PendingExecutionChange == nil {
			return true
		}
		for _, op := range a.Snapshot().Operations {
			if op.ID == applied.OperationID && (op.Status == "uncertain" && failure != "lost" || op.Status == "not_sent" || op.Status == "failed") {
				return true
			}
		}
		return false
	})
	after, _ := a.ReadSession(created.SessionID, "", 100)
	results["after"] = after
	if after.Session.PendingExecutionChange != nil {
		check("candidate_promoted", false)
		return
	}
	check("same_native_id", after.Session.Scope.NativeID == before.Session.Scope.NativeID)
	check("promoted_exact_candidate", digest(after.Session.ExecutionSnapshot) == digest(staged.Session.PendingExecutionChange.Snapshot))
	check("history_keeps_original", len(after.Session.ExecutionHistory) == 1 && digest(after.Session.ExecutionHistory[0]) == digest(before.Session.ExecutionSnapshot))
	check("native_binding_new_sources", digest(after.Session.Scope.ClaudeSettingsSources) == digest(after.Session.ExecutionSnapshot.ClaudeLocalSettings.Sources))
	read, _ := a.ReadExecution(created.SessionID)
	results["execution"] = read
	check("source_check_unchanged", read.Status == "unchanged")
	check("no_model_input", len(after.Session.View.Messages) == 0)
	repeated, err := a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref})
	check("apply_idempotent", err == nil && repeated == applied)
	check("one_native_apply", adapter.calls.Load() == 1)
	if failure == "disable-hooks" {
		check("target_guard_unavailable", after.Session.View.SettingsGuard != nil && after.Session.View.SettingsGuard.State == "unavailable")
	}
	if failure == "mixed" {
		check("both_capability_changes", len(preview.Changes) == 2)
		check("standalone_skill_loaded", len(read.SkillEvidence) == 1 && read.SkillEvidence[0].Verified && read.SkillEvidence[0].DescriptionListed)
		check("plugin_loaded", len(read.PluginEvidence) == 1 && read.PluginEvidence[0].Verified)
		content, _ := os.ReadFile(filepath.Join(root, "plugin-hooks.txt"))
		results["plugin_markers"] = string(content)
		check("plugin_hook_uses_new_settings_once", string(content) == "plugin-start:v2\n")
	}
	if failure == "lost" {
		check("receipt_actually_lost", adapter.drops.Load() == 1)
	}
	if failure == "cold" {
		check("first_application_closed", a.Close() == nil)
		request := filepath.Join(root, "cold-request.json")
		output := filepath.Join(root, "cold-result.json")
		writePluginMetadata(t, request, map[string]any{"directory": filepath.Join(c.StateDir, "runtimes/claude"), "id": after.Session.Scope.NativeID, "sources": after.Session.Scope.ClaudeSettingsSources, "output": output})
		cmd := exec.Command("node", filepath.Join(repository, "tests/claude_settings_adoption_restore.mjs"), request)
		cmd.Dir = cwd
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR=" + profile, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}
		for key, suffix := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
			cmd.Env = append(cmd.Env, key+"="+filepath.Join(root, suffix))
		}
		out, e := cmd.CombinedOutput()
		writePluginMetadata(t, filepath.Join(root, "cold-run.log"), string(out))
		check("native_cold_restore", e == nil)
		db2, e := OpenStore(c.StateDir)
		if e != nil {
			t.Fatal(e)
		}
		adapter.Adapter = runtime.NewClaude(c.StateDir)
		a, e = Open(c, db2, adapter)
		if e != nil {
			t.Fatal(e)
		}
		restored, e := a.ReadExecution(created.SessionID)
		check("application_cold_binding", e == nil && restored.Status == "unchanged" && digest(restored.Snapshot) == digest(after.Session.ExecutionSnapshot))
		results["cold"] = restored
	}
	writePluginMetadata(t, filepath.Join(root, "browser-responses.json"), map[string]any{"responses": map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + created.SessionID: after, "/api/sessions/" + created.SessionID + "/execution": read}, "session_id": created.SessionID, "stages": results})
}
