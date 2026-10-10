//go:build linux && nativeclaude

package runtime_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativePendingPermissionChanges(t *testing.T) {
	path := os.Getenv("NETCAP_PENDING_REQUEST")
	if path == "" {
		t.Skip("owned native pending-permission fixture only")
	}
	var request struct {
		Root, Cwd, Executable, Mode string
		Profiles                    map[string]struct{ Profile, Endpoint, Node string }
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &request) != nil {
		t.Fatal("invalid owned fixture request")
	}
	owned, e1 := os.Stat(filepath.Join(request.Root, "policy"))
	mounted, e2 := os.Stat("/etc/claude-code")
	if e1 != nil || e2 != nil || !os.SameFile(owned, mounted) {
		t.Fatal("owned policy namespace required")
	}
	c := app.Config{Version: 1, StateDir: filepath.Join(request.Root, "application"), Projects: []app.Project{{ID: "owned", Name: "Owned pending permissions", Cwd: request.Cwd}}}
	bridge, _ := filepath.Abs("../../bridges/claude/dist/main.js")
	for _, label := range []string{"A", "B"} {
		profile := request.Profiles[label]
		c.Instances = append(c.Instances, runtime.Instance{ID: label, Name: "Owned Claude " + label, Kind: "claude", Executable: request.Executable,
			Profile: profile.Profile, NodeExecutable: profile.Node, Bridge: bridge,
			EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}})
		c.Routes = append(c.Routes, runtime.Route{ID: label, InstanceID: label, Endpoint: profile.Endpoint, Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_PENDING_" + label})
	}
	store, err := app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(c, store, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	checks, observations := map[string]bool{}, map[string]any{}
	frames := map[string]map[string]json.RawMessage{}
	trace := []map[string]any{}
	sessions := map[string]string{}
	write := func(name string, value any) {
		t.Helper()
		body, e := json.MarshalIndent(value, "", "  ")
		if e != nil || os.WriteFile(filepath.Join(request.Root, name), body, 0600) != nil {
			t.Fatal("cannot preserve owned evidence", name)
		}
	}
	defer func() {
		write("application-result.json", map[string]any{"checks": checks, "observations": observations, "frames": frames, "http": trace, "sessions": sessions, "snapshot": a.Snapshot()})
	}()
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Fatal(name)
		}
	}
	get := func(id string) app.Session {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				return s
			}
		}
		return app.Session{}
	}
	wait := func(name string, ready func() bool) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); !ready(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("timed out:", name)
			}
		}
	}
	handler := console.Handler(a, "127.0.0.1:9468", strings.Repeat("c", 64), os.DirFS("."))
	call := func(method, path string, payload any) (int, json.RawMessage) {
		t.Helper()
		body, e := json.Marshal(payload)
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:9468"+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		out := append(json.RawMessage(nil), response.Body.Bytes()...)
		if !json.Valid(out) {
			t.Fatal("non-JSON response", response.Code)
		}
		trace = append(trace, map[string]any{"method": method, "path": path, "request": payload, "status": response.Code, "response": out})
		return response.Code, out
	}
	command := func(c app.Command) (int, app.CommandReceipt) {
		t.Helper()
		status, body := call(http.MethodPost, "/api/commands", c)
		var r app.CommandReceipt
		if status < 300 && json.Unmarshal(body, &r) != nil {
			t.Fatal("invalid command receipt")
		}
		return status, r
	}
	catalog := func(name string) runtime.PermissionRuleCatalog {
		t.Helper()
		status, body := call(http.MethodPost, "/api/sessions/"+sessions["A"]+"/permissions/catalog", map[string]any{})
		var out runtime.PermissionRuleCatalog
		if status != 200 || json.Unmarshal(body, &out) != nil {
			t.Fatal("permission report failed", status, string(body))
		}
		observations[name] = out
		return out
	}
	export := func(name string) {
		frame := map[string]json.RawMessage{}
		_, frame["/api/workspace"] = call(http.MethodGet, "/api/workspace", nil)
		for _, id := range sessions {
			_, frame["/api/sessions/"+id] = call(http.MethodGet, "/api/sessions/"+id, nil)
		}
		frames[name] = frame
	}
	operation := func(id string) app.Operation {
		for _, op := range a.Snapshot().Operations {
			if op.ID == id {
				return op
			}
		}
		return app.Operation{}
	}
	finished := func(id string) app.Operation {
		t.Helper()
		wait("original operation terminal", func() bool {
			return slices.Contains([]string{"succeeded", "not_sent", "failed", "uncertain", "interrupted"}, operation(id).Status)
		})
		return operation(id)
	}
	for _, label := range []string{"A", "B"} {
		status, r := command(app.Command{ID: "create-pending-" + label, Kind: "create", ProjectID: "owned", RouteID: label, Title: "Pending permissions " + label, Permission: "read_only"})
		check(label+"_create_accepted", status < 300 && r.SessionID != "")
		sessions[label] = r.SessionID
		wait("query ready", func() bool { s := get(r.SessionID); return s.Status == "idle" && s.View.NativeStatus == "sdk_live" })
	}
	first, sibling := get(sessions["A"]), get(sessions["B"])
	initial := catalog("initial-report")
	status, sent := command(app.Command{ID: "send-pending", Kind: "send", SessionID: first.ID, Text: "NETCAP_PERMISSION_CASE=pending ROUTE=A"})
	check("original_input_accepted", status < 300)
	var original runtime.Pending
	wait("original approval", func() bool {
		for _, p := range get(first.ID).View.Pending {
			if p.Status == "pending" && p.NativeTurn == sent.OperationID {
				original = p
				return true
			}
		}
		return false
	})
	var input map[string]string
	check("original_read_identity", json.Unmarshal(original.Request, &input) == nil && original.Kind == "Read" && input["file_path"] == filepath.Join(request.Cwd, "target-A.txt"))
	check("original_approval_observation_bound", original.PermissionGuard != nil && original.PermissionGuard.State == "observed" && !original.PermissionGuard.Complete && original.PermissionGuard.QueryID == initial.QueryID && original.PermissionGuard.ConfigurationID == initial.ConfigurationID && original.PermissionGuard.BaselineHash != "")
	observations["original-pending"] = original
	export("pending")
	policyPath := filepath.Join(request.Root, "policy/managed-settings.json")
	originalPolicy, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if request.Mode != "unchanged" && request.Mode != "local-drift" {
		target := map[string]any{"allowManagedPermissionRulesOnly": true, "permissions": map[string]any{"deny": []string{"Read(./target-A.txt)"}, "ask": []string{"Read(./target-B.txt)"}}}
		if request.Mode == "invalid" {
			target["permissions"] = map[string]any{"deny": 17}
		}
		if request.Mode == "user-invalid" {
			if os.WriteFile(filepath.Join(request.Profiles["A"].Profile, "settings.json"), []byte(`{"permissions":{"allow":17}}`), 0600) != nil {
				t.Fatal("owned invalid user settings failed")
			}
		} else {
			write("policy/managed-settings.json", target)
		}
		var report runtime.PermissionRuleCatalog
		wait("native reports changed policy", func() bool {
			report = catalog("changed-sample-" + time.Now().UTC().Format("150405.000000000"))
			if request.Mode == "user-invalid" {
				return len(report.Errors) > 0
			}
			if request.Mode == "invalid" {
				// This CLI drops invalid managed rules without a settings error.
				// Keep that observed limitation separate from source parsing coverage.
				return report.SampleHash != initial.SampleHash && !slices.ContainsFunc(report.Items, func(row runtime.PermissionRuleItem) bool {
					return row.Source == "policySettings" && row.Rule == "Read(./target-A.txt)"
				})
			}
			return slices.ContainsFunc(report.Items, func(row runtime.PermissionRuleItem) bool {
				return row.Source == "policySettings" && row.Rule == "Read(./target-A.txt)" && row.Behavior == "deny" && !row.NotInEffect
			})
		})
		check("changed_report_keeps_original_query", report.QueryID == initial.QueryID && report.ConfigurationID == initial.ConfigurationID && report.SampleHash != initial.SampleHash)
		observations["changed-report"] = report
		if request.Mode == "restore" {
			if os.WriteFile(policyPath, originalPolicy, 0600) != nil {
				t.Fatal("cannot restore owned policy bytes")
			}
			wait("native report restored", func() bool {
				return catalog("restored-sample-"+time.Now().UTC().Format("150405.000000000")).SampleHash == initial.SampleHash
			})
		}
	}
	if request.Mode == "local-drift" {
		if os.WriteFile(filepath.Join(request.Cwd, ".claude/settings.local.json"), []byte(`{"env":{"NETCAP_PENDING_VERSION":"changed"}}`), 0600) != nil {
			t.Fatal("owned local change failed")
		}
	}
	export("before-allow")
	status, reply := command(app.Command{ID: "allow-original-pending", Kind: "respond", SessionID: first.ID, PendingID: original.ID, OptionID: "allow"})
	observations["allow-http-status"], observations["allow-receipt"] = status, reply
	var outcome app.Operation
	if status < 300 {
		outcome = finished(reply.OperationID)
	}
	observations["allow-operation"] = outcome
	export("after-allow-transient")
	if request.Mode != "unchanged" && request.Mode != "local-drift" {
		want := "changed"
		if request.Mode == "user-invalid" {
			want = "unavailable"
		}
		wait("blocked approval visible in original request", func() bool {
			current := get(first.ID)
			return current.Connection == "ready" && slices.ContainsFunc(current.View.Pending, func(p runtime.Pending) bool {
				return p.ID == original.ID && p.PermissionGuard != nil && p.PermissionGuard.State == want && !p.PermissionGuard.Complete
			})
		})
		check("blocked_observation_visible", true)
	}
	export("after-allow")
	if request.Mode == "unchanged" {
		check("unchanged_approval_succeeds", status < 300 && outcome.Status == "succeeded")
	} else {
		check("stale_allow_never_sent", status >= 400 || outcome.Status == "not_sent")
		check("same_request_remains_pending", slices.ContainsFunc(get(first.ID).View.Pending, func(p runtime.Pending) bool {
			return p.ID == original.ID && p.ConnectionID == original.ConnectionID && p.NativeTurn == sent.OperationID && p.Status == "pending"
		}))
		kind := "respond"
		if request.Mode == "interrupt" {
			kind = "interrupt"
		}
		status, rejected := command(app.Command{ID: "finish-original-pending", Kind: kind, SessionID: first.ID, PendingID: original.ID, OptionID: "deny"})
		check("original_rejection_or_interrupt_accepted", status < 300)
		observations["finish-receipt"] = rejected
		end := finished(rejected.OperationID)
		observations["finish-operation"] = end
		check("original_rejection_or_interrupt_completed", end.Status == "succeeded" || request.Mode == "interrupt" && end.Status == "interrupted")
	}
	end := finished(sent.OperationID)
	check("original_input_reaches_terminal", end.Status == "succeeded" || request.Mode == "interrupt" && end.Status == "interrupted")
	current := get(first.ID)
	check("original_id_and_snapshot_kept", current.Scope.NativeID == first.Scope.NativeID && reflect.DeepEqual(current.ExecutionSnapshot, first.ExecutionSnapshot))
	check("only_one_original_input", len(slices.DeleteFunc(append([]runtime.Message(nil), current.View.Messages...), func(m runtime.Message) bool { return m.Role != "user" })) == 1)
	check("sibling_id_and_snapshot_kept", get(sibling.ID).Scope.NativeID == sibling.Scope.NativeID && reflect.DeepEqual(get(sibling.ID).ExecutionSnapshot, sibling.ExecutionSnapshot) && len(get(sibling.ID).View.Messages) == 0)
	export("finished")
	write("final-snapshot.json", a.Snapshot())
}
