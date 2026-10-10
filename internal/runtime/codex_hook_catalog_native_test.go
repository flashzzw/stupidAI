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

func TestCodexNativeHookCatalog(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-codex-hooks-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	results := map[string]runtime.HookSourceCatalog{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		data, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "inventories": results, "processes": f.Records, "passed": !t.Failed(), "scope": "production Runtime/Application/SQLite/Handler, native stdio fixture; directory discovery, no model/tool/hook execution or production Unix peer validation"}, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "result.json"), data, 0600); err != nil {
			t.Error(err)
		}
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
	profile := filepath.Join(root, "codex")
	config := filepath.Join(profile, "config.toml")
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	write(config, strings.Replace(string(content), "plugins=false", "plugins=true\nplugin_sharing=false", 1))
	user := filepath.Join(profile, "hooks.json")
	project := filepath.Join(cwd, ".codex/hooks.json")
	body := `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"printf PRIVATE_HOOK_BODY"}]}]}}`
	write(user, body)
	write(project, body)
	plugin := filepath.Join(cwd, "plugins/fixture")
	write(filepath.Join(plugin, ".codex-plugin/plugin.json"), `{"name":"fixture","version":"1.0.0"}`)
	write(filepath.Join(plugin, "hooks/hooks.json"), body)
	market := filepath.Join(cwd, ".agents/plugins/marketplace.json")
	write(market, `{"name":"owned","plugins":[{"name":"fixture","source":{"source":"local","path":"./plugins/fixture"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Productivity"}]}`)
	f.Restart(t)
	var installed any
	if err := f.Call(context.Background(), "plugin/install", map[string]any{"pluginName": "fixture", "marketplacePath": market}, &installed); err != nil {
		t.Fatal(err)
	}
	scope := runtime.Scope{Instance: f.Instance, Route: f.Route, Cwd: cwd, StateNamespace: profile}
	initial, err := f.Runtime.ReadHookCatalog(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	results["untrusted"] = initial
	bySource := func(c runtime.HookSourceCatalog, source string) *runtime.HookSourceItem {
		for _, h := range c.Items {
			if h.Source == source {
				item := h
				return &item
			}
		}
		return nil
	}
	check("untrusted_project_is_not_discovered", bySource(initial, "project") == nil)
	currentConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	write(config, string(currentConfig)+fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cwd))
	f.Restart(t)
	initial, err = f.Runtime.ReadHookCatalog(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	results["initial"] = initial
	u, p, plug := bySource(initial, "user"), bySource(initial, "project"), bySource(initial, "plugin")
	check("user_source_reported", u != nil && u.SourcePath == user)
	check("project_source_reported", p != nil && p.SourcePath == project)
	check("plugin_cache_and_id_reported", plug != nil && plug.PluginID == "fixture@owned" && strings.Contains(plug.SourcePath, "/plugins/cache/owned/fixture/1.0.0/"))
	check("trust_not_promoted_to_permission", plug != nil && plug.TrustStatus == "untrusted" && !plug.Disabled)
	check("directory_scope_not_complete_session_state", initial.Scope == "directory" && !initial.Complete && !initial.PolicyReported && initial.NativeID == "")
	check("catalog_did_not_create_thread", func() bool {
		for _, r := range f.Records {
			for _, c := range r.Calls {
				if c["method"] == "thread/start" || c["method"] == "turn/start" {
					return false
				}
			}
		}
		return true
	}())
	store, err := app.OpenStore(filepath.Join(root, "application"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}}, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ids := []string{}
	for i := 0; i < 2; i++ {
		r, err := a.Apply(app.Command{ID: fmt.Sprintf("create-%d", i), Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: fmt.Sprintf("Hook discovery %d", i+1)})
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
		if _, err := a.Apply(app.Command{ID: fmt.Sprintf("takeover-%d", i), Kind: "takeover", SessionID: r.SessionID}); err != nil {
			t.Fatal(err)
		}
	}
	before := a.Snapshot()
	profileBefore, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	handler := console.Handler(a, "127.0.0.1:9462", token, os.DirFS("."))
	read := func() runtime.HookSourceCatalog {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:9462/api/sessions/"+ids[0]+"/hooks/catalog", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", "http://127.0.0.1:9462")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var out runtime.HookSourceCatalog
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("catalog Handler: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	results["handler"] = read()
	check("handler_returns_saved_directory", results["handler"].Cwd == cwd && results["handler"].NativeID != "" && len(results["handler"].Items) == len(initial.Items))
	write(project, `{"hooks": PRIVATE_SOURCE_PARSE_ERROR`)
	results["broken"] = read()
	check("broken_source_warning_and_omission_visible", results["broken"].WarningCount > 0 && bySource(results["broken"], "project") == nil)
	check("broken_source_preserves_other_hooks", bySource(results["broken"], "plugin") != nil && bySource(results["broken"], "user") != nil)
	write(project, body)
	results["repaired"] = read()
	check("repair_seen_on_next_read", len(results["repaired"].Errors) == 0 && results["repaired"].WarningCount == 0 && bySource(results["repaired"], "project") != nil)
	data, _ := json.Marshal(results)
	check("private_command_and_diagnostics_absent", !strings.Contains(string(data), "PRIVATE_"))
	check("catalog_preserves_application_state", reflect.DeepEqual(before, a.Snapshot()))
	profileAfter, err := os.ReadFile(config)
	check("catalog_does_not_write_profile", err == nil && string(profileBefore) == string(profileAfter))
	check("no_model_turns", func() bool {
		for _, r := range f.Records {
			for _, c := range r.Calls {
				if c["method"] == "turn/start" {
					return false
				}
			}
		}
		return true
	}())
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids[0], "/api/sessions/" + ids[1]} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9462"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[path] = append([]byte{}, w.Body.Bytes()...)
	}
	browser, _ := json.MarshalIndent(map[string]any{"responses": responses, "inventories": results, "sessions": ids}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "browser-responses.json"), browser, 0600); err != nil {
		t.Fatal(err)
	}
}
