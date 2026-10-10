//go:build nativeclaude

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClaudeNativeSettingsFileConsumer(t *testing.T) {
	parent, executable := os.Getenv("NETCAP_SETTINGS_CONSUMER_ROOT"), os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if parent == "" || executable == "" {
		t.Skip("owned namespace fixture only")
	}
	owned, err := os.Stat(filepath.Join(parent, "policy"))
	actual, otherErr := os.Stat("/etc/claude-code")
	if err != nil || otherErr != nil || !os.SameFile(owned, actual) {
		t.Fatal("owned policy namespace required")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(parent, "consumer-native-")
	if err != nil {
		t.Fatal(err)
	}
	checks, reports := map[string]bool{}, map[string]any{}
	write := func(path string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil || os.WriteFile(path, append(data, '\n'), 0600) != nil {
			t.Fatal("cannot record native consumer evidence", err)
		}
	}
	t.Cleanup(func() {
		write(filepath.Join(root, "result.json"), map[string]any{"checks": checks, "reports": reports, "passed": !t.Failed()})
	})
	for _, mode := range []string{"close", "node-killed"} {
		t.Run(mode, func(t *testing.T) {
			check := func(name string, value bool) {
				t.Helper()
				checks[mode+"_"+name] = value
				if !value {
					t.Error(name)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			dir := filepath.Join(root, mode)
			for _, name := range []string{"state", "work", "custom", "profile"} {
				if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := exec.CommandContext(ctx, "git", "init", "--quiet", filepath.Join(dir, "work")).CombinedOutput(); err != nil {
				t.Fatalf("initialize owned project: %v: %s", err, output)
			}
			marker, hook := filepath.Join(dir, "startup.json"), filepath.Join(dir, "hook.py")
			hookBody := fmt.Sprintf("import os,json\nwith open(%q,'w') as f:json.dump({'sentinel':os.getenv('OWNED_CONSUMER')},f)\n", marker)
			if err := os.WriteFile(hook, []byte(hookBody), 0600); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(dir, "custom/original.json")
			write(original, map[string]any{"env": map[string]string{"OWNED_CONSUMER": "preserved"},
				"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": fmt.Sprintf("python3 %q", hook)}}}}}})
			scope := Scope{SessionID: uuid.NewString(), NativeID: uuid.NewString(), Cwd: filepath.Join(dir, "work"),
				Instance: Instance{ID: "claude", Kind: "claude", Executable: executable, Profile: filepath.Join(dir, "profile"), SettingsFile: original,
					Bridge: filepath.Join(repository, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING", "NODE_OPTIONS", "NETCAP_SETTINGS_WIRE", "NETCAP_SETTINGS_WIRE_MODE"}},
				Route: Route{Endpoint: "http://127.0.0.1:1", Model: "claude-sonnet-4-6"}}
			manager, err := newClaudeSettingsFiles(filepath.Join(dir, "state"))
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			use, err := manager.retain(ctx, id, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			defer use.file.Close()
			t.Setenv("NODE_OPTIONS", "--import="+filepath.Join(repository, "tests/claude_settings_wire_capture.mjs"))
			t.Setenv("NETCAP_SETTINGS_WIRE", filepath.Join(dir, "wire.jsonl"))
			t.Setenv("NETCAP_SETTINGS_WIRE_MODE", "session")
			transport, err := connectClaude(ctx, scope.Instance, filepath.Join(dir, "state/bridge"))
			if err != nil {
				t.Fatal(err)
			}
			defer transport.close()
			check("consumer_capability_advertised", transport.settingsFileConsumer == 1)
			params := map[string]any{"native_id": scope.NativeID, "source": scope.NativeID, "cwd": scope.Cwd, "executable": executable,
				"model": scope.Route.Model, "endpoint": scope.Route.Endpoint, "permission": "read_only", "probe": false,
				"settings_file": original, "settings_artifact": use.record.Artifact, "api_key": "fixture-consumer-key"}
			var receipt struct {
				NativeID string `json:"native_id"`
			}
			if err := transport.call(ctx, "create", params, &receipt); err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]any
			if err := transport.call(ctx, "snapshot", map[string]any{"native_id": scope.NativeID}, &snapshot); err != nil {
				t.Fatal(err)
			}
			pid, inputCount := 0, 0
			for _, entry := range snapshot["entries"].([]any) {
				row := entry.(map[string]any)
				if row["kind"] == "initialized" {
					pid = int(row["pid"].(float64))
				}
				if row["kind"] == "input" {
					inputCount++
				}
			}
			consumerFD := fmt.Sprintf("/proc/%d/fd/3", pid)
			info, err := os.Stat(consumerFD)
			check("actual_cli_inherits_the_artifact", err == nil && pid > 1 && fileIdentity(info) == use.record.FileID && receipt.NativeID == scope.NativeID)
			if err != nil {
				t.Fatal(err)
			}
			processExecutable, execErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			consumerLink, linkErr := os.Readlink(consumerFD)
			fdInfo, fdErr := os.ReadFile(fmt.Sprintf("/proc/%d/fdinfo/3", pid))
			resolvedExecutable, resolveErr := filepath.EvalSymlinks(executable)
			check("consumer_pid_is_actual_cli", execErr == nil && resolveErr == nil && processExecutable == resolvedExecutable && linkErr == nil && consumerLink == use.record.Artifact.File.Path && fdErr == nil)
			consumer := map[string]any{"pid": pid, "executable": processExecutable, "fd": 3, "fd_link": consumerLink, "fd_info": string(fdInfo), "file_identity": fileIdentity(info), "recorded_identity": use.record.FileID}
			use.file.Close()
			busy := manager.releaseRetained(ctx, id, scope)
			consumer["release_while_cli_live"] = fmt.Sprint(busy)
			check("live_cli_holds_file_after_go_descriptor_closes", errors.Is(busy, errClaudeSettingsFileBusy))
			var observed map[string]string
			for deadline := time.Now().Add(8 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				body, readErr := os.ReadFile(marker)
				if readErr == nil && json.Unmarshal(body, &observed) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("owned startup hook did not finish")
				}
			}
			check("derived_settings_load_original_hook_and_environment", observed["sentinel"] == "preserved")
			check("no_model_input", inputCount == 0)
			if mode == "node-killed" {
				if err := transport.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				<-transport.exited
			} else {
				transport.close()
			}
			for deadline := time.Now().Add(8 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				if _, err := os.Stat(consumerFD); os.IsNotExist(err) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("owned CLI did not close after its bridge ended")
				}
			}
			count, recoveryErr := manager.recover(ctx)
			_, exists := os.Stat(use.record.Artifact.File.Path)
			check("saved_execution_file_survives_no_live_consumer", recoveryErr == nil && count == 0 && exists == nil)
			reopened, err := manager.retain(ctx, id, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			check("same_version_reopens_same_file", reopened.record.Artifact == use.record.Artifact)
			reopened.file.Close()
			check("explicit_release_after_cli_exit", manager.releaseRetained(ctx, id, scope) == nil)
			reports[mode] = map[string]any{"directory": dir, "scope": scope, "artifact": use.record.Artifact, "pid": pid,
				"snapshot": snapshot, "startup": observed, "released": true, "consumer": consumer}
		})
	}
}
