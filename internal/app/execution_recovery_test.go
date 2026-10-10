package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"codex-netcap/internal/runtime"
)

type recoveryFixture struct {
	*applicationConfigurationFixture
	mode       string
	recoveries atomic.Int32
	entered    chan struct{}
	release    chan struct{}
	read       func(runtime.Intent) (runtime.ConfigurationRecoveryCheck, error)
}

func (f *recoveryFixture) Observe(ctx context.Context, s runtime.Scope) (runtime.View, error) {
	v, e := f.applicationConfigurationFixture.Observe(ctx, s)
	v.NativeStatus = "sdk_live"
	v.Evidence.Complete = true
	v.ExecutionClear = true
	v.SettingsGuard = &runtime.ClaudeSettingsGuardEvidence{Version: 1, State: "registered"}
	return v, e
}
func (f *recoveryFixture) ReadConfigurationRecovery(_ context.Context, in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
	return f.read(in)
}

func (f *recoveryFixture) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind != "recover_execution" {
		return f.applicationConfigurationFixture.Execute(ctx, in)
	}
	f.recoveries.Add(1)
	if f.entered != nil {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return runtime.Receipt{}, ctx.Err()
		}
	}
	if f.mode == "explicit-not-sent" {
		return runtime.Receipt{}, &runtime.NotSent{Reason: "fixture not sent"}
	}
	return runtime.Receipt{NativeID: in.Scope.NativeID}, nil
}
func (f *recoveryFixture) Reconcile(ctx context.Context, in runtime.Intent, receipt runtime.Receipt) (runtime.Resolution, error) {
	if in.Kind == "apply_execution" {
		v := runtime.View{NativeID: in.Scope.NativeID, Status: "unknown", ConfigurationFailure: &runtime.ConfigurationFailure{Version: 1, Operation: in.ID, Baseline: in.ConfigurationBaseline, Cleanup: "confirmed", Stage: "open_candidate", Reason: "stage_failed", At: now()}}
		return runtime.Resolution{State: "unknown", View: v}, nil
	}
	if in.Kind == "recover_execution" {
		if f.mode == "explicit-unknown" {
			return runtime.Resolution{State: "unknown"}, nil
		}
		v, _ := f.Observe(ctx, in.Scope)
		return runtime.Resolution{State: "finished", Status: "succeeded", View: v, Receipt: receipt, Evidence: runtime.Evidence{Complete: true}}, nil
	}
	return f.applicationConfigurationFixture.Reconcile(ctx, in, receipt)
}

func TestExecutionRecoveryCheckBindingsAndReadOnly(t *testing.T) {
	for _, mode := range []string{"explicit-success", "explicit-restart-prepared", "explicit-finish-drift", "explicit-unknown", "explicit-not-sent", "explicit-takeover", "explicit-dispatch-drift", "read-only", "file-drift", "base", "pending", "operation", "wrong-native", "wrong-operation", "wrong-baseline", "wrong-source", "unknown-version", "not-read-only", "missing-check", "duplicate-check", "unknown-status", "ownership-change", "source-change"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			path := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
			writePluginMetadata(t, path, `{}`)
			f := &recoveryFixture{applicationConfigurationFixture: &applicationConfigurationFixture{fixtureRuntime: newFixture()}, mode: mode}
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool {
				return a.Snapshot().Sessions[0].Status == "idle" && a.Snapshot().Sessions[0].View.SettingsGuard != nil
			})
			before, _ := a.ReadExecution(id)
			writePluginMetadata(t, path, `{"env":{"NETCAP_TEST":"v2"}}`)
			r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptLocalSettings: true}
			preview, e := a.PreviewExecutionChange(id, r)
			if e != nil {
				t.Fatal(e)
			}
			r.PreviewHash = preview.PreviewHash
			if _, e = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &r}); e != nil {
				t.Fatal(e)
			}
			staged, _ := a.ReadExecution(id)
			ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
			var prep CommandReceipt
			waitTest(t, func() bool {
				prep, e = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref})
				return e == nil
			})
			waitTest(t, func() bool {
				for _, o := range a.Snapshot().Operations {
					if o.ID == prep.OperationID {
						return o.Status == "succeeded"
					}
				}
				return false
			})
			var applied CommandReceipt
			waitTest(t, func() bool {
				applied, e = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref})
				return e == nil
			})
			waitTest(t, func() bool {
				for _, o := range a.Snapshot().Operations {
					if o.ID == applied.OperationID {
						return o.Status == "uncertain"
					}
				}
				return false
			})
			if mode != "file-drift" {
				writePluginMetadata(t, path, `{}`)
			}
			request := ExecutionRecoveryRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID, OperationID: applied.OperationID}
			switch mode {
			case "base":
				request.BaseSnapshotID = "other"
			case "pending":
				request.PendingChangeID = "other"
			case "operation":
				request.OperationID = "other"
			}
			f.read = func(in runtime.Intent) (runtime.ConfigurationRecoveryCheck, error) {
				out := runtime.ConfigurationRecoveryCheck{Version: 1, NativeID: in.Scope.NativeID, Operation: in.ID, Baseline: in.ConfigurationBaseline, At: now(), Source: "claude.configuration_recovery", ReadOnly: true}
				for _, kind := range []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"} {
					out.Checks = append(out.Checks, runtime.RecoveryCondition{Kind: kind, Status: "passed"})
				}
				switch mode {
				case "wrong-native":
					out.NativeID = "other"
				case "wrong-operation":
					out.Operation = "other"
				case "wrong-baseline":
					out.Baseline = "other"
				case "wrong-source":
					out.Source = "unverified"
				case "unknown-version":
					out.Version = 2
				case "not-read-only":
					out.ReadOnly = false
				case "missing-check":
					out.Checks = out.Checks[:5]
				case "duplicate-check":
					out.Checks[5] = out.Checks[0]
				case "unknown-status":
					out.Checks[0].Status = "probably"
				case "ownership-change":
					if _, e := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); e != nil {
						t.Fatal(e)
					}
				case "source-change":
					writePluginMetadata(t, path, `{"env":{"NETCAP_TEST":"v3"}}`)
				}
				return out, nil
			}
			out, e := a.ReadExecutionRecovery(context.Background(), id, request)
			valid := strings.HasPrefix(mode, "explicit-") || mode == "read-only" || mode == "file-drift"
			if (e == nil) != valid {
				t.Fatalf("%s: %v", mode, e)
			}
			if valid && out.Satisfied != (mode != "file-drift") {
				t.Fatal("original file drift was not retained")
			}
			after, _ := a.ReadExecution(id)
			if digest(after.Snapshot) != digest(before.Snapshot) || after.PendingChange == nil || after.PendingChange.ID != staged.PendingChange.ID || f.sends.Load() != 0 || f.applications.Load() != 1 {
				t.Fatal("recovery check changed execution")
			}
			if !strings.HasPrefix(mode, "explicit-") {
				return
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].View.ConfigurationFailure != nil })
			if mode == "explicit-takeover" || mode == "explicit-finish-drift" {
				f.entered = make(chan struct{})
				f.release = make(chan struct{})
			}
			if mode == "explicit-dispatch-drift" {
				a.mu.Lock()
				a.observing[id] = true
				a.mu.Unlock()
			}
			cmd := Command{ID: "recover", Kind: "recover_execution", SessionID: id, ExecutionRecovery: &request}
			receipt, e := a.Apply(cmd)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "explicit-dispatch-drift" {
				writePluginMetadata(t, path, `{"env":{"NETCAP_TEST":"changed"}}`)
				a.mu.Lock()
				delete(a.observing, id)
				a.mu.Unlock()
			}
			if mode == "explicit-takeover" {
				<-f.entered
				if _, e = a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); e != nil {
					t.Fatal(e)
				}
				close(f.release)
			}
			if mode == "explicit-finish-drift" {
				<-f.entered
				writePluginMetadata(t, path, `{"env":{"NETCAP_TEST":"changed"}}`)
				close(f.release)
			}
			expected := "succeeded"
			if mode == "explicit-unknown" || mode == "explicit-finish-drift" {
				expected = "uncertain"
			}
			if mode == "explicit-not-sent" || mode == "explicit-dispatch-drift" {
				expected = "not_sent"
			}
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == receipt.OperationID {
						return op.Status == expected
					}
				}
				return false
			})
			repeated, e := a.Apply(cmd)
			if e != nil || repeated.OperationID != receipt.OperationID {
				t.Fatal("command replay lost identity")
			}
			current := a.Snapshot().Sessions[0]
			if !current.Paused || digest(current.ExecutionSnapshot) != digest(before.Snapshot) {
				t.Fatal("recovery changed original snapshot or resumed automation")
			}
			if expected == "succeeded" {
				if current.PendingExecutionChange != nil || len(current.ExecutionChangeAttempts) != 1 || current.ExecutionHold {
					t.Fatal("missing failed attempt archive")
				}
			} else {
				p := current.PendingExecutionChange
				if p == nil || !current.ExecutionHold || p.OperationID != applied.OperationID {
					t.Fatal("lost failed candidate")
				}
				if expected == "not_sent" {
					if p.RecoveryOperationID != "" || p.Status != "applying" {
						t.Fatal("known unsent recovery not released")
					}
				} else {
					if p.RecoveryOperationID != receipt.OperationID || p.Status != "recovering" {
						t.Fatal("unknown recovery released")
					}
				}
			}
			if mode == "explicit-restart-prepared" {
				// Restore a synthetic durable prepared checkpoint. There is no
				// native dispatch from this checkpoint; Open must release it.
				a.mu.Lock()
				next := clone(a.state)
				op := next.Operations[receipt.OperationID]
				op.Status = "prepared"
				next.Operations[op.ID] = op
				previous := next.Operations[applied.OperationID]
				previous.Status = "uncertain"
				next.Operations[previous.ID] = previous
				session := next.Sessions[id]
				session.PendingExecutionChange = queryCopy(&session.ExecutionChangeAttempts[0].Change)
				session.PendingExecutionChange.Status = "recovering"
				session.ExecutionHold = true
				session.ExecutionChangeAttempts = nil
				next.Sessions[id] = session
				e = a.commit(next, "fixture.prepared", id)
				a.mu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				a = openFixture(t, c, f)
				restored := a.Snapshot().Sessions[0]
				if restored.PendingExecutionChange.RecoveryOperationID != "" || restored.PendingExecutionChange.Status != "applying" || !restored.ExecutionHold || !restored.Paused {
					t.Fatal("prepared recovery restart did not release only recovery")
				}
			}
			if mode == "explicit-takeover" && current.ControlMode != "human_takeover" {
				t.Fatal("overwrote input ownership")
			}
			expectedCalls := int32(1)
			if mode == "explicit-dispatch-drift" {
				expectedCalls = 0
			}
			if f.recoveries.Load() != expectedCalls {
				t.Fatal(fmt.Sprintf("unexpected recovery dispatch count %d", f.recoveries.Load()))
			}
		})
	}
}
