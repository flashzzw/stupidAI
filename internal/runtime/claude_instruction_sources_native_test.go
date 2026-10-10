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
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativeInstructionSources(t *testing.T) {
	path := os.Getenv("NETCAP_INSTRUCTION_REQUEST")
	if path == "" {
		t.Skip("owned native instruction fixture only")
	}
	var request struct {
		Root, Cwd, Profile, Executable, Endpoint string
		Files                                    []string
	}
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &request) != nil {
		t.Fatal("invalid native fixture request", err)
	}
	owned, e1 := os.Stat(filepath.Join(request.Root, "policy"))
	mounted, e2 := os.Stat("/etc/claude-code")
	if e1 != nil || e2 != nil || !os.SameFile(owned, mounted) {
		t.Fatal("owned policy namespace required")
	}
	c := app.Config{Version: 1, StateDir: filepath.Join(request.Root, "application"),
		Projects: []app.Project{{ID: "owned", Name: "Owned instructions", Cwd: request.Cwd}},
		Instances: []runtime.Instance{{ID: "claude", Name: "Owned Claude", Kind: "claude", Executable: request.Executable, Profile: request.Profile,
			Bridge: filepath.Join("../../bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}},
		Routes: []runtime.Route{{ID: "owned", InstanceID: "claude", Endpoint: request.Endpoint, Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_INSTRUCTION_KEY"}}}
	c.Instances[0].Bridge, _ = filepath.Abs(c.Instances[0].Bridge)
	store, err := app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	engine := runtime.NewClaude(c.StateDir)
	a, err := app.Open(c, store, engine)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	receipt, err := a.Apply(app.Command{ID: "create-instructions", Kind: "create", Title: "Instruction sources A", ProjectID: "owned", RouteID: "owned", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	var before app.Session
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == receipt.SessionID {
				before = s
			}
		}
		if before.Status == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native session did not become idle: %s %+v", before.Status, a.Snapshot().Operations)
		}
	}
	handler := console.Handler(a, "127.0.0.1:9468", strings.Repeat("c", 64), os.DirFS("."))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9468/api/sessions/"+before.ID+"/skills/catalog", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("catalog HTTP %d: %s", response.Code, response.Body.String())
	}
	var catalog app.SessionSkillCatalog
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Instructions == nil || !catalog.Instructions.Available || catalog.Complete || catalog.NativeID != before.Scope.NativeID {
		t.Fatal("missing bound native instruction sample")
	}
	for _, path := range request.Files {
		found := false
		for _, item := range catalog.Instructions.Items {
			found = found || item.Path == path
		}
		if !found {
			t.Fatalf("owned instruction source not reported: %s", path)
		}
	}
	for _, item := range catalog.Instructions.Items {
		if !strings.HasPrefix(item.Path, request.Root+string(filepath.Separator)) {
			t.Fatal("native query inherited an instruction source outside the owned fixture")
		}
	}
	if strings.Contains(response.Body.String(), "PRIVATE_INSTRUCTION_BODY_") {
		t.Fatal("instruction body leaked")
	}
	after := a.Snapshot()
	if len(after.Sessions) != 1 || len(after.Operations) != 1 || !reflect.DeepEqual(before.Scope, after.Sessions[0].Scope) || !reflect.DeepEqual(before.ExecutionSnapshot, after.Sessions[0].ExecutionSnapshot) || len(after.Sessions[0].View.Messages) != 0 {
		t.Fatal("catalog changed execution or sent input")
	}
	if err := os.WriteFile(filepath.Join(request.Root, "application-catalog.json"), response.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	writeJSON := func(name string, value any) {
		t.Helper()
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(request.Root, name), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("application-snapshot.json", after)
	for _, mode := range []string{"settings-guard", "settings-sources", "installed-binding"} {
		t.Run(mode, func(t *testing.T) {
			scope := before.Scope
			switch mode {
			case "settings-guard":
				scope.SettingsGuardVersion++
			case "settings-sources":
				scope.ClaudeSettingsSources = []runtime.ClaudeSettingsSource{{}}
			case "installed-binding":
				scope.ClaudeInstalledSettings = &runtime.ClaudeInstalledSettings{}
			}
			_, err := engine.ReadSkillCatalog(context.Background(), scope)
			if err == nil || !strings.Contains(err.Error(), "设置与安装绑定") {
				t.Fatalf("mismatched catalog binding was not rejected: %v", err)
			}
		})
	}
	// Export actual Handler DTOs for browser replay, including a second live
	// query so switching sessions can be checked without invented identities.
	second, err := a.Apply(app.Command{ID: "create-instructions-b", Kind: "create", Title: "Instruction sources B", ProjectID: "owned", RouteID: "owned", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		ready := false
		for _, s := range a.Snapshot().Sessions {
			ready = ready || s.ID == second.SessionID && s.Status == "idle"
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second native query did not become idle")
		}
	}
	responses := map[string]json.RawMessage{}
	paths := []string{"/api/workspace"}
	for _, id := range []string{before.ID, second.SessionID} {
		paths = append(paths, "/api/sessions/"+id, "/api/sessions/"+id+"/skills/catalog")
	}
	for _, path := range paths {
		method, payload := http.MethodGet, ""
		if strings.HasSuffix(path, "/skills/catalog") {
			method, payload = http.MethodPost, "{}"
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:9468"+path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("fixture HTTP %s %s: %d", method, path, response.Code)
		}
		responses[path] = append(json.RawMessage(nil), response.Body.Bytes()...)
	}
	final := a.Snapshot()
	if len(final.Sessions) != 2 || len(final.Operations) != 2 {
		t.Fatal("metadata reads dispatched another operation")
	}
	for _, session := range final.Sessions {
		if len(session.View.Messages) != 0 {
			t.Fatal("metadata read sent input")
		}
	}
	writeJSON("browser-fixture.json", map[string]any{"sessions": map[string]string{"a": before.ID, "b": second.SessionID}, "responses": responses, "files": request.Files})
}
