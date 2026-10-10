//go:build linux && nativeclaude

package runtime_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativeStartupSettingsRead(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in fixed Claude CLI")
	}
	repository, _ := filepath.Abs("../..")
	root, err := os.MkdirTemp("", "netcap-w0208-startup-source-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for key, name := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_STARTUP_SOURCE_KEY", "synthetic-only")
	repo, cwd, profile := filepath.Join(root, "repo"), filepath.Join(root, "repo/nested"), filepath.Join(root, "profile")
	for _, dir := range []string{cwd, profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	git := exec.Command("git", "init", "--quiet", repo)
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	marker := filepath.Join(root, "startup-marker")
	projectFile := filepath.Join(cwd, ".claude/settings.json")
	projectSettings := fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"printf ran >> %s"}]}]},"env":{"FIXTURE_VALUE":"PRIVATE_PREFLIGHT_SENTINEL"}}`, marker)
	write(projectFile, projectSettings)
	primary := filepath.Join(repo, ".claude/settings.local.json")
	write(primary, `{"env":{"PRIMARY_ONLY":"PRIVATE_PREFLIGHT_SENTINEL"}}`)
	write(filepath.Join(profile, "settings.json"), `{"env":{"USER_ONLY":"PRIVATE_PREFLIGHT_SENTINEL"}}`)
	custom := filepath.Join(root, "custom.json")
	write(custom, `{"permissions":{"ask":["Read"]}}`)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []app.Project{{ID: "project", Name: "Startup source reads", Cwd: cwd}}, Instances: []runtime.Instance{{ID: "claude", Kind: "claude", Executable: cli, Profile: profile, SettingsFile: custom, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}}, Routes: []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_STARTUP_SOURCE_KEY"}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := runtime.NewClaude(cfg.StateDir)
	a, err := app.Open(cfg, store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	results := map[string]any{}
	responses := map[string]json.RawMessage{}
	check := func(name string, value bool) {
		t.Helper()
		checks[name] = value
		if !value {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = a.Close()
		data, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "results": results, "responses": responses, "passed": !t.Failed(), "scope": "production Application/Runtime/Handler and real SDK source reads after owned Claude query closes; real WSL registry reads; no model input, plugin change or full preflight"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), data, 0600)
	})
	r, err := a.Apply(app.Command{ID: "create", Kind: "create", Title: "Startup source reads", ProjectID: "project", RouteID: "claude", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	find := func() app.Session {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == r.SessionID {
				return s
			}
		}
		return app.Session{}
	}
	for end := time.Now().Add(25 * time.Second); find().Status != "idle"; {
		if time.Now().After(end) {
			t.Fatal("native create did not become idle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err = a.Apply(app.Command{ID: "takeover", Kind: "takeover", SessionID: r.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	before := find()
	beforeOps := len(a.Snapshot().Operations)
	journal := filepath.Join(cfg.StateDir, "runtimes", "claude", before.Scope.NativeID+".jsonl")
	journalBefore, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	markerBefore, _ := os.ReadFile(marker)
	check("startup_marker_observed_once", string(markerBefore) == "ran")
	token := strings.Repeat("b", 64)
	handler := console.Handler(a, "127.0.0.1:9463", token, os.DirFS("."))
	sample := func(label string) runtime.ClaudeStartupSettings {
		t.Helper()
		path := "/api/sessions/" + r.SessionID + "/plugins/startup-settings"
		req := httptest.NewRequest("POST", "http://127.0.0.1:9463"+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("source read %d %s", rec.Code, rec.Body.String())
		}
		var out runtime.ClaudeStartupSettings
		if json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatal("invalid response")
		}
		results[label] = out
		return out
	}
	valid := sample("valid")
	check("resolver_reads_but_not_full_admission", valid.ResolverReturned && valid.ReadComplete && !valid.Complete)
	check("result_bound_to_session", valid.NativeID == before.Scope.NativeID && valid.SnapshotID == before.ExecutionSnapshot.ID && valid.Cwd == cwd)
	check("native_primary_file_preserved", len(valid.BoundLocalFiles) == 3 && valid.BoundLocalFiles[2].Path == primary)
	check("sdk_missing_primary_does_not_hide_file", func() bool {
		reported, read := false, false
		for _, s := range valid.Sources {
			reported = reported || s.Path == primary
		}
		for _, f := range valid.Files {
			read = read || f.Path == primary && f.Status == "read"
		}
		return !reported && read
	}())
	check("real_host_reads_classified", len(valid.Registry) == 2 && valid.Registry[0].Outcome == "absent" && valid.Registry[1].Outcome == "absent")
	write(projectFile, `{broken`)
	malformed := sample("malformed")
	check("malformed_source_not_success", malformed.ResolverReturned && !malformed.ReadComplete && !malformed.Complete)
	check("malformed_file_visible", func() bool {
		for _, f := range malformed.Files {
			if f.Path == projectFile {
				return f.Status == "invalid_json"
			}
		}
		return false
	}())
	write(projectFile, projectSettings)
	again := sample("restored")
	check("restored_file_can_be_read_again", again.ReadComplete)
	after := find()
	check("no_new_operation_or_snapshot", len(a.Snapshot().Operations) == beforeOps && reflect.DeepEqual(after.Scope, before.Scope) && reflect.DeepEqual(after.ExecutionSnapshot, before.ExecutionSnapshot) && after.Paused)
	journalAfter, _ := os.ReadFile(journal)
	markerAfter, _ := os.ReadFile(marker)
	check("no_query_restart_or_new_hook", string(journalBefore) == string(journalAfter) && string(markerBefore) == string(markerAfter))
	check("no_model_inputs", !strings.Contains(string(journalAfter), `"kind":"input"`))
	encoded, _ := json.Marshal(results)
	check("no_private_values_exported", !strings.Contains(string(encoded), "PRIVATE_PREFLIGHT_SENTINEL") && !strings.Contains(string(encoded), "printf ran"))
	for _, path := range []string{"/api/workspace", "/api/sessions/" + r.SessionID} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		responses[path] = append([]byte(nil), rec.Body.Bytes()...)
	}
	results["session_id"] = r.SessionID
}
