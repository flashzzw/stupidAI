//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/codex"
	"codex-netcap/internal/runtime"
)

type createReceiptFailure struct {
	app.Persistence
	failed atomic.Bool
}

func (s *createReceiptFailure) Save(state app.State) error {
	for _, op := range state.Operations {
		if op.Intent.Kind == "create" && op.Status == "succeeded" && op.Receipt.NativeID != "" && !s.failed.Swap(true) {
			return errors.New("injected failure before create receipt commit")
		}
	}
	return s.Persistence.Save(state)
}

type heldNativeCreate struct {
	runtime.Adapter
	fixture *runtime.NativeApplicationFixture
	created chan runtime.Receipt
	release chan struct{}
	listed  bool
}

func (f *heldNativeCreate) ReadCodexProjectHookSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	return f.fixture.Runtime.ReadCodexProjectHookSources(ctx, scope)
}

func (f *heldNativeCreate) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	receipt, err := f.Adapter.Execute(ctx, in)
	if err != nil || in.Kind != "create" {
		return receipt, err
	}
	// The native CLI currently needs a history item to persist its rollout.
	// This explicit synthetic item is not a model turn or a saved app message.
	err = f.fixture.Call(ctx, "thread/inject_items", map[string]any{"threadId": receipt.NativeID, "items": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "SYNTHETIC DURABLE HISTORY; no model turn"}}}}}, nil)
	if err != nil {
		return receipt, err
	}
	if f.listed {
		if err = f.fixture.Call(ctx, "thread/goal/set", map[string]any{"threadId": receipt.NativeID, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"}, nil); err != nil {
			return receipt, err
		}
	}
	f.created <- receipt
	select {
	case <-f.release:
		return receipt, nil
	case <-ctx.Done():
		return runtime.Receipt{}, ctx.Err()
	}
}

func waitNativeApplication(t *testing.T, a *app.Application, predicate func(app.Snapshot) bool) app.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot := a.Snapshot()
		if predicate(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(snapshot)
			t.Fatalf("application condition not reached: %s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCodexNativeApplicationRecoversLostCreateReceipt(t *testing.T) {
	for _, permission := range []string{"read_only", "workspace"} {
		for _, listed := range []bool{true, false} {
			name := "hidden"
			if listed {
				name = "listed"
			}
			for _, fault := range []string{"persistence", "transport"} {
				t.Run(permission+"/"+name+"/"+fault, func(t *testing.T) { testNativeApplicationRecovery(t, permission, listed, fault) })
			}
		}
	}
}

func testNativeApplicationRecovery(t *testing.T, permission string, listed bool, faultKind string) {
	f := runtime.NewNativeApplicationFixture(t, permission)
	visibility := "hidden"
	if listed {
		visibility = "listed"
	}
	checks := map[string]bool{}
	snapshots := map[string]app.Snapshot{}
	var original runtime.Receipt
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		if dir := os.Getenv("NETCAP_NATIVE_CODEX_EVIDENCE"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Error(err)
				return
			}
			data, err := json.MarshalIndent(map[string]any{"checks": checks, "snapshots": snapshots, "original_receipt": original, "processes": f.Records, "passed": !t.Failed(), "fault": faultKind, "scope": "production Application + SQLite + Codex Runtime; test-only native stdio; synthetic history and injected fault, no HTTP/Unix peer or hard process crash"}, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, "application-"+permission+"-"+visibility+"-"+faultKind+".json"), append(data, '\n'), 0600)
			}
			if err != nil {
				t.Error(err)
			}
		}
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Fatalf("application native check failed: %s", name)
		}
		t.Log("application native check passed:", name)
	}
	c := app.Config{Version: 1, StateDir: filepath.Join(f.Root, "console"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: filepath.Join(f.Root, "work")}}}
	db, err := app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	fault := &createReceiptFailure{Persistence: db}
	held := &heldNativeCreate{Adapter: f.Runtime, fixture: f, created: make(chan runtime.Receipt, 1), release: make(chan struct{}), listed: listed}
	var adapter runtime.Adapter = held
	var persistence app.Persistence = fault
	var created <-chan runtime.Receipt = held.created
	release := held.release
	if faultKind == "transport" {
		adapter, persistence = f.Runtime, db
		created, release = f.DropCreateReceipt(listed)
	}
	a, err := app.Open(c, persistence, adapter)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	command := app.Command{ID: "durable-create", Kind: "create", ProjectID: "work", RouteID: "fixture", Permission: permission, Title: "durable native fixture"}
	accepted, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case original = <-created:
	case <-time.After(10 * time.Second):
		t.Fatal("native creation did not reach receipt boundary")
	}
	check("native_created_before_commit", original.NativeID != "")
	_, err = a.Apply(app.Command{ID: "takeover-before-receipt", Kind: "takeover", SessionID: accepted.SessionID})
	check("human_takeover_during_create", err == nil)
	before := a.Snapshot().Sessions[0]
	controlGeneration := before.Generation
	close(release)
	if faultKind == "persistence" {
		snapshots["failed_commit"] = waitNativeApplication(t, a, func(s app.Snapshot) bool { return s.Error != "" })
		failed := snapshots["failed_commit"]
		check("receipt_not_in_authoritative_state", fault.failed.Load() && failed.Sessions[0].Scope.NativeID == "" && failed.Operations[0].Receipt.NativeID == "" && failed.Operations[0].Status == "dispatching")
	} else {
		snapshots["lost_reply"] = waitNativeApplication(t, a, func(s app.Snapshot) bool { return len(s.Operations) == 1 && s.Operations[0].Status == "uncertain" })
		failed := snapshots["lost_reply"]
		check("transport_lost_before_runtime_receipt", failed.Sessions[0].Scope.NativeID == "" && failed.Operations[0].Receipt.NativeID == "" && failed.Operations[0].Error == "unexpected EOF")
	}
	_, err = a.Apply(app.Command{ID: "blocked-input", Kind: "send", SessionID: accepted.SessionID, Text: "MUST NOT EXECUTE"})
	check("failure_blocks_input", err != nil)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	var disk codex.Thread
	err = f.Call(context.Background(), "thread/read", map[string]string{"threadId": original.NativeID}, &struct {
		Thread *codex.Thread `json:"thread"`
	}{&disk})
	check("native_restart_keeps_unloaded_thread", err == nil && disk.ID == original.NativeID && disk.Status.Type == "notLoaded")
	db, err = app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(c, db, f.Runtime)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	snapshots["reopened"] = a.Snapshot()
	check("reopen_has_no_native_receipt", snapshots["reopened"].Operations[0].Receipt.NativeID == "" && snapshots["reopened"].Sessions[0].Scope.NativeID == "")
	if !listed {
		snapshots["unknown"] = waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				for _, e := range op.Evidence {
					if e.Source == "codex.reconcile" {
						return op.Status == "uncertain"
					}
				}
			}
			return false
		})
		unknown := snapshots["unknown"]
		check("unlisted_thread_remains_unknown", unknown.Sessions[0].Status == "unknown" && unknown.Sessions[0].Scope.NativeID == "" && unknown.Operations[0].Receipt.NativeID == "")
		for _, kind := range []string{"send", "resume"} {
			_, err = a.Apply(app.Command{ID: "unknown-" + kind, Kind: kind, SessionID: accepted.SessionID, Text: "MUST NOT EXECUTE"})
			check("unknown_blocks_"+kind, err != nil)
		}
		wrong, wrongErr := a.Apply(app.Command{ID: "wrong-candidate", Kind: "associate", SessionID: accepted.SessionID, OperationID: accepted.OperationID, NativeID: "018f0000-0000-7000-8000-000000000001"})
		if wrongErr != nil {
			t.Fatal(wrongErr)
		}
		wrongState := waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == wrong.OperationID {
					return op.Status == "failed"
				}
			}
			return false
		})
		check("missing_candidate_does_not_bind", wrongState.Sessions[0].Scope.NativeID == "" && wrongState.Sessions[0].Paused)
		_, err = a.Apply(app.Command{ID: "original-candidate", Kind: "associate", SessionID: accepted.SessionID, OperationID: accepted.OperationID, NativeID: original.NativeID})
		check("explicit_original_candidate_accepted", err == nil)
		controlGeneration = a.Snapshot().Sessions[0].Generation
	}
	snapshots["recovered"] = waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == accepted.OperationID {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	recovered := snapshots["recovered"]
	session := recovered.Sessions[0]
	var operation app.Operation
	for _, op := range recovered.Operations {
		if op.ID == accepted.OperationID {
			operation = op
		}
	}
	check("same_session_and_native_thread", session.ID == accepted.SessionID && session.Scope.NativeID == original.NativeID && operation.Receipt.NativeID == original.NativeID)
	check("same_original_operation", operation.ID == accepted.OperationID && operation.Intent.Kind == "create")
	check("human_ownership_preserved", session.Paused && session.ControlMode == "human_takeover" && session.Generation == controlGeneration)
	check("execution_binding_preserved", reflect.DeepEqual(session.Scope.Instance, before.Scope.Instance) && reflect.DeepEqual(session.Scope.Route, before.Scope.Route) && session.Scope.Cwd == before.Scope.Cwd && session.Scope.StateNamespace == before.Scope.StateNamespace && session.ExecutionSnapshot != nil && before.ExecutionSnapshot != nil && session.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
	var policy struct {
		Type          string `json:"type"`
		NetworkAccess bool   `json:"networkAccess"`
	}
	if session.View.PermissionEvidence != nil {
		_ = json.Unmarshal(session.View.PermissionEvidence.Sandbox, &policy)
	}
	wantPolicy := "readOnly"
	if permission == "workspace" {
		wantPolicy = "workspaceWrite"
	}
	check("original_permission_verified", session.Permission == permission && session.View.PermissionEvidence != nil && session.View.PermissionEvidence.Verified && string(session.View.PermissionEvidence.ApprovalPolicy) == `"on-request"` && policy.Type == wantPolicy && !policy.NetworkAccess)
	if listed {
		var goal struct {
			Goal struct {
				Status    string `json:"status"`
				Objective string `json:"objective"`
			} `json:"goal"`
		}
		err = f.Call(context.Background(), "thread/goal/get", map[string]string{"threadId": original.NativeID}, &goal)
		check("paused_native_goal_preserved", err == nil && goal.Goal.Status == "paused" && goal.Goal.Objective == "SYNTHETIC PAUSED GOAL; never execute")
	}
	repeated, err := a.Apply(command)
	check("same_command_receipt_after_reopen", err == nil && repeated.SessionID == accepted.SessionID && repeated.OperationID == accepted.OperationID)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	db, err = app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(c, db, f.Runtime)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	snapshots["recovered_reopened"] = a.Snapshot()
	reopened := snapshots["recovered_reopened"].Sessions[0]
	check("recovered_binding_durable", reopened.Scope.NativeID == original.NativeID && reopened.ID == accepted.SessionID && reopened.Paused && reopened.ControlMode == "human_takeover")
	repeated, err = a.Apply(command)
	check("command_idempotency_durable", err == nil && repeated.SessionID == accepted.SessionID && repeated.OperationID == accepted.OperationID)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	starts, turns, forks := 0, 0, 0
	resumedOriginal := true
	for _, process := range f.Records {
		for _, call := range process.Calls {
			if call["method"] == "thread/start" {
				starts++
			}
			if call["method"] == "turn/start" {
				turns++
			}
			if call["method"] == "thread/fork" {
				forks++
			}
			if call["method"] == "thread/resume" {
				params, ok := call["params"].(map[string]any)
				resumedOriginal = resumedOriginal && ok && params["threadId"] == original.NativeID
			}
		}
	}
	check("one_native_create_zero_model_turns", starts == 1 && turns == 0 && forks == 0)
	check("only_original_thread_resumed", resumedOriginal)
}
