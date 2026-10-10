//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

// The provider uses the same fixed text Responses stream as tests/runtime/common.py.
// A completed model turn here proves transport and lifecycle, not autonomous work.
func TestCodexNativeSuccessfulHookRecords(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-hook-success-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root, 3*time.Minute)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	checks, samples := map[string]bool{}, map[string]any{}
	responses := map[string]map[string]json.RawMessage{}
	var requestMu sync.Mutex
	requests := []map[string]any{}
	check := func(name string, value bool) {
		t.Helper()
		checks[name] = value
		if !value {
			t.Errorf("failed: %s", name)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		var payload map[string]any
		if err != nil || json.Unmarshal(body, &payload) != nil {
			http.Error(w, "invalid fixture input", 400)
			return
		}
		auth := r.Header.Get("Authorization") == "Bearer synthetic-only"
		requestMu.Lock()
		requests = append(requests, map[string]any{"path": r.URL.Path, "auth_matches": auth, "body": payload})
		seq := len(requests)
		requestMu.Unlock()
		if !auth || r.Method != "POST" || r.URL.Path != "/v1/responses" {
			http.Error(w, "unexpected fixture request", 400)
			return
		}
		answer := "OWNED_HOOK_SUCCESS_REPLY"
		part := map[string]any{"type": "output_text", "text": answer, "annotations": []any{}}
		item := map[string]any{"id": fmt.Sprintf("msg_%d", seq), "type": "message", "role": "assistant", "status": "completed", "phase": "final_answer", "content": []any{part}}
		response := map[string]any{"id": fmt.Sprintf("resp_%d", seq), "object": "response", "status": "completed", "created_at": time.Now().Unix(), "model": payload["model"], "output": []any{item}, "usage": map[string]int{"input_tokens": 32, "output_tokens": 8, "total_tokens": 40}}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": response["id"], "object": "response", "status": "in_progress", "model": payload["model"], "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": item["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": answer},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": response},
		} {
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		requestMu.Lock()
		defer requestMu.Unlock()
		data, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "samples": samples, "responses": responses, "processes": f.Records, "provider_requests": requests, "passed": !t.Failed(), "scope": "production Application/SQLite/Runtime/Handler; selected synchronous UserPromptSubmit hook success, local fixed text provider, version changes and cold reopen; no autonomous method Gate"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), data, 0600)
	})
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, _ := os.ReadFile(configPath)
	f.Route.Endpoint = provider.URL + "/v1"
	write(configPath, strings.Replace(strings.Replace(string(config), "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1), "http://127.0.0.1:9/v1", f.Route.Endpoint, 1))
	profileBefore, _ := os.ReadFile(configPath)
	marker := filepath.Join(root, "hook-inputs.jsonl")
	packageAt := func(version string) string {
		source := filepath.Join(root, "source", version)
		write(filepath.Join(source, ".codex-plugin/plugin.json"), fmt.Sprintf(`{"name":"success-hook","version":"%s.0.0"}`, version))
		write(filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 ${PLUGIN_ROOT}/entry.py","timeout":3}]}]}}`)
		write(filepath.Join(source, "entry.py"), fmt.Sprintf("import json,sys\nfrom pathlib import Path\ndata=json.load(sys.stdin)\nwith Path(%q).open('a') as f:f.write(json.dumps({'version':%q,'input':data})+'\\n')\nprint('PRIVATE_HOOK_OUTPUT')\n", marker, version))
		return source
	}
	asset := app.CapabilityAsset{ID: "success-hooks", Name: "Successful hooks", Kind: "cli_plugin", Version: "asset-v1", Source: packageAt("1"), Backends: []string{"codex"}}
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: []app.CapabilityAsset{asset}}
	open := func() *app.Application {
		store, err := app.OpenStore(cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		a, err := app.Open(cfg, store, f.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := open()
	apply := func(command app.Command) app.CommandReceipt {
		t.Helper()
		r, err := a.Apply(command)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	find := func(id string) app.Session {
		t.Helper()
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				return s
			}
		}
		t.Fatal("missing session")
		return app.Session{}
	}
	waitOp := func(id string) app.Operation {
		t.Helper()
		var found app.Operation
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == id {
					found = op
					if op.Status == "failed" || op.Status == "not_sent" || op.Status == "uncertain" {
						t.Fatalf("operation %s: %s %s", op.Intent.Kind, op.Status, op.Error)
					}
					return op.Status == "succeeded"
				}
			}
			return false
		})
		return found
	}
	inspect := func() app.CapabilityHookInspection {
		t.Helper()
		items, err := a.ReadCapabilities(context.Background())
		if err != nil || len(items) != 1 {
			t.Fatal(err)
		}
		item := items[0]
		out, err := a.InspectCapabilityHooks(context.Background(), app.CapabilityHookInspectionRequest{AssetID: asset.ID, InstanceID: f.Instance.ID, Revision: item.Revision, SourceSHA256: item.Source.SHA256})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	inspection := inspect()
	samples["inspection-v1"] = inspection
	ids := map[string]string{}
	for _, row := range []struct{ label, mode, trust string }{{"A", "enabled", "trusted"}, {"B", "enabled", "untrusted"}, {"C", "disabled", "trusted"}} {
		r := apply(app.Command{ID: "create-" + row.label, Kind: "create", Title: "Successful hook " + row.label, ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", CapabilitySelections: []app.CapabilitySelection{{AssetID: asset.ID, Version: asset.Version, Mode: row.mode, HookReview: &app.CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: row.trust}}}})
		ids[row.label] = r.SessionID
		waitOp(r.OperationID)
		waitNativeApplication(t, a, func(_ app.Snapshot) bool {
			s := find(r.SessionID)
			return s.Status == "idle" && len(s.View.PluginEvidence) == 1 && s.View.PluginEvidence[0].Verified
		})
		s := find(r.SessionID)
		check(row.label+"_creation_does_not_invoke_hook", len(s.View.HookExecutions) == 0)
		check(row.label+"_selection_and_trust_bound", s.Scope.PluginSelections[0].Mode == row.mode && s.Scope.PluginSelections[0].Hooks.Trust == row.trust)
	}
	samples["initial"] = a.Snapshot()
	samples["sessions"] = ids
	turn := func(label, id, version string, invoke bool) {
		t.Helper()
		before := find(id)
		requestMu.Lock()
		requestCount := len(requests)
		requestMu.Unlock()
		sent := apply(app.Command{ID: label, Kind: "send", SessionID: id, Text: "OWNED_HOOK_CASE " + label})
		op := waitOp(sent.OperationID)
		waitNativeApplication(t, a, func(_ app.Snapshot) bool { return find(id).Status == "idle" })
		current := find(id)
		check(label+"_native_turn_completed", f.HasTurnEvent("turn/completed", current.Scope.NativeID, op.Receipt.NativeTurn))
		requestMu.Lock()
		count := len(requests)
		authenticated := count == requestCount+1 && requests[count-1]["auth_matches"] == true
		requestMu.Unlock()
		check(label+"_one_authenticated_model_request", authenticated)
		body, _ := json.Marshal(current.View.Messages)
		check(label+"_model_answer_recorded", strings.Contains(string(body), "OWNED_HOOK_SUCCESS_REPLY"))
		hooks := []runtime.HookExecution{}
		for _, h := range current.View.HookExecutions {
			if h.Operation == sent.OperationID {
				hooks = append(hooks, h)
			}
		}
		expected := 0
		if invoke {
			expected = 1
		}
		check(label+"_invocations_follow_selection", len(hooks) == expected)
		markerBody, _ := os.ReadFile(marker)
		matches := 0
		for _, line := range strings.Split(strings.TrimSpace(string(markerBody)), "\n") {
			var row struct {
				Version string `json:"version"`
				Input   struct {
					SessionID string `json:"session_id"`
					Prompt    string `json:"prompt"`
				} `json:"input"`
			}
			_ = json.Unmarshal([]byte(line), &row)
			if row.Input.SessionID == current.Scope.NativeID && row.Input.Prompt == "OWNED_HOOK_CASE "+label {
				matches++
				check(label+"_actual_script_version", row.Version == version)
			}
		}
		check(label+"_actual_marker_count", matches == expected)
		if len(hooks) == 1 {
			h := hooks[0]
			check(label+"_native_success_metadata", h.Status == "success" && h.NativeStatus == "completed" && h.Event == "userPromptSubmit" && h.TurnID == op.Receipt.NativeTurn && h.NativeRunID != "" && h.StartedAt != "" && h.FinishedAt != "" && h.DurationMS != nil && h.ExitCode == nil)
			check(label+"_original_asset_and_snapshot", h.AssetID == asset.ID && h.AssetVersion == "asset-v"+version && h.ExecutionSnapshotID == before.ExecutionSnapshot.ID && h.SourceHash == before.Scope.PluginSelections[0].SourceHash && h.Source == "plugin")
			encoded, _ := json.Marshal(h)
			check(label+"_private_output_omitted", !strings.Contains(string(encoded), "PRIVATE_HOOK_OUTPUT") && !strings.Contains(string(encoded), "python3"))
		}
		check(label+"_terminal_hook_releases_occupancy", current.View.ExecutionClear && len(current.View.Executions) == 0)
		samples[label] = a.Snapshot()
	}
	turn("A-v1-first", ids["A"], "1", true)
	turn("A-v1-second", ids["A"], "1", true)
	turn("B-untrusted", ids["B"], "1", false)
	turn("C-disabled", ids["C"], "1", false)
	change := func(label, trust string, review app.CapabilityHookInspection) {
		t.Helper()
		before := find(ids["A"])
		req := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{{AssetID: asset.ID, Mode: "enabled", Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: review.InspectionID, Trust: trust}}}}
		preview, err := a.PreviewExecutionChange(before.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		req.PreviewHash = preview.PreviewHash
		apply(app.Command{ID: label + "-save", Kind: "stage_execution", SessionID: before.ID, ExecutionChange: &req})
		staged := find(before.ID)
		check(label+"_save_keeps_old_snapshot", reflect.DeepEqual(staged.ExecutionSnapshot, before.ExecutionSnapshot))
		ref := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
		waitOp(apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: before.ID, ExecutionChange: &ref}).OperationID)
		waitOp(apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: before.ID, ExecutionChange: &ref}).OperationID)
		after := find(before.ID)
		check(label+"_same_id_exact_candidate", after.Scope.NativeID == before.Scope.NativeID && reflect.DeepEqual(after.ExecutionSnapshot, staged.PendingExecutionChange.Snapshot) && after.Paused)
		check(label+"_old_success_records_unchanged", reflect.DeepEqual(after.View.HookExecutions, before.View.HookExecutions))
		check(label+"_model_history_unchanged", reflect.DeepEqual(after.View.Messages, before.View.Messages))
		samples[label] = a.Snapshot()
		waitOp(apply(app.Command{ID: label + "-resume", Kind: "resume", SessionID: before.ID}).OperationID)
	}
	change("revoke", "untrusted", inspection)
	turn("A-revoked", ids["A"], "1", false)
	asset.Version, asset.Source = "asset-v2", packageAt("2")
	items, _ := a.ReadCapabilities(context.Background())
	preview, err := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: items[0].Revision, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	changeRequest := preview.Request
	changeRequest.PreviewHash = preview.PreviewHash
	apply(app.Command{ID: "register-v2", Kind: "manage_capability", CapabilityChange: &changeRequest})
	check("library_update_does_not_relabel_old_success", find(ids["A"]).View.HookExecutions[0].AssetVersion == "asset-v1" && find(ids["A"]).Scope.PluginSelections[0].Version == "1.0.0")
	inspection = inspect()
	samples["inspection-v2"] = inspection
	change("upgrade", "trusted", inspection)
	turn("A-v2", ids["A"], "2", true)
	check("B_keeps_old_untrusted_selection", find(ids["B"]).Scope.PluginSelections[0].Version == "1.0.0" && find(ids["B"]).Scope.PluginSelections[0].Hooks.Trust == "untrusted")
	export := func(label string) {
		t.Helper()
		token := strings.Repeat("b", 64)
		handler := console.Handler(a, "127.0.0.1:9463", token, os.DirFS("."))
		result := map[string]json.RawMessage{}
		paths := []string{"/api/workspace"}
		for _, id := range ids {
			paths = append(paths, "/api/sessions/"+id)
		}
		for _, path := range paths {
			r := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			result[path] = append([]byte(nil), w.Body.Bytes()...)
		}
		responses[label] = result
	}
	export("before-restart")
	before := find(ids["A"])
	requestMu.Lock()
	beforeRequests := len(requests)
	requestMu.Unlock()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	waitNativeApplication(t, a, func(_ app.Snapshot) bool { return find(ids["A"]).Connection == "ready" })
	after := find(ids["A"])
	samples["reopened"] = a.Snapshot()
	export("reopened")
	check("restart_keeps_original_id_and_snapshot", after.Scope.NativeID == before.Scope.NativeID && reflect.DeepEqual(after.ExecutionSnapshot, before.ExecutionSnapshot))
	check("restart_keeps_success_records_exactly", reflect.DeepEqual(after.View.HookExecutions, before.View.HookExecutions))
	check("restart_keeps_model_history", reflect.DeepEqual(after.View.Messages, before.View.Messages))
	requestMu.Lock()
	check("restart_does_not_send_model_input", len(requests) == beforeRequests)
	requestMu.Unlock()
	waitOp(apply(app.Command{ID: "resume-after-restart", Kind: "resume", SessionID: ids["A"]}).OperationID)
	turn("A-reopened-v2", ids["A"], "2", true)
	export("final")
	final := find(ids["A"])
	check("four_distinct_successes_keep_versions", len(final.View.HookExecutions) == 4 && func() bool {
		seen := map[string]bool{}
		versions := map[string]int{}
		for _, h := range final.View.HookExecutions {
			seen[h.ID] = true
			versions[h.AssetVersion]++
		}
		return len(seen) == 4 && versions["asset-v1"] == 2 && versions["asset-v2"] == 2
	}())
	profileAfter, _ := os.ReadFile(configPath)
	check("original_profile_unchanged", string(profileAfter) == string(profileBefore))
	check("only_three_threads_seven_inputs", f.CountMethod("thread/start") == 3 && f.CountMethod("turn/start") == 7 && f.CountMethod("turn/interrupt") == 0)
}
