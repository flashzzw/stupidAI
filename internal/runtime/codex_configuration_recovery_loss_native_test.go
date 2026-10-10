//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeConfigurationPolicyReplyLoss(t *testing.T) {
	for _, boundary := range []string{"policy", "unsubscribe"} {
		t.Run(boundary, func(t *testing.T) { nativeConfigurationAdoption(t, "mixed-retire-"+boundary) })
	}
}

func TestCodexNativeConfigurationRecoveryReplyLoss(t *testing.T) {
	for _, boundary := range []string{"bootstrap", "policy", "unsubscribe", "final"} {
		t.Run(boundary, func(t *testing.T) { nativeConfigurationAdoption(t, "mixed-retire-recovery-"+boundary) })
	}
}

func nativeConfigurationRecoveryLoss(t *testing.T, f *runtime.NativeApplicationFixture, application **app.Application, open func() *app.Application, thread func(string) map[string]any, export func(string), apply func(app.Command) app.CommandReceipt, original, prepared app.Session, command app.Command, receipt app.CommandReceipt, lost <-chan runtime.NativeConfigurationReply, boundary string, beforeCalls int, samples map[string]any, check func(string, bool)) {
	t.Helper()
	a := *application
	select {
	case reply := <-lost:
		samples["recovery_lost_reply"] = reply
	case <-time.After(15 * time.Second):
		t.Fatal("recovery reply was not dropped")
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == receipt.OperationID {
				if op.Status == "succeeded" || op.Status == "failed" || op.Status == "not_sent" {
					t.Fatalf("recovery result misclassified: %s %s", op.Status, op.Error)
				}
				return op.Status == "uncertain"
			}
		}
		return false
	})
	lostState := a.Snapshot().Sessions[0]
	samples["recovery_uncertain"] = lostState
	export("recovery_uncertain")
	check("double_loss_preserves_original", reflect.DeepEqual(lostState.Scope, original.Scope) && reflect.DeepEqual(lostState.ExecutionSnapshot, original.ExecutionSnapshot) && reflect.DeepEqual(lostState.ExecutionHistory, original.ExecutionHistory))
	check("double_loss_preserves_candidate", reflect.DeepEqual(lostState.PendingExecutionChange.Snapshot, prepared.PendingExecutionChange.Snapshot))
	check("double_loss_keeps_distinct_operations", lostState.PendingExecutionChange.OperationID == command.ExecutionRecovery.OperationID && lostState.PendingExecutionChange.RecoveryOperationID == receipt.OperationID && command.ExecutionRecovery.OperationID != receipt.OperationID)
	check("double_loss_holds_without_archive", lostState.PendingExecutionChange.Status == "recovering" && lostState.ExecutionHold && lostState.Paused && len(lostState.ExecutionChangeAttempts) == 0)
	expectedCalls := 1
	if boundary == "final" {
		expectedCalls = 2
	}
	check("double_loss_expected_recovery_dispatch", f.CountMethod("thread/resume") == beforeCalls+expectedCalls)
	duplicate := apply(command)
	check("double_loss_recovery_idempotent", duplicate.OperationID == receipt.OperationID)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	*application = open()
	a = *application
	waitNativeApplication(t, a, func(s app.Snapshot) bool { return s.Sessions[0].Status == "unknown" })
	reopened := a.Snapshot().Sessions[0]
	samples["recovery_reopened"] = reopened
	export("recovery_reopened")
	check("double_loss_durable_pending", reflect.DeepEqual(reopened.PendingExecutionChange, lostState.PendingExecutionChange) && reopened.ExecutionHold && reopened.Paused)
	check("double_loss_durable_original", reflect.DeepEqual(reopened.Scope, original.Scope) && reflect.DeepEqual(reopened.ExecutionSnapshot, original.ExecutionSnapshot) && reflect.DeepEqual(reopened.ExecutionHistory, original.ExecutionHistory) && len(reopened.ExecutionChangeAttempts) == 0)
	operations := []app.Operation{}
	for _, op := range a.Snapshot().Operations {
		if op.ID == receipt.OperationID || op.ID == command.ExecutionRecovery.OperationID {
			operations = append(operations, op)
			check("uncertain_"+op.Intent.Kind, op.Status == "uncertain" && op.Receipt.ConfigurationRecovery == nil)
		}
	}
	samples["recovery_operations"] = operations
	check("two_durable_operations", len(operations) == 2)
	after := thread("after_double_loss_restart")
	before := samples["retire_before"].(map[string]any)["thread"].(map[string]any)
	check("double_loss_same_id_history", after["id"] == before["id"] && reflect.DeepEqual(after["turns"], before["turns"]))
	check("double_loss_thread_not_loaded", after["status"].(map[string]any)["type"] == "notLoaded")
	_, err := a.ReadExecutionRecovery(context.Background(), original.ID, *command.ExecutionRecovery)
	check("double_loss_cannot_reuse_old_gate", err != nil)
	ref := app.ExecutionChangeRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, PendingChangeID: reopened.PendingExecutionChange.ID}
	for _, kind := range []string{"send", "resume", "prepare_execution", "apply_execution", "discard_execution", "recover_execution"} {
		cmd := app.Command{ID: "double-loss-reject-" + kind, Kind: kind, SessionID: original.ID}
		if kind == "send" {
			cmd.Text = "MUST NOT SEND"
		}
		if kind == "prepare_execution" || kind == "apply_execution" || kind == "discard_execution" {
			cmd.ExecutionChange = &ref
		}
		if kind == "recover_execution" {
			cmd.ExecutionRecovery = command.ExecutionRecovery
		}
		_, err := a.Apply(cmd)
		check("double_loss_rejects_"+kind, err != nil)
	}
	duplicate = apply(command)
	check("double_loss_reopened_idempotent", duplicate.OperationID == receipt.OperationID)
	check("double_loss_no_replay", f.CountMethod("thread/resume") == beforeCalls+expectedCalls)
}
