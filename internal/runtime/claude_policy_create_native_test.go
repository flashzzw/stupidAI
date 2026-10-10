//go:build linux && nativeclaude

package runtime_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestClaudeNativePolicyCreateBinding(t *testing.T) {
	base, cli := os.Getenv("NETCAP_CLAUDE_POLICY_TEST_ROOT"), os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if base == "" || cli == "" {
		t.Skip("use the owned policy namespace probe with --surface create")
	}
	policy := filepath.Join(base, "policy/managed-settings.json")
	owned, err := os.Stat(policy)
	mounted, mountErr := os.Stat("/etc/claude-code/managed-settings.json")
	namespace, nsErr := os.Readlink("/proc/self/ns/mnt")
	parent := os.Getenv("NETCAP_POLICY_PARENT_NAMESPACE")
	if err != nil || mountErr != nil || !os.SameFile(owned, mounted) || nsErr != nil || parent == "" || namespace == parent {
		t.Fatal("policy creation requires a private owned mount namespace")
	}
	repository, _ := filepath.Abs("../..")
	root, err := os.MkdirTemp(base, "netcap-policy-create-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(value, "", "  ")
		if text, ok := value.(string); ok {
			body, err = []byte(text), nil
		}
		if err != nil || os.WriteFile(path, body, 0600) != nil {
			t.Fatal("cannot write owned fixture")
		}
	}
	for key, name := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "xdg-state"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	t.Setenv("NETCAP_POLICY_CREATE_KEY", "fixture-local-policy-key")
	checks, reports := map[string]bool{}, map[string]any{}
	t.Cleanup(func() {
		write(policy, "{}")
		write(filepath.Join(root, "result.json"), map[string]any{"checks": checks, "reports": reports, "passed": !t.Failed(), "scope": "real Application/Handler create under owned policy; local providers, startup environment oracle, no model input"})
	})
	for _, mode := range []string{"route", "state", "helper", "invalid-env", "benign"} {
		t.Run(mode, func(t *testing.T) {
			check := func(name string, ok bool) {
				t.Helper()
				checks[mode+"_"+name] = ok
				if !ok {
					t.Error(name)
				}
			}
			caseDir := filepath.Join(root, mode)
			cwd, profile := filepath.Join(caseDir, "work"), filepath.Join(caseDir, "profile")
			otherProfile := filepath.Join(caseDir, "other-profile")
			for _, dir := range []string{cwd, profile, otherProfile} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := exec.Command("git", "init", "--quiet", cwd).Run(); err != nil {
				t.Fatal(err)
			}
			var modelCalls, originalCalls, otherCalls atomic.Int64
			server := func(counter *atomic.Int64) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					counter.Add(1)
					if r.Method != "HEAD" {
						modelCalls.Add(1)
					}
					w.WriteHeader(503)
				}))
			}
			original, other := server(&originalCalls), server(&otherCalls)
			defer original.Close()
			defer other.Close()
			marker, helperMarker := filepath.Join(caseDir, "startup.jsonl"), filepath.Join(caseDir, "helper-marker")
			hook, helper := filepath.Join(caseDir, "hook.py"), filepath.Join(caseDir, "helper.py")
			write(hook, fmt.Sprintf("import json,os\nwith open(%q,'a') as f:f.write(json.dumps({'profile_matches':os.getenv('CLAUDE_CONFIG_DIR')==%q,'route_matches':os.getenv('ANTHROPIC_BASE_URL')==%q})+'\\n')\n", marker, profile, original.URL))
			write(helper, fmt.Sprintf("from pathlib import Path\nPath(%q).write_text('unexpected helper invocation')\nprint('fixture-policy-key')\n", helperMarker))
			custom := filepath.Join(caseDir, "custom/original.json")
			write(custom, map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "python3 " + hook}}}}}})
			selected := map[string]any{"env": map[string]string{"OWNED_BENIGN": "PRIVATE_CREATE_BINDING_VALUE"}}
			switch mode {
			case "route":
				selected = map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": other.URL}}
			case "state":
				selected = map[string]any{"env": map[string]string{"CLAUDE_CONFIG_DIR": otherProfile}}
			case "helper":
				selected = map[string]any{"apiKeyHelper": "python3 " + helper}
			case "invalid-env":
				selected = map[string]any{"env": map[string]any{"OWNED_INVALID": 42}}
			}
			write(policy, selected)
			write(filepath.Join(caseDir, "policy-source.json"), selected)
			cfg := app.Config{Version: 1, StateDir: filepath.Join(caseDir, "application"), Projects: []app.Project{{ID: "project", Name: "Policy binding", Cwd: cwd}},
				Instances: []runtime.Instance{{ID: "claude", Kind: "claude", Executable: cli, Profile: profile, SettingsFile: custom, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}},
				Routes:    []runtime.Route{{ID: "route", InstanceID: "claude", Model: "claude-sonnet-4-6", Endpoint: original.URL, CredentialRef: "env:NETCAP_POLICY_CREATE_KEY"}}}
			store, err := app.OpenStore(cfg.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			a, err := app.Open(cfg, store, runtime.NewClaude(cfg.StateDir))
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			receipt, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
			if err != nil {
				t.Fatal(err)
			}
			status := ""
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
					t.Fatal("creation did not settle")
				}
			}
			view, err := a.ReadSession(receipt.SessionID, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(marker)
			if mode == "benign" {
				check("created_with_original_binding", status == "succeeded" && view.Session.Scope.NativeID != "")
				var env map[string]bool
				check("startup_environment_preserved", json.Unmarshal(body, &env) == nil && env["profile_matches"] && env["route_matches"])
			} else {
				check("rejected_before_query", status == "not_sent" && view.Session.Scope.NativeID == "")
				check("no_startup_side_effect", len(body) == 0)
			}
			_, helperErr := os.Stat(helperMarker)
			check("no_helper_execution", os.IsNotExist(helperErr))
			check("no_model_input", modelCalls.Load() == 0 && len(view.Session.View.Messages) == 0)
			token := strings.Repeat("d", 64)
			handler := console.Handler(a, "127.0.0.1:9463", token, os.DirFS("."))
			req := httptest.NewRequest("GET", "http://127.0.0.1:9463/api/sessions/"+receipt.SessionID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			check("visible_without_values", response.Code == 200 && !strings.Contains(response.Body.String(), "PRIVATE_CREATE_BINDING_VALUE"))
			reports[mode] = map[string]any{"case_directory": caseDir, "status": status, "session": view, "http": json.RawMessage(response.Body.Bytes()), "startup": string(body), "original_provider_requests": originalCalls.Load(), "other_provider_requests": otherCalls.Load()}
		})
	}
}
