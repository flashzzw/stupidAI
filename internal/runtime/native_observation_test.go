package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"codex-netcap/internal/codex"
)

func TestCodexNativeObservationReadsOriginalExecutionWithoutLoading(t *testing.T) {
	for _, scenario := range []string{"running", "idle", "queued", "terminal", "not_loaded", "unknown", "query_failure", "foreign_id", "foreign_cwd", "missing_namespace"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind = "codex"
			scope.Cwd, scope.Instance.Profile = t.TempDir(), t.TempDir()
			scope.StateNamespace = scope.Instance.Profile
			if scenario == "missing_namespace" {
				scope.StateNamespace = ""
			}
			f.call = func(method string, _ map[string]any) (any, error) {
				switch method {
				case "thread/read":
					id, cwd, status := scope.NativeID, scope.Cwd, "idle"
					if scenario == "foreign_id" {
						id = "foreign"
					}
					if scenario == "foreign_cwd" {
						cwd = t.TempDir()
					}
					if scenario == "running" {
						status = "active"
					}
					if scenario == "not_loaded" {
						status = "notLoaded"
					}
					if scenario == "unknown" {
						status = "unknown"
					}
					return map[string]any{"thread": map[string]any{"id": id, "cwd": cwd, "status": map[string]string{"type": status}}}, nil
				case "thread/turns/list":
					status := "completed"
					if scenario == "running" {
						status = "inProgress"
					}
					return map[string]any{"data": []any{map[string]any{"id": "turn", "status": status, "items": []any{map[string]any{"id": "tool", "type": "commandExecution", "command": "owned-fixture", "aggregatedOutput": "original output", "exitCode": 0}}}}}, nil
				case "thread/queue/list":
					if scenario == "queued" {
						return map[string]any{"data": []any{map[string]string{"id": "queued"}}}, nil
					}
				case "thread/backgroundTerminals/list":
					if scenario == "query_failure" {
						return nil, fmt.Errorf("terminal inventory unavailable")
					}
					if scenario == "terminal" {
						return map[string]any{"data": []any{map[string]string{"processId": "original-terminal"}}}, nil
					}
				}
				return emptyControl(method)
			}
			view, err := c.ObserveNativeExecution(context.Background(), scope)
			if view.TerminalCommand != "" {
				t.Fatal("read-only projection contains terminal control command")
			}
			if scenario == "foreign_id" || scenario == "foreign_cwd" || scenario == "missing_namespace" {
				if err == nil || len(view.Messages) > 0 {
					t.Fatalf("unbound execution accepted: %+v %v", view, err)
				}
				if len(f.methods) > 1 {
					t.Fatal("foreign history was read")
				}
				return
			}
			if scenario == "query_failure" {
				if view.Evidence.Complete || view.ExecutionClear {
					t.Fatal("failed inventory proved idle")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(view.Tools) != 1 || view.Tools[0].Output != "original output" {
				t.Fatal("original tool output missing")
			}
			if scenario == "running" && view.Status != "running" {
				t.Fatal("active turn hidden")
			}
			if (scenario == "terminal" || scenario == "queued") && (len(view.Executions) != 1 || view.ExecutionClear) {
				t.Fatal("native occupancy hidden")
			}
			for _, method := range f.methods {
				switch method {
				case "thread/read", "thread/turns/list", "thread/queue/list", "thread/goal/get", "thread/backgroundTerminals/list", "thread/list":
				default:
					t.Fatalf("read observation invoked %s", method)
				}
			}
		})
	}
}

func TestNativeObservationRPCRejectionKeepsSiblingConnection(t *testing.T) {
	for _, rejected := range []string{"thread/read", "thread/turns/list", "transport"} {
		t.Run(rejected, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind, scope.Instance.Profile, scope.Cwd = "codex", t.TempDir(), t.TempDir()
			scope.StateNamespace = scope.Instance.Profile
			requestID := json.RawMessage(`1`)
			f.requests[string(requestID)] = codex.Message{ID: requestID, Method: "item/commandExecution/requestApproval"}
			c.pending[scope.Instance.ID+"/sibling"] = Pending{NativeID: "sibling", Status: "pending", ResponseKind: "approval", ConnectionID: f.ConnectionID(), RequestID: requestID}
			f.call = func(method string, _ map[string]any) (any, error) {
				if rejected == "transport" {
					return nil, fmt.Errorf("connection closed")
				}
				if method == rejected {
					return nil, &codex.RPCError{Code: -32600, Message: "target history unavailable"}
				}
				if method == "thread/read" {
					return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd}}, nil
				}
				return emptyControl(method)
			}
			if _, err := c.ObserveNativeExecution(context.Background(), scope); err == nil {
				t.Fatal("rejected observation accepted")
			}
			if rejected == "transport" {
				if !f.closed || c.clients[scope.Instance.ID] != nil || c.pending[scope.Instance.ID+"/sibling"].Status != "unverified" {
					t.Fatal("broken transport remained usable")
				}
				return
			}
			if f.closed || c.clients[scope.Instance.ID] == nil || c.pending[scope.Instance.ID+"/sibling"].Status != "pending" {
				t.Fatal("target RPC rejection discarded a healthy sibling connection")
			}
		})
	}
}

func TestNativeObservationNeverFallsBackToOrdinaryAdapterObserve(t *testing.T) {
	a := NewAdapters(t.TempDir())
	defer a.Close()
	for _, kind := range []string{"claude", "opencode", "missing"} {
		if _, err := a.ObserveNativeExecution(context.Background(), Scope{Instance: Instance{Kind: kind}}); err == nil {
			t.Fatalf("unsupported %s observation accepted", kind)
		}
	}
}
