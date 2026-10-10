package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex-netcap/internal/codex"
)

func codexHookExecutionHarness(t *testing.T) (*Codex, *nativeFixture, Scope) {
	t.Helper()
	c, f, scope := nativeHarness(t)
	scope.Instance.Kind = "codex"
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": "idle"}, "turns": []any{}}}, nil
		case "thread/turns/list":
			return map[string]any{"data": []any{}}, nil
		default:
			return emptyControl(method)
		}
	}
	return c, f, scope
}

func TestCodexHookExecutionIdentityAndIncompleteStates(t *testing.T) {
	for _, mode := range []string{"duplicate", "late-start", "two-turns", "foreign", "conflicting-source", "missing-end", "invalid-start", "unknown-status", "disconnected"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := codexHookExecutionHarness(t)
			start := codexHookEvent("hook/started", "native", "turn-1", "running")
			end := codexHookEvent("hook/completed", "native", "turn-1", "failed")
			f.events = []codex.Message{start, end}
			wantStatus, wantCount, clear := "error", 1, true
			switch mode {
			case "duplicate":
				f.events = append(f.events, end)
			case "late-start":
				f.events = append(f.events, start)
			case "two-turns":
				f.events = append(f.events, codexHookEvent("hook/completed", "native", "turn-2", "failed"))
				wantCount = 2
			case "foreign":
				f.events = []codex.Message{codexHookEvent("hook/started", "other", "turn-1", "running")}
				wantCount = 0
			case "conflicting-source", "missing-end", "invalid-start", "unknown-status":
				var body map[string]any
				_ = json.Unmarshal(end.Params, &body)
				run := body["run"].(map[string]any)
				switch mode {
				case "conflicting-source":
					run["sourcePath"] = "/other/hooks.json"
				case "missing-end":
					delete(run, "completedAt")
				case "invalid-start":
					run["startedAt"] = -1
				case "unknown-status":
					run["status"] = "future-status"
				}
				f.events[1].Params, _ = json.Marshal(body)
				wantStatus, clear = "unknown", false
			case "disconnected":
				f.events = []codex.Message{start}
				if _, err := c.Observe(context.Background(), scope); err != nil {
					t.Fatal(err)
				}
				previous := f.call
				f.call = func(string, map[string]any) (any, error) { return nil, errors.New("lost native transport") }
				if _, err := c.Observe(context.Background(), scope); err == nil {
					t.Fatal("transport error missing")
				}
				f.call = previous
				f.closed = false
				c.clients[scope.Instance.ID] = f
				wantStatus, clear = "unknown", false
			}
			v, err := c.Observe(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if len(v.HookExecutions) != wantCount || v.ExecutionClear != clear {
				t.Fatalf("identity or occupancy mismatch: %+v", v)
			}
			for _, h := range v.HookExecutions {
				if h.Status != wantStatus {
					t.Fatalf("lost native outcome: %+v", h)
				}
			}
			if wantCount == 2 && v.HookExecutions[0].ID == v.HookExecutions[1].ID {
				t.Fatal("same raw native ID merged across turns")
			}
			if !clear && v.Evidence.Complete {
				t.Fatal("incomplete hook reported complete")
			}
		})
	}
}

func TestCodexHookExecutionTerminalRetainsNativeOutcome(t *testing.T) {
	for _, status := range []string{"completed", "failed", "blocked", "stopped"} {
		t.Run(status, func(t *testing.T) {
			c, f, scope := codexHookExecutionHarness(t)
			f.events = []codex.Message{codexHookEvent("hook/started", "native", "turn-1", "running"), codexHookEvent("hook/completed", "native", "turn-1", status)}
			v, err := c.Observe(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"completed": "success", "failed": "error", "blocked": "error", "stopped": "cancelled"}[status]
			if len(v.HookExecutions) != 1 {
				t.Fatal("missing native hook")
			}
			h := v.HookExecutions[0]
			if h.Status != want || h.NativeStatus != status || h.TurnID != "turn-1" || h.SourcePath != "/cache/hooks.json" || h.FinishedAt == "" || h.DurationMS == nil || *h.DurationMS != 3001 || h.ExitCode != nil || !v.ExecutionClear {
				t.Fatalf("native terminal summary not preserved: %+v", v)
			}
			b, _ := json.Marshal(v)
			if strings.Contains(string(b), "PRIVATE_") {
				t.Fatal("hook diagnostics leaked")
			}
		})
	}
}

func codexHookEvent(method, thread, turn, status string) codex.Message {
	run := map[string]any{"id": "session-start:0:/cache/hooks.json", "eventName": "sessionStart", "handlerType": "command", "executionMode": "sync", "scope": "thread", "sourcePath": "/cache/hooks.json", "source": "plugin", "status": status, "startedAt": int64(1790968947), "completedAt": nil, "durationMs": nil, "entries": []any{map[string]string{"kind": "error", "text": "PRIVATE_OUTPUT"}}, "statusMessage": "PRIVATE_STATUS"}
	if method == "hook/completed" {
		run["completedAt"] = int64(1790968950)
		run["durationMs"] = 3001
	}
	b, _ := json.Marshal(map[string]any{"threadId": thread, "turnId": turn, "run": run})
	return codex.Message{Method: method, Params: b}
}

func TestCodexHookExecutionStartedPreventsIdle(t *testing.T) {
	c, f, scope := codexHookExecutionHarness(t)
	f.events = []codex.Message{codexHookEvent("hook/started", "native", "turn-1", "running")}
	v, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.HookExecutions) != 1 || v.HookExecutions[0].Status != "running" || v.HookExecutions[0].Source != "plugin" {
		t.Fatalf("native hook execution was not retained: %+v", v.HookExecutions)
	}
	if v.ExecutionClear || len(v.Executions) != 1 || v.Executions[0].Kind != "hook" {
		t.Fatal("running hook was treated as an idle execution")
	}
}

func TestCodexHookExecutionTimeoutGetsSafeFailureClassification(t *testing.T) {
	c, f, scope := codexHookExecutionHarness(t)
	end := codexHookEvent("hook/completed", "native", "turn-1", "failed")
	end.Params = json.RawMessage(strings.Replace(string(end.Params), "PRIVATE_OUTPUT", "hook timed out after 3s", 1))
	f.events = []codex.Message{end}
	v, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v.HookExecutions)
	var rows []map[string]any
	_ = json.Unmarshal(b, &rows)
	if len(rows) != 1 || rows[0]["failure_kind"] != "timeout" {
		t.Fatal("native timeout has no safe diagnostic")
	}
}
