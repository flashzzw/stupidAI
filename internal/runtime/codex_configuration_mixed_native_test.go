//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeConfigurationAdoptionMixed(t *testing.T) {
	for _, mode := range []string{"mixed", "mixed-cold", "mixed-cold-prepared", "mixed-native-loss", "mixed-retire-cold", "mixed-retire-bootstrap", "mixed-retire-final"} {
		t.Run(mode, func(t *testing.T) { nativeConfigurationAdoption(t, mode) })
	}
}

func nativeConfigurationMixedRetirement(t *testing.T, f *runtime.NativeApplicationFixture, application **app.Application, open func() *app.Application, write func(string, string), thread func(string) map[string]any, export func(string), apply func(app.Command) app.CommandReceipt, waitOp func(string), original app.Session, path, body string, samples map[string]any, check func(string, bool), boundary string) {
	t.Helper()
	recoveryBoundary := strings.TrimPrefix(boundary, "recovery-")
	if recoveryBoundary == boundary {
		recoveryBoundary = ""
	} else {
		boundary = "policy"
	}
	a := *application
	nativeConfigurationMixedHookTurn(t, f, a, apply, original, "enabled-pack", true, check)
	before := thread("retire_before")
	write(path, strings.Replace(body, `model_reasoning_effort="high"`, `model_reasoning_effort="medium"`, 1))
	request := app.ExecutionChangeRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, AdoptCodexConfigurationSources: true, Selections: []app.CapabilitySelection{{AssetID: "bundle", Mode: "disabled"}, {AssetID: "loose", Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(original.ID, request)
	if err != nil {
		t.Fatal("retire preview:", err)
	}
	samples["retire_preview"] = preview
	request.PreviewHash = preview.PreviewHash
	apply(app.Command{ID: "retire-stage", Kind: "stage_execution", SessionID: original.ID, ExecutionChange: &request})
	staged := a.Snapshot().Sessions[0]
	samples["retire_staged"] = staged
	export("retire_staged")
	check("retire_saves_original_mixed", reflect.DeepEqual(staged.ExecutionSnapshot, original.ExecutionSnapshot) && len(staged.Scope.PluginSelections) == 2)
	ref := app.ExecutionChangeRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
	r := apply(app.Command{ID: "retire-prepare", Kind: "prepare_execution", SessionID: original.ID, ExecutionChange: &ref})
	waitOp(r.OperationID)
	prepared := a.Snapshot().Sessions[0]
	samples["retire_prepared"] = prepared
	export("retire_prepared")
	// Reopen both the durable Application and its owned native process. The
	// existing test closures follow the same Application pointer after restart.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	*application = open()
	a = *application
	check("retirement_cold_candidate_preserved", reflect.DeepEqual(a.Snapshot().Sessions[0].PendingExecutionChange, prepared.PendingExecutionChange) && a.Snapshot().Sessions[0].ExecutionHold)
	var lost <-chan runtime.NativeConfigurationReply
	if boundary != "cold" {
		lost = f.DropPluginChangeReceipt(original.Scope.NativeID, boundary)
	}
	command := app.Command{ID: "retire-apply", Kind: "apply_execution", SessionID: original.ID, ExecutionChange: &ref}
	r = apply(command)
	samples["retire_apply_receipt"] = r
	if lost != nil {
		select {
		case reply := <-lost:
			samples["retire_lost_reply"] = reply
		case <-time.After(15 * time.Second):
			t.Fatal("retirement reply was not dropped")
		}
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == r.OperationID {
					return op.Status == "uncertain"
				}
			}
			return false
		})
		unknown := a.Snapshot().Sessions[0]
		samples["retire_uncertain"] = unknown
		export("retire_uncertain")
		check("retirement_unknown_preserves_original", reflect.DeepEqual(unknown.ExecutionSnapshot, original.ExecutionSnapshot) && unknown.PendingExecutionChange != nil && unknown.ExecutionHold)
		duplicate := apply(command)
		check("retirement_loss_idempotent", duplicate.OperationID == r.OperationID)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		*application = open()
		a = *application
		recovery := app.ExecutionRecoveryRequest{BaseSnapshotID: original.ExecutionSnapshot.ID, PendingChangeID: ref.PendingChangeID, OperationID: r.OperationID}
		blocked, err := a.ReadExecutionRecovery(context.Background(), original.ID, recovery)
		check("mixed_recovery_requires_original_source", err == nil && !blocked.Satisfied)
		write(path, body)
		ready, err := a.ReadExecutionRecovery(context.Background(), original.ID, recovery)
		if err != nil {
			t.Fatal(err)
		}
		samples["retire_recovery_check"] = ready
		check("mixed_recovery_ready", ready.Satisfied)
		beforeRecoveryCalls := f.CountMethod("thread/resume")
		var recoveryLost <-chan runtime.NativeConfigurationReply
		if recoveryBoundary != "" {
			recoveryLost = f.DropPluginChangeReceipt(original.Scope.NativeID, recoveryBoundary)
		}
		recoveryCommand := app.Command{ID: "retire-recover", Kind: "recover_execution", SessionID: original.ID, ExecutionRecovery: &recovery}
		recovered := apply(recoveryCommand)
		samples["retire_recovery_receipt"] = recovered
		if recoveryBoundary != "" {
			nativeConfigurationRecoveryLoss(t, f, application, open, thread, export, apply, original, prepared, recoveryCommand, recovered, recoveryLost, recoveryBoundary, beforeRecoveryCalls, samples, check)
			return
		}
		waitOp(recovered.OperationID)
		current := a.Snapshot().Sessions[0]
		samples["retire_recovered"] = current
		export("retire_recovered")
		check("mixed_recovery_restores_original", reflect.DeepEqual(current.Scope, original.Scope) && reflect.DeepEqual(current.ExecutionSnapshot, original.ExecutionSnapshot) && current.PendingExecutionChange == nil && current.Paused)
		after := thread("retire_after_recovery")
		check("mixed_recovery_actual_high", after["reasoningEffort"] == "high")
		check("mixed_recovery_history_preserved", reflect.DeepEqual(before["turns"], after["turns"]))
		check("failed_retirement_archived_once", len(current.ExecutionChangeAttempts) == 1)
		for _, e := range current.View.PluginEvidence {
			check("recovered_"+e.AssetID, e.Verified && e.Mode == "enabled")
		}
		nativeConfigurationMixedHookTurn(t, f, a, apply, current, "recovered-pack", true, check)
		return
	}
	waitOp(r.OperationID)
	current := a.Snapshot().Sessions[0]
	samples["retired"] = current
	export("retired")
	check("retired_same_id_and_snapshot", current.Scope.NativeID == original.Scope.NativeID && current.ExecutionSnapshot.ID == staged.PendingExecutionChange.Snapshot.ID && current.PendingExecutionChange == nil)
	after := thread("retire_after")
	check("retired_actual_medium", after["reasoningEffort"] == "medium")
	check("retired_history_unchanged", reflect.DeepEqual(before["turns"], after["turns"]))
	for _, e := range current.View.PluginEvidence {
		check("retired_"+e.AssetID, e.Verified && e.Mode == "disabled")
	}
	nativeConfigurationMixedHookTurn(t, f, a, apply, current, "disabled-pack", false, check)
}

func nativeConfigurationMixedHookTurn(t *testing.T, f *runtime.NativeApplicationFixture, a *app.Application, apply func(app.Command) app.CommandReceipt, session app.Session, label string, want bool, check func(string, bool)) {
	t.Helper()
	text := "OWNED CONFIGURATION MIXED " + label
	sent := apply(app.Command{ID: label, Kind: "send", SessionID: session.ID, Text: text})
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == sent.OperationID && op.Receipt.NativeTurn != "" {
				return f.HasTurnEvent("error", session.Scope.NativeID, op.Receipt.NativeTurn)
			}
		}
		return false
	})
	apply(app.Command{ID: label + "-cancel", Kind: "interrupt", SessionID: session.ID})
	waitNativeApplication(t, a, func(s app.Snapshot) bool { return s.Sessions[0].Status == "idle" && s.Sessions[0].View.ExecutionClear })
	body, err := os.ReadFile(filepath.Join(f.Root, "marker.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var row struct {
			Version string `json:"version"`
			Input   struct {
				Prompt string `json:"prompt"`
				ID     string `json:"session_id"`
			} `json:"input"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.Version == "pack" && row.Input.Prompt == text && row.Input.ID == session.Scope.NativeID {
			count++
		}
	}
	wantCount := 0
	if want {
		wantCount = 1
	}
	check(label+"_actual_hook_invocations", count == wantCount)
}
