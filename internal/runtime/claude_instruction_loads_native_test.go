//go:build linux && nativeclaude

package runtime_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativeInstructionLoads(t *testing.T) {
	path := os.Getenv("NETCAP_INSTRUCTION_REQUEST")
	if path == "" {
		t.Skip("owned native instruction fixture only")
	}
	var request struct {
		Root, Cwd, Profile, Executable, Endpoint, ReadFile string
		Files                                              []string
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &request) != nil || request.ReadFile == "" {
		t.Fatal("invalid owned instruction fixture request")
	}
	owned, e1 := os.Stat(filepath.Join(request.Root, "policy"))
	mounted, e2 := os.Stat("/etc/claude-code")
	if e1 != nil || e2 != nil || !os.SameFile(owned, mounted) {
		t.Fatal("owned policy namespace required")
	}
	c := app.Config{Version: 1, StateDir: filepath.Join(request.Root, "application"),
		Projects: []app.Project{{ID: "owned", Name: "Owned load notifications", Cwd: request.Cwd}},
		Instances: []runtime.Instance{{ID: "claude", Name: "Owned Claude", Kind: "claude", Executable: request.Executable, Profile: request.Profile,
			Bridge: filepath.Join("../../bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}},
		Routes: []runtime.Route{{ID: "owned", InstanceID: "claude", Endpoint: request.Endpoint, Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_INSTRUCTION_KEY"}}}
	c.Instances[0].Bridge, _ = filepath.Abs(c.Instances[0].Bridge)
	open := func() *app.Application {
		store, err := app.OpenStore(c.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		a, err := app.Open(c, store, runtime.NewClaude(c.StateDir))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	defer func() { a.Close() }()
	checks := map[string]bool{}
	frames := map[string]map[string]json.RawMessage{}
	sessions := map[string]string{}
	write := func(name string, value any) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(request.Root, name), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		write("loads-result.json", map[string]any{"checks": checks, "frames": frames, "sessions": sessions})
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
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			s := get(id)
			if s.Status == "idle" && s.View.NativeStatus == native {
				return s
			}
			if time.Now().After(deadline) {
				t.Fatalf("session did not reach %s: %s %s", native, s.Status, s.View.NativeStatus)
			}
		}
	}
	requestHTTP := func(method, path string) json.RawMessage {
		t.Helper()
		payload := ""
		if method == http.MethodPost {
			payload = "{}"
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:9468"+path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		console.Handler(a, "127.0.0.1:9468", strings.Repeat("c", 64), os.DirFS(".")).ServeHTTP(response, req)
		if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("HTTP %s %s: %d", method, path, response.Code)
		}
		return append(json.RawMessage(nil), response.Body.Bytes()...)
	}
	export := func(name string) {
		frame := map[string]json.RawMessage{"/api/workspace": requestHTTP(http.MethodGet, "/api/workspace")}
		for _, id := range sessions {
			frame["/api/sessions/"+id] = requestHTTP(http.MethodGet, "/api/sessions/"+id)
		}
		frames[name] = frame
	}
	for _, name := range []string{"A", "B"} {
		receipt := apply(app.Command{ID: "create-loads-" + name, Kind: "create", Title: "Instruction loads " + name, ProjectID: "owned", RouteID: "owned", Permission: "read_only"})
		sessions[strings.ToLower(name)] = receipt.SessionID
		s := wait(receipt.SessionID, "sdk_live")
		loads := s.View.InstructionLoads
		check(name+"_startup_notifications", loads != nil && loads.Available && !loads.Complete && !loads.RecordingFailed && len(loads.Items) == len(request.Files))
		for _, path := range request.Files {
			found := false
			for _, load := range loads.Items {
				found = found || load.Path == path && load.NativeID == s.Scope.NativeID && load.Cwd == request.Cwd && load.Current && load.QueryID == loads.CurrentQueryID
			}
			check(name+"_owned_path_"+path, found)
		}
	}
	first, second := get(sessions["a"]), get(sessions["b"])
	check("two_queries_are_distinct", first.View.InstructionLoads.CurrentQueryID != second.View.InstructionLoads.CurrentQueryID)
	export("initial")
	turn := func(label string) app.CommandReceipt {
		t.Helper()
		receipt := apply(app.Command{ID: "instruction-" + label, Kind: "send", SessionID: first.ID, Text: "W0209_INSTRUCTION_CASE=" + label})
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			for _, op := range a.Snapshot().Operations {
				if op.ID != receipt.OperationID {
					continue
				}
				if op.Status == "succeeded" {
					return receipt
				}
				if op.Status == "failed" || op.Status == "not_sent" || op.Status == "uncertain" {
					t.Fatalf("input failed: %s %s", op.Status, op.Error)
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("owned input did not complete")
			}
		}
	}
	turn("initial")
	hot := turn("hot")
	nested := get(first.ID)
	nestedPath := filepath.Join(request.Cwd, "feature/CLAUDE.md")
	var observed runtime.InstructionLoad
	for _, load := range nested.View.InstructionLoads.Items {
		if load.Path == nestedPath {
			observed = load
		}
	}
	check("nested_notification_with_native_trigger", observed.ID != "" && observed.Reason == "nested_traversal" && observed.TriggerPath == request.ReadFile)
	check("nested_original_input_and_snapshot", observed.PromptID != "" && observed.Operation == hot.OperationID && observed.ExecutionSnapshotID == first.ExecutionSnapshot.ID && observed.InputCorrelation == "native_tool_message")
	check("native_prompt_and_input_ids_are_distinct", observed.PromptID != hot.OperationID)
	check("nested_same_query_and_native_id", observed.Current && observed.QueryID == first.View.InstructionLoads.CurrentQueryID && observed.NativeID == first.Scope.NativeID)
	encoded, _ := json.Marshal(nested.View.InstructionLoads)
	check("notification_metadata_has_no_instruction_body", !strings.Contains(string(encoded), "PRIVATE_INSTRUCTION"))
	check("notification_has_no_authority_side_effect", reflect.DeepEqual(first.Scope, nested.Scope) && reflect.DeepEqual(first.ExecutionSnapshot, nested.ExecutionSnapshot) && !nested.Paused && !nested.ExecutionHold)
	check("other_session_keeps_own_notifications", len(get(second.ID).View.InstructionLoads.Items) == len(request.Files))
	var catalog app.SessionSkillCatalog
	_ = json.Unmarshal(requestHTTP(http.MethodPost, "/api/sessions/"+first.ID+"/skills/catalog"), &catalog)
	missing := true
	for _, item := range catalog.Instructions.Items {
		missing = missing && item.Path != nestedPath
	}
	check("notification_supplements_missing_summary_source", missing && catalog.Instructions.Available)
	export("nested")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = open()
	offline := wait(first.ID, "sdk_offline")
	check("restart_preserves_notification_history", len(offline.View.InstructionLoads.Items) == len(nested.View.InstructionLoads.Items))
	check("closed_query_not_promoted_to_current", offline.View.InstructionLoads.CurrentQueryID == "" && !offline.View.InstructionLoads.Items[0].Current)
	export("offline")
	turn("cold")
	restored := wait(first.ID, "sdk_live")
	check("resume_keeps_native_and_snapshot", restored.Scope.NativeID == first.Scope.NativeID && restored.ExecutionSnapshot.ID == first.ExecutionSnapshot.ID)
	check("resume_has_new_query_observation_identity", restored.View.InstructionLoads.CurrentQueryID != first.View.InstructionLoads.CurrentQueryID)
	preserved := false
	for _, load := range restored.View.InstructionLoads.Items {
		if load.ID == observed.ID {
			preserved = !load.Current && load.QueryID == observed.QueryID && load.Operation == hot.OperationID && load.ExecutionSnapshotID == first.ExecutionSnapshot.ID
		}
	}
	check("historical_nested_notice_keeps_original_ownership", preserved)
	check("only_two_creates_and_three_explicit_inputs", len(a.Snapshot().Operations) == 5)
	export("restored")
	write("loads-browser-fixture.json", map[string]any{"frames": frames, "sessions": sessions, "nested": observed, "original_snapshot": first.ExecutionSnapshot.ID})
}
