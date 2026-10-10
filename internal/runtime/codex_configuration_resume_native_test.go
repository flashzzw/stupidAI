//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/codex"
	"codex-netcap/internal/runtime"
)

// Test actual same-ID reload semantics before exposing source adoption. Default
// inheritance and explicit override are recorded separately; no successful
// model call is used to infer a configuration change.
func TestCodexNativeConfigurationResumeBoundary(t *testing.T) {
	for _, mode := range []string{"warm-default", "warm-override", "detached-default", "detached-override", "cold-default", "cold-override", "warm-clear", "detached-clear", "cold-clear", "detached-clear-default", "cold-clear-default"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("", "netcap-w0209-config-resume-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("Evidence directory:", root)
			f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
			checks, samples := map[string]bool{}, map[string]any{}
			check := func(name string, ok bool) {
				t.Helper()
				checks[name] = ok
				if !ok {
					t.Errorf("failed: %s", name)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				_ = f.Runtime.Close()
				b, _ := json.MarshalIndent(map[string]any{"root": root, "mode": mode, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "owned native resume; one cancelled unavailable-provider turn to establish history; no successful model/tool"}, "", "  ")
				write(filepath.Join(root, "result.json"), string(b))
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
			path := filepath.Join(f.Instance.Profile, "config.toml")
			config, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			write(path, "model_reasoning_effort=\"low\"\n"+string(config))
			f.Restart(t)
			params := map[string]any{"cwd": filepath.Join(root, "work"), "model": f.Route.Model, "modelProvider": f.Route.Provider, "sandbox": "read-only", "approvalPolicy": "on-request"}
			started := call("started", "thread/start", params)
			id := started["thread"].(map[string]any)["id"].(string)
			check("initial_effort_low", started["reasoningEffort"] == "low")
			sent := call("seed", "turn/start", map[string]any{"threadId": id, "input": []any{map[string]any{"type": "text", "text": "OWNED RELOAD HISTORY; provider unavailable", "text_elements": []any{}}}})
			turn := sent["turn"].(map[string]any)["id"].(string)
			deadline := time.Now().Add(10 * time.Second)
			for !f.HasTurnEvent("error", id, turn) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			check("provider_error_observed", f.HasTurnEvent("error", id, turn))
			call("cancel", "turn/interrupt", map[string]any{"threadId": id, "turnId": turn})
			for !f.HasTurnEvent("turn/completed", id, turn) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			check("cancelled_turn_completed", f.HasTurnEvent("turn/completed", id, turn))
			before := call("before", "thread/read", map[string]any{"threadId": id, "includeTurns": true})
			var target any = "high"
			if strings.Contains(mode, "clear") {
				target = nil
				write(path, string(config))
			} else {
				write(path, "model_reasoning_effort=\"high\"\n"+string(config))
			}
			samples["disk"] = call("disk", "config/read", map[string]any{"cwd": filepath.Join(root, "work"), "includeLayers": true})
			params["threadId"] = id
			params["excludeTurns"] = true
			if strings.HasPrefix(mode, "detached-") {
				call("detached", "thread/unsubscribe", map[string]any{"threadId": id})
			}
			if strings.HasPrefix(mode, "cold-") {
				f.Restart(t)
			}
			if strings.HasSuffix(mode, "-override") || strings.HasSuffix(mode, "-clear") {
				params["config"] = map[string]any{"model_reasoning_effort": target}
			}
			var resumed map[string]any
			resumeErr := f.Call(context.Background(), "thread/resume", params, &resumed)
			if mode == "detached-clear" || mode == "cold-clear" {
				var rpc *codex.RPCError
				check("null_override_rejected", errors.As(resumeErr, &rpc) && rpc.Code == -32600 && strings.Contains(resumeErr.Error(), "reasoning_effort must not be empty"))
				if resumeErr != nil {
					samples["resume_error"] = resumeErr.Error()
				}
			} else {
				if resumeErr != nil {
					t.Fatal(resumeErr)
				}
				samples["resumed"] = resumed
			}
			after := call("after", "thread/read", map[string]any{"threadId": id, "includeTurns": true})
			check("same_id", after["thread"].(map[string]any)["id"] == id)
			check("history_unchanged", reflect.DeepEqual(before["thread"].(map[string]any)["turns"], after["thread"].(map[string]any)["turns"]))
			if resumeErr != nil {
				return
			}
			check("resume_same_id", resumed["thread"].(map[string]any)["id"] == id)
			check("read_agrees_with_resume", resumed["reasoningEffort"] == after["thread"].(map[string]any)["reasoningEffort"])
			if strings.HasPrefix(mode, "warm-") || mode == "detached-default" || mode == "detached-clear-default" {
				check("warm_resume_retains_old_effort", resumed["reasoningEffort"] == "low")
			}
			if mode == "detached-override" || strings.HasPrefix(mode, "cold-") {
				check("target_effort_applied", resumed["reasoningEffort"] == target)
			}
			samples["outcome"] = map[string]any{"disk_effort": target, "resumed_effort": resumed["reasoningEffort"], "target_applied": resumed["reasoningEffort"] == target}
		})
	}
}
