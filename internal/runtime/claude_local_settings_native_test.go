//go:build linux && nativeclaude

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClaudeNativeLocalSettingsAdmission(t *testing.T) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in owned native CLI")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := os.MkdirTemp("", "netcap-w0208-local-admission-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", evidence)
	checks := map[string]bool{}
	records := map[string]any{}
	t.Cleanup(func() {
		body, e := json.MarshalIndent(map[string]any{"root": evidence, "checks": checks, "records": records, "passed": !t.Failed(), "scope": "production Claude Runtime/Session; owned local route overrides and startup markers; no model input"}, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(evidence, "result.json"), body, 0600)
		}
		if e != nil {
			t.Error(e)
		}
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	for _, label := range []string{"project", "cwd-local", "primary-local", "valid", "ignored-intermediate", "ordinary", "home-root"} {
		t.Run(label, func(t *testing.T) {
			caseRoot := filepath.Join(evidence, label)
			if err := os.MkdirAll(caseRoot, 0700); err != nil {
				t.Fatal(err)
			}
			root, repo, worktree := claudeLocalRepo(t, caseRoot)
			for key, suffix := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
				dir := filepath.Join(root, suffix)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv(key, dir)
			}
			for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
				t.Setenv(key, "1")
			}
			t.Setenv("NETCAP_LOCAL_SETTINGS_KEY", "fixture-only")
			cwd := filepath.Join(worktree, "nested")
			allowed := label == "valid" || label == "ignored-intermediate" || label == "ordinary" || label == "home-root"
			if label == "ordinary" {
				cwd = filepath.Join(root, "ordinary")
			}
			if label == "home-root" {
				t.Setenv("HOME", repo)
			}
			path := filepath.Join(cwd, ".claude/settings.json")
			if label == "cwd-local" {
				path = filepath.Join(cwd, ".claude/settings.local.json")
			}
			if label == "primary-local" {
				path = filepath.Join(repo, ".claude/settings.local.json")
			}
			marker := filepath.Join(root, "startup-ran")
			settings := map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:2"}, "hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "printf ran > " + strconv.Quote(marker), "timeout": 3}}}}}}
			if allowed {
				settings["env"] = map[string]string{"NETCAP_SAFE": "preserved"}
				if label == "ignored-intermediate" {
					claudeLocalWrite(t, filepath.Join(worktree, ".claude/settings.local.json"), `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
				}
				if label == "home-root" {
					claudeLocalWrite(t, filepath.Join(repo, ".claude/settings.local.json"), `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
				}
			}
			body, _ := json.Marshal(settings)
			claudeLocalWrite(t, path, string(body))
			instance := Instance{ID: "claude", Kind: "claude", Executable: cli, Profile: filepath.Join(root, "profile"), Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}}
			cmd := exec.Command(cli, "--version")
			cmd.Env = claudeEnv(instance)
			version, e := cmd.Output()
			if e != nil || strings.TrimSpace(string(version)) != "2.1.280 (Claude Code)" {
				t.Fatalf("fixed CLI required: %q %v", version, e)
			}
			r := NewClaude(filepath.Join(root, "runtime"))
			scope := Scope{Instance: instance, Cwd: cwd, StateNamespace: instance.Profile, Route: Route{ID: "route", InstanceID: "claude", Endpoint: "http://127.0.0.1:1", Model: "claude-sonnet-4-6", CredentialRef: "env:NETCAP_LOCAL_SETTINGS_KEY"}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			receipt, e := r.Execute(ctx, Intent{ID: uuid.NewString(), Kind: "create", Scope: scope, Permission: "read_only"})
			cancel()
			if e == nil {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if _, statErr := os.Stat(marker); statErr == nil {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			errText := ""
			if e != nil {
				errText = e.Error()
			}
			if label == "valid" && e == nil {
				scope.NativeID = receipt.NativeID
				changed := filepath.Join(repo, ".claude/settings.local.json")
				claudeLocalWrite(t, changed, `{"env":{"ANTHROPIC_API_KEY":"fixture-replaced"}}`)
				_, sendErr := r.Execute(context.Background(), Intent{ID: uuid.NewString(), Kind: "send", Scope: scope, Permission: "read_only", Text: "MUST NOT REACH MODEL"})
				check("live_change_blocks_input", sendErr != nil && strings.Contains(sendErr.Error(), changed))
				if err := os.Remove(changed); err != nil {
					t.Fatal(err)
				}
				view, observeErr := r.Observe(context.Background(), scope)
				check("repair_keeps_original_id", observeErr == nil && view.NativeID == receipt.NativeID)
			}
			closeErr := r.Close()
			_, markerErr := os.Stat(marker)
			_, bridgeErr := os.Stat(filepath.Join(root, "runtime/runtimes/claude"))
			if allowed {
				check(label+"_accepted", e == nil && receipt.NativeID != "")
				check(label+"_startup_preserved", markerErr == nil)
				check(label+"_bridge_present", bridgeErr == nil)
			} else {
				check(label+"_rejects_original_source", e != nil && strings.Contains(errText, path) && strings.Contains(errText, "覆盖"))
				check(label+"_no_startup_hook", os.IsNotExist(markerErr))
				check(label+"_no_bridge_creation", os.IsNotExist(bridgeErr))
			}
			check(label+"_close", closeErr == nil)
			journals, _ := filepath.Glob(filepath.Join(root, "runtime/runtimes/claude", "*.jsonl"))
			noInput := true
			for _, journal := range journals {
				data, readErr := os.ReadFile(journal)
				if readErr != nil {
					t.Fatal(readErr)
				}
				noInput = noInput && !strings.Contains(string(data), `"kind":"input"`)
			}
			check(label+"_no_model_input", noInput)
			sourcePaths, sourceErr := claudeLocalSettingsPaths(context.Background(), scope)
			check(label+"_sources_resolved", sourceErr == nil)
			records[label] = map[string]any{"source_paths": sourcePaths, "cwd": cwd, "source": path, "error": errText, "receipt": receipt, "startup_observed": markerErr == nil, "version": strings.TrimSpace(string(version))}
		})
	}
}
