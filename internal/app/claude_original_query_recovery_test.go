package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type originalQueryRecoveryAppFixture struct {
	*applicationConfigurationFixture
	failApply       atomic.Bool
	failure         atomic.Pointer[runtime.ConfigurationFailure]
	recoveries      atomic.Int32
	resolutionMode  atomic.Int32
	badCheck        atomic.Bool
	durableRecovery func(runtime.Intent)
	mode            string
}

func (f *originalQueryRecoveryAppFixture) Observe(ctx context.Context, scope runtime.Scope) (runtime.View, error) {
	v, err := f.applicationConfigurationFixture.Observe(ctx, scope)
	v.NativeStatus, v.ExecutionClear = "sdk_live", true
	v.SettingsGuard = &runtime.ClaudeSettingsGuardEvidence{Version: 1, State: "registered"}
	return v, err
}

func (f *originalQueryRecoveryAppFixture) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind == "prepare_execution" {
		f.preparations.Add(1)
		return runtime.Receipt{NativeID: in.Scope.NativeID, ConfigurationBaseline: strings.Repeat("a", 64)}, nil
	}
	if in.Kind == "apply_execution" && f.failApply.Load() {
		f.failure.Store(&runtime.ConfigurationFailure{Version: 1, Operation: in.ID, Baseline: in.ConfigurationBaseline, Stage: "open_original", Reason: "stage_failed", Cleanup: "confirmed", At: now()})
	}
	if in.Kind != "recover_execution" {
		return f.applicationConfigurationFixture.Execute(ctx, in)
	}
	if !runtime.ClaudeOriginalQueryRecoveryMatchesFailure(in.ClaudeOriginalQueryRecovery, f.failure.Load()) {
		return runtime.Receipt{}, &runtime.NotSent{Reason: "fixture: explicit original-query mode missing or mismatched"}
	}
	count := f.recoveries.Add(1)
	if f.durableRecovery != nil {
		f.durableRecovery(in)
	}
	if f.mode == "not-sent" && count == 1 {
		return runtime.Receipt{}, &runtime.NotSent{Reason: "fixture: original recovery never started"}
	}
	receipt := runtime.Receipt{NativeID: in.Scope.NativeID, ClaudeOriginalQueryRecovery: queryCopy(in.ClaudeOriginalQueryRecovery)}
	if f.mode == "missing-receipt-mode" {
		receipt.ClaudeOriginalQueryRecovery = nil
		return receipt, nil
	}
	if f.mode == "wrong-receipt-mode" {
		receipt.ClaudeOriginalQueryRecovery.Failure.At = "2026-10-10T00:00:00Z"
		return receipt, nil
	}
	return runtime.Receipt{}, fmt.Errorf("fixture: original recovery reply lost")
}

func (f *originalQueryRecoveryAppFixture) ReadConfigurationRecovery(_ context.Context, in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
	failure := *f.failure.Load()
	if f.badCheck.Load() {
		failure.At = "2026-10-10T00:00:00Z"
	}
	out := runtime.ConfigurationRecoveryCheck{Version: 1, NativeID: in.Scope.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline, At: now(), Source: "claude.configuration_recovery", ReadOnly: true,
		OriginalQueryRetry: &runtime.ClaudeOriginalQueryRecovery{Version: 1, Failure: failure}}
	for _, kind := range []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"} {
		out.Checks = append(out.Checks, runtime.RecoveryCondition{Kind: kind, Status: "passed"})
	}
	return out, nil
}

func (f *originalQueryRecoveryAppFixture) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if in.Kind == "apply_execution" {
		if failure := f.failure.Load(); failure != nil && failure.Operation == in.ID {
			return runtime.Resolution{State: "unknown", View: runtime.View{NativeID: in.Scope.NativeID, Status: "unknown", ConfigurationFailure: queryCopy(failure)}}, nil
		}
	}
	if in.Kind != "recover_execution" {
		return f.applicationConfigurationFixture.Reconcile(ctx, in, receipt)
	}
	mode := f.resolutionMode.Load()
	if mode == 0 {
		return runtime.Resolution{State: "unknown"}, nil
	}
	v, _ := f.Observe(ctx, in.Scope)
	out := runtime.Receipt{NativeID: in.Scope.NativeID, ClaudeOriginalQueryRecovery: queryCopy(in.ClaudeOriginalQueryRecovery)}
	if mode == 1 {
		out.ClaudeOriginalQueryRecovery = nil
	}
	if mode == 2 {
		out.ClaudeOriginalQueryRecovery.Failure.Operation = "another"
	}
	return runtime.Resolution{State: "finished", Status: "succeeded", View: v, Receipt: out, Evidence: runtime.Evidence{Source: "fixture.durable_original_recovery", Complete: true}}, nil
}

func TestClaudeOriginalQueryRecoveryApplicationDurability(t *testing.T) {
	for _, mode := range []string{"inherited", "installed", "missing-receipt-mode", "wrong-receipt-mode", "not-sent", "missing-durable-mode", "wrong-durable-mode"} {
		t.Run(mode, func(t *testing.T) { originalQueryRecoveryApplicationScenario(t, mode) })
	}
}

func originalQueryRecoveryApplicationScenario(t *testing.T, mode string) {
	t.Helper()
	a, base, id, _, _ := pluginSettingsPreviewFixture(t, "recovery-local")
	cfg := queryCopy(a.config)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if mode == "installed" {
		source := filepath.Join(t.TempDir(), "plain-mcp.json")
		writePluginMetadata(t, source, `{"mcpServers":{"plain":{"command":"fixture"}}}`)
		cfg.CapabilityAssets = append(cfg.CapabilityAssets, CapabilityAsset{ID: "owned-plain", Name: "Owned plain MCP", Kind: "mcp", Version: "1", Source: source, Backends: []string{"claude"}})
	}
	f := &originalQueryRecoveryAppFixture{applicationConfigurationFixture: &applicationConfigurationFixture{fixtureRuntime: base}, mode: mode}
	f.resolveApply.Store(true)
	a = openFixture(t, cfg, f)
	reports := map[string]any{"mode": mode, "transport": "Production Application/SQLite query DTOs with controlled Runtime I/O; not native CLI or Handler evidence"}
	frames := map[string]any{}
	reports["frames"] = frames
	output := os.Getenv("NETCAP_ORIGINAL_RECOVERY_APP_REPORTS")
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
			if err != nil || os.WriteFile(filepath.Join(output, mode+".json"), append(body, '\n'), 0600) != nil {
				t.Error("cannot write original-query recovery report", err)
			}
		})
	}
	apply := func(prefix string) CommandReceipt {
		t.Helper()
		s := a.Snapshot().Sessions[0]
		ref := ExecutionChangeRequest{BaseSnapshotID: s.ExecutionSnapshot.ID, PendingChangeID: s.PendingExecutionChange.ID}
		var receipt CommandReceipt
		var err error
		waitTest(t, func() bool {
			receipt, err = a.Apply(Command{ID: prefix + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref})
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
			receipt, err = a.Apply(Command{ID: prefix + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref})
			return err == nil
		})
		return receipt
	}
	stage := func(prefix, selection string) {
		t.Helper()
		request := pluginSettingsStageRequest(t, a, id)
		request.CommandID, request.Mode = prefix+"-stage", selection
		if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil {
			preview, previewErr := a.PreviewSessionPluginSettings(context.Background(), id, request.PluginSettingsPreviewRequest)
			reports["stage_"+prefix] = preview
			t.Fatalf("stage %s: %v; preview: %v, problems: %+v", prefix, err, previewErr, preview.Problems)
		}
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	if mode == "installed" {
		stage("initial", "disabled")
		apply("initial")
		waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange == nil })
	}
	before := a.Snapshot().Sessions[0]
	reports["original"] = before
	if mode == "installed" {
		// An ordinary resource change preserves the installed selection. The
		// controlled Runtime does not publish native settings files, so it cannot
		// supply the file-consumer receipt required by a second native preview.
		request := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []CapabilitySelection{{AssetID: "owned-plain", Mode: "enabled"}}}
		preview, err := a.PreviewExecutionChange(id, request)
		if err != nil {
			t.Fatal(err)
		}
		request.PreviewHash = preview.PreviewHash
		if _, err := a.Apply(Command{ID: "candidate-stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
			t.Fatal(err)
		}
	} else {
		stage("candidate", "disabled")
	}
	f.failApply.Store(true)
	failed := apply("candidate")
	waitTest(t, func() bool {
		return a.Snapshot().Sessions[0].View.ConfigurationFailure != nil && a.Snapshot().Sessions[0].Status == "unknown"
	})
	current := a.Snapshot().Sessions[0]
	request := ExecutionRecoveryRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: current.PendingExecutionChange.ID, OperationID: failed.OperationID}
	f.badCheck.Store(true)
	if _, err := a.ReadExecutionRecovery(context.Background(), id, request); err == nil {
		t.Fatal("mismatched original failure was shown as recoverable")
	}
	f.badCheck.Store(false)
	check, err := a.ReadExecutionRecovery(context.Background(), id, request)
	if err != nil || !check.Satisfied || !check.OriginalQueryRetry || check.Native.OriginalQueryRetry == nil || check.InstalledSettings != nil {
		t.Fatalf("original recovery conditions: %+v %v", check, err)
	}
	if !reflect.DeepEqual(before.ExecutionSnapshot, a.Snapshot().Sessions[0].ExecutionSnapshot) || f.recoveries.Load() != 0 {
		t.Fatal("read-only check changed the original execution")
	}
	reports["conditions"] = check
	capture("failed")
	// A stale client omits the mode. Runtime rejects before an original query
	// can start; the original transaction remains available for explicit review.
	missing, err := a.Apply(Command{ID: "missing-mode", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == missing.OperationID {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	if f.recoveries.Load() != 0 || a.Snapshot().Sessions[0].PendingExecutionChange.RecoveryOperationID != "" {
		t.Fatal("missing mode started or retained a recovery")
	}
	dispatches := make(chan map[string]any, 2)
	f.durableRecovery = func(in runtime.Intent) {
		stored, err := a.store.Load()
		if err != nil {
			t.Error(err)
			return
		}
		op, session := stored.Operations[in.ID], stored.Sessions[id]
		if op.Status != "dispatching" || digest(op.Intent) != digest(in) || !runtime.ClaudeOriginalQueryRecoveryMatchesFailure(in.ClaudeOriginalQueryRecovery, session.PendingExecutionChange.Failure) ||
			session.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID || session.PendingExecutionChange.RecoverySnapshot != nil || !session.Paused || !session.ExecutionHold {
			t.Error("explicit mode and original failure were not durable before query recovery")
		}
		if output != "" {
			dispatches <- map[string]any{"session": session, "operation": op}
		}
	}
	request.OriginalQueryRetry = true
	command := Command{ID: "explicit-recover", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}
	receipt, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	expected := int32(1)
	if mode == "not-sent" {
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID {
					return op.Status == "not_sent"
				}
			}
			return false
		})
		if retry, err := a.Apply(command); err != nil || retry != receipt || f.recoveries.Load() != 1 {
			t.Fatal("unsent command replayed", err)
		}
		command.ID = "explicit-recover-again"
		receipt, err = a.Apply(command)
		if err != nil {
			t.Fatal(err)
		}
		expected = 2
	}
	reports["command"], reports["receipt"] = command, receipt
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == receipt.OperationID {
				return op.Status == "uncertain"
			}
		}
		return false
	})
	unknown := a.Snapshot().Sessions[0]
	if !reflect.DeepEqual(unknown.ExecutionSnapshot, before.ExecutionSnapshot) || unknown.PendingExecutionChange.RecoverySnapshot != nil || !unknown.ExecutionHold || !unknown.Paused {
		t.Fatal("unknown recovery promoted or replaced original version")
	}
	capture("recovering")
	if output != "" {
		saved := []map[string]any{}
		for range expected {
			saved = append(saved, <-dispatches)
		}
		reports["durable_dispatches"] = saved
	}
	if _, err := a.Apply(Command{ID: "repeat-unknown", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}); err == nil {
		t.Fatal("unknown recovery admitted another operation")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, cfg, f)
	if mode == "missing-durable-mode" || mode == "wrong-durable-mode" {
		f.resolutionMode.Store(1)
		if mode == "wrong-durable-mode" {
			f.resolutionMode.Store(2)
		}
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID {
					for _, evidence := range op.Evidence {
						if strings.Contains(evidence.Detail, "恢复方式不一致") {
							return true
						}
					}
				}
			}
			return false
		})
		if a.Snapshot().Sessions[0].PendingExecutionChange == nil {
			t.Fatal("invalid durable mode completed recovery")
		}
	}
	f.resolutionMode.Store(3)
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].PendingExecutionChange == nil })
	after := a.Snapshot().Sessions[0]
	reports["recovered"] = after
	capture("recovered")
	if !reflect.DeepEqual(before.Scope, after.Scope) || !reflect.DeepEqual(before.ExecutionSnapshot, after.ExecutionSnapshot) || !reflect.DeepEqual(before.ExecutionHistory, after.ExecutionHistory) ||
		len(after.ExecutionChangeAttempts) != 1 || after.ExecutionChangeAttempts[0].Change.Snapshot.ID != current.PendingExecutionChange.Snapshot.ID || !after.Paused || after.ExecutionHold || f.recoveries.Load() != expected {
		t.Fatal("recovery lost the original snapshot, history, failed candidate or no-replay behavior")
	}
	if err := verifyExecutionBinding(after); err != nil {
		t.Fatal(err)
	}
	if retry, err := a.Apply(command); err != nil || retry != receipt || f.recoveries.Load() != expected {
		t.Fatal("completed recovery command replayed", err)
	}
	stored, err := a.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	reports["durable_recovered"] = map[string]any{"session": stored.Sessions[id], "operation": stored.Operations[receipt.OperationID], "failed": stored.Operations[failed.OperationID]}
}
