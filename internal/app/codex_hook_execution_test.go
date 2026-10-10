package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type hookDisconnectAdapter struct {
	*fixtureRuntime
	fail atomic.Bool
}

func (d *hookDisconnectAdapter) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	if d.fail.Load() {
		return runtime.View{}, errors.New("native connection lost")
	}
	return d.fixtureRuntime.Observe(ctx, scope)
}

func TestCodexHookExecutionConnectionLossMakesLiveRecordUnknown(t *testing.T) {
	d := &hookDisconnectAdapter{fixtureRuntime: newFixture()}
	a := openFixture(t, fixtureConfig(t), d)
	_ = createFixture(t, a)
	s := a.Snapshot().Sessions[0]
	setControlView(d.fixtureRuntime, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "running", Evidence: runtime.Evidence{Complete: true}, HookExecutions: []runtime.HookExecution{codexRecordedHook("running")}})
	waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
	d.fail.Store(true)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Connection == "disconnected" })
	got := a.Snapshot().Sessions[0]
	if got.View.HookExecutions[0].Status != "unknown" {
		t.Fatal("disconnected API still claims a live hook")
	}
	for _, e := range got.View.Executions {
		if e.Kind == "hook" && e.Status != "unknown" {
			t.Fatal("occupancy still claims live hook")
		}
	}
}

func codexRecordedHook(status string) runtime.HookExecution {
	h := runtime.HookExecution{ID: "codex-hook-saved", NativeRunID: "session-start:0:/cache/hooks.json", TurnID: "native-turn", Event: "sessionStart", Source: "plugin", SourcePath: "/cache/hooks.json", StartedAt: "2026-10-03T01:00:00Z", Status: status, NativeStatus: "running"}
	if status == "error" {
		h.NativeStatus = "failed"
		h.FinishedAt = "2026-10-03T01:00:03Z"
		duration := int64(3001)
		h.DurationMS = &duration
	}
	return h
}

func TestCodexHookExecutionMissingTerminalKeepsOriginalOperationUncertain(t *testing.T) {
	f := newFixture()
	var staleIdle atomic.Bool
	f.resolve = func(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
		v, _ := f.Observe(ctx, in.Scope)
		state := "finished"
		if in.Kind == "send" && !staleIdle.Load() {
			state = "running"
		}
		return runtime.Resolution{State: state, Status: "succeeded", Receipt: receipt, View: v, Evidence: runtime.Evidence{Complete: true}}, nil
	}
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	r, err := a.Apply(Command{ID: "original-input", Kind: "send", SessionID: sid, Text: "original"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == r.OperationID {
				return op.Receipt.NativeTurn != ""
			}
		}
		return false
	})
	v, _ := f.Observe(context.Background(), s.Scope)
	h := codexRecordedHook("running")
	h.TurnID = r.OperationID
	v.HookExecutions = []runtime.HookExecution{h}
	v.Status = "running"
	setControlView(f, s.Scope.NativeID, v)
	waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
	v.HookExecutions = nil
	v.Status = "idle"
	v.ExecutionClear = true
	v.Evidence = runtime.Evidence{Complete: true, Source: "stale-idle-without-hook-terminal"}
	setControlView(f, s.Scope.NativeID, v)
	staleIdle.Store(true)
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].View.Evidence.Source == "stale-idle-without-hook-terminal"
	})
	for _, op := range a.Snapshot().Operations {
		if op.ID == r.OperationID && op.Status != "uncertain" {
			t.Fatal("original operation completed without its hook terminal")
		}
	}
}

func TestCodexHookExecutionLateEventKeepsOriginalOperation(t *testing.T) {
	f := newFixture()
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	var first CommandReceipt
	for _, id := range []string{"first-input", "second-input"} {
		r, err := a.Apply(Command{ID: id, Kind: "send", SessionID: sid, Text: id})
		if err != nil {
			t.Fatal(err)
		}
		if id == "first-input" {
			first = r
		}
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == r.OperationID {
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}
	view, err := f.Observe(context.Background(), s.Scope)
	if err != nil {
		t.Fatal(err)
	}
	h := codexRecordedHook("error")
	h.TurnID = first.OperationID
	view.HookExecutions = []runtime.HookExecution{h}
	setControlView(f, s.Scope.NativeID, view)
	waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
	read, err := a.ReadExecution(sid)
	if err != nil {
		t.Fatal(err)
	}
	if read.HookExecutions[0].Operation != first.OperationID {
		t.Fatal("late hook was not bound to the original turn's operation")
	}
}

func TestCodexHookExecutionRetainsHistoryWhenNativeDoesNotReplay(t *testing.T) {
	f := newFixture()
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true, At: "first"}, HookExecutions: []runtime.HookExecution{codexRecordedHook("error")}})
	waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true, Detail: "new observation without notification replay"}})
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].View.Evidence.Detail == "new observation without notification replay"
	})
	read, err := a.ReadExecution(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.HookExecutions) != 1 || read.HookExecutions[0].NativeStatus != "failed" {
		t.Fatal("new observation erased recorded hook failure")
	}
}

func TestCodexHookExecutionMissingTerminalBlocksSendAndHandoff(t *testing.T) {
	f := newFixture()
	a := openFixture(t, fixtureConfig(t), f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "running", Evidence: runtime.Evidence{Complete: true, At: "first"}, HookExecutions: []runtime.HookExecution{codexRecordedHook("running")}})
	waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true, At: "after-connection-change"}})
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].View.Evidence.At == "after-connection-change" })
	if _, err := a.Apply(Command{ID: "must-not-send", Kind: "send", SessionID: sid, Text: "not before hook reconciliation"}); err == nil {
		t.Fatal("missing hook terminal permitted another input")
	}
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	r, err := a.Apply(Command{ID: "resume", Kind: "resume", SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == r.OperationID {
				return terminal(op.Status)
			}
		}
		return false
	})
	for _, op := range a.Snapshot().Operations {
		if op.ID == r.OperationID && op.Status != "not_sent" {
			t.Fatal("handoff erased unresolved hook history")
		}
	}
	if f.sends.Load() != 0 {
		t.Fatal("unexpected input dispatched")
	}
}

func TestCodexHookExecutionRestartPreservesOutcomeAndUnknownOccupancy(t *testing.T) {
	for _, status := range []string{"error", "running"} {
		t.Run(status, func(t *testing.T) {
			cfg := fixtureConfig(t)
			f := newFixture()
			a := openFixture(t, cfg, f)
			sid := createFixture(t, a).SessionID
			s := a.Snapshot().Sessions[0]
			setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true}, HookExecutions: []runtime.HookExecution{codexRecordedHook(status)}})
			waitTest(t, func() bool { return len(a.Snapshot().Sessions[0].View.HookExecutions) == 1 })
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			f2 := newFixture()
			setControlView(f2, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true, Source: "restarted-native-without-replay"}})
			b := openFixture(t, cfg, f2)
			waitTest(t, func() bool { return b.Snapshot().Sessions[0].View.Evidence.Source == "restarted-native-without-replay" })
			got := b.Snapshot().Sessions[0]
			want := status
			if status == "running" {
				want = "unknown"
			}
			if got.ID != sid || got.Scope.NativeID != s.Scope.NativeID || len(got.View.HookExecutions) != 1 || got.View.HookExecutions[0].Status != want {
				t.Fatal("restart lost native hook history or identity")
			}
			if status == "running" && (got.View.ExecutionClear || got.Status == "idle" || got.View.Evidence.Complete) {
				t.Fatal("restart released unresolved hook occupancy")
			}
			if f2.creates.Load() != 0 || f2.sends.Load() != 0 {
				t.Fatal("restart replayed native work")
			}
		})
	}
}
