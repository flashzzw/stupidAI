//go:build linux && nativeclaude

package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func TestClaudeNativeApplicationInstalledPluginCatalog(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-installed-catalog-")
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
	profile, cwd := filepath.Join(root, "profile"), filepath.Join(root, "work")
	for _, dir := range []string{profile, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// This real local install contributes only tool hooks; no input is sent.
	installNativeHookCatalogFixture(t, root, profile, cwd, cli, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, cli, "--version")
	cmd.Dir = cwd
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR=" + profile, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	version, err := cmd.Output()
	cancel()
	if err != nil || strings.TrimSpace(string(version)) != "2.1.280 (Claude Code)" {
		t.Fatalf("fixed CLI required: %q %v", version, err)
	}
	t.Setenv("NETCAP_INSTALLED_CATALOG_FIXTURE", "fixture-only")
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Installed catalog", Cwd: cwd}},
		Instances: []runtime.Instance{{ID: "claude", Name: "Claude catalog", Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repo, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}},
		Routes:    []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_INSTALLED_CATALOG_FIXTURE"}}}
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(c, store, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	checks, results := map[string]bool{}, map[string]any{}
	t.Cleanup(func() {
		checks["application_close"] = a.Close() == nil
		writePluginMetadata(t, filepath.Join(root, "result.json"), map[string]any{"root": root, "version": strings.TrimSpace(string(version)), "checks": checks, "results": results, "passed": !t.Failed() && checks["application_close"], "scope": "real local CLI installation; production Application/SQLite/Runtime; no model input or HTTP listener"})
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	receipt, err := a.Apply(Command{ID: "create-catalog", Kind: "create", Title: "Installed plugin catalog", ProjectID: "project", RouteID: "claude", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		s := a.Snapshot()
		if len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].Scope.NativeID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native session not ready: %+v", s.Sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := a.Apply(Command{ID: "pause-catalog", Kind: "takeover", SessionID: receipt.SessionID}); err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadSession(receipt.SessionID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + receipt.SessionID: before}
	variants := map[string]InstalledPluginCatalog{}
	registry := filepath.Join(profile, "plugins/installed_plugins.json")
	known := filepath.Join(profile, "plugins/known_marketplaces.json")
	registryBefore, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"native", "drift", "unknown"} {
		if mode == "drift" {
			writePluginMetadata(t, filepath.Join(root, "hook-marketplace/plugin/.claude-plugin/plugin.json"), map[string]string{"name": "catalog-hook", "version": "2.0.0"})
		}
		if mode == "unknown" {
			writePluginMetadata(t, known, map[string]any{})
		}
		catalog, err := a.ReadSessionInstalledPlugins(context.Background(), receipt.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		variants[mode] = catalog
		check(mode+"_one_install", len(catalog.Entries) == 1)
		if len(catalog.Entries) != 1 {
			t.FailNow()
		}
		e := catalog.Entries[0]
		check(mode+"_original_binding", catalog.NativeID == before.Session.Scope.NativeID && catalog.Profile == profile && catalog.Cwd == cwd && catalog.SnapshotID == before.Session.ExecutionSnapshot.ID)
		check(mode+"_native_registration", e.SourceKey == "catalog-hook@hook-local" && e.Scope == "user" && e.RegisteredVersion == "1.0.0" && e.CacheStatus == "directory")
		if mode != "unknown" {
			check(mode+"_distinct_paths", e.CachePath != e.DeclaredPath && e.DeclaredPath == filepath.Join(root, "hook-marketplace/plugin") && e.ResolvedPath == e.DeclaredPath)
		}
		if mode == "native" {
			check("native_local_declaration", catalog.Complete && e.ManifestVersion == "1.0.0" && e.Status == "local_declaration")
		}
		if mode == "drift" {
			check("version_drift_visible", !catalog.Complete && e.ManifestVersion == "2.0.0" && e.Status == "version_mismatch")
		}
		if mode == "unknown" {
			check("unknown_origin_visible", !catalog.Complete && e.DeclaredPath == "" && e.Status == "unavailable")
		}
		after, err := a.ReadSession(receipt.SessionID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		check(mode+"_state_unchanged", digest(before.Session.Scope) == digest(after.Session.Scope) && digest(before.Session.ExecutionSnapshot) == digest(after.Session.ExecutionSnapshot) && before.Session.Generation == after.Session.Generation && before.Session.ControlMode == after.Session.ControlMode && len(before.Operations) == len(after.Operations))
		check(mode+"_no_model_messages", len(after.Session.View.Messages) == 0)
		body, _ := json.Marshal(catalog)
		check(mode+"_no_body_or_load_claim", !strings.Contains(string(body), "PRIVATE_") && !strings.Contains(string(body), `"enabled"`) && !strings.Contains(string(body), `"verified"`))
	}
	registryAfter, err := os.ReadFile(registry)
	check("registry_unchanged", err == nil && string(registryBefore) == string(registryAfter))
	paths, _ := filepath.Glob(filepath.Join(c.StateDir, "runtimes", "claude", "*.jsonl"))
	check("native_journal_exists", len(paths) > 0)
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		check("journal_no_input", !strings.Contains(string(body), `"kind":"input"`))
	}
	results["catalogs"] = variants
	responses["/api/sessions/"+receipt.SessionID+"/plugins/installed"] = variants["native"]
	writePluginMetadata(t, filepath.Join(root, "browser-responses.json"), map[string]any{"responses": responses, "sessions": map[string]string{"a": receipt.SessionID}, "variants": variants})
}
