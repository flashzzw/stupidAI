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

func TestCodexNativeConfigurationCatalog(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0209-config-catalog-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
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
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	user := "model_reasoning_effort=\"low\"\n" + string(config) + fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cwd)
	projectPath := filepath.Join(cwd, ".codex/config.toml")
	write(configPath, user)
	write(projectPath, "model_reasoning_effort=\"high\"\n")
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: cwd}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	responses, checks := map[string]any{}, map[string]bool{}
	check := func(name string, passed bool) {
		t.Helper()
		checks[name] = passed
		if !passed {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = a.Close()
		_ = f.Runtime.Close()
		body, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "responses": responses, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/Handler disk provenance; no model input, no snapshot upgrade or config application"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
	})
	created, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "work", RouteID: f.Route.ID, Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	initial := waitNativeApplication(t, a, func(s app.Snapshot) bool {
		return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].View.ExecutionClear
	}).Sessions[0]
	sid := created.SessionID
	handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
	catalogPath := "/api/sessions/" + sid + "/configuration/catalog"
	export := func(phase string) runtime.ConfigurationCatalog {
		t.Helper()
		rows := map[string]json.RawMessage{}
		for _, path := range []string{catalogPath, "/api/workspace", "/api/capabilities", "/api/sessions/" + sid, "/api/sessions/" + sid + "/execution"} {
			method, body := "GET", ""
			if path == catalogPath {
				method, body = "POST", "{}"
			}
			req := httptest.NewRequest(method, "http://127.0.0.1:9463"+path, strings.NewReader(body)).WithContext(runtime.NativeTraceContext(context.Background(), "catalog-"+phase))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("%s %s: %d %s", phase, path, rec.Code, rec.Body.String())
			}
			rows[path] = append(json.RawMessage(nil), rec.Body.Bytes()...)
		}
		responses[phase] = rows
		var out runtime.ConfigurationCatalog
		if err := json.Unmarshal(rows[catalogPath], &out); err != nil {
			t.Fatal(err)
		}
		current := a.Snapshot().Sessions[0]
		check(phase+"_same_snapshot_scope", reflect.DeepEqual(initial.ExecutionSnapshot, current.ExecutionSnapshot) && reflect.DeepEqual(initial.Scope, current.Scope))
		check(phase+"_read_scope", out.NativeID == initial.Scope.NativeID && out.Cwd == cwd && out.Scope == "disk_configuration" && !out.Complete)
		check(phase+"_no_model_or_operation", len(current.View.Messages) == 0 && len(a.Snapshot().Operations) == 1)
		check(phase+"_requirements_explicit", !out.RequirementsPresent && out.RequirementsSHA256 != "")
		return out
	}
	first := export("initial")
	check("settings_recorded_in_scope_and_snapshot", first.Settings != nil && initial.Scope.CodexConfigurationSources.Settings != nil && reflect.DeepEqual(initial.Scope.CodexConfigurationSources.Settings, initial.ExecutionSnapshot.CodexConfigurationSources.Settings))
	check("initial_settings_match_saved", first.SettingsDifference != nil && first.SettingsDifference.Status == "unchanged" && first.SettingsDifference.BaseSnapshotID == initial.ExecutionSnapshot.ID && first.Settings.ReasoningEffort.Value == "high")
	check("three_layers", len(first.Layers) == 3)
	check("project_user_system_order", len(first.Layers) == 3 && first.Layers[0].Kind == "project" && first.Layers[1].Kind == "user" && first.Layers[2].Kind == "system")
	write(projectPath, "# formatting only\nmodel_reasoning_effort = 'high'\n")
	if err := os.Chmod(projectPath, 0640); err != nil {
		t.Fatal(err)
	}
	format := export("format")
	check("format_settings_still_match", format.SettingsDifference != nil && format.SettingsDifference.Status == "unchanged")
	fileCheck, fileErr := a.ReadExecution(sid)
	check("project_file_bytes_and_mode_guarded", fileErr == nil && fileCheck.Status == "drifted")
	check("native_same_but_file_changed", first.ConfigurationSHA256 == format.ConfigurationSHA256 && first.LayersSHA256 == format.LayersSHA256 && first.Layers[0].File.SHA256 != format.Layers[0].File.SHA256 && format.Layers[0].File.Mode == 0640)
	write(projectPath, "model_reasoning_effort=\"medium\"\n")
	changed := export("changed")
	check("effort_delta_from_saved", changed.SettingsDifference != nil && changed.SettingsDifference.Status == "changed" && changed.SettingsDifference.Before.Value == "high" && changed.SettingsDifference.After.Value == "medium" && changed.SettingsDifference.EffortChanged && changed.SettingsDifference.EffortSourcesChanged && !changed.SettingsDifference.OtherValuesChanged && !changed.SettingsDifference.OtherSourcesChanged)
	check("parsed_change_visible", changed.ConfigurationSHA256 != format.ConfigurationSHA256 && changed.Layers[0].Version != format.Layers[0].Version)
	write(projectPath, "model_reasoning_effort=\"high\"\n")
	write(configPath, strings.Replace(user, "model_reasoning_effort=\"low\"", "model_reasoning_effort=\"medium\"", 1))
	hidden := export("hidden-effort")
	check("hidden_effort_change_not_effective_change", hidden.SettingsDifference != nil && hidden.SettingsDifference.Status == "changed" && !hidden.SettingsDifference.EffortChanged && hidden.SettingsDifference.EffortSourcesChanged && !hidden.SettingsDifference.OtherValuesChanged && !hidden.SettingsDifference.OtherSourcesChanged)
	write(configPath, "PRIVATE_CUSTOM_SETTING=\"PRIVATE_CONFIG_BODY\"\n"+user)
	custom := export("custom-source")
	check("custom_source_change_outside_merged_projection", custom.SettingsDifference != nil && custom.SettingsDifference.Status == "changed" && !custom.SettingsDifference.EffortChanged && !custom.SettingsDifference.OtherValuesChanged && custom.SettingsDifference.OtherSourcesChanged)
	encoded, _ := json.Marshal(custom)
	check("other_setting_body_and_key_private", !strings.Contains(string(encoded), "PRIVATE_"))
	write(configPath, "personality=\"friendly\"\n"+user)
	other := export("other-settings")
	check("other_setting_change_distinct", other.SettingsDifference != nil && other.SettingsDifference.Status == "changed" && !other.SettingsDifference.EffortChanged && other.SettingsDifference.OtherValuesChanged && other.SettingsDifference.OtherSourcesChanged)
	write(configPath, strings.Replace(user, "model_reasoning_effort=\"low\"\n", "", 1))
	write(projectPath, "# no effort override\n")
	unset := export("unset")
	check("unset_is_not_inferred_model_default", unset.SettingsDifference != nil && unset.SettingsDifference.After.State == "unset" && unset.SettingsDifference.After.Value == "" && unset.SettingsDifference.EffortChanged)
	write(projectPath, "model_reasoning_effort=\"medium\"\n")
	write(configPath, strings.Replace(user, "trust_level=\"trusted\"", "trust_level=\"untrusted\"", 1))
	untrusted := export("untrusted")
	check("disabled_layer_preserved", untrusted.Layers[0].Kind == "project" && untrusted.Layers[0].Disabled)
	for _, origin := range untrusted.Origins {
		if origin.Field == "model_reasoning_effort" {
			check("untrusted_origin_is_user", origin.LayerID == untrusted.Layers[1].ID)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	noInput, onlyReads, tagged := true, true, 0
	for _, process := range f.Records {
		for _, call := range process.Calls {
			noInput = noInput && call["method"] != "turn/start"
			if label, _ := call["trace_context"].(string); strings.HasPrefix(label, "catalog-") {
				tagged++
				onlyReads = onlyReads && (call["method"] == "config/read" || call["method"] == "configRequirements/read")
			}
		}
	}
	check("no_turn_start", noInput)
	check("catalog_only_native_reads", onlyReads && tagged == 32)
}
