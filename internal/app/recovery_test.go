package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

func seedOperation(t *testing.T, c Config, status string) string {
	t.Helper()
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := newState()
	scope := runtime.Scope{Instance: c.Instances[0], Route: c.Routes[0], Cwd: c.Projects[0].Cwd}
	state.Sessions["session"] = Session{ID: "session", Scope: scope, Status: "unknown", ControlMode: "human_takeover", Paused: true, Generation: 2, View: runtime.View{Pending: []runtime.Pending{{ID: "old-request", Status: "pending"}}}}
	state.Operations["original"] = Operation{ID: "original", SessionID: "session", Status: status, Generation: 2, Intent: runtime.Intent{ID: "original", Kind: "create", Scope: scope}, CreatedAt: now()}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	return "session"
}

func TestUnknownRestartAndVerifiedAssociationNeverResend(t *testing.T) {
	c := fixtureConfig(t)
	sid := seedOperation(t, c, "dispatching")
	f := newFixture()
	f.resolve = func(_ context.Context, in runtime.Intent, r runtime.Receipt) (runtime.Resolution, error) {
		if r.NativeID != "verified-native" {
			return runtime.Resolution{State: "unknown", Evidence: runtime.Evidence{Detail: "candidate does not match original source"}}, nil
		}
		return runtime.Resolution{State: "finished", Status: "succeeded", Receipt: r, View: runtime.View{NativeID: r.NativeID, Status: "idle", ExecutionClear: true}, Evidence: runtime.Evidence{Complete: true, Source: "fixture native source + cwd + input proof"}}, nil
	}
	a := openFixture(t, c, f)
	if a.Snapshot().Sessions[0].View.Pending[0].Status != "unverified" {
		t.Fatal("restored request silently became replyable")
	}
	for _, kind := range []string{"send", "resume"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: sid, Text: "again"}); err == nil {
			t.Fatalf("%s bypassed unknown", kind)
		}
	}
	wrong, err := a.Apply(Command{ID: "wrong", Kind: "associate", SessionID: sid, OperationID: "original", NativeID: "wrong-native"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == wrong.OperationID {
				return op.Status == "failed"
			}
		}
		return false
	})
	if _, err = a.Apply(Command{ID: "resume-again", Kind: "resume", SessionID: sid}); err == nil {
		t.Fatal("rejected association released original")
	}
	right, err := a.Apply(Command{ID: "right", Kind: "associate", SessionID: sid, OperationID: "original", NativeID: "verified-native"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == right.OperationID {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	s := a.Snapshot().Sessions[0]
	if s.Scope.NativeID != "verified-native" || !s.Paused || s.ControlMode != "human_takeover" || f.creates.Load() != 0 || f.sends.Load() != 0 {
		t.Fatalf("association altered ownership or executed: %+v", s)
	}
}

func TestPreparedRestartProvesNotSentWithoutRuntimeCall(t *testing.T) {
	c := fixtureConfig(t)
	seedOperation(t, c, "prepared")
	f := newFixture()
	a := openFixture(t, c, f)
	if a.Snapshot().Operations[0].Status != "not_sent" || a.Snapshot().Sessions[0].Status != "closed" || f.creates.Load() != 0 {
		t.Fatal("prepared restart was replayed or left ambiguous")
	}
}

func TestIncompleteEvidenceCannotReleaseUnknown(t *testing.T) {
	c := fixtureConfig(t)
	seedOperation(t, c, "uncertain")
	f := newFixture()
	var checks atomic.Int32
	f.resolve = func(context.Context, runtime.Intent, runtime.Receipt) (runtime.Resolution, error) {
		checks.Add(1)
		return runtime.Resolution{State: "finished", Status: "succeeded", View: runtime.View{NativeID: "native", Status: "idle"}, Evidence: runtime.Evidence{Complete: false}}, nil
	}
	a := openFixture(t, c, f)
	waitTest(t, func() bool { return checks.Load() > 0 })
	if _, err := a.Apply(Command{ID: "resume", Kind: "resume", SessionID: "session"}); err == nil {
		t.Fatal("incomplete evidence released occupancy")
	}
}

type terminalFailureStore struct {
	Persistence
	failed atomic.Bool
}

func (s *terminalFailureStore) Save(state State) error {
	for _, op := range state.Operations {
		if op.Intent.Kind == "send" && op.Status == "succeeded" && !s.failed.Swap(true) {
			return errors.New("fixture failure before terminal commit")
		}
	}
	return s.Persistence.Save(state)
}

func TestTerminalPersistenceFailureReconcilesWithoutSecondSend(t *testing.T) {
	c := fixtureConfig(t)
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &terminalFailureStore{Persistence: db}
	f := newFixture()
	a, err := Open(c, store, f)
	if err != nil {
		t.Fatal(err)
	}
	sid := createFixture(t, a).SessionID
	if _, err = a.Apply(Command{ID: "work", Kind: "send", SessionID: sid, Text: "one write"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Error != "" })
	if f.sends.Load() != 1 {
		t.Fatal("unexpected send count")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.Intent.Kind == "send" {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	if f.sends.Load() != 1 || !a.Snapshot().Sessions[0].Paused {
		t.Fatal("restart replayed or unpaused")
	}
}

func TestPauseBeforeDispatchCancelsOnlyPreparedIntent(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	// Holding the application state lock models a queued worker that has not
	// crossed its durable dispatch boundary when the pause generation changes.
	a.mu.Lock()
	op := Operation{ID: "queued", SessionID: "session", Status: "prepared", Generation: 1, Intent: runtime.Intent{ID: "queued", Kind: "create"}}
	a.state.Sessions["session"] = Session{ID: "session", Generation: 2, Paused: true, Status: "starting"}
	a.state.Operations[op.ID] = op
	a.busy["session"] = true
	a.wg.Add(1)
	go a.dispatch(op)
	a.mu.Unlock()
	waitTest(t, func() bool { return a.Snapshot().Operations[0].Status == "not_sent" })
	if f.creates.Load() != 0 {
		t.Fatal("paused prepared intent was sent")
	}
}
