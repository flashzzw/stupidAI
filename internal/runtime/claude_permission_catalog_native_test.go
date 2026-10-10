//go:build linux && nativeclaude

package runtime_test

import (
	"context"
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

func TestClaudeNativePermissionCatalog(t *testing.T) {
	path := os.Getenv("NETCAP_PERMISSION_REQUEST")
	if path == "" {
		t.Skip("owned native permission fixture only")
	}
	var request struct {
		Root, Cwd, Executable string
		Profiles              map[string]struct {
			Profile, SettingsFile, Endpoint, Node string
			UserSettings                          json.RawMessage
		}
		InitialPolicy json.RawMessage
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
	c := app.Config{Version: 1, StateDir: filepath.Join(request.Root, "application"),
		Projects: []app.Project{{ID: "owned", Name: "Owned permission rules", Cwd: request.Cwd}}}
	bridge, _ := filepath.Abs("../../bridges/claude/dist/main.js")
	for _, label := range []string{"A", "B"} {
		profile := request.Profiles[label]
		c.Instances = append(c.Instances, runtime.Instance{ID: label, Name: "Owned Claude " + label, Kind: "claude", Executable: request.Executable,
			Profile: profile.Profile, SettingsFile: profile.SettingsFile, NodeExecutable: profile.Node, Bridge: bridge,
			EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}})
		c.Routes = append(c.Routes, runtime.Route{ID: label, InstanceID: label, Endpoint: profile.Endpoint, Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_PERMISSION_" + label})
	}
	var engine *runtime.Claude
	open := func() *app.Application {
		store, err := app.OpenStore(c.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		engine = runtime.NewClaude(c.StateDir)
		a, err := app.Open(c, store, engine)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	defer func() { a.Close() }()
	checks := map[string]bool{}
	sessions := map[string]string{}
	frames := map[string]map[string]json.RawMessage{}
	reports := map[string]runtime.PermissionRuleCatalog{}
	cases := map[string]any{}
	trace := []map[string]any{}
	write := func(name string, value any) {
		t.Helper()
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(request.Root, name), encoded, 0600) != nil {
			t.Fatal("cannot preserve native fixture evidence", name, err)
		}
	}
	defer func() {
		write("application-result.json", map[string]any{"checks": checks, "sessions": sessions, "frames": frames, "reports": reports, "cases": cases, "http": trace})
	}()
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Fatal(name)
		}
	}
	apply := func(command app.Command) app.CommandReceipt {
		t.Helper()
		receipt, err := a.Apply(command)
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	get := func(id string) app.Session {
		for _, session := range a.Snapshot().Sessions {
			if session.ID == id {
				return session
			}
		}
		return app.Session{}
	}
	wait := func(id, native string) app.Session {
		t.Helper()
		for deadline := time.Now().Add(25 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			session := get(id)
			if session.Status == "idle" && session.View.NativeStatus == native {
				return session
			}
			if time.Now().After(deadline) {
				t.Fatalf("original query not ready: %s %s; operations: %+v", session.Status, session.View.NativeStatus, a.Snapshot().Operations)
			}
		}
	}
	wireSizes := func() map[string]int64 {
		out := map[string]int64{}
		for _, label := range []string{"A", "B"} {
			if info, err := os.Stat(filepath.Join(request.Root, "wire-"+label+".jsonl")); err == nil {
				out[label] = info.Size()
			}
		}
		return out
	}
	requestHTTP := func(method, path string, expected int) json.RawMessage {
		t.Helper()
		payload := ""
		if method == http.MethodPost {
			payload = "{}"
		}
		before := wireSizes()
		req := httptest.NewRequest(method, "http://127.0.0.1:9468"+path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		console.Handler(a, "127.0.0.1:9468", strings.Repeat("c", 64), os.DirFS(".")).ServeHTTP(response, req)
		body := append(json.RawMessage(nil), response.Body.Bytes()...)
		trace = append(trace, map[string]any{"method": method, "path": path, "status": response.Code, "response": body, "wire_before": before, "wire_after": wireSizes()})
		if response.Code != expected || !json.Valid(body) {
			t.Fatalf("HTTP %s %s: %d %s", method, path, response.Code, string(body))
		}
		return body
	}
	catalog := func(label, name string) runtime.PermissionRuleCatalog {
		t.Helper()
		body := requestHTTP(http.MethodPost, "/api/sessions/"+sessions[label]+"/permissions/catalog", 200)
		var out runtime.PermissionRuleCatalog
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		reports[name] = out
		return out
	}
	export := func(name string) {
		frame := map[string]json.RawMessage{"/api/workspace": requestHTTP(http.MethodGet, "/api/workspace", 200)}
		for _, label := range []string{"A", "B"} {
			id := sessions[label]
			frame["/api/sessions/"+id] = requestHTTP(http.MethodGet, "/api/sessions/"+id, 200)
			frame["/api/sessions/"+id+"/permissions/catalog"] = requestHTTP(http.MethodPost, "/api/sessions/"+id+"/permissions/catalog", 200)
		}
		frames[name] = frame
	}
	for _, label := range []string{"A", "B"} {
		receipt := apply(app.Command{ID: "create-permissions-" + label, Kind: "create", Title: "Permission rules " + label, ProjectID: "owned", RouteID: label, Permission: "read_only"})
		sessions[label] = receipt.SessionID
		wait(receipt.SessionID, "sdk_live")
		saved := get(receipt.SessionID)
		before := a.Snapshot()
		first := catalog(label, "initial-"+label)
		second := catalog(label, "repeated-"+label)
		check(label+"_same_query_and_sample", first.QueryID != "" && first.QueryID == second.QueryID && first.ConfigurationID == second.ConfigurationID && first.SampleHash == second.SampleHash)
		check(label+"_original_identity_and_partial_scope", first.NativeID == get(receipt.SessionID).Scope.NativeID && first.Cwd == request.Cwd && !first.Complete && !first.ManagedOnly && first.ConflictCount == 0)
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
			check(label+"_source_"+source, slices.ContainsFunc(first.Items, func(row runtime.PermissionRuleItem) bool { return row.Source == source }))
		}
		check(label+"_extra_directory_native_label", slices.ContainsFunc(first.Directories, func(row runtime.PermissionDirectory) bool {
			return row.Path == filepath.Join(request.Root, "extra-"+label) && row.Source == "localSettings"
		}))
		after := a.Snapshot()
		check(label+"_read_did_not_dispatch_or_change_snapshot", len(before.Operations) == len(after.Operations) && reflect.DeepEqual(get(receipt.SessionID).ExecutionSnapshot, saved.ExecutionSnapshot))
	}
	first, second := get(sessions["A"]), get(sessions["B"])
	check("queries_and_profiles_are_independent", reports["initial-A"].QueryID != reports["initial-B"].QueryID && first.Scope.StateNamespace != second.Scope.StateNamespace && first.Scope.NativeID != second.Scope.NativeID)
	export("initial")
	for _, mode := range []string{"permission", "settings-guard", "settings-sources", "installed-binding", "model", "settings-file"} {
		scope := first.Scope
		scope.RecoveryPermission = first.Permission
		switch mode {
		case "permission":
			scope.RecoveryPermission = "workspace"
		case "settings-guard":
			scope.SettingsGuardVersion++
		case "settings-sources":
			scope.ClaudeSettingsSources = []runtime.ClaudeSettingsSource{{}}
		case "installed-binding":
			scope.ClaudeInstalledSettings = &runtime.ClaudeInstalledSettings{}
		case "model":
			scope.Route.Model = "different"
		case "settings-file":
			scope.Instance.SettingsFile = "/different"
		}
		_, err := engine.ReadPermissionCatalog(context.Background(), scope)
		check("reject_wrong_"+mode, err != nil && strings.Contains(err.Error(), "当前保存的原生配置"))
	}
	turn := func(label, decision string, wantApprovals int) {
		t.Helper()
		receipt := apply(app.Command{ID: "permission-" + label, Kind: "send", SessionID: first.ID, Text: "NETCAP_PERMISSION_CASE=" + label + " ROUTE=A"})
		approved := map[string]runtime.Pending{}
		for deadline := time.Now().Add(25 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			current := get(first.ID)
			for _, pending := range current.View.Pending {
				if pending.Status != "pending" || approved[pending.ID].ID != "" {
					continue
				}
				var input struct {
					FilePath string `json:"file_path"`
				}
				if json.Unmarshal(pending.Request, &input) != nil || pending.Kind != "Read" || input.FilePath != filepath.Join(request.Cwd, "target-A.txt") || pending.NativeTurn != receipt.OperationID {
					t.Fatal("unexpected permission request in owned fixture")
				}
				check(label+"_expected_approval", wantApprovals > 0)
				catalog("A", label+"-pending")
				check(label+"_read_keeps_pending_approval", slices.ContainsFunc(get(first.ID).View.Pending, func(row runtime.Pending) bool { return row.ID == pending.ID && row.Status == "pending" }))
				approved[pending.ID] = pending
				apply(app.Command{ID: "answer-permission-" + label, Kind: "respond", SessionID: first.ID, PendingID: pending.ID, OptionID: decision})
			}
			complete := false
			for _, operation := range a.Snapshot().Operations {
				if operation.ID == receipt.OperationID {
					complete = operation.Status == "succeeded"
					if operation.Status == "failed" || operation.Status == "not_sent" || operation.Status == "uncertain" {
						t.Fatalf("owned input failed: %s %s", operation.Status, operation.Error)
					}
				}
			}
			if complete {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("owned permission input did not complete", label)
			}
		}
		current := wait(first.ID, "sdk_live")
		check(label+"_approval_count", len(approved) == wantApprovals)
		check(label+"_same_native_id_and_snapshot", current.Scope.NativeID == first.Scope.NativeID && reflect.DeepEqual(current.ExecutionSnapshot, first.ExecutionSnapshot))
		cases[label] = map[string]any{"receipt": receipt, "approvals": approved, "session": current}
	}
	turn("initial", "", 0)
	policyPath := "policy/managed-settings.json"
	write(policyPath, map[string]any{"allowManagedPermissionRulesOnly": true, "permissions": map[string]any{"allow": []string{"Read(./target-A.txt)", "Read(./target-B.txt)"}, "deny": []string{"Read(./managed-private/**)"}}})
	waitReport := func(name string, matches func(runtime.PermissionRuleCatalog) bool) runtime.PermissionRuleCatalog {
		t.Helper()
		for deadline, i := time.Now().Add(8*time.Second), 0; ; i++ {
			out := catalog("A", name+"-sample-"+time.Now().UTC().Format("150405.000000000"))
			if matches(out) {
				reports[name] = out
				return out
			}
			if time.Now().After(deadline) {
				t.Fatal("live report did not reach observed policy state", name, i)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	managed := waitReport("managed", func(out runtime.PermissionRuleCatalog) bool {
		return out.ManagedOnly && out.ConflictCount == 0 && slices.ContainsFunc(out.Items, func(row runtime.PermissionRuleItem) bool {
			return row.Source == "userSettings" && row.Rule == "Read(./target-A.txt)" && row.NotInEffect
		})
	})
	check("managed_change_stays_on_same_query", managed.QueryID == reports["initial-A"].QueryID && managed.ConfigurationID == reports["initial-A"].ConfigurationID && managed.SampleHash != reports["initial-A"].SampleHash)
	export("managed")
	turn("managed", "", 0)
	write(policyPath, map[string]any{"allowManagedPermissionRulesOnly": true, "permissions": map[string]any{"ask": []string{"Read(./target-A.txt)", "Read(./target-B.txt)"}}})
	waitReport("ask", func(out runtime.PermissionRuleCatalog) bool {
		return out.ManagedOnly && out.ConflictCount == 0 && slices.ContainsFunc(out.Items, func(row runtime.PermissionRuleItem) bool {
			return row.Source == "policySettings" && row.Rule == "Read(./target-A.txt)" && row.Behavior == "ask" && !row.NotInEffect
		}) && !slices.ContainsFunc(out.Items, func(row runtime.PermissionRuleItem) bool {
			return row.Source == "policySettings" && row.Behavior == "allow"
		})
	})
	if err := os.WriteFile(filepath.Join(request.Cwd, "target-A.txt"), []byte("OWNED_PERMISSION_TARGET_A_APPROVED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	turn("ask-allow", "allow", 1)
	turn("ask-deny", "deny", 1)
	export("ask")
	userFile := filepath.Join(request.Profiles["A"].Profile, "settings.json")
	originalUser, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte(`{"permissions":{"allow":"PRIVATE_INVALID_VALUE"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := waitReport("invalid-user", func(out runtime.PermissionRuleCatalog) bool { return len(out.Errors) > 0 })
	check("invalid_source_report_is_partial_and_redacted", !invalid.Complete && invalid.QueryID == managed.QueryID && !strings.Contains(string(requestHTTP(http.MethodPost, "/api/sessions/"+first.ID+"/permissions/catalog", 200)), "PRIVATE_INVALID_VALUE"))
	export("invalid")
	if err := os.WriteFile(userFile, originalUser, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.Root, policyPath), request.InitialPolicy, 0600); err != nil {
		t.Fatal(err)
	}
	waitReport("restored", func(out runtime.PermissionRuleCatalog) bool {
		return !out.ManagedOnly && out.ConflictCount == 0 && len(out.Errors) == 0
	})
	beforeRestart := get(first.ID)
	write("before-restart.json", a.Snapshot())
	a.Close()
	a = open()
	wait(first.ID, "sdk_offline")
	requestHTTP(http.MethodPost, "/api/sessions/"+first.ID+"/permissions/catalog", 409)
	check("closed_read_preserves_original_snapshot", reflect.DeepEqual(get(first.ID).ExecutionSnapshot, beforeRestart.ExecutionSnapshot))
	turn("resumed", "", 0)
	resumed := wait(first.ID, "sdk_live")
	newReport := catalog("A", "resumed")
	check("explicit_resume_preserves_native_id_and_changes_query", resumed.Scope.NativeID == first.Scope.NativeID && newReport.QueryID != managed.QueryID && newReport.ConfigurationID == managed.ConfigurationID)
	check("resume_keeps_prior_messages", len(resumed.View.Messages) >= len(beforeRestart.View.Messages))
	check("other_profile_never_received_input", len(get(second.ID).View.Messages) == 0 && reflect.DeepEqual(get(second.ID).ExecutionSnapshot, second.ExecutionSnapshot))
	frame := map[string]json.RawMessage{"/api/workspace": requestHTTP(http.MethodGet, "/api/workspace", 200),
		"/api/sessions/" + first.ID:                          requestHTTP(http.MethodGet, "/api/sessions/"+first.ID, 200),
		"/api/sessions/" + first.ID + "/permissions/catalog": requestHTTP(http.MethodPost, "/api/sessions/"+first.ID+"/permissions/catalog", 200)}
	frames["resumed"] = frame
	write("final-snapshot.json", a.Snapshot())
}
