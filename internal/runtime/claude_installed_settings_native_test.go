//go:build linux && nativeclaude

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

// The native adapter still executes exactly once. Only its response and later
// read-only reconciliation are delayed, so restart must use durable metadata.
type installedSettingsReceiptLoss struct {
	*runtime.Claude
	root         string
	drop         bool
	dropRecovery bool
	held         atomic.Bool
}

func (f *installedSettingsReceiptLoss) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "apply_execution" || in.Kind == "recover_execution" {
		name := "apply-calls.jsonl"
		if in.Kind == "recover_execution" {
			name = "recovery-calls.jsonl"
		}
		file, err := os.OpenFile(filepath.Join(f.root, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return runtime.Receipt{}, err
		}
		_ = json.NewEncoder(file).Encode(map[string]string{"operation": in.ID, "native_id": in.Scope.NativeID})
		_ = file.Close()
	}
	receipt, err := f.Claude.Execute(ctx, in)
	if (f.drop && in.Kind == "apply_execution" || f.dropRecovery && in.Kind == "recover_execution") && err == nil {
		f.held.Store(true)
		return runtime.Receipt{NativeID: in.Scope.NativeID}, errors.New("owned fixture dropped the successful native receipt")
	}
	return receipt, err
}

func (f *installedSettingsReceiptLoss) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if (f.drop && in.Kind == "apply_execution" || f.dropRecovery && in.Kind == "recover_execution") && f.held.Load() {
		return runtime.Resolution{State: "unknown", Receipt: receipt}, nil
	}
	return f.Claude.Reconcile(ctx, in, receipt)
}

func TestClaudeNativeInstalledSettingsApplication(t *testing.T) {
	request := os.Getenv("NETCAP_INSTALLED_SETTINGS_REQUEST")
	if request == "" {
		t.Skip("owned installed-settings namespace fixture only")
	}
	var fixture struct {
		Root   string     `json:"root"`
		Config app.Config `json:"config"`
		Mode   string     `json:"mode"`
	}
	data, err := os.ReadFile(request)
	if err != nil || json.Unmarshal(data, &fixture) != nil {
		t.Fatal("invalid owned fixture request", err)
	}
	owned, e1 := os.Stat(filepath.Join(fixture.Root, "policy"))
	actual, e2 := os.Stat("/etc/claude-code")
	if e1 != nil || e2 != nil || !os.SameFile(owned, actual) {
		t.Fatal("owned policy namespace required")
	}
	checks, reports := map[string]bool{}, map[string]any{}
	originalRecovery := strings.HasPrefix(fixture.Mode, "original-recovery")
	failureCase := fixture.Mode == "failure" || strings.HasPrefix(fixture.Mode, "recovery") || originalRecovery
	failureLabel := "disable-selected"
	if fixture.Mode == "recovery-installed" || fixture.Mode == "original-recovery-installed" {
		failureLabel = "enable-selected"
	}
	httpCalls := []map[string]any{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Error(name)
		}
	}
	write := func(path string, value any) {
		t.Helper()
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil || os.WriteFile(path, append(body, '\n'), 0600) != nil {
			t.Fatal("cannot persist fixture evidence", err)
		}
	}
	var a *app.Application
	var handler http.Handler
	opens := 0
	open := func() {
		t.Helper()
		store, err := app.OpenStore(fixture.Config.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		adapter := &installedSettingsReceiptLoss{Claude: runtime.NewClaude(fixture.Config.StateDir), root: fixture.Root, drop: fixture.Mode == "lost" && opens == 0, dropRecovery: (fixture.Mode == "recovery-lost" || fixture.Mode == "adoption-recovery-lost") && opens == 1 || fixture.Mode == "original-recovery-lost" && opens == 2}
		opens++
		a, err = app.Open(fixture.Config, store, adapter)
		if err != nil {
			t.Fatal(err)
		}
		handler = console.Handler(a, "127.0.0.1:9468", strings.Repeat("c", 64), os.DirFS("."))
	}
	open()
	t.Cleanup(func() {
		reports["http_calls"] = httpCalls
		check("owned_application_closed", a.Close() == nil)
		write(filepath.Join(fixture.Root, "result.json"), map[string]any{"passed": !t.Failed(), "checks": checks, "reports": reports})
	})
	call := func(path string, body any, out any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "http://127.0.0.1:9468"+path, strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		httpCalls = append(httpCalls, map[string]any{"path": path, "body": body, "status": response.Code, "response": json.RawMessage(append([]byte(nil), response.Body.Bytes()...))})
		if response.Code < 200 || response.Code >= 300 || out != nil && json.Unmarshal(response.Body.Bytes(), out) != nil {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	session := func(id string) app.Session {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				return s
			}
		}
		return app.Session{}
	}
	wait := func(label string, condition func() bool) {
		t.Helper()
		for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if condition() {
				return
			}
			if time.Now().After(deadline) {
				write(filepath.Join(fixture.Root, "failed-snapshot.json"), a.Snapshot())
				t.Fatal("timeout:", label)
			}
		}
	}
	command := func(cmd app.Command) app.CommandReceipt {
		t.Helper()
		var receipt app.CommandReceipt
		call("/api/commands", cmd, &receipt)
		return receipt
	}
	reject := func(cmd app.Command) bool {
		data, _ := json.Marshal(cmd)
		req := httptest.NewRequest("POST", "http://127.0.0.1:9468/api/commands", strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		httpCalls = append(httpCalls, map[string]any{"path": "/api/commands", "body": cmd, "status": response.Code, "response": json.RawMessage(append([]byte(nil), response.Body.Bytes()...))})
		return response.Code == http.StatusConflict
	}
	finish := func(receipt app.CommandReceipt) {
		t.Helper()
		wait("operation "+receipt.ID, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID && op.Status == "succeeded" {
					return session(receipt.SessionID).Status == "idle"
				}
			}
			return false
		})
	}
	ids := map[string]string{}
	for _, name := range []string{"a", "b"} {
		receipt := command(app.Command{ID: "create-" + name, Kind: "create", Title: "Installed " + name, ProjectID: name, RouteID: name, Permission: "read_only"})
		ids[name] = receipt.SessionID
		finish(receipt)
		finish(command(app.Command{ID: "history-" + name, Kind: "send", SessionID: receipt.SessionID, Text: "NETCAP_" + strings.ToUpper(name) + "_TURN_1"}))
	}
	capture := func(label string) {
		t.Helper()
		responses := map[string]json.RawMessage{}
		for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids["a"], "/api/sessions/" + ids["b"]} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9468"+path, nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("capture %s: %d", label, response.Code)
			}
			responses[path] = append([]byte(nil), response.Body.Bytes()...)
		}
		reports["browser_"+label] = responses
	}
	capture("before")
	before, sibling := session(ids["a"]), session(ids["b"])
	originalPath := before.Scope.Instance.SettingsFile
	if originalPath == "" {
		originalPath = filepath.Join(before.Scope.Instance.Profile, "settings.json")
	}
	original, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	reports["before"], reports["sibling_before"] = before, sibling
	apply := func(label, key, mode string, expected int) bool {
		t.Helper()
		if originalRecovery && label == failureLabel {
			check("original_recovery_closes_before_failure", a.Close() == nil)
			open()
			wait("cold original before startup failure", func() bool { s := session(ids["a"]); return s.Status == "idle" && s.View.NativeStatus == "sdk_offline" })
		}
		current := session(ids["a"])
		var catalog app.InstalledPluginCatalog
		call("/api/sessions/"+current.ID+"/plugins/installed", map[string]any{}, &catalog)
		var nativePreview app.InstalledPluginSettingsPreview
		call("/api/sessions/"+current.ID+"/plugins/settings-preview", app.PluginSettingsPreviewRequest{SourceKey: key, Mode: mode, SourceVersion: catalog.SourceVersion}, &nativePreview)
		var staged app.CommandReceipt
		call("/api/sessions/"+current.ID+"/plugins/settings-stage", app.PluginSettingsStageRequest{CommandID: "save-" + label,
			BaseSnapshotID: current.ExecutionSnapshot.ID, PluginSettingsPreviewRequest: app.PluginSettingsPreviewRequest{SourceKey: key, Mode: mode, SourceVersion: catalog.SourceVersion}}, &staged)
		saved := session(current.ID)
		candidate := saved.PendingExecutionChange.Snapshot
		binding := candidate.ClaudeInstalledPluginSettings.Binding
		reports[label+"_saved"] = saved.PendingExecutionChange
		capture(label + "_saved")
		_, fileErr := os.Stat(binding.Artifact.File.Path)
		check(label+"_save_preserves_active_binding", reflect.DeepEqual(current.Scope, saved.Scope) && reflect.DeepEqual(current.ExecutionSnapshot, saved.ExecutionSnapshot))
		check(label+"_save_does_not_publish_execution_file", os.IsNotExist(fileErr))
		check(label+"_all_prior_selections_retained", len(binding.Selections) == expected)
		base := app.ExecutionChangeRequest{BaseSnapshotID: current.ExecutionSnapshot.ID, PendingChangeID: saved.PendingExecutionChange.ID}
		journal := filepath.Join(fixture.Config.StateDir, "runtimes/a", current.Scope.NativeID+".jsonl")
		j0, _ := os.ReadFile(journal)
		if fixture.Mode == "source-drift" {
			if err := os.WriteFile(filepath.Join(fixture.Root, "market/selected/new-resource.txt"), []byte("changed after save"), 0600); err != nil {
				t.Fatal(err)
			}
			check("changed_source_is_rejected_before_preparation", reject(app.Command{ID: "prepare-source-drift", Kind: "prepare_execution", SessionID: current.ID, ExecutionChange: &base}))
			j1, _ := os.ReadFile(journal)
			_, err := os.Stat(binding.Artifact.File.Path)
			check("source_drift_does_not_publish_or_start_candidate", string(j0) == string(j1) && os.IsNotExist(err))
			check("source_drift_preserves_original_snapshot", reflect.DeepEqual(session(current.ID).ExecutionSnapshot, current.ExecutionSnapshot))
			reports["source_drift"] = a.Snapshot()
			return false
		}
		preparedReceipt := command(app.Command{ID: "prepare-" + label, Kind: "prepare_execution", SessionID: current.ID, ExecutionChange: &base})
		if fixture.Mode == "legacy" {
			wait("legacy bridge admission rejected", func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == preparedReceipt.OperationID {
						return op.Status == "not_sent"
					}
				}
				return false
			})
			j1, _ := os.ReadFile(journal)
			_, fileErr := os.Stat(binding.Artifact.File.Path)
			check("legacy_bridge_never_changes_original_query", string(j0) == string(j1))
			check("legacy_bridge_never_publishes_execution_file", os.IsNotExist(fileErr))
			check("legacy_bridge_cannot_apply", reject(app.Command{ID: "apply-legacy", Kind: "apply_execution", SessionID: current.ID, ExecutionChange: &base}))
			reports["legacy_rejection"] = a.Snapshot()
			return false
		}
		finish(preparedReceipt)
		capture(label + "_prepared")
		j1, _ := os.ReadFile(journal)
		check(label+"_prepare_is_read_only", string(j0) == string(j1) && session(current.ID).ExecutionHold && session(current.ID).Paused)
		if failureCase && label == failureLabel {
			marker := "a/fail-next-start"
			if originalRecovery {
				marker = "a/fail-original-start"
			}
			if err := os.WriteFile(filepath.Join(fixture.Root, marker), []byte("owned startup fault"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		applyCommand := app.Command{ID: "apply-" + label, Kind: "apply_execution", SessionID: current.ID, ExecutionChange: &base}
		startupBefore, _ := os.ReadFile(filepath.Join(fixture.Root, "a/startup.jsonl"))
		callsBefore, _ := os.ReadFile(filepath.Join(fixture.Root, "apply-calls.jsonl"))
		appliedReceipt := command(applyCommand)
		if fixture.Mode == "abandoned" {
			wait("candidate rejected before native application", func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == appliedReceipt.OperationID {
						return op.Status == "not_sent" && session(current.ID).Status == "idle"
					}
				}
				return false
			})
			_, exists := os.Stat(binding.Artifact.File.Path)
			check("unsent_candidate_keeps_file_until_discard", exists == nil)
			check("unsent_candidate_keeps_original_binding", reflect.DeepEqual(session(current.ID).Scope, current.Scope))
			command(app.Command{ID: "discard-unsent-installed", Kind: "discard_execution", SessionID: current.ID, ExecutionChange: &base})
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if _, err := os.Stat(binding.Artifact.File.Path); os.IsNotExist(err) {
					break
				}
			}
			_, exists = os.Stat(binding.Artifact.File.Path)
			check("discarded_unsent_candidate_releases_execution_file", os.IsNotExist(exists))
			reports["discarded"] = session(current.ID)
			reports["discarded_snapshot"] = a.Snapshot()
			check("discarded_close_before_restart", a.Close() == nil)
			open()
			check("discarded_apply_retry_uses_old_receipt", command(applyCommand) == appliedReceipt)
			_, exists = os.Stat(binding.Artifact.File.Path)
			check("discarded_restart_never_recreates_execution_file", os.IsNotExist(exists))
			return false
		}
		if (fixture.Mode == "lost" || failureCase) && label == failureLabel {
			wait("uncertain installed application", func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == appliedReceipt.OperationID {
						return op.Status == "uncertain" && session(current.ID).Status == "unknown"
					}
				}
				return false
			})
			uncertain := session(current.ID)
			reports["uncertain"] = uncertain
			capture("uncertain")
			check("uncertain_keeps_original_and_candidate", reflect.DeepEqual(current.ExecutionSnapshot, uncertain.ExecutionSnapshot) && reflect.DeepEqual(current.Scope, uncertain.Scope) && uncertain.PendingExecutionChange != nil && uncertain.ExecutionHold && uncertain.Paused)
			check("uncertain_cannot_send", reject(app.Command{ID: "uncertain-input", Kind: "send", SessionID: current.ID, Text: "must not replay"}))
			check("uncertain_cannot_discard", reject(app.Command{ID: "uncertain-discard", Kind: "discard_execution", SessionID: current.ID, ExecutionChange: &base}))
			check("close_uncertain_application", a.Close() == nil)
			if originalRecovery {
				_, failed := os.Stat(filepath.Join(fixture.Root, "a/original-start-failed.json"))
				_, candidate := os.Stat(filepath.Join(fixture.Root, "a/candidate-started"))
				check("owned_original_start_failed_before_candidate", failed == nil && os.IsNotExist(candidate))
				metadata, err := os.ReadFile(filepath.Join(fixture.Config.StateDir, "runtimes/a", current.Scope.NativeID+".json"))
				var stored struct {
					Change struct {
						Progress struct {
							Version int    `json:"version"`
							Phase   string `json:"phase"`
						} `json:"progress"`
						OriginalPlugins json.RawMessage `json:"original_plugins"`
					} `json:"_configuration"`
				}
				check("original_phase_is_durable_without_invented_inventory", err == nil && json.Unmarshal(metadata, &stored) == nil && stored.Change.Progress.Version == 1 && stored.Change.Progress.Phase == "original" && stored.Change.OriginalPlugins == nil)
				reports["cold_original_failure_metadata"] = json.RawMessage(metadata)
			} else if failureCase {
				_, started := os.Stat(filepath.Join(fixture.Root, "a/candidate-started"))
				check("failed_candidate_really_started", started == nil)
				if err := os.Remove(filepath.Join(fixture.Root, "market/selected/candidate-drift.txt")); err != nil {
					t.Fatal(err)
				}
			}
			open()
			if failureCase {
				wait("failed candidate diagnostic after restart", func() bool { return session(current.ID).View.ConfigurationFailure != nil })
				restarted := session(current.ID)
				reports["failed_after_restart"] = restarted
				capture("failed_after_restart")
				check("restored_sources_do_not_turn_failure_into_success", restarted.Status == "unknown" && restarted.PendingExecutionChange.Status == "applying" && restarted.ExecutionSnapshot.ID == current.ExecutionSnapshot.ID)
				check("failure_keeps_exact_operation_and_cleanup", restarted.View.ConfigurationFailure.Operation == appliedReceipt.OperationID && restarted.View.ConfigurationFailure.Cleanup == "confirmed")
				check("failed_apply_retry_reuses_receipt", command(applyCommand) == appliedReceipt)
				calls, _ := os.ReadFile(filepath.Join(fixture.Root, "apply-calls.jsonl"))
				check("failure_never_replays_apply", strings.Count(string(calls), "\n") == strings.Count(string(callsBefore), "\n")+1)
				if strings.HasPrefix(fixture.Mode, "recovery") || originalRecovery {
					ref := app.ExecutionRecoveryRequest{BaseSnapshotID: current.ExecutionSnapshot.ID, PendingChangeID: saved.PendingExecutionChange.ID, OperationID: appliedReceipt.OperationID}
					var recovery app.ExecutionRecoveryCheck
					call("/api/sessions/"+current.ID+"/execution/recovery-check", ref, &recovery)
					reports["recovery_conditions"] = recovery
					check("installed_recovery_conditions_satisfied", recovery.Satisfied)
					if originalRecovery {
						check("original_recovery_uses_explicit_pre_candidate_mode", recovery.OriginalQueryRetry && recovery.Native.OriginalQueryRetry != nil && restarted.View.ConfigurationFailure.Stage == "open_original")
						ref.OriginalQueryRetry = true
					}
					recoverCommand := app.Command{ID: "recover-installed", Kind: "recover_execution", SessionID: current.ID, ExecutionRecovery: &ref}
					receipt := command(recoverCommand)
					if fixture.Mode == "recovery-lost" || fixture.Mode == "original-recovery-lost" {
						wait("lost recovery receipt remains unknown", func() bool {
							for _, op := range a.Snapshot().Operations {
								if op.ID == receipt.OperationID {
									return op.Status == "uncertain" && session(current.ID).PendingExecutionChange.Status == "recovering"
								}
							}
							return false
						})
						reports["recovery_uncertain"] = session(current.ID)
						check("lost_recovery_retains_original_and_failed_candidate", reflect.DeepEqual(session(current.ID).Scope, current.Scope) && session(current.ID).PendingExecutionChange != nil && session(current.ID).Paused)
						check("lost_recovery_closes_before_reconcile", a.Close() == nil)
						open()
					}
					finish(receipt)
					recovered := session(current.ID)
					reports["recovered"] = recovered
					capture("recovered")
					check("recovery_restores_original_identity_and_binding", reflect.DeepEqual(recovered.Scope, current.Scope) && reflect.DeepEqual(recovered.ExecutionSnapshot, current.ExecutionSnapshot) && len(recovered.View.Messages) == len(current.View.Messages))
					check("recovery_archives_failed_candidate_and_stays_paused", recovered.PendingExecutionChange == nil && recovered.Paused && !recovered.ExecutionHold && len(recovered.ExecutionChangeAttempts) == 1)
					if originalRecovery {
						metadata, err := os.ReadFile(filepath.Join(fixture.Config.StateDir, "runtimes/a", current.Scope.NativeID+".json"))
						var stored struct {
							Change struct {
								OriginalPlugins json.RawMessage `json:"original_plugins"`
								Recovery        struct {
									OriginalQueryRetry *runtime.ClaudeOriginalQueryRecovery `json:"original_query_retry"`
									Plugins            struct {
										Available bool `json:"available"`
									} `json:"plugins"`
								} `json:"recovery"`
							} `json:"_configuration"`
						}
						check("original_recovery_keeps_first_inventory_separate", err == nil && json.Unmarshal(metadata, &stored) == nil && stored.Change.OriginalPlugins == nil && stored.Change.Recovery.Plugins.Available && reflect.DeepEqual(stored.Change.Recovery.OriginalQueryRetry, recovery.Native.OriginalQueryRetry))
						reports["cold_original_recovered_metadata"] = json.RawMessage(metadata)
					}
					check("recovery_retry_reuses_original_receipt", command(recoverCommand) == receipt)
					recoveryCalls, _ := os.ReadFile(filepath.Join(fixture.Root, "recovery-calls.jsonl"))
					check("recovery_has_one_native_dispatch", strings.Count(string(recoveryCalls), "\n") == 1)
					check("recovery_close_before_cold_restart", a.Close() == nil)
					open()
					wait("recovered original session after restart", func() bool { return session(current.ID).Status == "idle" })
					command(app.Command{ID: "resume-recovered", Kind: "resume", SessionID: current.ID})
					wait("recovered input ownership", func() bool { s := session(current.ID); return s.Status == "idle" && !s.Paused && s.View.ExecutionClear })
					finish(command(app.Command{ID: "recovered-input", Kind: "send", SessionID: current.ID, Text: "NETCAP_A_TURN_2_RECOVERED"}))
					check("recovery_cold_input_keeps_original_version", session(current.ID).ExecutionSnapshot.ID == current.ExecutionSnapshot.ID && session(current.ID).Scope.NativeID == current.Scope.NativeID)
					reports["recovered_cold"] = session(current.ID)
				}
				return false
			}
			finish(appliedReceipt)
			check("lost_receipt_retry_reuses_operation", command(applyCommand) == appliedReceipt)
			calls, _ := os.ReadFile(filepath.Join(fixture.Root, "apply-calls.jsonl"))
			check("lost_receipt_reconciles_without_apply_replay", strings.Count(string(calls), "\n") == 1)
		} else {
			finish(appliedReceipt)
		}
		applied := session(current.ID)
		if fixture.Mode == "cold-apply" && label == "disable-sibling" {
			startupAfter, _ := os.ReadFile(filepath.Join(fixture.Root, "a/startup.jsonl"))
			entries := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(startupAfter), string(startupBefore))), "\n")
			journaled := len(entries) > 0
			for _, line := range entries {
				var hook struct {
					Status    string `json:"native_transaction"`
					Operation string `json:"native_operation"`
				}
				journaled = journaled && json.Unmarshal([]byte(line), &hook) == nil && hook.Status == "applying" && hook.Operation == appliedReceipt.OperationID
			}
			check("cold_original_start_is_journaled_before_hook", journaled)
		}
		reports[label+"_applied"] = applied
		capture(label + "_applied")
		check(label+"_same_identity_and_history", applied.Scope.NativeID == before.Scope.NativeID && len(applied.View.Messages) == len(current.View.Messages))
		check(label+"_only_verified_candidate_promoted", applied.PendingExecutionChange == nil && applied.ExecutionSnapshot.ID == candidate.ID && applied.Scope.ClaudeInstalledSettings != nil && applied.Scope.ClaudeInstalledSettings.Artifact == binding.Artifact)
		check(label+"_native_components_observed", len(applied.View.PluginEvidence) == expected)
		for _, p := range applied.View.PluginEvidence {
			check(label+"_loading_evidence_"+p.NativeSource, p.Verified == (applied.View.NativeStatus == "sdk_live"))
		}
		info, err := os.Stat(binding.Artifact.File.Path)
		check(label+"_private_execution_file_exists", err == nil && info.Mode().Perm() == 0600)
		parent := filepath.Dir(originalPath)
		if before.Scope.Instance.SettingsFile == "" {
			parent = before.Scope.Cwd
		}
		check(label+"_file_keeps_original_directory_semantics", filepath.Dir(binding.Artifact.File.Path) == parent)
		check(label+"_remains_paused", applied.Paused && !applied.ExecutionHold)
		currentOriginal, _ := os.ReadFile(originalPath)
		check(label+"_original_settings_unchanged", string(currentOriginal) == string(original))
		other := session(ids["b"])
		check(label+"_other_profile_unchanged", reflect.DeepEqual(other.Scope, sibling.Scope) && reflect.DeepEqual(other.ExecutionSnapshot, sibling.ExecutionSnapshot) && len(other.View.Messages) == len(sibling.View.Messages))
		return true
	}
	if !apply("disable-selected", "selected@owned", "disabled", 1) {
		return
	}
	if fixture.Mode == "adoption" || strings.HasPrefix(fixture.Mode, "adoption-recovery") {
		original := session(ids["a"])
		local := filepath.Join(original.Scope.Cwd, ".claude/settings.local.json")
		originalLocal, localReadErr := os.ReadFile(local)
		if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			t.Fatal(err)
		}
		write(local, map[string]any{"env": map[string]string{"NETCAP_ADOPTED_SETTING": "v2"}})
		request := app.ExecutionChangeRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, AdoptLocalSettings: true}
		var preview app.ExecutionChangePreview
		call("/api/sessions/"+ids["a"]+"/execution/preview", request, &preview)
		request.PreviewHash = preview.PreviewHash
		command(app.Command{ID: "save-installed-adoption", Kind: "stage_execution", SessionID: ids["a"], ExecutionChange: &request})
		saved := session(ids["a"])
		reports["adoption_saved"] = saved
		capture("adoption_saved")
		ref := app.ExecutionChangeRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, PendingChangeID: saved.PendingExecutionChange.ID}
		finish(command(app.Command{ID: "prepare-installed-adoption", Kind: "prepare_execution", SessionID: ids["a"], ExecutionChange: &ref}))
		capture("adoption_prepared")
		if strings.HasPrefix(fixture.Mode, "adoption-recovery") {
			if localReadErr != nil {
				t.Fatal(localReadErr)
			}
			write(filepath.Join(fixture.Root, "a/fail-next-start"), "owned adoption startup fault")
			failed := command(app.Command{ID: "apply-installed-adoption", Kind: "apply_execution", SessionID: ids["a"], ExecutionChange: &ref})
			wait("installed adoption failure", func() bool {
				s := session(ids["a"])
				return s.Status == "unknown" && s.View.ConfigurationFailure != nil
			})
			capture("adoption_failed")
			check("failed_adoption_keeps_original_snapshot", reflect.DeepEqual(session(ids["a"]).ExecutionSnapshot, original.ExecutionSnapshot))
			check("close_failed_adoption", a.Close() == nil)
			if err := os.Remove(filepath.Join(fixture.Root, "market/selected/candidate-drift.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(local, originalLocal, 0600); err != nil {
				t.Fatal(err)
			}
			open()
			wait("restored adoption failure", func() bool {
				s := session(ids["a"])
				return s.Status == "unknown" && s.View.ConfigurationFailure != nil
			})
			capture("adoption_restored")
			r := app.ExecutionRecoveryRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, PendingChangeID: saved.PendingExecutionChange.ID, OperationID: failed.OperationID}
			var recovery app.ExecutionRecoveryCheck
			call("/api/sessions/"+ids["a"]+"/execution/recovery-check", r, &recovery)
			reports["adoption_recovery_conditions"] = recovery
			check("restored_local_identity_has_explicit_recovery_version", recovery.Satisfied && recovery.InstalledSettings != nil && recovery.InstalledSettings.SourceVersion != original.Scope.ClaudeInstalledSettings.StartupSourceVersion)
			if recovery.InstalledSettings == nil {
				t.Fatal("missing restoration plan")
			}
			r.InstalledSourceVersion = recovery.InstalledSettings.SourceVersion
			recoverCommand := app.Command{ID: "recover-adoption", Kind: "recover_execution", SessionID: ids["a"], ExecutionRecovery: &r}
			receipt := command(recoverCommand)
			if fixture.Mode == "adoption-recovery-lost" {
				wait("recovery identity receipt lost", func() bool {
					for _, op := range a.Snapshot().Operations {
						if op.ID == receipt.OperationID {
							return op.Status == "uncertain" && session(ids["a"]).PendingExecutionChange.Status == "recovering"
						}
					}
					return false
				})
				capture("adoption_recovery_unknown")
				check("unknown_recovery_keeps_original_and_new_plan", session(ids["a"]).ExecutionSnapshot.ID == original.ExecutionSnapshot.ID && session(ids["a"]).PendingExecutionChange.RecoverySnapshot != nil)
				check("close_unknown_identity_recovery", a.Close() == nil)
				open()
			}
			finish(receipt)
			recovered := session(ids["a"])
			reports["adoption_recovered"] = recovered
			capture("adoption_recovered")
			check("recovered_new_version_keeps_original_id_history", recovered.Scope.NativeID == original.Scope.NativeID && recovered.ExecutionSnapshot.ID != original.ExecutionSnapshot.ID && len(recovered.View.Messages) == len(original.View.Messages))
			check("recovered_snapshot_links_unchanged_original", recovered.ExecutionSnapshot.RestoredFrom == original.ExecutionSnapshot.ID && reflect.DeepEqual(recovered.ExecutionHistory[len(recovered.ExecutionHistory)-1], original.ExecutionSnapshot))
			check("recovered_snapshot_uses_reobserved_identity", recovered.Scope.ClaudeInstalledSettings.StartupSourceVersion == r.InstalledSourceVersion && recovered.Scope.ClaudeInstalledSettings.Artifact.ID == recovered.ExecutionSnapshot.ID)
			check("recovered_keeps_installed_choices_and_failure", runtime.EqualPluginSelections(recovered.Scope.ClaudeInstalledSettings.Selections, original.Scope.ClaudeInstalledSettings.Selections) && len(recovered.ExecutionChangeAttempts) == 1 && recovered.ExecutionChangeAttempts[0].Change.Snapshot.ID == saved.PendingExecutionChange.Snapshot.ID && recovered.Paused)
			check("recovery_identity_command_reuses_receipt", command(recoverCommand) == receipt)
			calls, _ := os.ReadFile(filepath.Join(fixture.Root, "recovery-calls.jsonl"))
			check("identity_recovery_dispatched_once", strings.Count(string(calls), "\n") == 1)
			check("close_recovery_before_cold_input", a.Close() == nil)
			open()
			wait("identity recovery cold session", func() bool { return session(ids["a"]).Status == "idle" })
			command(app.Command{ID: "resume-identity-recovered", Kind: "resume", SessionID: ids["a"]})
			wait("identity recovery input ownership", func() bool { s := session(ids["a"]); return s.Status == "idle" && !s.Paused && s.View.ExecutionClear })
			finish(command(app.Command{ID: "identity-recovered-input", Kind: "send", SessionID: ids["a"], Text: "NETCAP_A_TURN_2_IDENTITY_RECOVERED"}))
			check("cold_input_uses_recovered_identity_version", session(ids["a"]).ExecutionSnapshot.ID == recovered.ExecutionSnapshot.ID && session(ids["a"]).Scope.NativeID == original.Scope.NativeID)
			reports["adoption_recovered_cold"] = session(ids["a"])
			return
		}
		finish(command(app.Command{ID: "apply-installed-adoption", Kind: "apply_execution", SessionID: ids["a"], ExecutionChange: &ref}))
		adopted := session(ids["a"])
		reports["adoption_applied"] = adopted
		capture("adoption_applied")
		check("adoption_keeps_original_id_history_and_plugins", adopted.Scope.NativeID == original.Scope.NativeID && len(adopted.View.Messages) == len(original.View.Messages) && runtime.EqualPluginSelections(adopted.Scope.ClaudeInstalledSettings.Selections, original.Scope.ClaudeInstalledSettings.Selections))
		check("adoption_binds_actual_new_startup_sources", adopted.Scope.ClaudeInstalledSettings.StartupSourceVersion != original.Scope.ClaudeInstalledSettings.StartupSourceVersion && adopted.Scope.ClaudeInstalledSettings.Artifact.ID == adopted.ExecutionSnapshot.ID)
		body, err := os.ReadFile(filepath.Join(fixture.Root, "a/startup.jsonl"))
		check("adoption_native_hook_observes_new_local_setting", err == nil && strings.Contains(string(body), `"adopted": "v2"`))
		check("adoption_preserves_old_snapshot", reflect.DeepEqual(adopted.ExecutionHistory[len(adopted.ExecutionHistory)-1], original.ExecutionSnapshot))
		check("adoption_close_before_cold_input", a.Close() == nil)
		open()
		wait("adopted session after cold restart", func() bool { return session(ids["a"]).Status == "idle" })
		command(app.Command{ID: "resume-adopted", Kind: "resume", SessionID: ids["a"]})
		wait("adopted input ownership", func() bool { s := session(ids["a"]); return s.Status == "idle" && !s.Paused && s.View.ExecutionClear })
		finish(command(app.Command{ID: "adopted-cold-input", Kind: "send", SessionID: ids["a"], Text: "NETCAP_A_TURN_2_ADOPTED"}))
		check("adoption_cold_input_keeps_version", session(ids["a"]).ExecutionSnapshot.ID == adopted.ExecutionSnapshot.ID && session(ids["a"]).Scope.NativeID == original.Scope.NativeID)
		reports["adoption_cold"] = session(ids["a"])
		return
	}
	resume := func(label string) {
		t.Helper()
		command(app.Command{ID: "resume-" + label, Kind: "resume", SessionID: ids["a"]})
		wait("input ownership after resume "+label, func() bool { s := session(ids["a"]); return s.Status == "idle" && !s.Paused && s.View.ExecutionClear })
	}
	resume("second-turn")
	finish(command(app.Command{ID: "second-turn", Kind: "send", SessionID: ids["a"], Text: "NETCAP_A_TURN_2"}))
	continued := session(ids["a"])
	check("continued_input_reverifies_loaded_plugin", continued.View.NativeStatus == "sdk_live" && len(continued.View.PluginEvidence) == 1 && continued.View.PluginEvidence[0].Verified)
	if fixture.Mode == "cold-apply" {
		check("cold_apply_closes_original_application", a.Close() == nil)
		open()
		wait("original session cold before apply", func() bool { s := session(ids["a"]); return s.Status == "idle" && s.View.NativeStatus == "sdk_offline" })
		check("cold_apply_has_no_original_query", session(ids["a"]).View.NativeStatus == "sdk_offline")
	}
	if !apply("disable-sibling", "sibling@owned", "disabled", 2) || !apply("enable-selected", "selected@owned", "enabled", 2) {
		return
	}
	// Ordinary changes after installed settings must preserve every installed
	// selection while binding their new execution version's own private file.
	installedBefore := session(ids["a"])
	changeRequest := app.ExecutionChangeRequest{BaseSnapshotID: installedBefore.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{{AssetID: "plain", Mode: "enabled"}}}
	var preview app.ExecutionChangePreview
	call("/api/sessions/"+ids["a"]+"/execution/preview", changeRequest, &preview)
	changeRequest.PreviewHash = preview.PreviewHash
	command(app.Command{ID: "save-plain-mcp", Kind: "stage_execution", SessionID: ids["a"], ExecutionChange: &changeRequest})
	changeRef := app.ExecutionChangeRequest{BaseSnapshotID: installedBefore.ExecutionSnapshot.ID, PendingChangeID: session(ids["a"]).PendingExecutionChange.ID}
	finish(command(app.Command{ID: "prepare-plain-mcp", Kind: "prepare_execution", SessionID: ids["a"], ExecutionChange: &changeRef}))
	finish(command(app.Command{ID: "apply-plain-mcp", Kind: "apply_execution", SessionID: ids["a"], ExecutionChange: &changeRef}))
	mixed := session(ids["a"])
	reports["ordinary_mcp_change"] = mixed
	check("ordinary_change_preserves_installed_choices", runtime.EqualPluginSelections(mixed.Scope.ClaudeInstalledSettings.Selections, installedBefore.Scope.ClaudeInstalledSettings.Selections))
	check("ordinary_change_binds_new_file_version", mixed.Scope.ClaudeInstalledSettings.Artifact.ID == mixed.ExecutionSnapshot.ID && mixed.Scope.ClaudeInstalledSettings.Artifact.ID != installedBefore.Scope.ClaudeInstalledSettings.Artifact.ID)
	check("ordinary_mcp_is_loaded", len(mixed.Scope.MCPSelections) == 1 && mixed.Scope.MCPSelections[0].Server == "plain")
	for _, e := range mixed.View.CapabilityEvidence {
		if e.Server == "plain" {
			check("ordinary_mcp_native_connection_verified", e.Verified && e.RuntimeStatus == "connected")
		}
	}
	resume("tool-turn")
	tool := command(app.Command{ID: "tool-turn", Kind: "send", SessionID: ids["a"], Text: "NETCAP_A_TURN_3_INSTALL_MCP"})
	wait("installed MCP approval", func() bool { return session(ids["a"]).Status == "waiting_approval" })
	withApproval := session(ids["a"])
	var pending runtime.Pending
	for _, item := range withApproval.View.Pending {
		if item.Status == "pending" {
			pending = item
		}
	}
	check("installed_tool_requires_its_own_approval", pending.Kind == "mcp__plugin_selected_identity__whoami" && pending.NativeTurn == tool.OperationID)
	command(app.Command{ID: "allow-installed-tool", Kind: "respond", SessionID: ids["a"], PendingID: pending.ID, OptionID: "allow"})
	finish(tool)
	current := session(ids["a"])
	reports["after_tool"] = current
	capture("after_tool")
	file := current.Scope.ClaudeInstalledSettings.Artifact
	info, err := os.Stat(file.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	check("close_before_cold_restart", a.Close() == nil)
	open()
	wait("same session after cold restart", func() bool { return session(ids["a"]).Status == "idle" })
	check("cold_restart_preserves_version", reflect.DeepEqual(current.Scope, session(ids["a"]).Scope) && current.ExecutionSnapshot.ID == session(ids["a"]).ExecutionSnapshot.ID)
	coldInfo, err := os.Stat(file.File.Path)
	check("cold_restart_reuses_same_file", err == nil && os.SameFile(info, coldInfo))
	resume("cold-input")
	finish(command(app.Command{ID: "cold-input", Kind: "send", SessionID: ids["a"], Text: "NETCAP_A_TURN_4_COLD"}))
	reports["cold"] = session(ids["a"])
	capture("cold")
	check("cold_input_keeps_original_identity", session(ids["a"]).Scope.NativeID == before.Scope.NativeID)
	check("four_model_turns_and_original_history", len(session(ids["a"]).View.Messages) >= 8)
	for _, name := range []string{"a", "b"} {
		body, err := os.ReadFile(filepath.Join(fixture.Root, name, "startup.jsonl"))
		check(name+"_declaration_loaded_instead_of_cache", err == nil && !strings.Contains(string(body), "CACHE_CANARY") && strings.Contains(string(body), "custom-"+strings.ToUpper(name)))
	}
	reports["snapshot"] = a.Snapshot()
	write(filepath.Join(fixture.Root, "sessions.json"), ids)
	t.Log(fmt.Sprintf("%d native checks", len(checks)))
}
