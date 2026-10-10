package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func setControlView(f *fixtureRuntime, id string, view runtime.View) {
	f.mu.Lock()
	f.views[id] = view
	f.mu.Unlock()
}

func TestResumeDoesNotClaimInFlightNativeExecution(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "running",
		Evidence: runtime.Evidence{Complete: true}, Executions: []runtime.Execution{{ID: "external-tool", Status: "running"}}})
	if _, err := a.Apply(Command{ID: "manual", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "resume", Kind: "resume", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if !a.Snapshot().Sessions[0].Paused {
		t.Fatal("resume claimed input without checking the still-running native tool")
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.Intent.Kind == "resume" {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	if !a.Snapshot().Sessions[0].Paused || f.sends.Load() != 0 {
		t.Fatal("failed handoff resumed or sent native input")
	}
}

func TestResumeKeepsPauseWhenHistoryOrRequestsAreUnresolved(t *testing.T) {
	for _, mode := range []string{"partial-history", "pending-approval", "wrong-identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture()
			a := openFixture(t, fixtureConfig(t), f)
			sid := createFixture(t, a).SessionID
			s := a.Snapshot().Sessions[0]
			view := runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true}}
			switch mode {
			case "partial-history":
				view.Evidence.Complete = false
			case "pending-approval":
				view.Pending = []runtime.Pending{{ID: "pending", Status: "pending"}}
			case "wrong-identity":
				view.NativeID = "different-native-session"
			}
			setControlView(f, s.Scope.NativeID, view)
			if _, err := a.Apply(Command{ID: "pause", Kind: "takeover", SessionID: sid}); err != nil {
				t.Fatal(err)
			}
			r, err := a.Apply(Command{ID: "check", Kind: "resume", SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == r.OperationID {
						return op.Status == "not_sent"
					}
				}
				return false
			})
			if !a.Snapshot().Sessions[0].Paused || f.sends.Load() != 0 {
				t.Fatal("unverified handoff granted control")
			}
		})
	}
}

func TestInterruptedHandoffIsNotReplayedOnRestart(t *testing.T) {
	c := fixtureConfig(t)
	sid := seedOperation(t, c, "dispatching")
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	op := state.Operations["original"]
	op.Intent.Kind = "resume"
	state.Operations[op.ID] = op
	if err := db.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	a := openFixture(t, c, f)
	waitTest(t, func() bool { return a.Snapshot().Operations[0].Status == "not_sent" })
	if s := a.Snapshot().Sessions[0]; s.ID != sid || !s.Paused || s.ControlMode != "human_takeover" || f.sends.Load() != 0 {
		t.Fatal("restart replayed the interrupted input grant")
	}
}

func TestResumeAcknowledgesHistoryButYieldsToNewExternalInput(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	view := runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true,
		Evidence: runtime.Evidence{Complete: true}, Messages: []runtime.Message{{ID: "external-before", Role: "user", Text: "original terminal input"}}}
	setControlView(f, s.Scope.NativeID, view)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].ControlMode == "human_takeover" })
	if _, err := a.Apply(Command{ID: "resume-known-input", Kind: "resume", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return !a.Snapshot().Sessions[0].Paused })
	// Observe the same history repeatedly through the production reconciliation
	// path; previously every old external message revoked the new handoff.
	for range 2 {
		a.wg.Add(1)
		a.observe(a.Snapshot().Sessions[0])
	}
	if a.Snapshot().Sessions[0].Paused {
		t.Fatal("already acknowledged historical input revoked an explicit handoff")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	a.wg.Add(1)
	a.observe(a.Snapshot().Sessions[0])
	if a.Snapshot().Sessions[0].ControlMode != "managed" {
		t.Fatal("restart forgot acknowledged external history")
	}
	view.Messages = append(view.Messages, runtime.Message{ID: "external-after", Role: "user", Text: "new terminal input"})
	setControlView(f, s.Scope.NativeID, view)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].ControlMode == "human_takeover" })
	if a.Snapshot().Sessions[0].ControlMode != "human_takeover" || f.sends.Load() != 0 {
		t.Fatal("new external input did not yield control without a resend")
	}
}

func TestObservationNeverRebindsAnExistingNativeIdentity(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	createFixture(t, a)
	s := a.Snapshot().Sessions[0]
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: "unexpected-copy", Status: "idle", ExecutionClear: true,
		Evidence: runtime.Evidence{Complete: true}, Messages: []runtime.Message{{ID: "foreign", Text: "copied history"}}})
	a.wg.Add(1)
	a.observe(s)
	got := a.Snapshot().Sessions[0]
	if got.Scope.NativeID != s.Scope.NativeID {
		t.Fatal("observation silently rebound the native session")
	}
	if got.Status != "unknown" || !got.Paused || len(got.View.Messages) != 0 {
		t.Fatalf("identity mismatch did not preserve the original view and pause: %+v", got)
	}
}

func TestStateDirectoryRetargetCannotReuseTheOldSession(t *testing.T) {
	c := fixtureConfig(t)
	first := c.Instances[0].Profile
	second := t.TempDir()
	config, err := os.ReadFile(filepath.Join(first, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(second, "config.toml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "profile")
	if err = os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	c.Instances[0].Profile = alias
	f := newFixture()
	a := openFixture(t, c, f)
	sid := createFixture(t, a).SessionID
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(Command{ID: "wrong-namespace", Kind: "send", SessionID: sid, Text: "must stay in original state"}); err == nil {
		t.Fatal("identical config content allowed a different state namespace")
	}
	if f.sends.Load() != 0 {
		t.Fatal("namespace drift sent native input")
	}
}

func TestInstanceAliasesExposeSharedStateWithoutInventingIsolation(t *testing.T) {
	c := fixtureConfig(t)
	alias := filepath.Join(t.TempDir(), "same-profile")
	if err := os.Symlink(c.Instances[0].Profile, alias); err != nil {
		t.Fatal(err)
	}
	other := c.Instances[0]
	other.ID, other.Profile = "alias", alias
	c.Instances = append(c.Instances, other)
	a := openFixture(t, c, newFixture())
	instances := a.Workspace().Instances
	if len(instances) != 2 || instances[0].StateNamespace == "" || instances[0].StateNamespace != instances[1].StateNamespace {
		t.Fatal("profile aliases were presented as distinct namespaces")
	}
	if len(instances[0].SharedStateWith) != 1 || instances[0].SharedStateWith[0] != "alias" || instances[1].SharedStateWith[0] != "instance" {
		t.Fatal("shared-state relationship missing from public workspace view")
	}
}

type blockedHandoffRuntime struct {
	*fixtureRuntime
	started chan struct{}
	release chan struct{}
}

func (r *blockedHandoffRuntime) ObserveFresh(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	return r.Observe(ctx, scope)
}

func (r *blockedHandoffRuntime) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return runtime.View{}, ctx.Err()
		}
	}
	return r.fixtureRuntime.Observe(ctx, scope)
}

func TestLateHandoffCannotUndoASecondTakeover(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	r := &blockedHandoffRuntime{fixtureRuntime: f, started: make(chan struct{}, 1), release: make(chan struct{})}
	a := openFixture(t, c, r)
	sid := createFixture(t, a).SessionID
	s := a.Snapshot().Sessions[0]
	setControlView(f, s.Scope.NativeID, runtime.View{NativeID: s.Scope.NativeID, Status: "idle", ExecutionClear: true, Evidence: runtime.Evidence{Complete: true}})
	released := false
	defer func() {
		if !released {
			close(r.release)
		}
	}()
	if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "resume", Kind: "resume", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("handoff did not observe original runtime")
	}
	if _, err := a.Apply(Command{ID: "takeover-again", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	close(r.release)
	released = true
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.Intent.Kind == "resume" {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	if s := a.Snapshot().Sessions[0]; !s.Paused || s.ControlMode != "human_takeover" || f.sends.Load() != 0 {
		t.Fatal("late handoff overrode a newer takeover")
	}
}

type cachedControlRuntime struct{ *fixtureRuntime }

func (r *cachedControlRuntime) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	// Display polling may reuse a previously complete idle view even after
	// the underlying connection becomes unavailable.
	return runtime.View{NativeID: scope.NativeID, Status: "idle", ExecutionClear: true,
		Evidence: runtime.Evidence{Complete: true, Source: "cached-view"}}, nil
}

func (r *cachedControlRuntime) ObserveFresh(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	return runtime.View{}, context.DeadlineExceeded
}

func TestResumeCannotGrantControlFromDisplayCache(t *testing.T) {
	r := &cachedControlRuntime{newFixture()}
	a := openFixture(t, fixtureConfig(t), r)
	sid := createFixture(t, a).SessionID
	if _, err := a.Apply(Command{ID: "takeover-cached", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	receipt, err := a.Apply(Command{ID: "resume-cached", Kind: "resume", SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID {
				return terminal(op.Status)
			}
		}
		return false
	})
	if !a.Snapshot().Sessions[0].Paused {
		t.Fatal("resume granted control from cached idle evidence while the fresh read failed")
	}
}
