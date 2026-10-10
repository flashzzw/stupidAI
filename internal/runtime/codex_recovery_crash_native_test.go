//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/codex"
	"codex-netcap/internal/runtime"
)

type recoveryCrashStore struct {
	app.Persistence
	fixture      *runtime.NativeApplicationFixture
	point, ready string
	config       app.Config
}

func recoveryCrashChange(s app.Session) *app.ExecutionChange {
	if s.PendingExecutionChange != nil {
		return s.PendingExecutionChange
	}
	if len(s.ExecutionChangeAttempts) > 0 {
		return &s.ExecutionChangeAttempts[len(s.ExecutionChangeAttempts)-1].Change
	}
	return nil
}

func (s *recoveryCrashStore) Save(state app.State) error {
	status := "acknowledged"
	if s.point == "prepared" {
		status = "prepared"
	} else if s.point == "before_finish" || s.point == "after_finish" {
		status = "succeeded"
	}
	for _, op := range state.Operations {
		if op.Intent.Kind != "recover_execution" || op.Status != status || (status != "prepared" && op.Receipt.ConfigurationRecovery == nil) {
			continue
		}
		if s.point == "prepared" || s.point == "after_ack" || s.point == "after_finish" {
			if err := s.Persistence.Save(state); err != nil {
				return err
			}
		}
		persisted, err := s.Persistence.Load()
		if err != nil {
			return err
		}
		native, err := s.fixture.Checkpoint()
		if err != nil {
			return err
		}
		change := recoveryCrashChange(state.Sessions[op.SessionID])
		if change == nil {
			return fmt.Errorf("crash checkpoint lost failed candidate")
		}
		command := app.Command{ID: op.CommandID, Kind: "recover_execution", SessionID: op.SessionID, ExecutionRecovery: &app.ExecutionRecoveryRequest{BaseSnapshotID: change.BaseSnapshotID, PendingChangeID: change.ID, OperationID: change.OperationID}}
		checkpoint := nativeCrashCheckpoint{Point: s.point, Config: s.config, Command: command, Persisted: persisted, Attempted: state, Native: native}
		body, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if err = os.WriteFile(s.ready+".tmp", body, 0600); err != nil {
			return err
		}
		if err = os.Rename(s.ready+".tmp", s.ready); err != nil {
			return err
		}
		// The parent kills this actual Application process while Save is held.
		// No error return, graceful Close, or synthetic persisted state is used.
		select {}
	}
	return s.Persistence.Save(state)
}

func TestCodexNativeRecoveryCrashChild(t *testing.T) {
	if os.Getenv("NETCAP_RECOVERY_CRASH_ROOT") == "" {
		t.Skip("subprocess-only recovery crash fixture")
	}
	point := os.Getenv("NETCAP_RECOVERY_CRASH_POINT")
	switch point {
	case "prepared", "before_ack", "after_ack", "before_finish", "after_finish":
	default:
		t.Fatal("invalid recovery crash boundary")
	}
	codexNativeHookSelectionFlow(t, true, "crash-"+point)
}

func TestCodexNativeOriginalRecoveryHardCrash(t *testing.T) {
	const prSetChildSubreaper = 36
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0); errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() { _, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 0, 0, 0, 0, 0) })
	for _, point := range []string{"prepared", "before_ack", "after_ack", "before_finish", "after_finish"} {
		t.Run(point, func(t *testing.T) { testCodexRecoveryCrash(t, point) })
	}
}

func testCodexRecoveryCrash(t *testing.T, point string) {
	root, err := os.MkdirTemp("", "netcap-w0208-recovery-crash-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	checks, samples := map[string]bool{}, map[string]app.Snapshot{}
	var checkpoint nativeCrashCheckpoint
	var f *runtime.NativeApplicationFixture
	var nativeStatus syscall.WaitStatus
	var forced bool
	helperStatus := ""
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Fatalf("recovery crash check failed: %s", name)
		}
	}
	t.Cleanup(func() {
		if f != nil {
			_ = f.Runtime.Close()
		}
		report := map[string]any{"root": root, "point": point, "passed": !t.Failed(), "checks": checks, "checkpoint": checkpoint, "samples": samples, "helper_status": helperStatus, "native_exit_status": nativeStatus.ExitStatus(), "forced_native_cleanup": forced, "scope": "Actual SIGKILL of owned Application/SQLite process at recovery commit boundary; owned native stdio daemon exits on pipe close; isolated failed-provider history, no successful model or tool invocation, no shared daemon/proxy"}
		if f != nil {
			report["recovery_processes"] = f.Records
		}
		body, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			t.Error(marshalErr)
		} else if err := os.WriteFile(filepath.Join(root, "crash-result.json"), body, 0600); err != nil {
			t.Error(err)
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCodexNativeRecoveryCrashChild$", "-test.v")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NETCAP_NATIVE_CODEX_TEST=" + os.Getenv("NETCAP_NATIVE_CODEX_TEST"), "NETCAP_NATIVE_CODEX_PROXY_TEST=" + os.Getenv("NETCAP_NATIVE_CODEX_PROXY_TEST"), "NETCAP_RECOVERY_CRASH_ROOT=" + root, "NETCAP_RECOVERY_CRASH_POINT=" + point}
	log, err := os.Create(filepath.Join(root, "application.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(145 * time.Second)
	for {
		body, readErr := os.ReadFile(filepath.Join(root, "checkpoint.json"))
		if readErr == nil {
			if err := json.Unmarshal(body, &checkpoint); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-done:
			t.Fatalf("child exited before checkpoint: %v; see %s", waitErr, log.Name())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("checkpoint timeout; see %s", log.Name())
		}
		time.Sleep(25 * time.Millisecond)
	}
	check("native_identity_before_kill", checkpoint.Native.MatchesLiveProcess())
	nativeProcess, err := os.FindProcess(checkpoint.Native.PID)
	if err != nil {
		t.Fatal(err)
	}
	nativeReaped := false
	t.Cleanup(func() {
		if !nativeReaped {
			select {
			case <-done:
			default:
				_ = cmd.Process.Kill()
				<-done
			}
			nativeStatus, forced = waitAdoptedNative(t, nativeProcess)
		}
		_ = nativeProcess.Release()
	})
	accepted := checkpoint.Persisted.Commands[checkpoint.Command.ID]
	before := checkpoint.Persisted.Sessions[accepted.SessionID]
	attempted := checkpoint.Attempted.Operations[accepted.OperationID]
	persisted := checkpoint.Persisted.Operations[accepted.OperationID]
	change := recoveryCrashChange(before)
	check("exact_checkpoint_binding", checkpoint.Point == point && accepted.OperationID == attempted.ID && change != nil && attempted.Intent.ConfigurationRecoveryOf == change.OperationID)
	check("original_scope_and_snapshot", reflect.DeepEqual(attempted.Intent.Scope, before.Scope) && attempted.ExecutionSnapshotID == before.ExecutionSnapshot.ID)
	check("automation_paused_before_kill", before.Paused)
	switch point {
	case "prepared":
		check("prepared_intent_committed_without_ack", persisted.Status == "prepared" && persisted.Receipt.ConfigurationRecovery == nil)
	case "before_ack":
		check("native_confirmation_not_committed", attempted.Status == "acknowledged" && attempted.Receipt.ConfigurationRecovery != nil && persisted.Status == "dispatching" && persisted.Receipt.ConfigurationRecovery == nil)
	case "after_ack":
		check("ack_committed_before_return", persisted.Status == "acknowledged" && persisted.Receipt.ConfigurationRecovery != nil && before.PendingExecutionChange != nil)
	case "before_finish":
		check("verified_result_not_finalized", attempted.Status == "succeeded" && persisted.Status != "succeeded" && persisted.Receipt.ConfigurationRecovery != nil && before.PendingExecutionChange != nil)
	case "after_finish":
		check("verified_result_committed", persisted.Status == "succeeded" && persisted.Receipt.ConfigurationRecovery != nil && before.PendingExecutionChange == nil && len(before.ExecutionChangeAttempts) == 1)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done
	helperStatus = cmd.ProcessState.String()
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	check("application_sigkill", waitErr != nil && status.Signaled() && status.Signal() == syscall.SIGKILL)
	nativeStatus, forced = waitAdoptedNative(t, nativeProcess)
	nativeReaped = true
	check("owned_native_exited_on_pipe_close", !forced && nativeStatus.Exited() && nativeStatus.ExitStatus() == 0)
	_, err = os.Stat(filepath.Join(root, "graceful-cleanup-ran"))
	check("no_graceful_cleanup", os.IsNotExist(err))
	f = runtime.ReopenNativeApplicationFixture(t, "read_only", root)
	var disk struct {
		Thread codex.Thread `json:"thread"`
	}
	err = f.Call(context.Background(), "thread/read", map[string]string{"threadId": before.Scope.NativeID}, &disk)
	check("original_thread_remains_unloaded", err == nil && disk.Thread.ID == before.Scope.NativeID && disk.Thread.Status.Type == "notLoaded")
	db, err := app.OpenStore(checkpoint.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(checkpoint.Config, db, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	find := func(snapshot app.Snapshot) app.Session {
		for _, session := range snapshot.Sessions {
			if session.ID == before.ID {
				return session
			}
		}
		t.Fatal("lost original session")
		return app.Session{}
	}
	samples["reopened"] = a.Snapshot()
	expected := "succeeded"
	if point == "before_ack" {
		expected = "uncertain"
	}
	if point == "prepared" {
		expected = "not_sent"
	}
	samples["reconciled"] = waitNativeApplication(t, a, func(s app.Snapshot) bool {
		return crashOperation(s, accepted.OperationID).Status == expected && find(s).View.NativeStatus == "notLoaded"
	})
	after := find(samples["reconciled"])
	check("original_configuration_and_history_preserved", reflect.DeepEqual(after.Scope, before.Scope) && reflect.DeepEqual(after.ExecutionSnapshot, before.ExecutionSnapshot) && reflect.DeepEqual(after.ExecutionHistory, before.ExecutionHistory) && len(after.View.Turns) == 6)
	check("recovery_does_not_grant_input", after.Paused && !after.View.ExecutionClear)
	check("no_passive_configuration_replay", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == 0)
	if expected == "succeeded" {
		check("one_complete_failed_candidate_archive", after.PendingExecutionChange == nil && !after.ExecutionHold && len(after.ExecutionChangeAttempts) == 1 && reflect.DeepEqual(after.ExecutionChangeAttempts[0].Change.Snapshot, change.Snapshot))
		check("old_apply_failed", crashOperation(samples["reconciled"], change.OperationID).Status == "failed")
		check("current_capabilities_unverified", len(after.View.PluginEvidence) == 1 && !after.View.PluginEvidence[0].Verified && len(after.View.SkillEvidence) == 1 && !after.View.SkillEvidence[0].Verified)
	} else {
		check("unconfirmed_candidate_retained", after.PendingExecutionChange != nil && after.ExecutionHold && reflect.DeepEqual(after.PendingExecutionChange.Snapshot, change.Snapshot) && len(after.ExecutionChangeAttempts) == 0)
		check("old_apply_remains_uncertain", crashOperation(samples["reconciled"], change.OperationID).Status == "uncertain")
		_, err = a.Apply(app.Command{ID: "blocked-send", Kind: "send", SessionID: before.ID, Text: "MUST NOT EXECUTE"})
		check("unknown_or_prepared_blocks_input", err != nil)
	}
	repeated, err := a.Apply(checkpoint.Command)
	check("original_command_idempotent", err == nil && repeated.OperationID == accepted.OperationID)
	check("idempotent_request_never_replays", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == 0)
	if point == "before_ack" {
		retry := checkpoint.Command
		retry.ID = "must-not-retry-unknown"
		_, err = a.Apply(retry)
		check("unknown_recovery_rejects_new_retry", err != nil)
		time.Sleep(900 * time.Millisecond)
		check("unknown_kept_without_ack", crashOperation(a.Snapshot(), accepted.OperationID).Status == "uncertain" && nativeConfigurationCallCount(t, f, before.Scope.NativeID) == 0)
	} else {
		if point == "prepared" {
			check("unsent_recovery_marker_released", after.PendingExecutionChange.RecoveryOperationID == "" && after.PendingExecutionChange.Status == "applying")
			retry := checkpoint.Command
			retry.ID = "explicit-after-unsent"
			receipt, err := a.Apply(retry)
			if err != nil {
				t.Fatal(err)
			}
			samples["new-explicit-recovery"] = waitNativeApplication(t, a, func(s app.Snapshot) bool { return crashOperation(s, receipt.OperationID).Status == "succeeded" })
			check("new_explicit_recovery_distinct", receipt.OperationID != accepted.OperationID && crashOperation(samples["new-explicit-recovery"], accepted.OperationID).Status == "not_sent")
		}
		resumed, err := a.Apply(app.Command{ID: "explicit-resume-after-crash", Kind: "resume", SessionID: before.ID})
		if err != nil {
			t.Fatal(err)
		}
		samples["explicit-resume"] = waitNativeApplication(t, a, func(s app.Snapshot) bool { return crashOperation(s, resumed.OperationID).Status == "succeeded" })
		ready := find(samples["explicit-resume"])
		check("explicit_resume_verifies_original_capabilities", !ready.Paused && ready.Scope.NativeID == before.Scope.NativeID && ready.View.PluginEvidence[0].Verified && ready.View.SkillEvidence[0].Verified)
		check("explicit_resume_has_no_model_input", len(ready.View.Turns) == 6)
		_, err = a.Apply(app.Command{ID: "pause-after-check", Kind: "pause", SessionID: before.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	samples["final"] = a.Snapshot()
	artifact, err := os.ReadFile(filepath.Join(root, "work", "retained.txt"))
	check("artifact_unchanged", err == nil && string(artifact) == "owned recovery artifact; preserve across SIGKILL\n")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Records {
		for _, call := range p.Calls {
			check("no_new_thread_or_model_after_crash", call["method"] != "thread/start" && call["method"] != "thread/fork" && call["method"] != "turn/start")
		}
	}
}
