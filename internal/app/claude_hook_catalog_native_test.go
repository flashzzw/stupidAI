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
)

func installNativeHookCatalogFixture(t *testing.T, root, profile, cwd, cli string, disabled bool) {
	t.Helper()
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	market := filepath.Join(root, "hook-marketplace")
	write(filepath.Join(market, ".claude-plugin/marketplace.json"), map[string]any{"name": "hook-local", "owner": map[string]string{"name": "Fixture"}, "plugins": []any{map[string]string{"name": "catalog-hook", "source": "./plugin"}}})
	write(filepath.Join(market, "plugin/.claude-plugin/plugin.json"), map[string]string{"name": "catalog-hook", "version": "1.0.0"})
	hooks := func(event, command string) map[string]any {
		return map[string]any{event: []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]string{"type": "command", "command": command}}}}}
	}
	write(filepath.Join(market, "plugin/hooks/hooks.json"), map[string]any{"hooks": hooks("PreToolUse", "echo PRIVATE_PLUGIN_HOOK")})
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR=" + profile,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}
	for _, args := range [][]string{{"plugin", "marketplace", "add", market}, {"plugin", "install", "catalog-hook@hook-local", "--scope", "user", "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Dir = cwd
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("fixture plugin install: %v: %s", err, out)
		}
	}
	body, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	settings["disableAllHooks"] = disabled
	settings["hooks"] = hooks("PreToolUse", "echo PRIVATE_USER_HOOK")
	write(filepath.Join(profile, "settings.json"), settings)
	write(filepath.Join(cwd, ".claude/settings.json"), map[string]any{"hooks": hooks("PostToolUse", "echo PRIVATE_PROJECT_HOOK")})
}

func nativeHookCatalog(t *testing.T, a *Application, c Config, sessions map[string]string, check func(string, bool), results map[string]any) {
	t.Helper()
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot()}
	for _, label := range []string{"a", "b"} {
		id := sessions[label]
		before, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := a.ReadSessionHookCatalog(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		after, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		check(label+"_hook_original_identity", catalog.NativeID == before.Session.Scope.NativeID && catalog.Cwd == before.Session.Scope.Cwd)
		check(label+"_hook_coverage_explicit", !catalog.Complete && len(catalog.Items) == 3 && len(catalog.Limitations) == 4)
		check(label+"_hook_read_keeps_bindings", digest(before.Session.Scope) == digest(after.Session.Scope) && digest(before.Session.ExecutionSnapshot) == digest(after.Session.ExecutionSnapshot) && before.Session.Generation == after.Session.Generation && before.Session.ControlMode == after.Session.ControlMode && len(before.Operations) == len(after.Operations))
		user, project, plugin, disabled := false, false, false, true
		for _, item := range catalog.Items {
			user = user || item.Source == "userSettings"
			project = project || item.Source == "projectSettings"
			plugin = plugin || item.Source == "pluginHook" && item.PluginName == "catalog-hook@hook-local"
			disabled = disabled && item.Disabled == (label == "b")
		}
		check(label+"_hook_native_sources", user && project && plugin)
		check(label+"_hook_native_disabled_policy", disabled && catalog.Policy.AllDisabled == (label == "b"))
		body, _ := json.Marshal(catalog)
		check(label+"_hook_no_command_secrets", !strings.Contains(string(body), "PRIVATE_") && !strings.Contains(string(body), "commandText"))
		check(label+"_hook_zero_model_messages", len(after.Session.View.Messages) == 0)
		results[label+"_hook_catalog"] = catalog
		responses["/api/sessions/"+id] = after
		responses["/api/sessions/"+id+"/hooks/catalog"] = catalog
	}
	body, err := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(c.StateDir), "hook-browser-responses.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
