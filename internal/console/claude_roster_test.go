package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

type rosterBindingRuntime struct {
	*runtime.Claude
	record runtime.NativeSession
}

func (r *rosterBindingRuntime) Discover(context.Context, runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
	return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{r.record}}, nil
}
func (r *rosterBindingRuntime) InspectNative(context.Context, runtime.DiscoveryRequest, string) (runtime.NativeSession, error) {
	return r.record, nil
}

func (r *rosterBindingRuntime) ReadNativeHistory(context.Context, runtime.DiscoveryRequest, string) (runtime.NativeHistorySnapshot, error) {
	return runtime.NativeHistorySnapshot{Session: r.record, Entries: []runtime.NativeHistoryEntry{}}, nil
}

func TestClaudeRosterHTTPPreservesReadOnlyBinding(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "claude")
	script := "#!/bin/sh\nif [ \"$1\" = '--version' ]; then printf '%s\\n' '2.1.280 (Claude Code)'; elif [ \"$1\" = 'agents' ] && [ \"$2\" = '--json' ] && [ \"$3\" = '--all' ] && [ \"$4\" = '--cwd' ]; then cat \"$CLAUDE_CONFIG_DIR/roster-fixture.json\"; else exit 9; fi\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(root, "unused.js")
	if err := os.WriteFile(bridge, []byte("throw new Error('do not run');"), 0600); err != nil {
		t.Fatal(err)
	}
	const id = "12345678-1234-4234-8234-123456789abc"
	row := map[string]any{"id": "12345678", "sessionId": id, "cwd": root, "kind": "background", "pid": 4242, "state": "working", "status": "idle", "startedAt": 1790880000000}
	writeRoster := func(value any) {
		t.Helper()
		data, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(profile, "roster-fixture.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRoster([]any{row})
	config := app.Config{Version: 1, StateDir: filepath.Join(root, "state"), Projects: []app.Project{{ID: "project", Name: "原生清单受控验证", Cwd: root}}, Instances: []runtime.Instance{{ID: "claude", Kind: "claude", Executable: binary, Profile: profile, Bridge: bridge}}}
	store, err := app.OpenStore(config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	rt := &rosterBindingRuntime{Claude: runtime.NewClaude(config.StateDir), record: runtime.NativeSession{NativeID: id, Cwd: root, Title: "Claude 原生后台受控来源", Source: "sdk_history", NativeStatus: "unknown"}}
	a, err := app.Open(config, store, rt)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	defer a.Close()
	preview, err := a.PreviewNative(context.Background(), app.NativePreviewRequest{ProjectID: "project", InstanceID: "claude", NativeID: id})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := a.BindNative(context.Background(), app.NativeBindingRequest{CommandID: "bind", ProjectID: "project", InstanceID: "claude", NativeID: id, PreviewToken: preview.BindingToken})
	if err != nil {
		t.Fatal(err)
	}
	const host = "127.0.0.1:9454"
	token := strings.Repeat("a", 64)
	h := Handler(a, host, token, fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://"+host)
		req.AddCookie(&http.Cookie{Name: "netcap_session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if a.Workspace().Routes == nil {
		t.Fatal("read-only workspace must return an empty route array")
	}
	responses := map[string]json.RawMessage{}
	before, _ := json.Marshal(a.Snapshot())
	path := "/api/sessions/" + bound.SessionID + "/native/observe"
	w := call("POST", path, `{}`)
	var sample app.NativeObservation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sample) != nil || sample.Status != "sampled" || sample.View.AgentRoster == nil || sample.View.ExecutionClear || sample.View.Evidence.Complete {
		t.Fatalf("bad sample: %d %s", w.Code, w.Body.String())
	}
	responses[path] = append(json.RawMessage(nil), w.Body.Bytes()...)
	historyPath := "/api/sessions/" + bound.SessionID + "/native/history"
	history := call("POST", historyPath, `{}`)
	if history.Code != 200 {
		t.Fatalf("history fixture: %d %s", history.Code, history.Body.String())
	}
	responses[historyPath] = append(json.RawMessage(nil), history.Body.Bytes()...)
	writeRoster([]any{})
	w = call("POST", path, `{}`)
	if json.Unmarshal(w.Body.Bytes(), &sample) != nil || sample.Status != "unavailable" || sample.Error == "" {
		t.Fatalf("missing identity admitted: %s", w.Body.String())
	}
	responses[path+"?fixture=missing"] = append(json.RawMessage(nil), w.Body.Bytes()...)
	after, _ := json.Marshal(a.Snapshot())
	if string(before) != string(after) {
		t.Fatal("query changed persisted session or operation")
	}
	for _, kind := range []string{"send", "resume", "interrupt"} {
		data, _ := json.Marshal(map[string]any{"command_id": "forbidden-" + kind, "kind": kind, "session_id": bound.SessionID})
		if w := call("POST", "/api/commands", string(data)); w.Code < 400 {
			t.Fatalf("native binding acquired %s", kind)
		}
	}
	for _, p := range []string{"/api/workspace", "/api/sessions/" + bound.SessionID} {
		w = call("GET", p, "")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[p] = append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	if file := os.Getenv("NETCAP_CLAUDE_ROSTER_HTTP_OUTPUT"); file != "" {
		data, _ := json.MarshalIndent(map[string]any{"session_id": bound.SessionID, "responses": responses, "scope": "real Application/Handler/Claude roster query, controlled CLI and metadata binding, no listener or native worker"}, "", "  ")
		if err := os.WriteFile(file, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
