//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeHookExecutionRecords(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-codex-hook-records-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	samples := map[string]app.Snapshot{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "snapshots": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/SQLite/Runtime/Handler with native stdio; owned plugin hook timeout, unavailable loopback provider and explicit interrupt; no successful model/tool/plugin execution"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), b, 0600)
	})
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cwd := filepath.Join(root, "work")
	configPath := filepath.Join(root, "codex/config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	write(configPath, strings.Replace(string(content), "plugins=false", "plugins=true\ncodex_hooks=true\nplugin_sharing=false", 1))
	plugin := filepath.Join(cwd, "plugins/fixture")
	write(filepath.Join(plugin, ".codex-plugin/plugin.json"), `{"name":"fixture","version":"1.0.0"}`)
	// Exercise a real deadline rather than depending on an environment where
	// even an already-exited command can fail to report completion.
	write(filepath.Join(plugin, "hooks/hooks.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"exec sleep 2","timeout":1}]}]}}`)
	market := filepath.Join(cwd, ".agents/plugins/marketplace.json")
	write(market, `{"name":"owned","plugins":[{"name":"fixture","source":{"source":"local","path":"./plugins/fixture"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Productivity"}]}`)
	f.Restart(t)
	if err := f.Call(context.Background(), "plugin/install", map[string]any{"pluginName": "fixture", "marketplacePath": market}, nil); err != nil {
		t.Fatal(err)
	}
	catalog, err := f.Runtime.ReadHookCatalog(context.Background(), runtime.Scope{Instance: f.Instance, Cwd: cwd, StateNamespace: f.Instance.Profile})
	if err != nil || len(catalog.Items) != 1 {
		t.Fatalf("hook setup: %v %+v", err, catalog)
	}
	h := catalog.Items[0]
	if err := f.Call(context.Background(), "config/value/write", map[string]any{"keyPath": "hooks.state", "value": map[string]any{h.Key: map[string]any{"enabled": true, "trusted_hash": h.DefinitionHash}}, "mergeStrategy": "replace"}, nil); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}}
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
	profileBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 2; i++ {
		r, err := a.Apply(app.Command{ID: fmt.Sprintf("create-%d", i), Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: []string{"Codex hook failure", "No hook invocation"}[i]})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.SessionID)
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, session := range s.Sessions {
				if session.ID == r.SessionID {
					return session.Status == "idle"
				}
			}
			return false
		})
	}
	find := func(s app.Snapshot) app.Session {
		for _, session := range s.Sessions {
			if session.ID == ids[0] {
				return session
			}
		}
		t.Fatal("session missing")
		return app.Session{}
	}
	check("no_hook_on_thread_creation", len(find(a.Snapshot()).View.HookExecutions) == 0)
	send, err := a.Apply(app.Command{ID: "first-input", Kind: "send", SessionID: ids[0], Text: "Owned hook record probe; provider intentionally unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitNativeApplication(t, a, func(s app.Snapshot) bool {
		session := find(s)
		return len(session.View.HookExecutions) > 0 && session.View.HookExecutions[0].Status == "error"
	})
	samples["failed-before-interrupt"] = snap
	session := find(snap)
	got := session.View.HookExecutions[0]
	check("native_failure_visible", got.NativeStatus == "failed" && got.Status == "error")
	check("native_source_path_preserved", got.Source == "plugin" && got.SourcePath == h.SourcePath)
	check("native_run_and_turn_preserved", got.NativeRunID != "" && got.TurnID != "")
	check("original_operation_bound", got.Operation == send.OperationID)
	check("duration_and_timestamps_preserved", got.DurationMS != nil && *got.DurationMS >= 1000 && got.StartedAt != "" && got.FinishedAt != "")
	check("exit_code_not_invented", got.ExitCode == nil)
	_, err = a.Apply(app.Command{ID: "cancel-offline-turn", Kind: "interrupt", SessionID: ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	stopped := waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == send.OperationID {
				return op.Status == "interrupted"
			}
		}
		return false
	})
	samples["interrupted"] = stopped
	check("failed_hook_did_not_hide_turn_cancellation", find(stopped).View.HookExecutions[0].Status == "error")
	before := find(stopped)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	restarted := waitNativeApplication(t, a, func(s app.Snapshot) bool {
		session := find(s)
		return session.Connection == "ready" && len(session.View.HookExecutions) == 1
	})
	samples["restarted"] = restarted
	after := find(restarted)
	check("restart_keeps_session_and_run", after.ID == before.ID && after.Scope.NativeID == before.Scope.NativeID && after.View.HookExecutions[0].ID == got.ID)
	check("restart_keeps_outcome_and_operation", after.View.HookExecutions[0].Status == "error" && after.View.HookExecutions[0].Operation == send.OperationID)
	profileAfter, err := os.ReadFile(configPath)
	check("application_does_not_write_hook_configuration", err == nil && string(profileAfter) == string(profileBefore))
	token := strings.Repeat("b", 64)
	handler := console.Handler(a, "127.0.0.1:9463", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids[0], "/api/sessions/" + ids[1]} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[path] = append([]byte(nil), w.Body.Bytes()...)
	}
	check("handler_exposes_recorded_failure", strings.Contains(string(responses["/api/sessions/"+ids[0]]), `"native_status":"failed"`))
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "browser-responses.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
