//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

// Establish what config/read proves before binding its provenance to snapshots.
// All configs and threads belong to this fixture; no model input is sent.
func TestCodexNativeConfigurationLayers(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0209-config-layers-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	samples, checks := map[string]any{}, map[string]bool{}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, passed bool) {
		t.Helper()
		checks[name] = passed
		if !passed {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		body, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "fixed native disk layers/origins and loaded-thread distinction; owned files, no model input"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
	})
	call := func(label, method string, params map[string]any) map[string]any {
		t.Helper()
		var out map[string]any
		if err := f.Call(context.Background(), method, params, &out); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		samples[label] = out
		return out
	}
	project := filepath.Join(root, "work")
	child := filepath.Join(project, "child")
	userPath := filepath.Join(f.Instance.Profile, "config.toml")
	original, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	user := "model_reasoning_effort=\"low\"\n" + string(original) + fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", project)
	write(userPath, user)
	parentPath := filepath.Join(project, ".codex/config.toml")
	childPath := filepath.Join(child, ".codex/config.toml")
	write(parentPath, "model_reasoning_effort=\"medium\"\n")
	write(childPath, "model_reasoning_effort=\"high\"\n")
	cmd := exec.Command("git", "init", "--quiet", project)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned git: %s %v", output, err)
	}
	f.Restart(t)
	read := func(label, cwd string) map[string]any {
		return call(label, "config/read", map[string]any{"cwd": cwd, "includeLayers": true})
	}
	effort := func(response map[string]any) any {
		return response["config"].(map[string]any)["model_reasoning_effort"]
	}
	start := func(label, cwd string, overrides map[string]any) map[string]any {
		return call(label, "thread/start", map[string]any{"cwd": cwd, "model": f.Route.Model, "modelProvider": f.Route.Provider, "sandbox": "read-only", "approvalPolicy": "on-request", "config": overrides})
	}
	initial := read("child_initial", child)
	check("nearest_project_wins", effort(initial) == "high")
	check("parent_wins_in_parent_directory", effort(read("parent_initial", project)) == "medium")
	check("origins_returned", initial["origins"] != nil)
	loaded := start("thread_initial", child, map[string]any{})
	id := loaded["thread"].(map[string]any)["id"].(string)
	check("thread_initial_effort", loaded["reasoningEffort"] == "high")
	write(childPath, "# same parsed settings\nmodel_reasoning_effort = 'high'\n")
	format := read("child_format_only", child)
	check("formatting_does_not_change_native_layers", reflect.DeepEqual(initial["layers"], format["layers"]))
	if err := os.Chmod(childPath, 0640); err != nil {
		t.Fatal(err)
	}
	mode := read("child_mode_only", child)
	check("permissions_not_in_native_layers", reflect.DeepEqual(format["layers"], mode["layers"]))
	write(childPath, "model_reasoning_effort=\"xhigh\"\n")
	hot := read("child_changed", child)
	check("hot_file_change_visible_in_disk_config", effort(hot) == "xhigh")
	check("changed_layers_visible", !reflect.DeepEqual(initial["layers"], hot["layers"]))
	thread := call("loaded_after_disk_change", "thread/read", map[string]any{"threadId": id, "includeTurns": false})
	check("disk_config_not_loaded_thread_config", thread["thread"].(map[string]any)["reasoningEffort"] == "high")
	fresh := start("thread_after_disk_change", child, map[string]any{})
	check("new_thread_uses_changed_disk_config", fresh["reasoningEffort"] == "xhigh")
	override := start("thread_explicit_override", child, map[string]any{"model_reasoning_effort": "low"})
	check("thread_override_applies", override["reasoningEffort"] == "low")
	afterOverride := read("disk_after_thread_override", child)
	check("thread_override_not_in_config_read", effort(afterOverride) == "xhigh" && reflect.DeepEqual(hot["origins"], afterOverride["origins"]))
	write(parentPath, "model_reasoning_effort=\"low\"\n")
	hidden := read("overridden_parent_changed", child)
	check("overridden_parent_keeps_effective_value", effort(hidden) == "xhigh")
	check("overridden_parent_change_still_in_layers", !reflect.DeepEqual(afterOverride["layers"], hidden["layers"]))
	if err := os.Remove(childPath); err != nil {
		t.Fatal(err)
	}
	deleted := read("child_file_removed", child)
	check("removed_file_reveals_parent", effort(deleted) == "low")
	write(userPath, strings.Replace(user, "trust_level=\"trusted\"", "trust_level=\"untrusted\"", 1))
	write(childPath, "model_reasoning_effort=\"high\"\n")
	untrusted := read("untrusted_project", child)
	check("untrusted_uses_user_value", effort(untrusted) == "low")
	disabled := 0
	for _, layer := range untrusted["layers"].([]any) {
		if l := layer.(map[string]any); l["disabledReason"] != nil && l["disabledReason"] != "" {
			disabled++
		}
	}
	check("untrusted_layers_retained_with_reason", disabled == 2)
}
