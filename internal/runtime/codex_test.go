package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"codex-netcap/internal/codex"
)

type nativeFixture struct {
	call     func(string, map[string]any) (any, error)
	events   []codex.Message
	requests map[string]codex.Message
	methods  []string
	replies  []any
	closed   bool
}

func (f *nativeFixture) Call(_ context.Context, method string, params, out any) error {
	f.methods = append(f.methods, method)
	value, err := f.call(method, params.(map[string]any))
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}
func (f *nativeFixture) Drain() []codex.Message { events := f.events; f.events = nil; return events }
func (f *nativeFixture) Close() error           { f.closed = true; return nil }
func (f *nativeFixture) ConnectionID() string   { return "connection-1" }
func (f *nativeFixture) PendingRequest(id json.RawMessage) (codex.Message, bool) {
	r, ok := f.requests[string(id)]
	return r, ok && !f.closed
}
func (f *nativeFixture) Respond(_ context.Context, id json.RawMessage, result any) error {
	if _, ok := f.PendingRequest(id); !ok {
		return codex.ErrRequestExpired
	}
	delete(f.requests, string(id))
	f.replies = append(f.replies, result)
	return nil
}

func nativeHarness(t *testing.T) (*Codex, *nativeFixture, Scope) {
	t.Helper()
	c := NewCodex()
	f := &nativeFixture{requests: map[string]codex.Message{}}
	scope := Scope{Instance: Instance{ID: "fixture"}, NativeID: "native", Cwd: "/fixture", Route: Route{Provider: "local", Model: "model"}}
	c.clients[scope.Instance.ID] = f
	return c, f, scope
}

func emptyControl(method string) (any, error) {
	switch method {
	case "thread/goal/get":
		return map[string]any{"goal": nil}, nil
	case "thread/queue/list", "thread/backgroundTerminals/list", "thread/list":
		return map[string]any{"data": []any{}}, nil
	}
	return nil, fmt.Errorf("unexpected native method %s", method)
}

func TestFreshUnmaterializedThreadIsIdleOnlyWithPositiveMetadata(t *testing.T) {
	for _, message := range []string{"no rollout found", "is not materialized yet"} {
		for _, status := range []string{"idle", "active"} {
			t.Run(message+"/"+status, func(t *testing.T) {
				c, f, scope := nativeHarness(t)
				f.call = func(method string, params map[string]any) (any, error) {
					if method == "thread/read" {
						if params["includeTurns"] == true {
							return nil, &codex.RPCError{Code: -32600, Message: message}
						}
						return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": status}}}, nil
					}
					if method == "thread/turns/list" {
						return nil, &codex.RPCError{Code: -32600, Message: "is not materialized yet"}
					}
					return emptyControl(method)
				}
				view, err := c.Observe(context.Background(), scope)
				if status == "idle" && (err != nil || !view.ExecutionClear) {
					t.Fatalf("fresh idle: %+v %v", view, err)
				}
				if status == "active" && err == nil {
					t.Fatal("missing running history became idle")
				}
				for _, method := range f.methods {
					if method == "thread/resume" {
						t.Fatal("resumed unmaterialized thread")
					}
				}
			})
		}
	}
}

func TestReconcileRequiresInputAndFinishedExecutionScope(t *testing.T) {
	c, f, scope := nativeHarness(t)
	background := true
	partial := false
	f.call = func(method string, params map[string]any) (any, error) {
		if method == "thread/read" {
			view := "full"
			if partial {
				view = "partial"
			}
			return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": "idle"}, "turns": []any{map[string]any{"id": "turn", "status": "completed", "itemsView": view, "items": []any{
				map[string]any{"id": "input", "type": "userMessage", "clientId": "operation", "content": []any{map[string]string{"type": "text", "text": "work"}}},
			}}}}}, nil
		}
		if method == "thread/backgroundTerminals/list" && background {
			return map[string]any{"data": []any{map[string]string{"processId": "still-running"}}}, nil
		}
		return emptyControl(method)
	}
	in := Intent{ID: "operation", Kind: "send", Scope: scope, Text: "work"}
	result, err := c.Reconcile(context.Background(), in, Receipt{})
	if err != nil || result.State != "running" {
		t.Fatalf("released background execution: %+v %v", result, err)
	}
	background = false
	result, err = c.Reconcile(context.Background(), in, Receipt{})
	if err != nil || result.State != "finished" || result.Receipt.NativeTurn != "turn" {
		t.Fatalf("original turn not recovered: %+v %v", result, err)
	}
	in.Text = "different work"
	result, err = c.Reconcile(context.Background(), in, Receipt{})
	if err != nil || result.State != "unknown" {
		t.Fatal("associated mismatched input")
	}
	in.Text = "work"
	partial = true
	if _, err = c.Reconcile(context.Background(), in, Receipt{}); err == nil {
		t.Fatal("partial history accepted as final evidence")
	}
}

func TestResolvedNativeRequestIsRejectedAtReplyBarrier(t *testing.T) {
	c, f, scope := nativeHarness(t)
	params := json.RawMessage(`{"threadId":"native","turnId":"turn","command":"date","availableDecisions":["accept","cancel"]}`)
	request := codex.Message{ID: json.RawMessage(`5`), Method: "item/commandExecution/requestApproval", Params: params}
	f.requests["5"] = request
	f.events = []codex.Message{request}
	c.collect(scope.Instance, f)
	pending := c.pending["fixture/connection-1/5"]
	f.call = func(method string, _ map[string]any) (any, error) {
		if method != "thread/read" {
			return nil, fmt.Errorf("unexpected %s", method)
		}
		delete(f.requests, "5")
		f.events = append(f.events, codex.Message{Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"native","requestId":5}`)})
		return map[string]any{"thread": map[string]string{"id": "native"}}, nil
	}
	_, err := c.Execute(context.Background(), Intent{ID: "reply", Kind: "respond", Scope: scope, Pending: &pending, OptionID: "decision-0"})
	var notSent *NotSent
	if !errors.As(err, &notSent) || len(f.replies) != 0 {
		t.Fatalf("replied after native resolution: %v", err)
	}
}

func TestAsyncQuestionUsesCorrelatedInputInsteadOfRPCResponse(t *testing.T) {
	c, f, scope := nativeHarness(t)
	var submitted map[string]any
	f.call = func(method string, params map[string]any) (any, error) {
		if method == "thread/resume" {
			return codex.Started{Thread: codex.Thread{ID: scope.NativeID, Status: codex.Status{Type: "idle"}}, Model: scope.Route.Model, Provider: scope.Route.Provider, ApprovalPolicy: json.RawMessage(`"on-request"`), Sandbox: json.RawMessage(`{"type":"readOnly"}`)}, nil
		}
		if method == "thread/read" {
			return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": "idle"}, "turns": []any{map[string]any{"id": "turn", "status": "completed", "itemsView": "full", "items": []any{
				map[string]any{"id": "question", "type": "agentMessage", "delivery": "async", "text": "Color?", "questions": []any{map[string]any{"title": "Color?", "options": []string{"green", "blue"}}}},
			}}}}}, nil
		}
		if method == "turn/start" {
			submitted = params
			return map[string]any{"turn": map[string]string{"id": "answer-turn", "status": "inProgress"}}, nil
		}
		return emptyControl(method)
	}
	view, err := c.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pending) != 1 || view.Status != "waiting_input" {
		t.Fatalf("question lost: %+v", view)
	}
	pending := view.Pending[0]
	receipt, err := c.Execute(context.Background(), Intent{ID: "reply-operation", Kind: "respond", Scope: scope, Permission: "read_only", Pending: &pending, Answers: map[string][]string{"question-0": {"green"}}})
	if err != nil || receipt.NativeTurn != "answer-turn" || len(f.replies) != 0 {
		t.Fatalf("wrong asynchronous reply channel: %+v %v", receipt, err)
	}
	if submitted["clientUserMessageId"] != "reply-operation" {
		t.Fatal("answer lost operation correlation")
	}
}

func TestIdleChildStillOccupiesItsQueueOrGoal(t *testing.T) {
	for _, kind := range []string{"queue", "native_goal"} {
		t.Run(kind, func(t *testing.T) {
			_, f, _ := nativeHarness(t)
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "thread/list" {
					return map[string]any{"data": []any{map[string]any{"id": "child", "status": map[string]string{"type": "idle"}}}}, nil
				}
				if method == "thread/turns/list" {
					return map[string]any{"data": []any{map[string]any{"id": "done", "status": "completed", "itemsView": "full", "items": []any{}}}}, nil
				}
				if params["threadId"] == "child" {
					if method == "thread/queue/list" && kind == "queue" {
						return map[string]any{"data": []any{map[string]string{"id": "queued-child-input"}}}, nil
					}
					if method == "thread/goal/get" && kind == "native_goal" {
						return map[string]any{"goal": map[string]string{"status": "active"}}, nil
					}
				}
				return emptyControl(method)
			}
			work, err := executions(context.Background(), f, "parent")
			if err != nil || len(work) == 0 {
				t.Fatalf("idle child lost %s occupancy: %+v %v", kind, work, err)
			}
		})
	}
}
