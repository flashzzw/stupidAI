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

func TestCodexNativeSkillDiscovery(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0207-native-skills-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	results := map[string]runtime.SkillInventory{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("check failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		report := map[string]any{"root": root, "version": "codex-cli 0.158.0", "checks": checks, "inventories": results, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/SQLite/Handler and Runtime; real native stdio test transport, no Unix proxy/peer/auth, model request, skill loading or method Gate"}
		data, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Error(e)
			return
		}
		if e = os.WriteFile(filepath.Join(root, "result.json"), data, 0600); e != nil {
			t.Error(e)
		}
	})
	write := func(path, text string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	skill := func(path, name, description string) {
		write(path, fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nBODY_MUST_NOT_APPEAR_IN_DISCOVERY\n", name, description))
	}
	shared := filepath.Join(root, "home/.agents/skills/shared/SKILL.md")
	project := filepath.Join(root, "work/.agents/skills/project/SKILL.md")
	skill(shared, "shared", "Shared fixture method")
	skill(project, "project-method", "Project fixture method")
	config := filepath.Join(root, "codex/config.toml")
	contents, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	write(config, string(contents)+fmt.Sprintf("\n[[skills.config]]\npath=%q\nenabled=false\n", shared))
	f.Restart(t)
	scope := runtime.Scope{Instance: f.Instance, Route: f.Route, Cwd: filepath.Join(root, "work")}
	first, err := f.Runtime.DiscoverSkills(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	results["initial"] = first
	find := func(in runtime.SkillInventory, path string) *runtime.DiscoveredSkill {
		for _, s := range in.Skills {
			if s.Path == path {
				return &s
			}
		}
		return nil
	}
	u, p := find(first, shared), find(first, project)
	check("native_user_disabled", u != nil && u.Scope == "user" && !u.Enabled)
	check("native_project_enabled", p != nil && p.Scope == "repo" && p.Enabled)
	check("initial_complete", first.Complete && first.Cwd == scope.Cwd)
	check("discovery_does_not_create_thread", func() bool {
		for _, r := range f.Records {
			for _, call := range r.Calls {
				if call["method"] == "thread/start" || call["method"] == "turn/start" {
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
	a, err := app.Open(app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: scope.Cwd}}}, store, f.Runtime)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	receipt, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool { return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" })
	if _, err = a.Apply(app.Command{ID: "takeover", Kind: "takeover", SessionID: receipt.SessionID}); err != nil {
		t.Fatal(err)
	}
	second, err := a.Apply(app.Command{ID: "create-second", Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: "Second discovery session"})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, session := range s.Sessions {
			if session.ID == second.SessionID {
				return session.Status == "idle"
			}
		}
		return false
	})
	if _, err = a.Apply(app.Command{ID: "takeover-second", Kind: "takeover", SessionID: second.SessionID}); err != nil {
		t.Fatal(err)
	}
	before := a.Snapshot()
	handler := console.Handler(a, "127.0.0.1:9454", strings.Repeat("a", 64), os.DirFS("."))
	read := func() runtime.SkillInventory {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:9454/api/sessions/"+receipt.SessionID+"/skills/discover", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:9454")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var out runtime.SkillInventory
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("Handler discovery: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	broken := filepath.Join(root, "work/.agents/skills/broken/SKILL.md")
	write(broken, "---\nname: broken\n---\nBROKEN_BODY_MUST_NOT_LEAK\n")
	partial := read()
	results["partial"] = partial
	check("invalid_source_visible", !partial.Complete && len(partial.Errors) > 0 && partial.Errors[0].Path == broken)
	check("valid_sources_remain_visible", find(partial, shared) != nil && find(partial, project) != nil)
	skill(broken, "repaired-method", "Repaired fixture description")
	repaired := read()
	results["repaired"] = repaired
	check("force_reload_discovers_repair", repaired.Complete && find(repaired, broken) != nil)
	if err = os.Remove(project); err != nil {
		t.Fatal(err)
	}
	removed := read()
	results["removed"] = removed
	check("force_reload_removes_deleted_source", removed.Complete && find(removed, project) == nil)
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + receipt.SessionID, "/api/sessions/" + second.SessionID} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9454"+path, nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("browser export: %s %d", path, w.Code)
		}
		responses[path] = append([]byte(nil), w.Body.Bytes()...)
	}
	browser, err := json.MarshalIndent(map[string]any{"responses": responses, "inventories": results, "sessions": []string{receipt.SessionID, second.SessionID}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "browser-responses.json"), browser, 0600); err != nil {
		t.Fatal(err)
	}
	check("discovery_preserves_application_state", reflect.DeepEqual(before, a.Snapshot()))
	check("no_model_or_configuration_writes", func() bool {
		for _, r := range f.Records {
			for _, call := range r.Calls {
				m, _ := call["method"].(string)
				if m == "turn/start" || strings.Contains(m, "config/write") {
					return false
				}
			}
		}
		return true
	}())
	data, _ := json.Marshal(results)
	check("no_skill_body_in_result", !strings.Contains(string(data), "BODY_MUST") && !strings.Contains(string(data), "BROKEN_BODY"))
	now, err := os.ReadFile(config)
	check("profile_unchanged", err == nil && string(now) == string(contents)+fmt.Sprintf("\n[[skills.config]]\npath=%q\nenabled=false\n", shared))
}
