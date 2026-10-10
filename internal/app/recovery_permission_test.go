package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type permissionPropagationRuntime struct {
	*fixtureRuntime
	mode            string
	observed        atomic.Int32
	fresh           atomic.Int32
	mismatch        atomic.Bool
	withoutEvidence atomic.Bool
}

func (f *permissionPropagationRuntime) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	if scope.RecoveryPermission != f.mode {
		return runtime.View{}, fmt.Errorf("saved permission did not reach runtime: %q", scope.RecoveryPermission)
	}
	f.observed.Add(1)
	view, err := f.fixtureRuntime.Observe(ctx, scope)
	view.Status, view.ExecutionClear, view.Evidence.Complete = "idle", true, true
	view.NativeID = scope.NativeID
	view.PermissionEvidence = &runtime.PermissionEvidence{Source: "fixture", ExpectedMode: f.mode, Verified: true}
	if err == nil && f.mismatch.Load() {
		if f.withoutEvidence.Load() {
			view.PermissionEvidence = nil
		} else {
			view.PermissionEvidence.Verified = false
		}
		err = fmt.Errorf("original permission mismatch")
	}
	return view, err
}
func (f *permissionPropagationRuntime) ObserveFresh(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	f.fresh.Add(1)
	view, err := f.Observe(ctx, scope)
	return view, err
}

func TestSavedPermissionReachesPollingRestartAndExplicitHandoff(t *testing.T) {
	for _, mode := range []string{"read_only", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t)
			f := &permissionPropagationRuntime{fixtureRuntime: newFixture(), mode: mode}
			a := openFixture(t, config, f)
			r, err := a.Apply(Command{ID: "create-permission", Kind: "create", ProjectID: "project", RouteID: "route", Permission: mode})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool {
				return len(a.Snapshot().Sessions) > 0 && a.Snapshot().Sessions[0].Status == "idle" && f.observed.Load() > 0
			})
			if f.observed.Load() == 0 {
				t.Fatal("initial poll did not prove permission propagation")
			}
			if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: r.SessionID}); err != nil {
				t.Fatal(err)
			}
			before := a.Snapshot().Sessions[0]
			if before.Permission != mode || before.Scope.RecoveryPermission != "" || before.ExecutionSnapshot == nil || before.ExecutionSnapshot.Permission != mode {
				t.Fatal("permission authority or immutable snapshot changed")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			observed := f.observed.Load()
			b := openFixture(t, config, f)
			waitTest(t, func() bool { return f.observed.Load() > observed })
			check, err := b.Apply(Command{ID: "resume", Kind: "resume", SessionID: r.SessionID})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool {
				for _, op := range b.Snapshot().Operations {
					if op.ID == check.OperationID {
						return terminal(op.Status)
					}
				}
				return false
			})
			for _, op := range b.Snapshot().Operations {
				if op.ID == check.OperationID && (op.Status != "succeeded" || op.Intent.Permission != mode || op.Intent.Scope.RecoveryPermission != "") {
					t.Fatalf("handoff permission lost: %+v", op)
				}
			}
			if f.fresh.Load() != 1 || b.Snapshot().Sessions[0].Paused {
				t.Fatal("explicit checked handoff did not complete")
			}
			if _, err := b.Apply(Command{ID: "pause-again", Kind: "takeover", SessionID: r.SessionID}); err != nil {
				t.Fatal(err)
			}
			f.mismatch.Store(true)
			f.withoutEvidence.Store(mode == "workspace")
			failed, err := b.Apply(Command{ID: "reject-mismatch", Kind: "resume", SessionID: r.SessionID})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool {
				for _, op := range b.Snapshot().Operations {
					if op.ID == failed.OperationID {
						return op.Status == "not_sent"
					}
				}
				return false
			})
			after := b.Snapshot().Sessions[0]
			if after.View.PermissionEvidence == nil || after.View.PermissionEvidence.Verified {
				t.Fatal("failed recovery left old permission evidence marked verified")
			}
			if !after.Paused || after.Permission != mode || after.Scope.NativeID != before.Scope.NativeID || after.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID || f.sends.Load() != 0 {
				t.Fatal("failed policy check changed original identity, snapshot or input authority")
			}
		})
	}
}
