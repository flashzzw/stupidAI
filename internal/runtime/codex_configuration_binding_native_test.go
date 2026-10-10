//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCodexNativeConfigurationBindingColdDrift(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0209-config-binding-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	cwd := filepath.Join(root, "work")
	path := filepath.Join(cwd, ".codex/config.toml")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	write(configPath, string(config)+fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cwd))
	write(path, "model_reasoning_effort=\"low\"\n")
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: cwd}}}
	open := func() *app.Application {
		t.Helper()
		store, err := app.OpenStore(cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		a, err := app.Open(cfg, store, f.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	checks := map[string]bool{}
	responses := map[string]any{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = a.Close()
		_ = f.Runtime.Close()
		body, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "responses": responses, "processes": f.Records, "passed": !t.Failed(), "scope": "one cancelled unavailable-loopback-provider input establishes history; project config mode drift across real native restart; no successful model/tool/hook"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
	})
	created, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "work", RouteID: f.Route.ID, Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	session := func() app.Session { return a.Snapshot().Sessions[0] }
	waitIdle := func() app.Session {
		return waitNativeApplication(t, a, func(s app.Snapshot) bool {
			return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].View.ExecutionClear
		}).Sessions[0]
	}
	initial := waitIdle()
	seed, err := a.Apply(app.Command{ID: "seed", Kind: "send", SessionID: created.SessionID, Text: "OWNED SOURCE BINDING HISTORY; provider unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == seed.OperationID && op.Receipt.NativeTurn != "" {
				return f.HasTurnEvent("error", initial.Scope.NativeID, op.Receipt.NativeTurn)
			}
		}
		return false
	})
	if _, err := a.Apply(app.Command{ID: "cancel", Kind: "interrupt", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
	baseline := waitIdle()
	check("source_bound_to_scope", baseline.ExecutionSnapshot.CodexConfigurationSources != nil && reflect.DeepEqual(baseline.ExecutionSnapshot.CodexConfigurationSources, baseline.Scope.CodexConfigurationSources))
	export := func(phase string) {
		t.Helper()
		handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
		rows := map[string]json.RawMessage{}
		for _, p := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + created.SessionID, "/api/sessions/" + created.SessionID + "/execution"} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+p, nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("%s %s: %d", phase, p, rec.Code)
			}
			rows[p] = append(json.RawMessage(nil), rec.Body.Bytes()...)
		}
		responses[phase] = rows
	}
	export("initial")
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(created.SessionID)
	check("mode_drift_visible", err == nil && read.Status == "drifted")
	_, err = a.Apply(app.Command{ID: "blocked-before-close", Kind: "send", SessionID: created.SessionID, Text: "MUST NOT SEND"})
	check("mode_drift_blocks_input", err != nil)
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		return s.Sessions[0].Status == "unknown" && !s.Sessions[0].View.ExecutionClear
	})
	export("drift")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	waitNativeApplication(t, a, func(s app.Snapshot) bool { return s.Sessions[0].Status == "unknown" })
	read, err = a.ReadExecution(created.SessionID)
	check("reopened_drift_still_visible", err == nil && read.Status == "drifted")
	check("reopened_keeps_scope_snapshot", reflect.DeepEqual(baseline.Scope, session().Scope) && reflect.DeepEqual(baseline.ExecutionSnapshot, session().ExecutionSnapshot))
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := f.Call(context.Background(), "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal(err)
	}
	check("drift_does_not_reload_thread", len(loaded.Data) == 0)
	_, err = a.Apply(app.Command{ID: "blocked-after-close", Kind: "resume", SessionID: created.SessionID})
	check("reopened_resume_blocked", err != nil)
	export("reopened")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	restored := waitIdle()
	check("restored_original_id", restored.Scope.NativeID == baseline.Scope.NativeID)
	check("restored_original_snapshot", reflect.DeepEqual(restored.ExecutionSnapshot, baseline.ExecutionSnapshot))
	check("restored_original_history", reflect.DeepEqual(restored.View.Messages, baseline.View.Messages) && reflect.DeepEqual(restored.View.Turns, baseline.View.Turns))
	read, err = a.ReadExecution(created.SessionID)
	check("restored_sources_match", err == nil && read.Status == "unchanged")
	export("restored")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	starts, turns := 0, 0
	for _, p := range f.Records {
		for _, call := range p.Calls {
			if call["method"] == "thread/start" {
				starts++
			}
			if call["method"] == "turn/start" {
				turns++
			}
		}
	}
	check("no_new_thread_or_input", starts == 1 && turns == 1)
}
