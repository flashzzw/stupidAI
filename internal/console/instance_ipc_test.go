package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

// Exercise the actual Describe -> Application -> authenticated Handler path.
// The default executable only supports --version. Opt in to the pinned native
// executable for browser evidence without starting a query or a listener.
func TestClaudeLocalIPCDiagnosticHTTP(t *testing.T) {
	root := t.TempDir()
	binary := os.Getenv("NETCAP_NATIVE_CLAUDE_IPC_TEST")
	native := binary != ""
	setVersion := func(version string) {
		t.Helper()
		if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = '--version' ] || exit 9\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if !native {
		binary = filepath.Join(root, "claude")
		setVersion("2.1.280 (Claude Code)")
	}
	bridge := filepath.Join(root, "unused-bridge.js")
	if err := os.WriteFile(bridge, []byte("throw new Error('diagnosis must not start a query');\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := app.Config{Version: 1, StateDir: filepath.Join(root, "state"),
		Instances: []runtime.Instance{{ID: "claude-ipc", Name: "Claude 本地通道检查", Kind: "claude", Executable: binary, Profile: root, Bridge: bridge, Version: "2.1.280"}},
		Projects:  []app.Project{{ID: "fixture-project", Name: "通道诊断测试", Cwd: root}},
		Routes:    []runtime.Route{{ID: "fixture-route", InstanceID: "claude-ipc", Model: "fixture"}},
	}
	store, err := app.OpenStore(config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(config, store, runtime.NewClaude(config.StateDir))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	defer a.Close()
	const host = "127.0.0.1:9452"
	token := strings.Repeat("a", 64)
	h := Handler(a, host, token, fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	request := func(method, path string, authorized bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader("{}"))
		if authorized {
			r.AddCookie(&http.Cookie{Name: "netcap_session", Value: token})
		}
		r.Header.Set("Origin", "http://"+host)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/workspace", "/api/execution-profiles/diagnose"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "diagnose") {
			method = http.MethodPost
		}
		if got := request(method, path, false).Code; got != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", path, got)
		}
	}
	responses := map[string]json.RawMessage{}
	readWorkspace := func() runtime.Description {
		t.Helper()
		w := request(http.MethodGet, "/api/workspace", true)
		var body struct {
			Instances []app.InstanceStatus `json:"instances"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Instances) != 1 {
			t.Fatalf("workspace response: %d %s", w.Code, w.Body.String())
		}
		if body.Instances[0].ConfigError != "" {
			t.Fatalf("unexpected profile error: %s", body.Instances[0].ConfigError)
		}
		responses["/api/workspace"] = append(json.RawMessage(nil), w.Body.Bytes()...)
		return body.Instances[0].Description
	}
	initial := readWorkspace()
	if initial.LocalIPC == nil || initial.Compatibility != "supported" || initial.LocalIPC.At == "" {
		t.Fatalf("missing local environment projection: %+v", initial)
	}
	refresh := func() runtime.Description {
		t.Helper()
		w := request(http.MethodPost, "/api/execution-profiles/diagnose", true)
		var statuses []app.InstanceStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &statuses) != nil || len(statuses) != 1 {
			t.Fatalf("diagnose response: %d %s", w.Code, w.Body.String())
		}
		responses["/api/execution-profiles/diagnose"] = append(json.RawMessage(nil), w.Body.Bytes()...)
		d := readWorkspace()
		if !reflect.DeepEqual(d, statuses[0].Description) {
			t.Fatal("workspace retained stale diagnostic")
		}
		return d
	}
	updated := refresh()
	if updated.LocalIPC == nil || updated.LocalIPC.At == initial.LocalIPC.At || !reflect.DeepEqual(updated.Capabilities, initial.Capabilities) {
		t.Fatal("refresh failed to resample, or changed native capabilities")
	}
	if !native {
		setVersion("future-version")
		if d := refresh(); d.LocalIPC != nil || d.Compatibility != "unverified" {
			t.Fatal("version change retained old transport diagnosis")
		}
	}
	if len(a.Snapshot().Sessions) != 0 || len(a.Snapshot().Operations) != 0 {
		t.Fatal("diagnosis created a session or operation")
	}
	if path := os.Getenv("NETCAP_CLAUDE_IPC_HTTP_OUTPUT"); path != "" {
		data, err := json.MarshalIndent(map[string]any{"scope": "production Application/Handler + actual local IPC, version-only CLI, no query or HTTP listener", "native_cli": native, "responses": responses}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
