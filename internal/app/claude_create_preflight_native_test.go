//go:build linux && nativeclaude

package app_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativeCreateRejectsInvalidSettingsBeforeStartup(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in fixed native Claude CLI")
	}
	repository, _ := filepath.Abs("../..")
	root, err := os.MkdirTemp("", "netcap-claude-create-preflight-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	for key, name := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_CREATE_PREFLIGHT_KEY", "fixture-no-model")
	for _, mode := range []string{"invalid-custom", "invalid-project", "invalid-user", "valid-custom", "valid-default"} {
		t.Run(mode, func(t *testing.T) {
			caseRoot := filepath.Join(root, mode)
			cwd, profile := filepath.Join(caseRoot, "work"), filepath.Join(caseRoot, "profile")
			write := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(caseRoot, "startup.txt")
			script := filepath.Join(caseRoot, "hook.py")
			write(script, "import sys\nfrom pathlib import Path\nwith Path("+fmt.Sprintf("%q", marker)+").open('a') as f:f.write(sys.argv[1]+'\\n')\n")
			hook := func(source string) map[string]any {
				return map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "python3 " + script + " " + source}}}}}
			}
			user := map[string]any{"hooks": hook("user"), "env": map[string]string{"CLAUDE_CODE_SIMPLE": "0", "NETCAP_PRIVATE_SETTING": "PRIVATE_CREATE_PREFLIGHT_VALUE"}}
			project := map[string]any{"hooks": hook("project")}
			custom := map[string]any{"permissions": map[string]any{"ask": []string{"Bash"}}}
			switch mode {
			case "invalid-user":
				user["permissions"] = map[string]any{"allow": 42}
			case "invalid-project":
				project["permissions"] = map[string]any{"allow": 42}
			case "invalid-custom":
				custom["permissions"] = map[string]any{"allow": 42}
			}
			put := func(path string, value any) { body, _ := json.Marshal(value); write(path, string(body)) }
			put(filepath.Join(profile, "settings.json"), user)
			put(filepath.Join(cwd, ".claude/settings.json"), project)
			customPath := ""
			if mode != "valid-default" {
				customPath = filepath.Join(caseRoot, "custom.json")
				put(customPath, custom)
			}
			cfg := app.Config{Version: 1, StateDir: filepath.Join(caseRoot, "application"),
				Projects: []app.Project{{ID: "project", Name: "Create preflight", Cwd: cwd}},
				Instances: []runtime.Instance{{ID: "claude", Kind: "claude", Executable: cli, Profile: profile, SettingsFile: customPath,
					Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}},
				Routes: []runtime.Route{{ID: "route", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:NETCAP_CREATE_PREFLIGHT_KEY"}}}
			store, err := app.OpenStore(cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			a, err := app.Open(cfg, store, runtime.NewClaude(cfg.StateDir))
			if err != nil {
				t.Fatal(err)
			}
			checks, results := map[string]bool{}, map[string]any{}
			t.Cleanup(func() {
				_ = a.Close()
				data, _ := json.MarshalIndent(map[string]any{"checks": checks, "results": results, "passed": !t.Failed(), "scope": "real Application/Runtime/SDK create; local settings and owned startup hooks; no model input"}, "", "  ")
				_ = os.WriteFile(filepath.Join(caseRoot, "result.json"), data, 0600)
			})
			check := func(name string, value bool) {
				t.Helper()
				checks[name] = value
				if !value {
					t.Errorf("failed: %s", name)
				}
			}
			receipt, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
			if err != nil {
				t.Fatal(err)
			}
			var status string
			for deadline := time.Now().Add(40 * time.Second); ; time.Sleep(20 * time.Millisecond) {
				for _, op := range a.Snapshot().Operations {
					if op.ID == receipt.OperationID {
						status = op.Status
					}
				}
				if status == "succeeded" || status == "not_sent" || status == "failed" || status == "uncertain" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("create did not reach an observable terminal state")
				}
			}
			view, _ := a.ReadSession(receipt.SessionID, "", 100)
			results["session"], results["status"] = view, status
			body, _ := os.ReadFile(marker)
			results["startup"] = string(body)
			invalid := strings.HasPrefix(mode, "invalid-")
			transcripts, _ := filepath.Glob(filepath.Join(profile, "projects", "*", "*.jsonl"))
			if invalid {
				check("invalid_schema_rejected_before_creation", status == "not_sent" && view.Session.Scope.NativeID == "")
				check("no_startup_hook_ran", len(body) == 0)
				check("invalid_settings_create_no_native_history", len(transcripts) == 0)
			} else {
				check("valid_settings_create_original_session", status == "succeeded" && view.Session.Scope.NativeID != "")
				check("valid_hooks_run_once", strings.Count(string(body), "user\n") == 1 && strings.Count(string(body), "project\n") == 1)
				check("no_metadata_session_history_created", len(transcripts) == 0 || len(transcripts) == 1 && filepath.Base(transcripts[0]) == view.Session.Scope.NativeID+".jsonl")
			}
			check("no_model_messages", len(view.Session.View.Messages) == 0)
			token := strings.Repeat("c", 64)
			h := console.Handler(a, "127.0.0.1:9573", token, os.DirFS("."))
			req := httptest.NewRequest("GET", "http://127.0.0.1:9573/api/sessions/"+receipt.SessionID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			check("session_result_visible_without_setting_values", response.Code == 200 && !strings.Contains(response.Body.String(), "PRIVATE_CREATE_PREFLIGHT_VALUE"))
			results["http"] = json.RawMessage(response.Body.Bytes())
		})
	}
}
