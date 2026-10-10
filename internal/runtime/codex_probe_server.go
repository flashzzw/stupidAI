package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type codexProbeServer struct {
	PID       int    `json:"pid"`
	Birth     string `json:"birth"`
	SessionID string `json:"session_id"`
	Socket    string `json:"socket"`
	cmd       *exec.Cmd
	exited    chan struct{}
}

func (s *codexProbeServer) alive() bool { return s.Birth != "" && processBirth(s.PID) == s.Birth }

func startCodexProbeServer(ctx context.Context, scope Scope) (*codexProbeServer, error) {
	path := filepath.Join(filepath.Dir(scope.Instance.Profile), "server.json")
	if b, err := os.ReadFile(path); err == nil {
		var record codexProbeServer
		if json.Unmarshal(b, &record) != nil || record.SessionID != scope.SessionID || record.Socket != scope.Instance.Socket || record.PID == 0 || record.Birth == "" {
			return nil, fmt.Errorf("Codex 探测后台所有权记录不完整，需核对原进程")
		}
		if record.alive() {
			return &record, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if scope.NoAutoResume {
		return nil, fmt.Errorf("原探测后台未运行；只读核对不会启动或重启后台")
	}
	version := exec.CommandContext(ctx, scope.Instance.ServerExecutable, "--version")
	version.Env = probeEnv(scope.Instance)
	b, err := version.Output()
	if err != nil || strings.TrimSpace(string(b)) != "codex-cli 0.158.0" {
		return nil, fmt.Errorf("Codex 探测后台需要已验证版本 0.158.0")
	}
	// Reserve the launch before Start. A crash in the short start/record window
	// leaves explicit uncertainty instead of spawning a second unowned server.
	reserved := codexProbeServer{SessionID: scope.SessionID, Socket: scope.Instance.Socket}
	b, _ = json.Marshal(reserved)
	if err := writeOpenCodeRecord(path, b); err != nil {
		return nil, err
	}
	if err := os.Remove(scope.Instance.Socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	cmd := exec.Command(scope.Instance.ServerExecutable, "app-server", "--listen", "unix://"+scope.Instance.Socket)
	cmd.Env, cmd.Dir = probeEnv(scope.Instance), scope.Cwd
	key, _ := strings.CutPrefix(scope.Route.CredentialRef, "env:")
	cmd.Env = append(cmd.Env, key+"="+os.Getenv(key))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	s := &codexProbeServer{PID: cmd.Process.Pid, Birth: processBirth(cmd.Process.Pid), SessionID: scope.SessionID, Socket: scope.Instance.Socket, cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.exited) }()
	b, _ = json.Marshal(s)
	if err := writeOpenCodeRecord(path, b); err != nil {
		s.stop()
		return nil, err
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		if _, err := os.Stat(scope.Instance.Socket); err == nil && s.alive() {
			return s, nil
		}
		select {
		case <-ctx.Done():
			s.stop()
			return nil, ctx.Err()
		case <-s.exited:
			return nil, fmt.Errorf("隔离 Codex 后台启动失败")
		case <-timer.C:
		}
	}
}

func (s *codexProbeServer) stop() {
	if s.alive() {
		_ = syscall.Kill(-s.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.alive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.alive() {
		_ = syscall.Kill(-s.PID, syscall.SIGKILL)
	}
	if s.exited != nil {
		select {
		case <-s.exited:
		case <-time.After(time.Second):
		}
	}
}
