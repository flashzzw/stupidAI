//go:build linux && nativeclaude

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func TestClaudeNativeApplicationSettingsGuard(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-settings-guard-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		dir := filepath.Join(root, suffix)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_LOCAL_APP_KEY", "fixture-only")
	repo, cwd, profile := filepath.Join(root, "repo"), filepath.Join(root, "repo/nested"), filepath.Join(root, "profile")
	for _, dir := range []string{cwd, profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	git := exec.Command("git", "init", "--quiet", repo)
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if out, e := git.CombinedOutput(); e != nil {
		t.Fatalf("fixture git: %v %s", e, out)
	}
	path := filepath.Join(repo, ".claude/settings.local.json")
	marker := filepath.Join(root, "hooks.txt")
	settings := func(version string) map[string]any {
		hooks := map[string]any{}
		for _, event := range []string{"SessionStart", "SessionEnd"} {
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf '%s:%s:%%s\\n' \"$NETCAP_GUARD_VERSION\" >> %q", version, event, marker), "timeout": 3}}}}
		}
		return map[string]any{"env": map[string]string{"NETCAP_GUARD_VERSION": version}, "hooks": hooks}
	}
	writePluginMetadata(t, path, settings("v1"))
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Local settings admission", Cwd: cwd}}, Instances: []runtime.Instance{{ID: "claude", Name: "Claude local settings", Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}}, Routes: []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_LOCAL_APP_KEY"}}}
	a := openFixture(t, c, runtime.NewClaude(c.StateDir))
	checks := map[string]bool{}
	results := map[string]any{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		checks["application_close"] = a.Close() == nil
		content, _ := os.ReadFile(marker)
		results["markers"] = string(content)
		checks["original_end_hook"] = strings.Contains(string(content), "v1:SessionEnd:v1") && !strings.Contains(string(content), "v2:")
		if !checks["original_end_hook"] {
			t.Error("new settings affected original native close")
		}
		writePluginMetadata(t, filepath.Join(root, "result.json"), map[string]any{"checks": checks, "results": results, "passed": !t.Failed(), "root": root, "scope": "production Application/SQLite/Runtime/Session source binding and local ConfigChange blocking; no model input"})
	})
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "claude", Permission: "read_only", Title: "Settings guard"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatal("guard observation did not settle")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	wait(func() bool {
		s := a.Snapshot()
		return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].Scope.NativeID != "" && s.Sessions[0].View.SettingsGuard != nil
	})
	before, err := a.ReadSession(created.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	check("guard_bound_to_snapshot", before.Session.Scope.SettingsGuardVersion == 1 && before.Session.ExecutionSnapshot.ClaudeLocalSettings.GuardVersion == 1)
	check("guard_registered", before.Session.View.SettingsGuard != nil && before.Session.View.SettingsGuard.State == "registered")
	results["before"] = before
	writePluginMetadata(t, path, settings("v2"))
	wait(func() bool {
		s := a.Snapshot().Sessions[0]
		return s.View.SettingsGuard != nil && len(s.View.SettingsGuard.Changes) > 0
	})
	after, err := a.ReadSession(created.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	check("source_still_drifted", read.Status == "drifted")
	check("original_snapshot", digest(before.Session.ExecutionSnapshot) == digest(after.Session.ExecutionSnapshot))
	check("original_scope", digest(before.Session.Scope) == digest(after.Session.Scope))
	change := after.Session.View.SettingsGuard.Changes[0]
	check("specific_change_path", change.Path == path && change.Source == "local_settings" && change.Decision == "block_requested")
	_, err = a.Apply(Command{ID: "blocked-send", Kind: "send", SessionID: created.SessionID, Text: "MUST NOT REACH MODEL"})
	check("source_check_still_blocks_send", err != nil)
	check("no_model_messages", len(after.Session.View.Messages) == 0)
	results["after"] = after
	results["execution"] = read
	writePluginMetadata(t, filepath.Join(root, "browser-responses.json"), map[string]any{"responses": map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + created.SessionID: after}, "session_id": created.SessionID})
}
