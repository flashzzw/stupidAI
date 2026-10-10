//go:build linux && nativeclaude

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativePluginSettingsPreview(t *testing.T) {
	testClaudeNativePluginSettings(t, false)
}

func TestClaudeNativeInstalledPluginSettingsCandidate(t *testing.T) {
	testClaudeNativePluginSettings(t, true)
}

func testClaudeNativePluginSettings(t *testing.T, stageCandidate bool) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in fixed native Claude CLI")
	}
	repository, _ := filepath.Abs("../..")
	root, err := os.MkdirTemp(os.Getenv("NETCAP_SETTINGS_PREVIEW_ROOT"), "netcap-settings-preview-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		var body []byte
		if s, ok := value.(string); ok {
			body = []byte(s)
		} else {
			body, err = json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	policyFile, nativeCalls := "", ""
	if base := os.Getenv("NETCAP_CLAUDE_POLICY_TEST_ROOT"); base != "" {
		owned, ownErr := os.Stat(filepath.Join(base, "policy"))
		mounted, mountErr := os.Stat("/etc/claude-code")
		namespace, nsErr := os.Readlink("/proc/self/ns/mnt")
		parent := os.Getenv("NETCAP_POLICY_PARENT_NAMESPACE")
		if ownErr != nil || mountErr != nil || !os.SameFile(owned, mounted) || nsErr != nil || parent == "" || namespace == parent {
			t.Fatal("policy scenarios require the probe's private mount namespace")
		}
		policyFile = filepath.Join(base, "policy/managed-settings.json")
		originalPolicy, err := os.ReadFile(policyFile)
		if err != nil || strings.TrimSpace(string(originalPolicy)) != "{}" {
			t.Fatal("policy scenarios must start with an owned empty policy")
		}
		defer write(policyFile, string(originalPolicy))
		nativeCalls = filepath.Join(root, "native-calls.jsonl")
		wrapper := filepath.Join(root, "native-cli")
		write(wrapper, fmt.Sprintf("#!/usr/bin/python3\nimport os,sys,json\nwith open(%q,'a') as f:f.write(json.dumps({'argv':sys.argv[1:]})+'\\n')\nos.execve(%q,[%q,*sys.argv[1:]],os.environ)\n", nativeCalls, cli, cli))
		if err := os.Chmod(wrapper, 0700); err != nil {
			t.Fatal(err)
		}
		cli = wrapper
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
	t.Setenv("NETCAP_PREVIEW_FIXTURE_KEY", "synthetic-local-key")
	// Capture unmodified metadata wire replies so preservation can be audited
	// from native settings/rules rather than only the validator's booleans.
	node := filepath.Join(root, "node-with-settings-wire")
	write(node, fmt.Sprintf("#!/usr/bin/python3\nimport os,sys\nos.execvp('node',['node','--import',%q,*sys.argv[1:]])\n", filepath.Join(repository, "tests/claude_settings_wire_capture.mjs")))
	if err := os.Chmod(node, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NETCAP_SETTINGS_WIRE", filepath.Join(root, "native-settings-wire.jsonl"))
	profile, cwd, custom := filepath.Join(root, "profile"), filepath.Join(root, "work"), filepath.Join(root, "custom/original.json")
	for _, dir := range []string{profile, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec.Command("git", "init", "--quiet", cwd).Run(); err != nil {
		t.Fatal(err)
	}
	market := filepath.Join(root, "market")
	write(filepath.Join(market, ".claude-plugin/marketplace.json"), map[string]any{"name": "preview-local", "owner": map[string]string{"name": "Fixture"}, "plugins": []any{map[string]string{"name": "selected", "source": "./selected"}}})
	manifest := filepath.Join(market, "selected/.claude-plugin/plugin.json")
	write(manifest, map[string]string{"name": "selected", "version": "1.0.0", "description": "PRIVATE_PREVIEW_DECLARATION"})
	marker := filepath.Join(root, "hook-marker")
	hooks := func(label string) map[string]any {
		return map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "printf '" + label + "\\n' >> " + marker, "timeout": 5}}}}}
	}
	write(filepath.Join(market, "selected/hooks/hooks.json"), map[string]any{"hooks": hooks("plugin")})
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR=" + profile, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1"}
	for i, args := range [][]string{{"--version"}, {"plugin", "marketplace", "add", market}, {"plugin", "install", "selected@preview-local", "--scope", "user", "--json"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Dir = cwd
		cmd.Env = env
		body, err := cmd.CombinedOutput()
		cancel()
		write(filepath.Join(root, []string{"version.txt", "market-install.txt", "plugin-install.txt"}[i]), string(body))
		if err != nil {
			t.Fatalf("owned install failed: %v", err)
		}
		if i == 0 && strings.TrimSpace(string(body)) != "2.1.280 (Claude Code)" {
			t.Fatal("fixed CLI required")
		}
	}
	write(filepath.Join(cwd, ".claude/settings.json"), map[string]any{"hooks": hooks("project")})
	settings := map[string]any{"env": map[string]string{"NETCAP_PRIVATE_PREVIEW": "PRIVATE_PREVIEW_VALUE", "CLAUDE_CODE_SIMPLE": "0"}, "permissions": map[string]any{"deny": []string{"Read(/anchored/**)", "Edit(/anchored/**)"}}, "hooks": hooks("custom")}
	write(custom, settings)
	original, _ := os.ReadFile(custom)
	var requests atomic.Int64
	var unexpectedRequests atomic.Int64
	var phase atomic.Value
	phase.Store("create")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if r.Method != "HEAD" || r.URL.Path != "/api/hello" || len(body) != 0 {
			unexpectedRequests.Add(1)
		}
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		var fields []string
		for key := range parsed {
			fields = append(fields, key)
		}
		messages, _ := parsed["messages"].([]any)
		row, _ := json.Marshal(map[string]any{"phase": phase.Load(), "method": r.Method, "path": r.URL.Path, "bytes": len(body), "fields": fields, "messages": len(messages)})
		f, err := os.OpenFile(filepath.Join(root, "http-requests.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err == nil {
			_, _ = f.Write(append(row, '\n'))
			_ = f.Close()
		}
		http.Error(w, "unexpected metadata model request", 503)
	}))
	defer provider.Close()
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []app.Project{{ID: "project", Name: "Plugin settings preview", Cwd: cwd}},
		Instances: []runtime.Instance{{ID: "claude", Kind: "claude", Executable: cli, NodeExecutable: node, Profile: profile, SettingsFile: custom, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING", "NETCAP_SETTINGS_WIRE"}}},
		Routes:    []runtime.Route{{ID: "claude", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: provider.URL, CredentialRef: "env:NETCAP_PREVIEW_FIXTURE_KEY"}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	adapter := runtime.NewClaude(cfg.StateDir)
	a, err := app.Open(cfg, store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	checks, results, responses := map[string]bool{}, map[string]any{}, map[string]json.RawMessage{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		check("application_closed", a.Close() == nil)
		write(filepath.Join(root, "result.json"), map[string]any{"root": root, "checks": checks, "results": results, "responses": responses, "passed": !t.Failed(), "scope": "real fixed CLI and Application/Handler; isolated local installed plugin; bare settings flags only, no installed execution admission"})
	})
	receipt, err := a.Apply(app.Command{ID: "create", Kind: "create", Title: "Plugin settings preview", ProjectID: "project", RouteID: "claude", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	find := func() app.Session {
		for _, s := range a.Snapshot().Sessions {
			if s.ID == receipt.SessionID {
				return s
			}
		}
		return app.Session{}
	}
	for end := time.Now().Add(30 * time.Second); find().Status != "idle"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("original query did not become idle")
		}
	}
	if _, err := a.Apply(app.Command{ID: "takeover", Kind: "takeover", SessionID: receipt.SessionID}); err != nil {
		t.Fatal(err)
	}
	if !stageCandidate {
		if err := adapter.Close(); err != nil {
			t.Fatal(err)
		}
	}
	before := find()
	requestsBefore := requests.Load()
	phase.Store("preview")
	opsBefore := len(a.Snapshot().Operations)
	journal := filepath.Join(cfg.StateDir, "runtimes", "claude", before.Scope.NativeID+".jsonl")
	journalBefore, _ := os.ReadFile(journal)
	markerBefore, _ := os.ReadFile(marker)
	check("original_query_ran_three_startup_hooks", strings.Count(string(markerBefore), "\n") == 3)
	token := strings.Repeat("b", 64)
	handler := console.Handler(a, "127.0.0.1:9463", token, os.DirFS("."))
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://127.0.0.1:9463"+path, strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	base := "/api/sessions/" + receipt.SessionID
	readCatalog := func() app.InstalledPluginCatalog {
		rec := call("POST", base+"/plugins/installed", map[string]any{})
		if rec.Code != 200 {
			t.Fatalf("catalog %d: %s", rec.Code, rec.Body.String())
		}
		var c app.InstalledPluginCatalog
		if json.Unmarshal(rec.Body.Bytes(), &c) != nil {
			t.Fatal("catalog invalid")
		}
		return c
	}
	catalog := readCatalog()
	results["catalog"] = catalog
	check("four_actual_metadata_file_versions", len(catalog.SourceFiles) == 4 && len(catalog.SourceVersion) == 64)
	check("cache_and_declared_sources_distinct", len(catalog.Entries) == 1 && catalog.Entries[0].CachePath != catalog.Entries[0].DeclaredPath)
	if stageCandidate {
		write(filepath.Join(root, "journal-before.jsonl"), string(journalBefore))
		a = exerciseNativeInstalledSettingsCandidate(t, a, cfg, root, catalog, check, results)
		check("candidate_no_additional_provider_request", requests.Load() == requestsBefore && unexpectedRequests.Load() == 0)
		check("candidate_no_additional_execution_operation", len(a.Snapshot().Operations) == opsBefore)
		journalAfter, _ := os.ReadFile(journal)
		markerAfter, _ := os.ReadFile(marker)
		// Restart deliberately closes the original query. Only that close may
		// follow its initial journal; staging/discarding must not open another.
		var tail struct {
			Kind string `json:"kind"`
		}
		closeErr := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(journalAfter), string(journalBefore)))), &tail)
		check("candidate_original_close_recorded", strings.HasPrefix(string(journalAfter), string(journalBefore)) && closeErr == nil && tail.Kind == "closed")
		check("candidate_no_new_native_query_or_hook", strings.Count(string(journalAfter), `"kind":"initialized"`) == 1 && string(markerAfter) == string(markerBefore))
		write(filepath.Join(root, "journal.jsonl"), string(journalAfter))
		write(filepath.Join(root, "hook-result.txt"), string(markerAfter))
		results["session_id"] = receipt.SessionID
		return
	}
	preview := func(label, mode string, version string) app.InstalledPluginSettingsPreview {
		rec := call("POST", base+"/plugins/settings-preview", app.PluginSettingsPreviewRequest{SourceKey: "selected@preview-local", Mode: mode, SourceVersion: version})
		if rec.Code != 200 {
			t.Fatalf("preview %s %d: %s", label, rec.Code, rec.Body.String())
		}
		var out app.InstalledPluginSettingsPreview
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		results[label] = out
		return out
	}
	for _, mode := range []string{"disabled", "enabled"} {
		out := preview(mode, mode, catalog.SourceVersion)
		encoded, _ := json.Marshal(out.Validation)
		var observed map[string]any
		_ = json.Unmarshal(encoded, &observed)
		preserved, _ := observed["preservation"].(map[string]any)
		check(mode+"_native_other_settings_and_permissions_preserved", preserved != nil && preserved["matches"] == true)
		check(mode+"_native_baseline_is_original_file", preserved != nil && preserved["baseline_settings_file"] == custom)
		check(mode+"_native_flag_matches", out.Validation != nil && out.Validation.SchemaValid && len(out.Validation.PluginSettings) == 1 && out.Validation.PluginSettings[0].Matches)
		check(mode+"_native_child_holds_file", out.Validation != nil && out.Validation.FileHolderObserved && out.Validation.ProcessStopped)
		check(mode+"_sources_stable_not_full_admission", out.SourcesStable && !out.Complete && len(out.Problems) == 0)
		check(mode+"_original_session_binding", out.NativeID == before.Scope.NativeID && out.SnapshotID == before.ExecutionSnapshot.ID && out.Cwd == cwd)
		check(mode+"_same_directory_private_copy", out.Artifact != nil && filepath.Dir(out.Artifact.File.Path) == filepath.Dir(custom) && out.Artifact.File.Mode == 0600 && out.Artifact.Original.Path == custom)
		check(mode+"_file_released", out.Cleanup.State == "released" && len(out.Cleanup.Issues) == 0)
		if out.Artifact != nil {
			_, err := os.Lstat(out.Artifact.File.Path)
			check(mode+"_file_actually_absent", os.IsNotExist(err))
		}
	}
	afterBytes, _ := os.ReadFile(custom)
	check("original_settings_bytes_unchanged", string(afterBytes) == string(original))
	manifestBefore, _ := os.ReadFile(manifest)
	write(manifest, string(manifestBefore)+"\n")
	stale := call("POST", base+"/plugins/settings-preview", app.PluginSettingsPreviewRequest{SourceKey: "selected@preview-local", Mode: "disabled", SourceVersion: catalog.SourceVersion})
	check("same_version_whitespace_drift_rejected", stale.Code == 409)
	results["stale_error"] = json.RawMessage(stale.Body.Bytes())
	fresh := readCatalog()
	check("raw_metadata_drift_changes_source_version", fresh.SourceVersion != catalog.SourceVersion && reflect.DeepEqual(fresh.Entries, catalog.Entries))
	out := preview("refreshed", "disabled", fresh.SourceVersion)
	check("refreshed_source_preview_matches", out.Validation != nil && out.Validation.SchemaValid && out.SourcesStable && len(out.Problems) == 0)
	settings["permissions"] = map[string]any{"allow": "invalid-format"}
	write(custom, settings)
	invalid := preview("invalid", "disabled", fresh.SourceVersion)
	check("invalid_settings_not_success", len(invalid.Problems) > 0 || invalid.Validation != nil && !invalid.Validation.SchemaValid)
	check("invalid_settings_copy_not_retained", invalid.Cleanup.State != "retained" && len(invalid.Cleanup.Issues) == 0)
	write(custom, string(original))
	write(manifest, string(manifestBefore))
	noCustom := before.Scope
	noCustom.Instance.SettingsFile = ""
	bare := runtime.PreviewClaudePluginSettings(context.Background(), cfg.StateDir, noCustom, runtime.ClaudePluginSetting{SourceKey: "selected@preview-local", Enabled: false})
	results["no_custom"] = bare
	check("without_custom_copy_uses_cwd", bare.Artifact != nil && bare.Artifact.Original.Kind == "absent" && filepath.Dir(bare.Artifact.File.Path) == cwd && bare.Cleanup.State == "released")
	check("without_custom_native_flag_matches", bare.Validation != nil && bare.Validation.SchemaValid && bare.Validation.PluginSettings[0].Matches && bare.SourcesStable)
	link := filepath.Join(root, "custom-alias")
	if err := os.Symlink(filepath.Dir(custom), link); err != nil {
		t.Fatal(err)
	}
	aliasScope := before.Scope
	aliasScope.Instance.SettingsFile = filepath.Join(link, "original.json")
	alias := runtime.PreviewClaudePluginSettings(context.Background(), cfg.StateDir, aliasScope, runtime.ClaudePluginSetting{SourceKey: "selected@preview-local", Enabled: false})
	results["parent_alias"] = alias
	check("parent_alias_identity_and_cleanup", alias.Artifact != nil && filepath.Dir(alias.Artifact.File.Path) == link && filepath.Dir(alias.Artifact.File.ResolvedPath) == filepath.Dir(custom) && alias.Cleanup.State == "released")
	check("parent_alias_native_flag_matches", alias.Validation != nil && alias.Validation.SchemaValid && alias.Validation.PluginSettings[0].Matches && alias.SourcesStable)
	if policyFile != "" {
		helperMarker, helper := filepath.Join(root, "policy-helper-marker"), filepath.Join(root, "policy-helper.py")
		write(helper, fmt.Sprintf("#!/usr/bin/python3\nfrom pathlib import Path\nimport json\nPath(%q).write_text('unexpected policy execution')\nprint(json.dumps({'managedSettings':{'permissions':{'deny':['Read(/owned-helper-policy/**)']}}}))\n", helperMarker))
		if err := os.Chmod(helper, 0700); err != nil {
			t.Fatal(err)
		}
		callsBefore, err := os.ReadFile(nativeCalls)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"singular", "platform-command", "platform-static", "platform-other", "bindings-route", "bindings-state", "bindings-helper", "bindings-invalid-env"} {
			declaration := map[string]any{"path": helper, "timeoutMs": 3000}
			policy := map[string]any{"policyHelper": declaration}
			switch mode {
			case "platform-command":
				policy = map[string]any{"policyHelpers": map[string]any{"linux": declaration}}
			case "platform-static":
				policy = map[string]any{"policyHelpers": map[string]any{"default": map[string]any{"permissions": map[string]any{"deny": []string{"Read(/owned-static-policy/**)"}}}}}
			case "platform-other":
				policy = map[string]any{"policyHelpers": map[string]any{"windows": declaration}}
			case "bindings-route":
				policy = map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}}
			case "bindings-state":
				policy = map[string]any{"env": map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(root, "other-profile")}}
			case "bindings-helper":
				policy = map[string]any{"apiKeyHelper": "/usr/bin/python3 " + helper}
			case "bindings-invalid-env":
				policy = map[string]any{"env": map[string]any{"OWNED_INVALID": 42}}
			}
			write(policyFile, policy)
			rec := call("POST", base+"/plugins/startup-settings", map[string]any{})
			var read runtime.ClaudeStartupSettings
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &read) != nil {
				t.Fatal("owned policy source response invalid", rec.Code, rec.Body.String())
			}
			results["policy_read_"+mode] = read
			check(mode+"_policy_source_read_only", read.ReadComplete && !read.Complete && read.NativeID == before.Scope.NativeID && read.SnapshotID == before.ExecutionSnapshot.ID)
			platform := strings.HasPrefix(mode, "platform-")
			check(mode+"_helper_flags_preserved", read.PolicyHelperPresent == (mode == "singular") && read.PolicyHelpersPresent == platform)
			found := false
			for _, layer := range read.Sources {
				found = found || layer.Source == "managed" && layer.PolicyOrigin == "file" && layer.HasPolicyHelper == (mode == "singular") && layer.HasPolicyHelpers == platform
			}
			if strings.HasPrefix(mode, "bindings-") {
				check(mode+"_binding_conflict_visible", len(read.StartupConflicts) > 0)
			} else {
				check(mode+"_helper_source_preserved", found)
			}
			out := preview("policy_preview_"+mode, "disabled", catalog.SourceVersion)
			check(mode+"_preview_rejected_before_candidate", len(out.Problems) > 0 && out.Validation == nil && out.Artifact == nil && out.Cleanup.State == "not_created" && !out.Complete)
			callsAfter, err := os.ReadFile(nativeCalls)
			check(mode+"_no_native_launch", err == nil && string(callsAfter) == string(callsBefore))
			_, err = os.Stat(helperMarker)
			check(mode+"_no_policy_helper_execution", os.IsNotExist(err))
		}
		managedPermissions := map[string]any{"allowManagedPermissionRulesOnly": true, "permissions": map[string]any{"allow": []string{"Read(/owned-managed/**)"}, "deny": []string{"Edit(/owned-managed/**)"}}}
		write(policyFile, managedPermissions)
		write(filepath.Join(root, "managed-permission-policy.json"), managedPermissions)
		managed := preview("managed_permissions", "disabled", catalog.SourceVersion)
		check("managed_permission_settings_preserved", managed.Validation != nil && managed.Validation.Preservation != nil && managed.Validation.Preservation.Matches && managed.SourcesStable && len(managed.Problems) == 0)
		write(policyFile, "{}")
		out := preview("policy_restored", "disabled", catalog.SourceVersion)
		check("restored_policy_preview_available", out.Validation != nil && out.Validation.SchemaValid && out.Validation.PluginSettings[0].Matches && out.SourcesStable && out.Cleanup.State == "released")
	}
	after := find()
	journalAfter, _ := os.ReadFile(journal)
	markerAfter, _ := os.ReadFile(marker)
	check("no_snapshot_or_control_change", reflect.DeepEqual(before.Scope, after.Scope) && reflect.DeepEqual(before.ExecutionSnapshot, after.ExecutionSnapshot) && before.Generation == after.Generation && before.ControlMode == after.ControlMode && len(a.Snapshot().Operations) == opsBefore)
	check("no_query_restart_or_hook_execution", string(journalBefore) == string(journalAfter) && string(markerBefore) == string(markerAfter))
	check("no_model_request", unexpectedRequests.Load() == 0 && !strings.Contains(string(journalAfter), `"kind":"input"`))
	check("preview_has_no_http_request", requests.Load() == requestsBefore)
	results["http_counts"] = map[string]int64{"before_preview": requestsBefore, "after_preview": requests.Load(), "unexpected": unexpectedRequests.Load()}
	encoded, _ := json.Marshal(results)
	check("no_private_settings_or_manifest_values", !strings.Contains(string(encoded), "PRIVATE_PREVIEW_VALUE") && !strings.Contains(string(encoded), "PRIVATE_PREVIEW_DECLARATION"))
	for _, path := range []string{"/api/workspace", "/api/snapshot", base} {
		rec := call("GET", path, nil)
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		responses[path] = append([]byte(nil), rec.Body.Bytes()...)
	}
	results["session_id"] = receipt.SessionID
	write(filepath.Join(root, "journal.jsonl"), string(journalAfter))
	write(filepath.Join(root, "hook-result.txt"), string(markerAfter))
}
