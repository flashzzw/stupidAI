package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type applicationConfigurationFixture struct {
	*fixtureRuntime
	applications       atomic.Int32
	preparations       atomic.Int32
	blockObservations  atomic.Bool
	observationStarted chan struct{}
	observationRelease chan struct{}
	resolveApply       atomic.Bool
	durable            func(runtime.Intent)
}

func (f *applicationConfigurationFixture) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	if f.blockObservations.Load() {
		select {
		case f.observationStarted <- struct{}{}:
		default:
		}
		select {
		case <-f.observationRelease:
		case <-ctx.Done():
			return runtime.View{}, ctx.Err()
		}
	}
	return f.fixtureRuntime.Observe(ctx, scope)
}

func (f *applicationConfigurationFixture) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "prepare_execution" {
		f.preparations.Add(1)
		return runtime.Receipt{NativeID: in.Scope.NativeID, ConfigurationBaseline: "verified-idle-history"}, nil
	}
	if in.Kind == "apply_execution" {
		if f.durable != nil {
			f.durable(in)
		}
		f.applications.Add(1)
		return runtime.Receipt{NativeID: in.Scope.NativeID}, errors.New("fixture: native applied but wire receipt lost")
	}
	return f.fixtureRuntime.Execute(ctx, in)
}

func (f *applicationConfigurationFixture) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if in.Kind != "apply_execution" {
		return f.fixtureRuntime.Reconcile(ctx, in, receipt)
	}
	if !f.resolveApply.Load() {
		return runtime.Resolution{State: "unknown"}, nil
	}
	view, _ := f.fixtureRuntime.Observe(ctx, in.Scope)
	view.NativeStatus = "idle"
	view.Status = "idle"
	view.ExecutionClear = true
	view.Evidence = runtime.Evidence{Complete: true, Source: "fixture actual configuration"}
	return runtime.Resolution{State: "finished", Status: "succeeded", Receipt: runtime.Receipt{NativeID: in.Scope.NativeID}, View: view, Evidence: view.Evidence}, nil
}

func TestExecutionApplyDurabilityCrashAndLateTakeoverRetainVersionHistory(t *testing.T) {
	c := fixtureConfig(t)
	c.CapabilityAssets = []CapabilityAsset{{ID: "identity", Name: "Identity", Kind: "mcp", Version: "v1", Source: filepath.Join(c.Instances[0].Profile, "config.toml"), Backends: []string{"codex"}, CodexMCP: "identity"}}
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture(), observationStarted: make(chan struct{}, 1), observationRelease: make(chan struct{})}
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "identity", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(sid)
	r := changeRequest(t, a, sid)
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(sid)
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	f.blockObservations.Store(true)
	<-f.observationStarted
	var prep CommandReceipt
	waitTest(t, func() bool {
		prep, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: sid, ExecutionChange: &ref})
		return err == nil
	})
	if f.preparations.Load() != 0 {
		t.Fatal("configuration preparation raced a display observation")
	}
	f.blockObservations.Store(false)
	close(f.observationRelease)
	waitTest(t, func() bool {
		for _, o := range a.Snapshot().Operations {
			if o.ID == prep.OperationID {
				return o.Status == "succeeded"
			}
		}
		return false
	})
	f.durable = func(in runtime.Intent) {
		state, err := a.store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		op := state.Operations[in.ID]
		s := state.Sessions[sid]
		if op.Status != "dispatching" || op.ExecutionSnapshotID != staged.PendingChange.Snapshot.ID || s.ExecutionSnapshot.ID != before.Snapshot.ID || !s.ExecutionHold || s.PendingExecutionChange.Status != "applying" {
			t.Errorf("native apply preceded durable candidate/intent: %+v", op)
		}
	}
	var applied CommandReceipt
	waitTest(t, func() bool {
		applied, err = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: sid, ExecutionChange: &ref})
		return err == nil
	})
	waitTest(t, func() bool { return f.applications.Load() == 1 && a.Snapshot().Sessions[0].Status == "unknown" })
	if _, err := a.Apply(Command{ID: "overwrite", Kind: "discard_execution", SessionID: sid, ExecutionChange: &ref}); err == nil {
		t.Fatal("unknown native mutation discarded")
	}
	if _, err := a.Apply(Command{ID: "manual", Kind: "takeover", SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	read, _ := a.ReadExecution(sid)
	if !reflect.DeepEqual(read.Snapshot, before.Snapshot) || read.PendingChange == nil || !read.Hold {
		t.Fatal("crash guessed an effective version")
	}
	f.resolveApply.Store(true)
	waitTest(t, func() bool { r, _ := a.ReadExecution(sid); return r.PendingChange == nil })
	read, _ = a.ReadExecution(sid)
	s := a.Snapshot().Sessions[0]
	if !reflect.DeepEqual(read.Snapshot, staged.PendingChange.Snapshot) || len(read.History) != 1 || !reflect.DeepEqual(read.History[0], before.Snapshot) || s.ControlMode != "human_takeover" || !s.Paused || f.applications.Load() != 1 {
		t.Fatal("late reconciliation overwrote history, re-applied, or granted control")
	}
	if got, err := a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: sid, ExecutionChange: &ref}); err != nil || got != applied {
		t.Fatal("durable command retry changed operation")
	}
	for _, op := range a.Snapshot().Operations {
		if op.ID == created.OperationID && op.ExecutionSnapshotID != before.Snapshot.ID {
			t.Fatal("old operation changed version")
		}
	}
}
