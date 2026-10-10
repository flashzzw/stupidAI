//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeInheritedHookSourceDrift(t *testing.T) {
	codexNativeHookSourceDrift(t, "user")
}

func TestCodexNativeProjectHookSourceDrift(t *testing.T) {
	codexNativeHookSourceDrift(t, "project")
}

func TestCodexNativeWorktreeHookSourceDrift(t *testing.T) {
	codexNativeHookSourceDrift(t, "worktree")
}
func TestCodexNativeNewProjectHookSourceDrift(t *testing.T) {
	codexNativeHookSourceDrift(t, "new-folder")
}

func codexNativeHookSourceDrift(t *testing.T, mode string) {
	root, err := os.MkdirTemp("", "netcap-w0209-codex-hook-sources-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	samples := map[string]any{}
	checks := map[string]bool{}
	responses := map[string]any{}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		inputCount := 0
		for _, process := range f.Records {
			for _, call := range process.Calls {
				if call["method"] == "turn/start" {
					inputCount++
				}
			}
		}
		check("only_original_seed_input", inputCount == 1)
		body, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "case": mode, "scope": "owned Application/Runtime; one explicitly interrupted unavailable-loopback-provider input establishes history; no successful model, tool or hook execution"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
		body, _ = json.MarshalIndent(map[string]any{"responses": responses, "samples": samples}, "", "  ")
		write(filepath.Join(root, "browser-responses.json"), string(body))
	})
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	write(configPath, strings.Replace(string(config), "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1))
	cwd := filepath.Join(root, "work")
	hookPath := filepath.Join(f.Instance.Profile, "hooks.json")
	if mode == "worktree" {
		git := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Owned fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Owned fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("owned git fixture: %s: %v", output, err)
			}
		}
		main := filepath.Join(root, "main-repo")
		git("init", "--quiet", main)
		git("-C", main, "commit", "--quiet", "--allow-empty", "-m", "Owned fixture")
		git("-C", main, "worktree", "add", "--quiet", "--detach", filepath.Join(root, "linked"))
		cwd = filepath.Join(root, "linked", "child")
		write(filepath.Join(root, "linked/.codex/hooks.json"), `{"hooks":{}}`)
		write(filepath.Join(cwd, ".codex/hooks.json"), `{"hooks":{}}`)
		hookPath = filepath.Join(main, ".codex/hooks.json")
	}
	if mode != "user" {
		if mode != "worktree" {
			hookPath = filepath.Join(cwd, ".codex/hooks.json")
		}
		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		trustRoot := cwd
		if mode == "worktree" {
			trustRoot = filepath.Join(root, "linked")
		}
		pathJSON, _ := json.Marshal(trustRoot)
		trust := "\n[projects." + string(pathJSON) + "]\ntrust_level=\"trusted\"\n"
		if mode == "worktree" {
			mainJSON, _ := json.Marshal(filepath.Join(root, "main-repo"))
			trust += "\n[projects." + string(mainJSON) + "]\ntrust_level=\"trusted\"\n"
		}
		write(configPath, string(content)+trust)
	}
	v1 := `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"echo OWNED_V1","timeout":1}]}]}}`
	if mode != "new-folder" {
		write(hookPath, v1)
	}
	source := filepath.Join(root, "source")
	write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"owned","version":"1.0.0"}`)
	write(filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"true","timeout":1}]}]}}`)
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}, CapabilityAssets: []app.CapabilityAsset{{ID: "hooks", Name: "Owned hooks", Kind: "cli_plugin", Version: "v1", Source: source, Backends: []string{"codex"}}}}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	assets, err := a.ReadCapabilities(context.Background())
	if err != nil || len(assets) != 1 {
		t.Fatal(err)
	}
	inspection, err := a.InspectCapabilityHooks(context.Background(), app.CapabilityHookInspectionRequest{AssetID: "hooks", InstanceID: f.Instance.ID, Revision: assets[0].Revision, SourceSHA256: assets[0].Source.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", CapabilitySelections: []app.CapabilitySelection{{AssetID: "hooks", Mode: "enabled", HookReview: &app.CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "untrusted"}}}})
	if err != nil {
		t.Fatal(err)
	}
	find := func(s app.Snapshot) app.Session {
		for _, session := range s.Sessions {
			if session.ID == created.SessionID {
				return session
			}
		}
		t.Fatal("created session missing")
		return app.Session{}
	}
	initial := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
		v := find(s)
		return v.Status == "idle" && len(v.View.PluginEvidence) == 1 && v.View.PluginEvidence[0].Verified
	}))
	seed, err := a.Apply(app.Command{ID: "seed-history", Kind: "send", SessionID: initial.ID, Text: "OWNED source-version history; provider intentionally unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		checkpoint, err := f.Checkpoint()
		if err != nil {
			return false
		}
		var rows []struct {
			Events []struct {
				Method string `json:"method"`
				Params struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
				} `json:"params"`
			} `json:"events"`
		}
		if json.Unmarshal(checkpoint.Trace, &rows) != nil {
			return false
		}
		for _, row := range rows {
			for _, event := range row.Events {
				if event.Method == "error" && event.Params.ThreadID == initial.Scope.NativeID {
					for _, op := range s.Operations {
						if op.ID == seed.OperationID && op.Receipt.NativeTurn == event.Params.TurnID {
							return true
						}
					}
				}
			}
		}
		return false
	})
	if _, err := a.Apply(app.Command{ID: "cancel-seed", Kind: "interrupt", SessionID: initial.ID}); err != nil {
		t.Fatal(err)
	}
	initial = find(waitNativeApplication(t, a, func(s app.Snapshot) bool { v := find(s); return v.Status == "idle" && v.View.ExecutionClear }))
	var historyBefore struct {
		Thread struct {
			Turns json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if err := f.Call(context.Background(), "thread/read", map[string]any{"threadId": initial.Scope.NativeID, "includeTurns": true}, &historyBefore); err != nil {
		t.Fatal(err)
	}
	samples["history_before"] = historyBefore
	check("seed_explicitly_interrupted", strings.Contains(string(historyBefore.Thread.Turns), `"interrupted"`))
	samples["initial"] = initial
	export := func(phase string) {
		t.Helper()
		handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
		rows := map[string]json.RawMessage{}
		for _, path := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + initial.ID, "/api/sessions/" + initial.ID + "/execution"} {
			r := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("export %s: %d", path, w.Code)
			}
			rows[path] = append(json.RawMessage(nil), w.Body.Bytes()...)
		}
		responses[phase] = rows
	}
	export("initial")
	before, err := a.ReadSessionHookCatalog(context.Background(), initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	samples["catalog_before"] = before
	check("original_catalog_source", mode == "new-folder" && len(before.Items) == 0 || mode != "new-folder" && len(before.Items) == 1 && before.Items[0].SourcePath == hookPath)
	if mode != "user" {
		saved := initial.ExecutionSnapshot.CodexProjectHooks
		check("project_sources_recorded", saved != nil && saved.Version == 1 && saved.Cwd == cwd && saved.Files != nil)
		if saved == nil {
			return
		}
		check("project_scope_matches_snapshot", reflect.DeepEqual(saved, initial.Scope.CodexProjectHooks))
		found := false
		declaredLocal := false
		for _, file := range saved.Files {
			if file.File.Path == hookPath {
				found = file.Discovered && file.File.Kind == "file" && file.Declared == (mode == "project")
			}
			if file.File.Path == filepath.Join(cwd, ".codex/hooks.json") {
				declaredLocal = file.Declared && !file.Discovered
			}
		}
		if mode == "new-folder" {
			check("initial_project_collection_empty", len(saved.Files) == 0)
		} else {
			check("actual_project_source_recorded", found)
		}
		if mode == "worktree" {
			check("worktree_local_declaration_distinct", declaredLocal)
		}
	}
	v2 := strings.Replace(v1, "OWNED_V1", "OWNED_V2", 1)
	write(hookPath, v2)
	first, second := sha256.Sum256([]byte(v1)), sha256.Sum256([]byte(v2))
	samples["source_hashes"] = []string{hex.EncodeToString(first[:]), hex.EncodeToString(second[:])}
	after, err := a.ReadSessionHookCatalog(context.Background(), initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	samples["catalog_after"] = after
	view, err := f.Runtime.Observe(context.Background(), initial.Scope)
	if err != nil {
		t.Fatal(err)
	}
	samples["after_view"] = view
	check("source_bytes_changed", first != second)
	check("source_drift_withdraws_input_readiness", !view.ExecutionClear)
	if view.ExecutionClear {
		return
	}
	check("identity_unchanged", view.NativeID == initial.Scope.NativeID)
	check("no_hook_execution", len(view.HookExecutions) == 0)
	check("snapshot_unchanged", find(a.Snapshot()).ExecutionSnapshot.ID == initial.ExecutionSnapshot.ID)
	read, err := a.ReadExecution(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	samples["drifted_execution"] = read
	check("offline_check_scope_explicit", read.Status == "drifted" || mode == "new-folder" && read.Status == "unchanged")
	_, err = a.Apply(app.Command{ID: "must-not-send", Kind: "send", SessionID: initial.ID, Text: "MUST NOT SEND"})
	if mode == "new-folder" && err == nil {
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.CommandID == "must-not-send" {
					return op.Status == "not_sent" && strings.Contains(op.Error, "项目 hook")
				}
			}
			return false
		})
		check("source_drift_rejects_input", true)
	} else {
		check("source_drift_rejects_input", err != nil)
	}
	drifted := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
		v := find(s)
		return v.Status == "unknown" && !v.View.ExecutionClear && strings.Contains(v.View.Evidence.Detail, "hook")
	}))
	samples["drifted_session"] = drifted
	check("application_withdraws_readiness", !drifted.View.Evidence.Complete)
	export("drifted")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	store, err = app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	read, err = a.ReadExecution(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	samples["cold_drifted_execution"] = read
	check("restart_keeps_recorded_source", (read.Status == "drifted" || mode == "new-folder" && read.Status == "unchanged") && read.Snapshot.ID == initial.ExecutionSnapshot.ID && *read.Snapshot.CodexUserHooks == *initial.ExecutionSnapshot.CodexUserHooks && reflect.DeepEqual(read.Snapshot.CodexProjectHooks, initial.ExecutionSnapshot.CodexProjectHooks))
	_, err = a.Apply(app.Command{ID: "must-not-resume", Kind: "resume", SessionID: initial.ID})
	if mode == "new-folder" && err == nil {
		// File-only Application preflight cannot see a newly introduced layer;
		// the Runtime must reject it before thread/resume.
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.CommandID == "must-not-resume" {
					return op.Status == "not_sent" && strings.Contains(op.Error, "项目 hook")
				}
			}
			return false
		})

		check("cold_drift_rejects_resume", true)
	} else {
		check("cold_drift_rejects_resume", err != nil)
	}
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := f.Call(context.Background(), "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal(err)
	}
	check("cold_drift_did_not_load_thread", len(loaded.Data) == 0)
	if mode == "new-folder" {
		if err := os.Remove(hookPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Dir(hookPath)); err != nil {
			t.Fatal(err)
		}
	} else {
		write(hookPath, v1)
	}
	read, err = a.ReadExecution(initial.ID)
	check("restored_file_matches_record", err == nil && read.Status == "unchanged")
	_, err = a.Apply(app.Command{ID: "resume-original", Kind: "resume", SessionID: initial.ID})
	if err != nil {
		t.Fatal(err)
	}
	restored := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
		v := find(s)
		return v.Status == "idle" && v.View.Evidence.At != initial.View.Evidence.At && v.View.ExecutionClear && len(v.View.PluginEvidence) == 1 && v.View.PluginEvidence[0].Verified
	}))
	samples["restored"] = restored
	check("restored_project_record_unchanged", reflect.DeepEqual(restored.ExecutionSnapshot.CodexProjectHooks, initial.ExecutionSnapshot.CodexProjectHooks))
	check("restored_original_identity", restored.Scope.NativeID == initial.Scope.NativeID && restored.ExecutionSnapshot.ID == initial.ExecutionSnapshot.ID)
	check("restored_no_hooks_executed", len(restored.View.HookExecutions) == 0)
	if err := f.Call(context.Background(), "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal(err)
	}
	check("restored_original_actually_loaded", len(loaded.Data) == 1 && loaded.Data[0] == initial.Scope.NativeID)
	var historyAfter struct {
		Thread struct {
			Turns json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if err := f.Call(context.Background(), "thread/read", map[string]any{"threadId": initial.Scope.NativeID, "includeTurns": true}, &historyAfter); err != nil {
		t.Fatal(err)
	}
	samples["history_after"] = historyAfter
	check("original_history_preserved", string(historyBefore.Thread.Turns) == string(historyAfter.Thread.Turns))
	export("restored")
}
