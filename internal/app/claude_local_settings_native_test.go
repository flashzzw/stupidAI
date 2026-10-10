//go:build linux && nativeclaude

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func TestClaudeNativeApplicationLocalSettingsRejection(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-local-settings-app-")
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
	writePluginMetadata(t, path, map[string]any{"env": map[string]string{"NETCAP_LOCAL_APP": "safe"}})
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Local settings admission", Cwd: cwd}}, Instances: []runtime.Instance{{ID: "claude", Name: "Claude local settings", Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}}, Routes: []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_LOCAL_APP_KEY"}}}
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	mutation := &localSettingsMutationStore{Persistence: store, path: path}
	a, err := Open(c, mutation, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	results := map[string]any{}
	t.Cleanup(func() {
		checks["application_close"] = a.Close() == nil
		writePluginMetadata(t, filepath.Join(root, "result.json"), map[string]any{"root": root, "checks": checks, "results": results, "passed": !t.Failed() && checks["application_close"], "scope": "production Application/SQLite/Claude Runtime/Session; zero model input"})
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	created, err := a.Apply(Command{ID: "create", Kind: "create", Title: "Local settings", ProjectID: "project", RouteID: "claude", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatal("application condition did not settle")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool {
		s := a.Snapshot()
		return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].Scope.NativeID != ""
	})
	before, err := a.ReadSession(created.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Change after durable send intent so the dispatch check, rather than the
	// synchronous source check, must preserve a persistent not_sent result.
	mutation.arm("send", []byte(`{"env":{"ANTHROPIC_API_KEY":"fixture-other"}}`))
	receipt, err := a.Apply(Command{ID: "blocked-send", Kind: "send", SessionID: created.SessionID, Text: "MUST NOT REACH MODEL"})
	if err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	after, err := a.ReadSession(created.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	check("original_identity", after.Session.Scope.NativeID == before.Session.Scope.NativeID)
	check("original_snapshot", digest(after.Session.ExecutionSnapshot) == digest(before.Session.ExecutionSnapshot))
	check("original_scope", digest(after.Session.Scope) == digest(before.Session.Scope))
	var rejected Operation
	for _, op := range after.Operations {
		if op.ID == receipt.OperationID {
			rejected = op
		}
	}
	check("durable_not_sent", rejected.ID == receipt.OperationID && rejected.Status == "not_sent")
	check("specific_source_error", strings.Contains(rejected.Error, path) && strings.Contains(rejected.Error, "来源待核对"))
	check("no_credential_value_in_error", !strings.Contains(rejected.Error, "fixture-other"))
	check("no_model_messages", len(after.Session.View.Messages) == 0)
	results["before"] = before
	results["rejected"] = after
	writePluginMetadata(t, path, map[string]any{"env": map[string]string{"NETCAP_LOCAL_APP": "safe"}})
	_, err = a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: created.SessionID})
	check("takeover_preserved", err == nil)
	final, err := a.ReadSession(created.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	check("repair_keeps_original_id", final.Session.Scope.NativeID == before.Session.Scope.NativeID)
	writePluginMetadata(t, filepath.Join(root, "browser-responses.json"), map[string]any{"responses": map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + created.SessionID: final}, "session_id": created.SessionID})
	paths, _ := filepath.Glob(filepath.Join(c.StateDir, "runtimes/claude", "*.jsonl"))
	check("native_journal_present", len(paths) == 1)
	for _, p := range paths {
		body, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		check("journal_no_input", !strings.Contains(string(body), `"kind":"input"`))
	}
	results["final"] = final
}
