package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type claudeHello struct {
	Version                   int    `json:"version"`
	SDK                       string `json:"sdk"`
	SettingsFileConsumer      int    `json:"settings_file_consumer"`
	InstalledSettings         int    `json:"installed_settings"`
	InstalledSettingsRecovery int    `json:"installed_settings_recovery"`
	OriginalQueryRecovery     int    `json:"original_query_recovery"`
	PendingPermissions        int    `json:"pending_permissions"`
	PersistentControl         int    `json:"persistent_control"`
	Controller                struct {
		Version  int    `json:"version"`
		Binding  string `json:"binding"`
		PID      int    `json:"pid"`
		Birth    string `json:"birth"`
		StateDir string `json:"state_dir"`
	} `json:"controller"`
}

type claudeControllerPeer struct {
	dir       string
	namespace os.FileInfo
	socket    os.FileInfo
	pid       int
	birth     string
	binding   string
}

func claudeControllerBinding(i Instance, dir string) string {
	i.Name, i.NotificationSender = "", ""
	data, _ := json.Marshal(struct {
		Instance     Instance
		StateDir     string
		IsolatedHome string
		Environment  []string
	}{i, dir, i.isolatedHome, claudeEnv(i)})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (p *claudeTransport) hello(ctx context.Context, params any) (claudeHello, error) {
	var hello claudeHello
	err := p.call(ctx, "hello", params, &hello)
	if err != nil || hello.Version != 1 || hello.SDK != "0.3.280" {
		return hello, fmt.Errorf("Claude bridge 握手不兼容: %v", err)
	}
	p.settingsFileConsumer = hello.SettingsFileConsumer
	p.installedSettings = hello.InstalledSettings
	p.installedSettingsRecovery = hello.InstalledSettingsRecovery
	p.originalQueryRecovery = hello.OriginalQueryRecovery
	p.pendingPermissions = hello.PendingPermissions
	return hello, nil
}

func attachClaudeController(ctx context.Context, i Instance, dir string) (*claudeTransport, error) {
	socket, err := os.Lstat(filepath.Join(dir, "controller.sock"))
	if err != nil || socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("旧 Claude bridge 尚无可核对的私有控制端点；保留原执行")
	}
	if stat, ok := socket.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("Claude 控制端点不属于当前用户")
	}
	namespace, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer namespace.Close()
	info, err := namespace.Stat()
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	// Resolve through an owned directory FD rather than truncating a long WSL
	// state path to sockaddr_un's small pathname limit.
	connection, err := dialer.DialContext(ctx, "unix", fmt.Sprintf("/proc/self/fd/%d/controller.sock", namespace.Fd()))
	if err != nil {
		return nil, fmt.Errorf("不能连接原 Claude 控制端点；保留原执行: %w", err)
	}
	raw, err := connection.(*net.UnixConn).SyscallConn()
	var credential *syscall.Ucred
	var credentialErr error
	if err == nil {
		err = raw.Control(func(fd uintptr) {
			credential, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		})
	}
	if err != nil || credentialErr != nil || credential == nil || credential.Uid != uint32(os.Geteuid()) || credential.Pid <= 0 {
		connection.Close()
		return nil, fmt.Errorf("无法核对原 Claude 控制进程的本地身份")
	}
	peer := &claudeControllerPeer{dir: dir, namespace: info, socket: socket, pid: int(credential.Pid), birth: processBirth(int(credential.Pid)), binding: claudeControllerBinding(i, dir)}
	if err := peer.verify(); err != nil {
		connection.Close()
		return nil, err
	}
	p := &claudeTransport{input: connection, pending: map[string]chan bridgeResponse{}, done: make(chan struct{})}
	go p.read(connection)
	hello, err := p.hello(ctx, map[string]any{"controller_binding": claudeControllerBinding(i, dir)})
	if err == nil && (hello.PersistentControl != 1 || hello.Controller.Version != 1 ||
		hello.Controller.Binding != claudeControllerBinding(i, dir) || hello.Controller.PID != peer.pid ||
		hello.Controller.Birth != peer.birth || hello.Controller.StateDir != dir) {
		err = fmt.Errorf("Claude 控制端点与原实例/进程/状态目录绑定不一致")
	}
	if err == nil {
		err = peer.verify()
	}
	if err != nil {
		p.detach()
		return nil, err
	}
	p.controller = peer
	return p, nil
}

func (p *claudeControllerPeer) verify() error {
	socket, err := os.Lstat(filepath.Join(p.dir, "controller.sock"))
	dir, dirErr := os.Stat(p.dir)
	if err != nil || dirErr != nil || !os.SameFile(p.socket, socket) || !os.SameFile(p.namespace, dir) ||
		p.birth == "" || processBirth(p.pid) != p.birth {
		return fmt.Errorf("Claude 原控制进程或端点已变化，需重新核对原执行")
	}
	return nil
}

// Transport loss is not an instruction to close SDK queries. The original
// bridge keeps its instance lock, approvals and event reader for the next Go
// owner. Normal shutdown/reaping still explicitly closes the owned bridge.
func (p *claudeTransport) detach() {
	_ = p.input.Close()
	p.fail(io.EOF)
}

func (p *claudeTransport) closeController() {
	peer := p.controller
	if err := peer.verify(); err != nil {
		p.detach()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := p.call(ctx, "shutdown", map[string]any{}, nil)
	cancel()
	p.detach()
	if err != nil && peer.verify() == nil {
		_ = syscall.Kill(peer.pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(9 * time.Second)
	for processBirth(peer.pid) == peer.birth && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processBirth(peer.pid) == peer.birth {
		// Only this verified, separately created bridge group may be stopped.
		// Neither its exit nor this cleanup proves native task success.
		if group, err := syscall.Getpgid(peer.pid); err == nil && group == peer.pid {
			_ = syscall.Kill(-peer.pid, syscall.SIGKILL)
		}
	}
	if p.exited != nil {
		select {
		case <-p.exited:
		case <-time.After(time.Second):
		}
	}
}
