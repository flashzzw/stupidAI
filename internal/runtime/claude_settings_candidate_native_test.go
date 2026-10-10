//go:build linux && nativeclaude

package runtime_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func exerciseNativeInstalledSettingsCandidate(t *testing.T, a *app.Application, cfg app.Config, root string, catalog app.InstalledPluginCatalog, check func(string, bool), results map[string]any) *app.Application {
	t.Helper()
	before := a.Snapshot().Sessions[0]
	base := "/api/sessions/" + before.ID
	handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
	var calls []map[string]any
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://127.0.0.1:9463"+path, strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		calls = append(calls, map[string]any{"method": method, "path": path, "body": body, "status": rec.Code, "response": json.RawMessage(append([]byte(nil), rec.Body.Bytes()...))})
		return rec
	}
	capture := func(label string) {
		t.Helper()
		responses := map[string]json.RawMessage{}
		for _, path := range []string{"/api/workspace", "/api/snapshot", base} {
			response := call("GET", path, nil)
			if response.Code != 200 {
				t.Fatalf("capture %s: %d", label, response.Code)
			}
			responses[path] = append([]byte(nil), response.Body.Bytes()...)
		}
		results[label] = responses
	}
	file := func(name string) string {
		body, _ := os.ReadFile(filepath.Join(root, name))
		return string(body)
	}
	capture("candidate_before")
	original, _ := os.ReadFile(before.Scope.Instance.SettingsFile)
	wireBefore := file("native-settings-wire.jsonl")
	request := app.PluginSettingsStageRequest{CommandID: "save-installed-settings", BaseSnapshotID: before.ExecutionSnapshot.ID,
		PluginSettingsPreviewRequest: app.PluginSettingsPreviewRequest{SourceKey: "selected@preview-local", Mode: "disabled", SourceVersion: catalog.SourceVersion}}
	for _, scenario := range []string{"base", "source"} {
		bad := request
		if scenario == "base" {
			bad.BaseSnapshotID = "stale-snapshot"
		} else {
			bad.SourceVersion = strings.Repeat("0", 64)
		}
		response := call("POST", base+"/plugins/settings-stage", bad)
		check("candidate_rejects_stale_"+scenario, response.Code == 409)
	}
	check("candidate_client_proof_rejected", call("POST", base+"/plugins/settings-stage", map[string]any{"command_id": "forged", "validation": map[string]bool{"matches": true}}).Code == 400)
	check("candidate_invalid_request_has_no_native_reads", file("native-settings-wire.jsonl") == wireBefore)
	response := call("POST", base+"/plugins/settings-stage", request)
	check("candidate_saved_by_real_handler", response.Code == 200)
	if response.Code != 200 {
		t.Fatalf("native candidate save: %s", response.Body.String())
	}
	var receipt app.CommandReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	after := a.Snapshot().Sessions[0]
	change := after.PendingExecutionChange
	if change == nil || change.Snapshot.ClaudeInstalledPluginSettings == nil {
		t.Fatal("saved candidate missing")
	}
	proof := change.Snapshot.ClaudeInstalledPluginSettings
	results["candidate_receipt"], results["candidate_change"], results["candidate_preview"] = receipt, change, proof.Preview
	check("candidate_receipt_binds_persistent_version", receipt.Status == "pending" && receipt.PendingChangeID == change.ID && receipt.OperationID == "")
	check("candidate_version_links_original", change.Snapshot.SchemaVersion == 2 && change.Snapshot.PreviousID == before.ExecutionSnapshot.ID && change.Snapshot.Revision == 2 && change.Snapshot.ID != before.ExecutionSnapshot.ID)
	check("candidate_original_snapshot_unchanged", reflect.DeepEqual(after.ExecutionSnapshot, before.ExecutionSnapshot))
	check("candidate_original_scope_identity_control_unchanged", reflect.DeepEqual(before.Scope, after.Scope) && before.Generation == after.Generation && before.ControlMode == after.ControlMode && before.Paused == after.Paused && !after.ExecutionHold)
	check("candidate_bound_actual_install_registration", proof.Preview.SourceVersion == catalog.SourceVersion && reflect.DeepEqual(proof.Preview.SourceFiles, catalog.SourceFiles) && proof.Preview.Entry == catalog.Entries[0])
	check("candidate_bound_declaration_tree", proof.Resource.Kind == "directory" && proof.Resource.Path == catalog.Entries[0].DeclaredPath && len(proof.Resource.SHA256) == 64)
	check("candidate_startup_report_binds_complete_observation", proof.Preview.StartupSourceVersion == proof.StartupSettings.Fingerprint() && proof.StartupSettings.ReadComplete && !proof.StartupSettings.Complete)
	check("candidate_native_version_and_target_match", proof.Preview.Validation.CLIVersion == "2.1.280" && proof.Preview.Validation.SchemaValid && proof.Preview.Validation.PluginSettings[0].Matches && !*proof.Preview.Validation.PluginSettings[0].ActualEnabled)
	check("candidate_native_preserves_other_settings_and_permissions", proof.Preview.Validation.Preservation.Matches && proof.Preview.Validation.Preservation.BaselineSettingsFile == before.Scope.Instance.SettingsFile)
	check("candidate_native_original_and_derived_processes_stopped", proof.Preview.Validation.ProcessStopped && proof.Preview.Validation.Preservation.BaselineProcessStopped && proof.Preview.Validation.FileHolderObserved)
	check("candidate_preview_not_full_launch_admission", !proof.Preview.Complete && proof.Preview.SourcesStable && len(proof.Preview.Problems) == 0)
	check("candidate_temporary_file_released", proof.Preview.Cleanup.State == "released" && len(proof.Preview.Cleanup.Issues) == 0)
	_, err := os.Lstat(proof.Preview.Artifact.File.Path)
	check("candidate_temporary_file_actually_absent", os.IsNotExist(err))
	current, _ := os.ReadFile(before.Scope.Instance.SettingsFile)
	check("candidate_original_settings_bytes_unchanged", string(current) == string(original))
	capture("candidate_saved")
	wireAfter := file("native-settings-wire.jsonl")
	check("candidate_save_performed_native_read", len(wireAfter) > len(wireBefore))
	duplicate := call("POST", base+"/plugins/settings-stage", request)
	check("candidate_same_request_same_receipt", duplicate.Code == 200 && duplicate.Body.String() == response.Body.String())
	changed := request
	changed.Mode = "enabled"
	check("candidate_same_id_different_selection_rejected", call("POST", base+"/plugins/settings-stage", changed).Code == 409)
	ref := app.ExecutionChangeRequest{BaseSnapshotID: request.BaseSnapshotID, PendingChangeID: change.ID}
	for _, kind := range []string{"stage_execution", "prepare_execution", "apply_execution"} {
		r := call("POST", "/api/commands", app.Command{ID: kind, Kind: kind, SessionID: before.ID, ExecutionChange: &ref})
		check("candidate_"+kind+"_explicitly_rejected", r.Code == 409 && strings.Contains(r.Body.String(), "此候选格式尚未接入"))
	}
	check("candidate_retry_and_rejections_no_native_reads", file("native-settings-wire.jsonl") == wireAfter)
	check("candidate_no_apply_operation_or_pause", len(a.Snapshot().Operations) == 1 && a.Snapshot().Sessions[0].Generation == before.Generation && !a.Snapshot().Sessions[0].ExecutionHold)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Keep an owned, closed SQLite checkpoint for independent read-only audit.
	db, err := os.ReadFile(filepath.Join(cfg.StateDir, "console.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "saved-console.sqlite"), db, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(cfg, store, runtime.NewClaude(cfg.StateDir))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	handler = console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
	reopened := a.Snapshot().Sessions[0]
	check("candidate_restart_restores_exact_proposal", reflect.DeepEqual(reopened.PendingExecutionChange, change))
	check("candidate_restart_keeps_effective_version_and_identity", reflect.DeepEqual(reopened.ExecutionSnapshot, before.ExecutionSnapshot) && reflect.DeepEqual(reopened.Scope, before.Scope))
	duplicate = call("POST", base+"/plugins/settings-stage", request)
	check("candidate_restart_retry_same_receipt", duplicate.Code == 200 && duplicate.Body.String() == response.Body.String())
	check("candidate_restart_retry_no_native_reads", file("native-settings-wire.jsonl") == wireAfter)
	second := request
	second.CommandID = "other-candidate"
	check("candidate_competing_proposal_rejected", call("POST", base+"/plugins/settings-stage", second).Code == 409)
	capture("candidate_restarted")
	wrong := ref
	wrong.PendingChangeID = "stale-pending"
	check("candidate_stale_discard_rejected", call("POST", "/api/commands", app.Command{ID: "stale-discard", Kind: "discard_execution", SessionID: before.ID, ExecutionChange: &wrong}).Code == 409)
	discarded := call("POST", "/api/commands", app.Command{ID: "discard-candidate", Kind: "discard_execution", SessionID: before.ID, ExecutionChange: &ref})
	check("candidate_discarded_by_existing_command", discarded.Code == 202 && a.Snapshot().Sessions[0].PendingExecutionChange == nil)
	check("candidate_discard_keeps_original_binding", reflect.DeepEqual(a.Snapshot().Sessions[0].ExecutionSnapshot, before.ExecutionSnapshot) && reflect.DeepEqual(a.Snapshot().Sessions[0].Scope, before.Scope))
	duplicate = call("POST", base+"/plugins/settings-stage", request)
	check("candidate_retry_after_discard_does_not_recreate", duplicate.Code == 200 && duplicate.Body.String() == response.Body.String() && a.Snapshot().Sessions[0].PendingExecutionChange == nil)
	check("candidate_discard_and_old_retry_no_native_reads", file("native-settings-wire.jsonl") == wireAfter)
	capture("candidate_discarded")
	results["candidate_http_calls"] = calls
	return a
}
