package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type installedSourceRecoveryFixture struct {
	*applicationConfigurationFixture
	recoveries      atomic.Int32
	resolveRecovery atomic.Bool
	durableRecovery func(runtime.Intent)
	mode            string
	retired         chan runtime.Scope
}

func (f *installedSourceRecoveryFixture) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	v, err := f.applicationConfigurationFixture.Observe(ctx, scope)
	v.NativeStatus, v.ExecutionClear = "sdk_live", true
	v.SettingsGuard = &runtime.ClaudeSettingsGuardEvidence{Version: 1, State: "registered"}
	return v, err
}

func (f *installedSourceRecoveryFixture) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind != "recover_execution" {
		return f.applicationConfigurationFixture.Execute(ctx, in)
	}
	count := f.recoveries.Add(1)
	if f.durableRecovery != nil {
		f.durableRecovery(in)
	}
	switch f.mode {
	case "missing-receipt-binding":
		return runtime.Receipt{NativeID: in.Scope.NativeID}, nil
	case "wrong-receipt-binding":
		return runtime.Receipt{NativeID: in.Scope.NativeID, ClaudeInstalledSettings: in.Scope.ClaudeInstalledSettings}, nil
	case "not-sent":
		if count == 1 {
			return runtime.Receipt{}, &runtime.NotSent{Reason: "fixture: recovery was never sent"}
		}
	}
	return runtime.Receipt{}, fmt.Errorf("fixture: durable recovery reply lost")
}

func (f *installedSourceRecoveryFixture) RetireClaudeInstalledSettings(ctx context.Context, scope runtime.Scope) error {
	select {
	case f.retired <- scope:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *installedSourceRecoveryFixture) ReadConfigurationRecovery(_ context.Context, in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
	out := runtime.ConfigurationRecoveryCheck{Version: 1, NativeID: in.Scope.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline, At: now(), Source: "claude.configuration_recovery", ReadOnly: true}
	for _, kind := range []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"} {
		out.Checks = append(out.Checks, runtime.RecoveryCondition{Kind: kind, Status: "passed"})
	}
	if in.ClaudeInstalledRecovery != nil {
		out.InstalledRecovery = queryCopy(&in.ClaudeInstalledRecovery.Binding)
	}
	return out, nil
}

func (f *installedSourceRecoveryFixture) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if in.Kind == "apply_execution" && in.PreviousScope != nil && !slices.Equal(in.PreviousScope.ClaudeSettingsSources, in.Scope.ClaudeSettingsSources) {
		v := runtime.View{NativeID: in.Scope.NativeID, Status: "unknown", ConfigurationFailure: &runtime.ConfigurationFailure{Version: 1, Operation: in.ID, Baseline: in.ConfigurationBaseline, Stage: "open_candidate", Reason: "stage_failed", Cleanup: "confirmed", At: now()}}
		return runtime.Resolution{State: "unknown", View: v}, nil
	}
	if in.Kind == "recover_execution" {
		if !f.resolveRecovery.Load() {
			return runtime.Resolution{State: "unknown"}, nil
		}
		scope := in.Scope
		scope.ClaudeInstalledSettings = &in.ClaudeInstalledRecovery.Binding
		v, _ := f.Observe(ctx, scope)
		return runtime.Resolution{State: "finished", Status: "succeeded", View: v, Receipt: runtime.Receipt{NativeID: scope.NativeID, ClaudeInstalledSettings: scope.ClaudeInstalledSettings}, Evidence: runtime.Evidence{Source: "fixture.durable_recovery", Complete: true}}, nil
	}
	return f.applicationConfigurationFixture.Reconcile(ctx, in, receipt)
}

func TestInstalledSettingsRecoveryPublishesNewIdentityVersionAfterDurableConfirmation(t *testing.T) {
	installedSettingsRecoveryScenario(t, "lost-receipt")
}

func TestInstalledSettingsRecoveryReceiptAndRetirementBoundaries(t *testing.T) {
	for _, mode := range []string{"missing-receipt-binding", "wrong-receipt-binding", "not-sent"} {
		t.Run(mode, func(t *testing.T) { installedSettingsRecoveryScenario(t, mode) })
	}
}

func TestInstalledSettingsRecoverySubsequentChangeHasOwnVersion(t *testing.T) {
	installedSettingsRecoveryScenario(t, "subsequent-adoption")
}

func installedSettingsRecoveryScenario(t *testing.T, mode string) {
	t.Helper()
	a, base, id, _, _ := pluginSettingsPreviewFixture(t, "recovery-local")
	cfg := queryCopy(a.config)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f := &installedSourceRecoveryFixture{applicationConfigurationFixture: &applicationConfigurationFixture{fixtureRuntime: base}, mode: mode, retired: make(chan runtime.Scope, 10)}
	f.resolveApply.Store(true)
	a = openFixture(t, cfg, f)
	output := os.Getenv("NETCAP_INSTALLED_RECOVERY_REPORT")
	reports := map[string]any{"scenario": mode, "transport": "Application query DTOs with a controlled runtime; no native CLI"}
	frames := map[string]any{}
	reports["frames"] = frames
	capture := func(label string) {
		t.Helper()
		if output == "" {
			return
		}
		read, err := a.ReadSession(id, "", 60)
		if err != nil {
			t.Fatal(err)
		}
		frames[label] = map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: read}
	}
	if output != "" {
		t.Cleanup(func() {
			reports["passed"] = !t.Failed()
			body, err := json.MarshalIndent(reports, "", "  ")
			if err != nil || os.WriteFile(output, append(body, '\n'), 0600) != nil {
				t.Error("cannot write owned recovery report", err)
			}
		})
	}
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].Status == "idle" && a.Snapshot().Sessions[0].View.SettingsGuard != nil
	})
	if _, err := a.StageSessionPluginSettings(context.Background(), id, pluginSettingsStageRequest(t, a, id)); err != nil {
		t.Fatal(err)
	}
	apply := func(prefix string) CommandReceipt {
		t.Helper()
		s := a.Snapshot().Sessions[0]
		r := ExecutionChangeRequest{BaseSnapshotID: s.ExecutionSnapshot.ID, PendingChangeID: s.PendingExecutionChange.ID}
		var receipt CommandReceipt
		var err error
		waitTest(t, func() bool {
			receipt, err = a.Apply(Command{ID: prefix + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &r})
			return err == nil
		})
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID {
					return op.Status == "succeeded"
				}
			}
			return false
		})
		waitTest(t, func() bool {
			receipt, err = a.Apply(Command{ID: prefix + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &r})
			return err == nil
		})
		return receipt
	}
	apply("installed")
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].PendingExecutionChange == nil && a.Snapshot().Sessions[0].Status == "idle"
	})
	before := a.Snapshot().Sessions[0]
	reports["original"] = before
	local := filepath.Join(before.Scope.Cwd, ".claude/settings.local.json")
	original, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, local, `{"env":{"OWNED_LOCAL":"candidate"}}`)
	r := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, AdoptLocalSettings: true}
	preview, err := a.PreviewExecutionChange(id, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "adopt-save", Kind: "stage_execution", SessionID: id, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	failed := apply("adopt")
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].View.ConfigurationFailure != nil && a.Snapshot().Sessions[0].Status == "unknown"
	})
	writePluginMetadata(t, local, string(original))
	current := a.Snapshot().Sessions[0]
	request := ExecutionRecoveryRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: current.PendingExecutionChange.ID, OperationID: failed.OperationID}
	report, err := a.ReadExecutionRecovery(context.Background(), id, request)
	if err != nil || !report.Satisfied || report.InstalledSettings == nil {
		t.Fatalf("recovery observation: %+v %v", report, err)
	}
	if report.InstalledSettings.NextRevision != before.ExecutionSnapshot.Revision+1 || !slices.Contains(report.InstalledSettings.RestoredFiles, local) {
		t.Fatal("restored identity version was not explained")
	}
	if !reflect.DeepEqual(a.Snapshot().Sessions[0].ExecutionSnapshot, before.ExecutionSnapshot) || f.recoveries.Load() != 0 {
		t.Fatal("read-only recovery mutated execution")
	}
	if _, err := a.Apply(Command{ID: "unreviewed", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}); err == nil {
		t.Fatal("unreviewed restored identity was applied")
	}
	request.InstalledSourceVersion = report.InstalledSettings.SourceVersion
	writePluginMetadata(t, local, string(original))
	if _, err := a.Apply(Command{ID: "stale", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}); err == nil {
		t.Fatal("restoration after preview reused a stale identity")
	}
	report, err = a.ReadExecutionRecovery(context.Background(), id, request)
	if err != nil || !report.Satisfied || report.InstalledSettings == nil {
		t.Fatalf("updated recovery observation: %+v %v", report, err)
	}
	request.InstalledSourceVersion = report.InstalledSettings.SourceVersion
	reports["conditions"] = report
	capture("restored")
	dispatchEvidence := make(chan map[string]any, 2)
	f.durableRecovery = func(in runtime.Intent) {
		stored, err := a.store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		op, s := stored.Operations[in.ID], stored.Sessions[id]
		if op.Status != "dispatching" || in.ClaudeInstalledRecovery == nil || s.PendingExecutionChange.RecoverySnapshot == nil ||
			op.ExecutionSnapshotID != s.PendingExecutionChange.RecoverySnapshot.ID || s.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID || digest(op.Intent) != digest(in) {
			t.Error("new identity was not durably bound before recovery dispatch")
		}
		if output != "" {
			dispatchEvidence <- map[string]any{"session": s, "operation": op}
		}
	}
	command := Command{ID: "recover", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}
	reports["command"] = command
	receipt, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	expectedCalls := int32(1)
	if mode == "not-sent" {
		var abandoned runtime.Scope
		waitTest(t, func() bool {
			select {
			case abandoned = <-f.retired:
				return true
			default:
				return false
			}
		})
		unsent := a.Snapshot().Sessions[0]
		if !reflect.DeepEqual(unsent.ExecutionSnapshot, before.ExecutionSnapshot) || unsent.PendingExecutionChange.RecoverySnapshot != nil ||
			unsent.PendingExecutionChange.RecoveryOperationID != "" || unsent.PendingExecutionChange.Status != "applying" || !unsent.ExecutionHold || !unsent.Paused {
			t.Fatal("known unsent recovery changed the original or kept an active recovery plan")
		}
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID && (op.Status != "not_sent" || !reflect.DeepEqual(abandoned.ClaudeInstalledSettings, &op.Intent.ClaudeInstalledRecovery.Binding)) {
				t.Fatal("retirement did not select the exact abandoned recovery version")
			}
		}
		if abandoned.ClaudeInstalledSettings == nil || abandoned.ClaudeInstalledSettings.Artifact.ID == before.ExecutionSnapshot.ID ||
			abandoned.ClaudeInstalledSettings.Artifact.ID == current.PendingExecutionChange.Snapshot.ID {
			t.Fatal("retirement selected a current, historical or failed candidate file")
		}
		if repeated, err := a.Apply(command); err != nil || repeated.OperationID != receipt.OperationID || f.recoveries.Load() != 1 {
			t.Fatal("retry of the unsent command dispatched recovery again", err)
		}
		// Only a new explicit command creates another planned recovery version.
		command.ID = "recover-after-not-sent"
		receipt, err = a.Apply(command)
		if err != nil {
			t.Fatal(err)
		}
		expectedCalls = 2
		pending := a.Snapshot().Sessions[0].PendingExecutionChange
		if pending.RecoverySnapshot == nil || pending.RecoverySnapshot.ID == abandoned.ClaudeInstalledSettings.Artifact.ID {
			t.Fatal("new recovery reused the retired version identity")
		}
	}
	reports["receipt"] = receipt
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID {
				if op.Status == "uncertain" && strings.Contains(mode, "receipt-binding") && !strings.Contains(op.Error, "缺少匹配的安装设置版本") {
					t.Error("incomplete recovery receipt was not diagnosed")
				}
				return op.Status == "uncertain"
			}
		}
		return false
	})
	unknown := a.Snapshot().Sessions[0]
	capture("recovering")
	if output != "" {
		dispatches := []map[string]any{}
		for range expectedCalls {
			select {
			case evidence := <-dispatchEvidence:
				dispatches = append(dispatches, evidence)
			default:
				t.Fatal("durable dispatch evidence missing")
			}
		}
		reports["durable_dispatches"] = dispatches
	}
	if unknown.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID || unknown.PendingExecutionChange.RecoverySnapshot == nil || !unknown.ExecutionHold {
		t.Fatal("lost receipt promoted or lost the recovery version")
	}
	recoveryID := unknown.PendingExecutionChange.RecoverySnapshot.ID
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.resolveRecovery.Store(true)
	a = openFixture(t, cfg, f)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange == nil })
	after := a.Snapshot().Sessions[0]
	reports["recovered"] = after
	capture("recovered")
	if output != "" {
		stored, err := a.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		reports["durable_recovered"] = map[string]any{"session": stored.Sessions[id], "operation": stored.Operations[receipt.OperationID], "failed": stored.Operations[failed.OperationID]}
	}
	if after.Scope.NativeID != before.Scope.NativeID || after.ExecutionSnapshot.ID != recoveryID || after.ExecutionSnapshot.RestoredFrom != before.ExecutionSnapshot.ID ||
		!reflect.DeepEqual(after.ExecutionHistory[len(after.ExecutionHistory)-1], before.ExecutionSnapshot) || len(after.ExecutionChangeAttempts) != 1 ||
		after.ExecutionChangeAttempts[0].Change.Snapshot.ID != current.PendingExecutionChange.Snapshot.ID || f.recoveries.Load() != expectedCalls || !after.Paused {
		t.Fatal("recovery lost original identity/history, failed candidate, new source version or no-replay")
	}
	if err := verifyExecutionBinding(after); err != nil {
		t.Fatal(err)
	}
	if repeated, err := a.Apply(command); err != nil || repeated.OperationID != receipt.OperationID {
		t.Fatal("recovery command retry changed operation", err)
	}
	if mode == "subsequent-adoption" {
		writePluginMetadata(t, local, `{"env":{"OWNED_LOCAL":"next-ordinary-change"}}`)
		r := ExecutionChangeRequest{BaseSnapshotID: after.ExecutionSnapshot.ID, AdoptLocalSettings: true}
		preview, err := a.PreviewExecutionChange(id, r)
		if err != nil {
			t.Fatal(err)
		}
		r.PreviewHash = preview.PreviewHash
		if _, err := a.Apply(Command{ID: "next-change", Kind: "stage_execution", SessionID: id, ExecutionChange: &r}); err != nil {
			t.Fatal(err)
		}
		staged := a.Snapshot().Sessions[0]
		if staged.PendingExecutionChange.Snapshot.RestoredFrom != "" ||
			staged.PendingExecutionChange.Snapshot.PreviousID != after.ExecutionSnapshot.ID ||
			!reflect.DeepEqual(staged.ExecutionSnapshot, after.ExecutionSnapshot) {
			t.Fatal("ordinary change inherited a recovery marker or rewrote the recovered snapshot")
		}
	}
	select {
	case <-f.retired:
		t.Fatal("a referenced recovery, original or failed candidate file was retired")
	default:
	}
}
