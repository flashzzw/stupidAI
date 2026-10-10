package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

type hookDeactivationApplicationFixture struct {
	*capabilityHookReader
	prepared chan runtime.Intent
}

func (f *hookDeactivationApplicationFixture) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "prepare_execution" {
		f.prepared <- queryCopy(in)
		return runtime.Receipt{NativeID: in.Scope.NativeID, ConfigurationBaseline: "fixture-idle-history"}, nil
	}
	return f.capabilityHookReader.Execute(ctx, in)
}

func TestCodexHookDeactivationPreparationBindsSavedCandidate(t *testing.T) {
	for _, scenario := range []string{"valid", "human-takeover", "native-active", "incomplete-execution", "source-drift"} {
		t.Run(scenario, func(t *testing.T) {
			a, f, request := inspectionFixture(t)
			inspection, err := a.InspectCapabilityHooks(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: request.AssetID, Mode: "enabled", HookReview: &CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: "trusted"}}}})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before := a.Snapshot().Sessions[0]
			change := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []CapabilitySelection{{AssetID: request.AssetID, Mode: "disabled"}}}
			preview, err := a.PreviewExecutionChange(created.SessionID, change)
			if err != nil {
				t.Fatal(err)
			}
			change.PreviewHash = preview.PreviewHash
			if _, err = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &change}); err != nil {
				t.Fatal(err)
			}
			wrapper := &hookDeactivationApplicationFixture{capabilityHookReader: f, prepared: make(chan runtime.Intent, 1)}
			a.mu.Lock()
			a.runtime = wrapper
			a.mu.Unlock()
			f.mu.Lock()
			view := f.views[before.Scope.NativeID]
			view.Status = "unknown"
			view.NativeStatus = "idle"
			view.Evidence.Complete = true
			if scenario == "native-active" {
				view.NativeStatus = "active"
			}
			if scenario == "incomplete-execution" {
				view.Evidence.Complete = false
			}
			f.views[before.Scope.NativeID] = view
			f.mu.Unlock()
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "unknown" })
			if scenario == "human-takeover" {
				if _, err = a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: created.SessionID}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "source-drift" {
				if err = os.WriteFile(filepath.Join(inspection.Source.Resolved, "hooks/hooks.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			staged := a.Snapshot().Sessions[0]
			base := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
			var prepared CommandReceipt
			if scenario != "valid" {
				_, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &base})
				if err == nil || len(wrapper.prepared) != 0 {
					t.Fatal("unsafe hook deactivation dispatched", scenario)
				}
				return
			}
			waitTest(t, func() bool {
				prepared, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &base})
				return err == nil
			})
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange.Baseline != "" })
			in := <-wrapper.prepared
			session := a.Snapshot().Sessions[0]
			if in.ConfigurationTarget == nil || in.Scope.PluginSelections[0].Mode != "enabled" || in.ConfigurationTarget.PluginSelections[0].Mode != "disabled" || session.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID {
				t.Fatal("preparation lost original or candidate binding")
			}
			state, err := a.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			op := state.Operations[prepared.OperationID]
			if digest(op.Intent.ConfigurationTarget) != digest(in.ConfigurationTarget) || verifyExecutionChangeIntent(session, op) != nil {
				t.Fatal("preparation target not durably validated")
			}
			for _, tamper := range []string{"missing", "foreign-id", "enable", "permission"} {
				bad := queryCopy(op)
				switch tamper {
				case "missing":
					bad.Intent.ConfigurationTarget = nil
				case "foreign-id":
					bad.Intent.ConfigurationTarget.NativeID = "foreign"
				case "enable":
					bad.Intent.ConfigurationTarget.PluginSelections[0].Mode = "enabled"
				case "permission":
					bad.Intent.Permission = "workspace"
				}
				if verifyExecutionChangeIntent(session, bad) == nil {
					t.Fatal("modified durable preparation target accepted", tamper)
				}
			}
			if _, err = a.Apply(Command{ID: "input", Kind: "send", SessionID: created.SessionID, Text: "must not send"}); err == nil || f.sends.Load() != 0 {
				t.Fatal("preparation authorized input")
			}
		})
	}
}
