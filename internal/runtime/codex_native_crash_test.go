//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
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

type nativeCrashCheckpoint struct {
	Point     string                          `json:"point"`
	Config    app.Config                      `json:"config"`
	Command   app.Command                     `json:"command"`
	Persisted app.State                       `json:"persisted"`
	Attempted app.State                       `json:"attempted"`
	Native    runtime.NativeProcessCheckpoint `json:"native"`
}

type crashBoundaryStore struct {
	app.Persistence
	fixture      *runtime.NativeApplicationFixture
	point, ready string
	config       app.Config
	command      app.Command
}

func (s *crashBoundaryStore) Save(state app.State) error {
	targetKind := "create"
	if s.point == "resume_before_commit" {
		targetKind = "resume"
	}
	for _, op := range state.Operations {
		if op.Intent.Kind != targetKind || op.Status != "succeeded" || op.Receipt.NativeID == "" {
			continue
		}
		if s.point == "after_commit" {
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
		checkpoint := nativeCrashCheckpoint{Point: s.point, Config: s.config, Command: s.command, Persisted: persisted, Attempted: state, Native: native}
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if err = os.WriteFile(s.ready+".tmp", data, 0600); err != nil {
			return err
		}
		if err = os.Rename(s.ready+".tmp", s.ready); err != nil {
			return err
		}
		// The parent kills the whole application process here. Never return a
		// synthetic storage error or run Application.Close at this boundary.
		select {}
	}
	return s.Persistence.Save(state)
}

func TestCodexNativeApplicationCrashChild(t *testing.T) {
	root := os.Getenv("NETCAP_NATIVE_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess-only crash fixture")
	}
	permission, point := os.Getenv("NETCAP_NATIVE_CRASH_PERMISSION"), os.Getenv("NETCAP_NATIVE_CRASH_POINT")
	if !filepath.IsAbs(root) || (permission != "read_only" && permission != "workspace") || (point != "before_commit" && point != "after_commit" && point != "resume_before_commit") {
		t.Fatal("invalid crash fixture parameters")
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "graceful-cleanup-ran"), []byte("unexpected cleanup"), 0600)
	})
	f := runtime.NewNativeApplicationFixtureAt(t, permission, root)
	if err := os.WriteFile(filepath.Join(root, "work", "retained.txt"), []byte("owned fixture artifact; preserve across crash\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := app.Config{Version: 1, StateDir: filepath.Join(root, "console"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: filepath.Join(root, "work")}}}
	command := app.Command{ID: "crash-create", Kind: "create", ProjectID: "work", RouteID: "fixture", Permission: permission, Title: "hard crash fixture"}
	db, err := app.OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	store := &crashBoundaryStore{Persistence: db, fixture: f, point: point, ready: filepath.Join(root, "checkpoint.json"), config: c, command: command}
	held := &heldNativeCreate{Adapter: f.Runtime, fixture: f, created: make(chan runtime.Receipt, 1), release: make(chan struct{}), listed: true}
	a, err := app.Open(c, store, held)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-held.created:
	case <-time.After(10 * time.Second):
		t.Fatal("native create did not reach boundary")
	}
	if _, err = a.Apply(app.Command{ID: "takeover-before-crash", Kind: "takeover", SessionID: accepted.SessionID}); err != nil {
		t.Fatal(err)
	}
	close(held.release)
	if point == "resume_before_commit" {
		waitNativeApplication(t, a, func(s app.Snapshot) bool { return len(s.Operations) == 1 && s.Operations[0].Status == "succeeded" })
		if _, err = a.Apply(app.Command{ID: "crash-resume", Kind: "resume", SessionID: accepted.SessionID}); err != nil {
			t.Fatal(err)
		}
	}
	<-time.After(30 * time.Second)
	t.Fatal("parent did not kill the stopped application process")
}

func waitAdoptedNative(t *testing.T, process *os.Process) (syscall.WaitStatus, bool) {
	t.Helper()
	var status syscall.WaitStatus
	forced := false
	deadline := time.Now().Add(5 * time.Second)
	for {
		pid, err := syscall.Wait4(process.Pid, &status, syscall.WNOHANG, nil)
		if err != nil {
			t.Fatalf("wait owned native process: %v", err)
		}
		if pid == process.Pid {
			return status, forced
		}
		if time.Now().After(deadline) {
			if forced {
				t.Fatal("owned native process did not exit after cleanup")
			}
			_ = process.Kill()
			forced = true
			deadline = time.Now().Add(2 * time.Second)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCodexNativeApplicationHardCrash(t *testing.T) {
	// A subreaper can wait for the owned stdio daemon after its application
	// parent is killed. This opt-in test runs sequentially in its own test host.
	const prSetChildSubreaper = 36
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0); errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() { _, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 0, 0, 0, 0, 0) })
	for _, permission := range []string{"read_only", "workspace"} {
		for _, point := range []string{"before_commit", "after_commit", "resume_before_commit"} {
			t.Run(permission+"/"+point, func(t *testing.T) { testNativeApplicationHardCrash(t, permission, point) })
		}
	}
}

func testNativeApplicationHardCrash(t *testing.T, permission, point string) {
	root := t.TempDir()
	checks := map[string]bool{}
	snapshots := map[string]app.Snapshot{}
	var checkpoint nativeCrashCheckpoint
	var nativeStatus syscall.WaitStatus
	var forced bool
	var f *runtime.NativeApplicationFixture
	var helperStatus string
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Fatalf("hard crash check failed: %s", name)
		}
		t.Log("hard crash check passed:", name)
	}
	t.Cleanup(func() {
		if f != nil {
			_ = f.Runtime.Close()
		}
		if dir := os.Getenv("NETCAP_NATIVE_CODEX_EVIDENCE"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Error(err)
				return
			}
			report := map[string]any{"checks": checks, "checkpoint": checkpoint, "snapshots": snapshots, "helper_status": helperStatus, "native_exit_status": nativeStatus.ExitStatus(), "native_signal": int(nativeStatus.Signal()), "forced_native_cleanup": forced, "passed": !t.Failed(), "scope": "SIGKILL of real Application/SQLite test process; native stdio exits on pipe close; no production HTTP or Unix proxy/peer"}
			if f != nil {
				report["recovery_processes"] = f.Records
			}
			data, err := json.MarshalIndent(report, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, "crash-"+permission+"-"+point+".json"), append(data, '\n'), 0600)
			}
			if err != nil {
				t.Error(err)
			}
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCodexNativeApplicationCrashChild$", "-test.v")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NETCAP_NATIVE_CODEX_TEST=" + os.Getenv("NETCAP_NATIVE_CODEX_TEST"), "NETCAP_NATIVE_CODEX_PROXY_TEST=" + os.Getenv("NETCAP_NATIVE_CODEX_PROXY_TEST"), "NETCAP_NATIVE_CRASH_ROOT=" + root, "NETCAP_NATIVE_CRASH_PERMISSION=" + permission, "NETCAP_NATIVE_CRASH_POINT=" + point}
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
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, readErr := os.ReadFile(filepath.Join(root, "checkpoint.json"))
		if readErr == nil {
			if err = json.Unmarshal(data, &checkpoint); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-done:
			b, _ := os.ReadFile(log.Name())
			t.Fatalf("child stopped before checkpoint: %v %s", waitErr, b)
		default:
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(log.Name())
			t.Fatalf("child checkpoint timeout: %s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	check("native_process_identity_before_kill", checkpoint.Native.MatchesLiveProcess())
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
	check("checkpoint_at_requested_boundary", checkpoint.Point == point)
	accepted := checkpoint.Persisted.Commands["crash-create"]
	attempted := checkpoint.Attempted.Operations[accepted.OperationID]
	original := attempted.Receipt.NativeID
	persisted := checkpoint.Persisted.Operations[accepted.OperationID]
	check("native_creation_completed_before_kill", original != "" && attempted.Status == "succeeded")
	if point == "before_commit" {
		check("receipt_not_yet_committed", persisted.Status == "dispatching" && persisted.Receipt.NativeID == "" && checkpoint.Persisted.Sessions[accepted.SessionID].Scope.NativeID == "")
	} else if point == "after_commit" {
		check("receipt_committed_before_delivery", persisted.Status == "succeeded" && persisted.Receipt.NativeID == original && checkpoint.Persisted.Sessions[accepted.SessionID].Scope.NativeID == original)
	} else {
		resumeID := checkpoint.Persisted.Commands["crash-resume"].OperationID
		check("resume_grant_not_yet_committed", checkpoint.Persisted.Operations[resumeID].Status == "dispatching" && checkpoint.Attempted.Operations[resumeID].Status == "succeeded" && !checkpoint.Attempted.Sessions[accepted.SessionID].Paused && checkpoint.Attempted.Sessions[accepted.SessionID].ControlMode == "managed")
	}
	check("manual_takeover_was_durable", checkpoint.Persisted.Sessions[accepted.SessionID].Paused && checkpoint.Persisted.Sessions[accepted.SessionID].ControlMode == "human_takeover")
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done
	helperStatus = cmd.ProcessState.String()
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	check("application_killed_by_sigkill", waitErr != nil && status.Signaled() && status.Signal() == syscall.SIGKILL)
	nativeStatus, forced = waitAdoptedNative(t, nativeProcess)
	nativeReaped = true
	check("owned_native_exited_after_pipe_close", !forced && nativeStatus.Exited() && nativeStatus.ExitStatus() == 0)
	_, err = os.Stat(filepath.Join(root, "graceful-cleanup-ran"))
	check("no_graceful_cleanup_ran", os.IsNotExist(err))
	f = runtime.ReopenNativeApplicationFixture(t, permission, root)
	var disk codex.Thread
	err = f.Call(context.Background(), "thread/read", map[string]string{"threadId": original}, &struct {
		Thread *codex.Thread `json:"thread"`
	}{&disk})
	check("same_unloaded_native_thread", err == nil && disk.ID == original && disk.Status.Type == "notLoaded")
	db, err := app.OpenStore(checkpoint.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(checkpoint.Config, db, f.Runtime)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	snapshots["reopened"] = a.Snapshot()
	if point == "before_commit" {
		check("reopened_original_uncertain_operation", len(snapshots["reopened"].Operations) == 1 && snapshots["reopened"].Operations[0].ID == accepted.OperationID && snapshots["reopened"].Operations[0].Status == "uncertain")
		_, err = a.Apply(app.Command{ID: "blocked-before-recovery", Kind: "send", SessionID: accepted.SessionID, Text: "MUST NOT EXECUTE"})
		check("unknown_blocks_input", err != nil)
	} else if point == "after_commit" {
		check("reopened_committed_original_operation", len(snapshots["reopened"].Operations) == 1 && snapshots["reopened"].Operations[0].ID == accepted.OperationID && snapshots["reopened"].Operations[0].Status == "succeeded")
	} else {
		resumeID := checkpoint.Persisted.Commands["crash-resume"].OperationID
		check("reopened_unfinished_resume", crashOperation(snapshots["reopened"], resumeID).Status == "uncertain")
	}
	snapshots["recovered"] = waitNativeApplication(t, a, func(s app.Snapshot) bool {
		if point == "resume_before_commit" && crashOperation(s, checkpoint.Persisted.Commands["crash-resume"].OperationID).Status != "not_sent" {
			return false
		}
		return crashOperation(s, accepted.OperationID).Status == "succeeded" && s.Sessions[0].View.PermissionEvidence != nil && s.Sessions[0].View.PermissionEvidence.Verified
	})
	recovered := snapshots["recovered"].Sessions[0]
	check("original_session_and_native_identity", recovered.ID == accepted.SessionID && recovered.Scope.NativeID == original)
	check("manual_ownership_survives_crash", recovered.Paused && recovered.ControlMode == "human_takeover" && recovered.Generation == checkpoint.Persisted.Sessions[accepted.SessionID].Generation)
	check("saved_binding_survives_crash", reflect.DeepEqual(recovered.Scope.Instance, checkpoint.Persisted.Sessions[accepted.SessionID].Scope.Instance) && recovered.ExecutionSnapshot.ID == checkpoint.Persisted.Sessions[accepted.SessionID].ExecutionSnapshot.ID)
	var policy struct {
		Type string `json:"type"`
	}
	if recovered.View.PermissionEvidence != nil {
		_ = json.Unmarshal(recovered.View.PermissionEvidence.Sandbox, &policy)
	}
	wantPolicy := "readOnly"
	if permission == "workspace" {
		wantPolicy = "workspaceWrite"
	}
	check("original_permission_after_crash", recovered.Permission == permission && recovered.View.PermissionEvidence != nil && recovered.View.PermissionEvidence.Verified && policy.Type == wantPolicy && string(recovered.View.PermissionEvidence.ApprovalPolicy) == `"on-request"`)
	repeated, err := a.Apply(checkpoint.Command)
	check("command_idempotent_after_sigkill", err == nil && repeated.OperationID == accepted.OperationID && repeated.SessionID == accepted.SessionID)
	if point == "resume_before_commit" {
		resumeID := checkpoint.Persisted.Commands["crash-resume"].OperationID
		check("interrupted_handoff_not_replayed", crashOperation(snapshots["recovered"], resumeID).Status == "not_sent" && recovered.Paused && recovered.ControlMode == "human_takeover")
		retried, retryErr := a.Apply(app.Command{ID: "crash-resume", Kind: "resume", SessionID: accepted.SessionID})
		check("repeated_resume_keeps_original_failed_grant", retryErr == nil && retried.OperationID == resumeID && a.Snapshot().Sessions[0].Paused)
	}
	artifact, err := os.ReadFile(filepath.Join(root, "work", "retained.txt"))
	check("artifact_preserved", err == nil && string(artifact) == "owned fixture artifact; preserve across crash\n")
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	var traces []struct {
		Calls []struct {
			Method string `json:"method"`
		} `json:"calls"`
		Events []struct {
			Method string `json:"method"`
		} `json:"events"`
	}
	if err = json.Unmarshal(checkpoint.Native.Trace, &traces); err != nil {
		t.Fatal(err)
	}
	starts, turns, forks, turnEvents := 0, 0, 0, 0
	for _, p := range traces {
		for _, call := range p.Calls {
			if call.Method == "thread/start" {
				starts++
			}
			if call.Method == "turn/start" {
				turns++
			}
			if call.Method == "thread/fork" {
				forks++
			}
		}
		for _, event := range p.Events {
			if event.Method == "turn/started" {
				turnEvents++
			}
		}
	}
	for _, p := range f.Records {
		for _, call := range p.Calls {
			if call["method"] == "thread/start" {
				starts++
			}
			if call["method"] == "turn/start" {
				turns++
			}
			if call["method"] == "thread/fork" {
				forks++
			}
		}
		for _, event := range p.Events {
			if event.Method == "turn/started" {
				turnEvents++
			}
		}
	}
	check("one_create_zero_model_inputs", starts == 1 && turns == 0 && forks == 0 && turnEvents == 0)
}

func crashOperation(snapshot app.Snapshot, id string) app.Operation {
	for _, operation := range snapshot.Operations {
		if operation.ID == id {
			return operation
		}
	}
	return app.Operation{}
}
