package app

import (
	"context"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexExecutionRecoveryUsesOriginalBindings(t *testing.T) {
	for _, mode := range []string{"recover", "wrong-base", "wrong-candidate", "wrong-operation", "takeover"} {
		t.Run(mode, func(t *testing.T) {
			c := codexPluginFixture(t)
			f := &recoveryFixture{applicationConfigurationFixture: &applicationConfigurationFixture{fixtureRuntime: newFixture()}, mode: "explicit-success"}
			a := openFixture(t, c, f)
			created, e := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
			if e != nil {
				t.Fatal(e)
			}
			sid := created.SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(sid)
			request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "disabled"}}}
			preview, e := a.PreviewExecutionChange(sid, request)
			if e != nil {
				t.Fatal(e)
			}
			request.PreviewHash = preview.PreviewHash
			if _, e = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &request}); e != nil {
				t.Fatal(e)
			}
			staged, _ := a.ReadExecution(sid)
			ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
			_, e = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: sid, ExecutionChange: &ref})
			if e != nil {
				t.Fatal(e)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange.Baseline != "" })
			var applied CommandReceipt
			waitTest(t, func() bool {
				applied, e = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: sid, ExecutionChange: &ref})
				return e == nil
			})
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == applied.OperationID {
						return op.Status == "uncertain"
					}
				}
				return false
			})
			f.read = func(in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
				out := runtime.ConfigurationRecoveryCheck{Version: 1, NativeID: in.Scope.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline, At: now(), Source: "codex.configuration_recovery", ReadOnly: true}
				for _, kind := range []string{"transaction", "thread_unloaded", "work_clear", "original_history", "original_configuration", "read_stable"} {
					out.Checks = append(out.Checks, runtime.RecoveryCondition{Kind: kind, Status: "passed"})
				}
				return out, nil
			}
			recover := ExecutionRecoveryRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID, OperationID: applied.OperationID}
			switch mode {
			case "wrong-base":
				recover.BaseSnapshotID = "other"
			case "wrong-candidate":
				recover.PendingChangeID = "other"
			case "wrong-operation":
				recover.OperationID = "other"
			case "takeover":
				if _, e = a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: sid}); e != nil {
					t.Fatal(e)
				}
			}
			report, readErr := a.ReadExecutionRecovery(context.Background(), sid, recover)
			receipt, applyErr := a.Apply(Command{ID: "recover", Kind: "recover_execution", SessionID: sid, ExecutionRecovery: &recover})
			if mode != "recover" {
				if readErr == nil || applyErr == nil || f.recoveries.Load() != 0 {
					t.Fatal("unbound recovery accepted")
				}
				return
			}
			if readErr != nil || applyErr != nil || !report.Satisfied || report.Coverage != "native_thread_and_recorded_sources" {
				t.Fatalf("recovery rejected: %v %v %+v", readErr, applyErr, report)
			}
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == receipt.OperationID {
						return op.Status == "succeeded"
					}
				}
				return false
			})
			after := a.Snapshot().Sessions[0]
			attempt := after.ExecutionChangeAttempts[0].Change
			if digest(after.ExecutionSnapshot) != digest(before.Snapshot) || !after.Paused || after.ExecutionHold || attempt.Failure != nil || attempt.FailureDetail == "" || digest(attempt.Snapshot) != digest(staged.PendingChange.Snapshot) || f.recoveries.Load() != 1 {
				t.Fatal("recovery changed original or lost uncertain candidate")
			}
		})
	}
}
