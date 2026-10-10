//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeHookManagedRequirementsAdmission(t *testing.T) {
	codexNativeManagedHookFlow(t, false, "")
}

func TestCodexNativeManagedHookDeactivation(t *testing.T) {
	codexNativeManagedHookFlow(t, true, "")
}

func TestCodexNativeHookRetirementIncompleteDetach(t *testing.T) {
	codexNativeManagedHookFlow(t, true, "thread/unsubscribe")
}

func TestCodexNativeHookRetirementIncompleteResume(t *testing.T) {
	codexNativeManagedHookFlow(t, true, "thread/resume")
}

func TestCodexNativeHookRetirementApplicationReceiptLoss(t *testing.T) {
	codexNativeManagedHookFlow(t, true, "application")
}

func codexNativeManagedHookFlow(t *testing.T, deactivate bool, incompleteReceipt string) {
	prefix := "netcap-w0208-managed-hooks-"
	if deactivate {
		prefix = "netcap-w0208-hook-deactivation-"
	}
	if incompleteReceipt != "" {
		prefix = "netcap-w0208-hook-retirement-receipt-"
	}
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	checks, samples := map[string]bool{}, map[string]any{}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "deactivation": deactivate, "incomplete_receipt": incompleteReceipt, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "real native 0.158.0 no-requirements control; stricter managed-only RESPONSE and optional incomplete retirement RESPONSE injected at test transport boundary, production Runtime/Application/SQLite/Handler; no real managed policy enforcement, model/tool/hook, Unix proxy or Windows"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
	})
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, _ := os.ReadFile(configPath)
	write(configPath, strings.Replace(string(config), "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1))
	profileBefore, _ := os.ReadFile(configPath)
	f.Restart(t)
	var policy struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	if err = f.Call(context.Background(), "configRequirements/read", map[string]any{}, &policy); err != nil {
		t.Fatal(err)
	}
	if string(policy.Requirements) != "null" {
		t.Fatal("fixture needs a native no-requirements control; do not replace existing managed policy")
	}
	samples["native-requirements"] = policy
	check("native_explicit_no_requirements", true)
	source := filepath.Join(root, "source")
	write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"managed-check","version":"1.0.0"}`)
	write(filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"true","timeout":1}]}]}}`)
	asset := app.CapabilityAsset{ID: "hooks", Name: "Local hook fixture", Kind: "cli_plugin", Version: "asset-v1", Source: source, Backends: []string{"codex"}}
	assets := []app.CapabilityAsset{asset}
	if deactivate {
		entry := filepath.Join(root, "home/.agents/skills/retained/SKILL.md")
		write(entry, "---\nname: retained-method\ndescription: Owned retained directory\n---\nKeep this original directory selection.\n")
		assets = append(assets, app.CapabilityAsset{ID: "retained", Name: "Retained native method", Kind: "skill", Version: "method-v1", Source: filepath.Dir(entry), CodexSkill: entry, Backends: []string{"codex"}})
	}
	stateDir := filepath.Join(root, "application")
	store, err := app.OpenStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var adapter runtime.Adapter = f.Runtime
	var applicationLoss *nativeConfigurationApplicationLoss
	if incompleteReceipt == "application" {
		applicationLoss = &nativeConfigurationApplicationLoss{Codex: f.Runtime, received: make(chan nativeApplicationConfigurationReply, 1)}
		adapter = applicationLoss
	}
	a, err := app.Open(app.Config{Version: 1, StateDir: stateDir, Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: assets}, store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	items, err := a.ReadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := a.InspectCapabilityHooks(context.Background(), app.CapabilityHookInspectionRequest{AssetID: asset.ID, InstanceID: f.Instance.ID, Revision: items[0].Revision, SourceSHA256: items[0].Source.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	samples["inspection"] = inspection
	create := func(label, mode string, includeDirectory bool) app.CommandReceipt {
		t.Helper()
		picks := []app.CapabilitySelection{{AssetID: asset.ID, Mode: mode, Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "trusted"}}}
		if includeDirectory {
			picks = append(picks, app.CapabilitySelection{AssetID: "retained", Mode: "enabled"})
		}
		r, e := a.Apply(app.Command{ID: "create-" + strings.ReplaceAll(label, " ", "-"), Kind: "create", Title: label, ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", CapabilitySelections: picks})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	find := func(snapshot app.Snapshot, id string) app.Session {
		for _, s := range snapshot.Sessions {
			if s.ID == id {
				return s
			}
		}
		return app.Session{}
	}
	ready := func(id string) app.Session {
		return find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			s := find(snapshot, id)
			return s.Status == "idle" && len(s.View.PluginEvidence) == 1 && s.View.PluginEvidence[0].Verified
		}), id)
	}
	allowed := create("Original local hook", "enabled", deactivate)
	original := ready(allowed.SessionID)
	var direct app.CommandReceipt
	if deactivate {
		direct = create("Direct retire hook", "enabled", false)
		retiring := ready(direct.SessionID)
		if err = f.Call(context.Background(), "thread/goal/set", map[string]any{"threadId": retiring.Scope.NativeID, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err = a.Apply(app.Command{ID: "pause-direct", Kind: "pause", SessionID: direct.SessionID}); err != nil {
			t.Fatal(err)
		}
	}
	check("native_no_requirements_accepts_reviewed_hook", original.ExecutionSnapshot.PluginSelections[0].Hooks.Trust == "trusted")
	control := "takeover"
	if deactivate {
		control = "pause"
		if err = f.Call(context.Background(), "thread/goal/set", map[string]any{"threadId": original.Scope.NativeID, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.Apply(app.Command{ID: "takeover-original", Kind: control, SessionID: allowed.SessionID}); err != nil {
		t.Fatal(err)
	}
	samples["before-restriction"] = a.Snapshot()
	count := f.CountMethod
	starts := count("thread/start")
	restriction := f.RestrictNativeHookRequirements()
	denied := create("Managed policy rejected", "enabled", deactivate)
	state := waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
		for _, op := range snapshot.Operations {
			if op.ID == denied.OperationID {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	check("managed_response_prevents_native_creation", count("thread/start") == starts && find(state, denied.SessionID).Scope.NativeID == "")
	reason := ""
	for _, op := range state.Operations {
		if op.ID == denied.OperationID {
			reason = op.Error
		}
	}
	check("managed_rejection_explains_trust_does_not_override", strings.Contains(reason, "仅允许受管 hook") && strings.Contains(reason, "信任审核不能覆盖"))
	disabled := create("Disabled local hook", "disabled", deactivate)
	disabledState := ready(disabled.SessionID)
	check("managed_response_allows_disabled_package", disabledState.Scope.PluginSelections[0].Mode == "disabled" && count("thread/start") == starts+1)
	changed := find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
		s := find(snapshot, allowed.SessionID)
		return s.Status == "unknown" && len(s.View.PluginEvidence) == 1 && !s.View.PluginEvidence[0].Verified
	}), allowed.SessionID)
	check("changed_requirements_withdraw_readiness", changed.ExecutionSnapshot.ID == original.ExecutionSnapshot.ID && changed.Scope.NativeID == original.Scope.NativeID)
	check("changed_requirements_keep_specific_reason", strings.Contains(changed.View.PluginEvidence[0].Detail, "仅允许受管 hook"))
	check("changed_requirements_preserve_saved_trust", changed.ExecutionSnapshot.PluginSelections[0].Hooks.Trust == "trusted" && changed.Paused)
	_, err = a.Apply(app.Command{ID: "blocked-input", Kind: "send", SessionID: allowed.SessionID, Text: "must not reach native"})
	check("changed_requirements_block_input", err != nil && count("turn/start") == 0)
	duplicate := create("Managed policy rejected", "enabled", deactivate)
	check("rejected_command_keeps_original_operation", duplicate.OperationID == denied.OperationID && count("thread/start") == starts+1)
	samples["restricted"] = a.Snapshot()
	if deactivate {
		apply := func(command app.Command) app.CommandReceipt {
			t.Helper()
			var last error
			for end := time.Now().Add(4 * time.Second); time.Now().Before(end); {
				r, e := a.Apply(command)
				if e == nil {
					return r
				}
				last = e
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal(last)
			return app.CommandReceipt{}
		}
		for _, pick := range []struct{ mode, label, id string }{{"disabled", "disabled", allowed.SessionID}, {"inherit", "inherit", allowed.SessionID}, {"inherit", "direct-inherit", direct.SessionID}} {
			mode, label, id := pick.mode, pick.label, pick.id
			before := find(a.Snapshot(), id)
			if label == "direct-inherit" {
				check("direct_retirement_starts_unknown_without_other_skills", before.Status == "unknown" && len(before.Scope.SkillSelections) == 0 && before.Scope.PluginSelections[0].Mode == "enabled")
			}
			calls := nativeConfigurationCallCount(t, f, before.Scope.NativeID)
			request := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{{AssetID: asset.ID, Mode: mode}}}
			preview, e := a.PreviewExecutionChange(id, request)
			if e != nil {
				t.Fatal(e)
			}
			samples[label+"-preview"] = preview
			request.PreviewHash = preview.PreviewHash
			apply(app.Command{ID: label + "-stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request})
			staged := find(a.Snapshot(), id)
			samples[label+"-staged"] = a.Snapshot()
			base := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
			check(label+"_save_keeps_original", staged.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
			prepared := apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &base})
			preparedState := waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
				for _, op := range snapshot.Operations {
					if op.ID == prepared.OperationID {
						return op.Status == "succeeded" || op.Status == "not_sent"
					}
				}
				return false
			})
			preparing := find(preparedState, id)
			if preparing.PendingExecutionChange.Baseline == "" {
				t.Fatalf("managed hook preparation failed: %+v", preparedState.Operations)
			}
			samples[label+"-prepared"] = preparedState
			check(label+"_prepare_does_not_apply", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls)
			command := app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &base}
			var lost <-chan runtime.NativeConfigurationReply
			if incompleteReceipt != "" && label == "direct-inherit" {
				if applicationLoss != nil {
					applicationLoss.armed.Store(true)
				} else {
					lost = f.InvalidateRetirementReceipt(before.Scope.NativeID, incompleteReceipt)
				}
			}
			applied := apply(command)
			if applicationLoss != nil && label == "direct-inherit" {
				select {
				case reply := <-applicationLoss.received:
					samples["application-retirement-receipt-loss"] = reply
					check("application_loss_has_matching_acknowledgement", reply.Receipt.NativeID == before.Scope.NativeID && reply.Intent.ID == applied.OperationID)
				case <-time.After(10 * time.Second):
					t.Fatal("application retirement reply not intercepted")
				}
			}
			if lost != nil {
				select {
				case reply := <-lost:
					samples["incomplete-retirement-receipt"] = reply
				case <-time.After(10 * time.Second):
					t.Fatal("native retirement reply not intercepted")
				}
				state := waitNativeApplication(t, a, func(s app.Snapshot) bool {
					for _, op := range s.Operations {
						if op.ID == applied.OperationID {
							return op.Status == "succeeded" || op.Status == "uncertain"
						}
					}
					return false
				})
				current := find(state, id)
				check("incomplete_receipt_keeps_old_snapshot", current.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
				check("incomplete_receipt_keeps_exact_candidate", current.PendingExecutionChange != nil && current.PendingExecutionChange.Snapshot.ID == staged.PendingExecutionChange.Snapshot.ID)
				check("incomplete_receipt_keeps_pause_and_hold", current.ExecutionHold && current.Paused)
				expected := calls
				if incompleteReceipt == "thread/resume" {
					expected++
				}
				check("incomplete_receipt_native_call_count", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == expected)
				for _, commandID := range []string{"incomplete-reconcile-a", "incomplete-reconcile-b"} {
					apply(app.Command{ID: commandID, Kind: "reconcile", SessionID: id})
				}
				state = waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
					for _, op := range snapshot.Operations {
						if op.ID == applied.OperationID {
							return op.Status == "succeeded" || slices.ContainsFunc(op.Evidence, func(e runtime.Evidence) bool { return e.Source == "codex.plugin-configuration" })
						}
					}
					return false
				})
				current = find(state, id)
				check("incomplete_receipt_reconcile_remains_unknown", current.Status == "unknown" && current.PendingExecutionChange != nil)
				_, e := a.Apply(app.Command{ID: "incomplete-input", Kind: "send", SessionID: id, Text: "must not send"})
				check("incomplete_receipt_blocks_input", e != nil && count("turn/start") == 0)
				retry := apply(command)
				check("incomplete_receipt_duplicate_keeps_operation", retry.OperationID == applied.OperationID)
				_, e = a.Apply(app.Command{ID: "incomplete-reapply", Kind: "apply_execution", SessionID: id, ExecutionChange: &base})
				check("incomplete_receipt_reapply_rejected", e != nil)
				check("incomplete_receipt_no_configuration_replay", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == expected)
				samples["incomplete-retirement-state"] = a.Snapshot()
				continue
			}
			after := find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
				s := find(snapshot, id)
				return s.PendingExecutionChange == nil && s.Status == "idle"
			}), id)
			check(label+"_same_id_and_snapshot_history", after.Scope.NativeID == before.Scope.NativeID && after.ExecutionSnapshot.PreviousID == before.ExecutionSnapshot.ID && after.Paused)
			if len(before.Scope.SkillSelections) > 0 {
				check(label+"_retains_directory_version_and_selection", len(after.Scope.SkillSelections) == 1 && after.Scope.SkillSelections[0].ResourceHash == before.Scope.SkillSelections[0].ResourceHash && after.Scope.SkillSelections[0].Mode == "enabled" && len(after.View.SkillEvidence) == 1 && after.View.SkillEvidence[0].Verified)
			} else {
				check(label+"_does_not_add_skills", len(after.Scope.SkillSelections) == 0)
			}
			check(label+"_no_model_input", count("turn/start") == 0)
			check(label+"_applies_once", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
			retry := apply(command)
			check(label+"_duplicate_keeps_operation", retry.OperationID == applied.OperationID && nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
			samples[label+"-applied"] = a.Snapshot()
		}
		var goal struct {
			Goal struct {
				Status string `json:"status"`
			} `json:"goal"`
		}
		if err = f.Call(context.Background(), "thread/goal/get", map[string]any{"threadId": original.Scope.NativeID}, &goal); err != nil {
			t.Fatal(err)
		}
		check("original_paused_goal_preserved", goal.Goal.Status == "paused")
		if err = f.Call(context.Background(), "thread/goal/get", map[string]any{"threadId": find(a.Snapshot(), direct.SessionID).Scope.NativeID}, &goal); err != nil {
			t.Fatal(err)
		}
		check("direct_retirement_keeps_paused_goal", goal.Goal.Status == "paused")
	}
	samples["policy-response-boundary"] = restriction.Samples()
	check("policy_boundary_was_exercised", len(restriction.Samples()) >= 3)
	handler := console.Handler(a, "127.0.0.1:9468", strings.Repeat("d", 64), os.DirFS("."))
	responses := map[string]json.RawMessage{}
	paths := []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + allowed.SessionID, "/api/sessions/" + denied.SessionID, "/api/sessions/" + disabled.SessionID}
	if deactivate {
		paths = append(paths, "/api/sessions/"+direct.SessionID)
	}
	for _, path := range paths {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9468"+path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("d", 64))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("Handler %s: %d", path, w.Code)
		}
		responses[path] = append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": map[string]string{"original": allowed.SessionID, "denied": denied.SessionID, "disabled": disabled.SessionID, "direct": direct.SessionID}, "samples": samples}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(b))
	profileAfter, _ := os.ReadFile(configPath)
	check("profile_unchanged", string(profileAfter) == string(profileBefore))
	check("no_model_or_policy_writes", count("turn/start") == 0 && count("config/value/write") == 0 && count("config/batchWrite") == 0)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.OpenStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !deactivate {
		check("sqlite_keeps_original_snapshot_and_unknown", persisted.Sessions[allowed.SessionID].ExecutionSnapshot.ID == original.ExecutionSnapshot.ID && persisted.Sessions[allowed.SessionID].Status == "unknown")
	} else {
		final := persisted.Sessions[allowed.SessionID]
		check("sqlite_keeps_deactivated_id_history_and_owner", final.Scope.NativeID == original.Scope.NativeID && len(final.ExecutionHistory) == 2 && final.ExecutionHistory[0].ID == original.ExecutionSnapshot.ID && len(final.Scope.PluginSelections) == 0 && len(final.Scope.SkillSelections) == 1 && final.ControlMode == "managed" && final.Paused)
		retired := persisted.Sessions[direct.SessionID]
		if incompleteReceipt == "" || applicationLoss != nil {
			check("sqlite_keeps_direct_retirement", len(retired.ExecutionHistory) == 1 && len(retired.Scope.PluginSelections) == 0 && len(retired.Scope.SkillSelections) == 0 && retired.Paused && retired.ControlMode == "managed")
		} else {
			check("sqlite_keeps_uncertain_retirement", len(retired.ExecutionHistory) == 0 && len(retired.Scope.PluginSelections) == 1 && retired.PendingExecutionChange != nil && retired.ExecutionHold && retired.Paused && retired.ControlMode == "managed")
		}
	}
	check("sqlite_keeps_rejection_without_native_id", persisted.Operations[denied.OperationID].Status == "not_sent" && persisted.Sessions[denied.SessionID].Scope.NativeID == "")
	check("no_hook_execution_claims", len(persisted.Sessions[allowed.SessionID].View.HookExecutions) == 0 && len(persisted.Sessions[disabled.SessionID].View.HookExecutions) == 0)
}
