//go:build linux && nativeclaude

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type localSettingsMutationStore struct {
	Persistence
	mu         sync.Mutex
	path, kind string
	body       []byte
	changed    bool
}

func (s *localSettingsMutationStore) Save(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Persistence.Save(state); err != nil {
		return err
	}
	if s.changed || s.kind == "" {
		return nil
	}
	for _, op := range state.Operations {
		if op.Status == "prepared" && op.Intent.Kind == s.kind {
			s.changed = true
			return os.WriteFile(s.path, s.body, 0600)
		}
	}
	return nil
}

func (s *localSettingsMutationStore) arm(kind string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kind, s.body, s.changed = kind, body, false
}
func (s *localSettingsMutationStore) wasChanged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func TestClaudeNativeSettingsVersionBeforeStartup(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in native CLI")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-settings-version-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		p := filepath.Join(root, suffix)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, p)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_SETTINGS_VERSION_KEY", "fixture-only")
	repo, cwd, profile := filepath.Join(root, "repo"), filepath.Join(root, "repo/nested"), filepath.Join(root, "profile")
	for _, p := range []string{cwd, profile} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	git := exec.Command("git", "init", "--quiet", repo)
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}
	if out, e := git.CombinedOutput(); e != nil {
		t.Fatalf("fixture git: %v %s", e, out)
	}
	path, marker := filepath.Join(repo, ".claude/settings.local.json"), filepath.Join(root, "startup-version")
	settings := func(version string) []byte {
		body, e := json.Marshal(map[string]any{"env": map[string]string{"NETCAP_SETTINGS_VERSION": version}, "hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("printf '%%s' \"$NETCAP_SETTINGS_VERSION\" > %q", marker), "timeout": 3}}}}}})
		if e != nil {
			t.Fatal(e)
		}
		return body
	}
	original := settings("v1")
	writePluginMetadata(t, path, string(original))
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Settings version", Cwd: cwd}}, Instances: []runtime.Instance{{ID: "claude", Name: "Claude versions", Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}}, Routes: []runtime.Route{{ID: "claude", InstanceID: "claude", Endpoint: "http://127.0.0.1:1", Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_SETTINGS_VERSION_KEY"}}}
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &localSettingsMutationStore{Persistence: db, path: path, kind: "create", body: settings("v2")}
	a, err := Open(c, store, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	results := map[string]any{}
	t.Cleanup(func() {
		checks["application_close"] = a.Close() == nil
		writePluginMetadata(t, filepath.Join(root, "result.json"), map[string]any{"root": root, "checks": checks, "results": results, "passed": !t.Failed() && checks["application_close"], "scope": "real Application/SQLite/Claude Runtime; valid local settings change after durable intent, before dispatch; no model input"})
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	receipt, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "claude", Permission: "read_only", Title: "Settings version race"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var operation Operation
	for {
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID {
				operation = op
			}
		}
		if operation.Status == "not_sent" || operation.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("create did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if operation.Status == "succeeded" {
		time.Sleep(150 * time.Millisecond)
	}
	read, err := a.ReadExecution(receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, markerErr := os.Stat(marker)
	_, bridgeErr := os.Stat(filepath.Join(c.StateDir, "runtimes/claude"))
	check("mutation_after_intent", store.wasChanged())
	check("durable_not_sent", operation.Status == "not_sent")
	check("specific_source_error", strings.Contains(operation.Error, path))
	check("no_startup_hook", os.IsNotExist(markerErr))
	check("no_bridge_creation", os.IsNotExist(bridgeErr))
	check("source_read_reports_drift", read.Status == "drifted")
	before, _ := json.Marshal(read.Snapshot)
	check("local_versions_recorded", strings.Contains(string(before), `"claude_local_settings"`))
	results["drifted"] = read
	results["operation"] = operation
	sessionRead, err := a.ReadSession(receipt.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + receipt.SessionID: sessionRead}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := a.ReadExecution(receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	check("restored_original_version", restored.Status == "unchanged" && digest(read.Snapshot) == digest(restored.Snapshot))
	check("no_automatic_retry", a.Snapshot().Operations[0].Status == "not_sent")
	results["restored"] = restored
	responses["/api/sessions/"+receipt.SessionID+"/execution"] = read
	writePluginMetadata(t, filepath.Join(root, "browser-responses.json"), map[string]any{"responses": responses, "session_id": receipt.SessionID, "variants": map[string]any{"drifted": read, "restored": restored}})
}
