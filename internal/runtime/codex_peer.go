package runtime

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The peer is obtained from the explicitly configured Unix socket, never by
// scanning processes. Keeping its identity alongside the proxy detects daemon
// restarts/socket replacement and namespace drift before reads or writes.
type codexPeer struct {
	connection net.Conn
	file       os.FileInfo
	pid        int
	birth      string
	namespace  os.FileInfo
}

func openCodexPeer(ctx context.Context, instance Instance) (*codexPeer, error) {
	socket := instance.Socket
	file, err := os.Stat(socket)
	if err != nil || file.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("Codex socket 不是可核对的本地后台端点")
	}
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("无法连接指定 Codex 本地后台")
	}
	peer := &codexPeer{connection: connection, file: file}
	raw, err := connection.(*net.UnixConn).SyscallConn()
	var credentials *syscall.Ucred
	var credentialErr error
	if err == nil {
		err = raw.Control(func(fd uintptr) {
			credentials, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		})
	}
	if err != nil || credentialErr != nil || credentials == nil || credentials.Uid != uint32(os.Geteuid()) || credentials.Pid <= 0 {
		connection.Close()
		return nil, fmt.Errorf("无法验证 Codex 后台属于当前本地用户")
	}
	peer.pid, peer.birth = int(credentials.Pid), processBirth(int(credentials.Pid))
	if err := peer.verifyNamespace(ctx, instance); err != nil {
		connection.Close()
		return nil, err
	}
	// Check the running image, rather than a configured path that may have
	// been upgraded while this daemon was still alive.
	version := exec.CommandContext(ctx, filepath.Join("/proc", strconv.Itoa(peer.pid), "exe"), "--version")
	version.Env = instanceEnv(instance)
	output, err := version.Output()
	if err != nil || strings.TrimSpace(string(output)) != "codex-cli 0.158.0" {
		connection.Close()
		return nil, fmt.Errorf("Codex 后台运行映像不是已验证的 0.158.0，需完成该版本路由契约验证")
	}
	if err := peer.verifyNamespace(ctx, instance); err != nil {
		connection.Close()
		return nil, err
	}
	return peer, nil
}

func (p *codexPeer) verify(socket string) error {
	file, err := os.Stat(socket)
	if err != nil || p.file == nil || !os.SameFile(p.file, file) || p.birth == "" || processBirth(p.pid) != p.birth {
		return fmt.Errorf("Codex 后台身份或 socket 已变化，需重新核对连接")
	}
	return nil
}

type verifiedCodexConnection struct {
	codexConnection
	peer *codexPeer
}

func (p *verifiedCodexConnection) Close() error {
	if p.peer.connection != nil {
		_ = p.peer.connection.Close()
	}
	return p.codexConnection.Close()
}

func (p *verifiedCodexConnection) VerifyCredential(ctx context.Context, instance Instance, key, value string) error {
	data, err := p.peer.readEnvironment(ctx, instance.Socket)
	if err != nil {
		return err
	}
	if err := verifyCodexEnvironment(data, instance.Profile, key, value); err != nil {
		return err
	}
	return p.peer.verify(instance.Socket)
}

func (p *codexPeer) readEnvironment(ctx context.Context, socket string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("Codex 后台环境核对已取消或超时")
	}
	if err := p.verify(socket); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join("/proc", strconv.Itoa(p.pid), "environ"))
	if err != nil {
		return nil, fmt.Errorf("不能读取指定 Codex 后台环境，无法证明配置或凭据来源")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, fmt.Errorf("指定 Codex 后台环境无法完整核对")
	}
	if err := p.verify(socket); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("Codex 后台环境核对已取消或超时")
	}
	return data, nil
}

func (p *codexPeer) verifyNamespace(ctx context.Context, instance Instance) error {
	data, err := p.readEnvironment(ctx, instance.Socket)
	if err != nil {
		return err
	}
	selected, err := parseCodexEnvironment(data, "")
	if err != nil {
		return err
	}
	info, err := verifyCodexProfile(selected, instance.Profile)
	if err != nil {
		return err
	}
	if p.namespace != nil && !os.SameFile(p.namespace, info) {
		return fmt.Errorf("Codex 后台状态目录已变化，需重新核对连接")
	}
	if err := p.verify(instance.Socket); err != nil {
		return err
	}
	p.namespace = info
	return nil
}

func parseCodexEnvironment(data []byte, key string) (map[string]string, error) {
	if len(data) == 0 || data[len(data)-1] != 0 {
		return nil, fmt.Errorf("Codex 后台环境读取不完整")
	}
	selected := map[string]string{}
	for _, entry := range bytes.Split(data, []byte{0}) {
		name, raw, ok := bytes.Cut(entry, []byte{'='})
		if !ok {
			continue
		}
		nameString := string(name)
		if (key == "" || nameString != key) && nameString != "CODEX_HOME" && nameString != "HOME" {
			continue
		}
		if _, duplicate := selected[nameString]; duplicate {
			return nil, fmt.Errorf("Codex 后台环境含重复的配置或凭据变量")
		}
		selected[nameString] = string(raw)
	}
	return selected, nil
}

func verifyCodexProfile(selected map[string]string, profile string) (os.FileInfo, error) {
	actual := selected["CODEX_HOME"]
	if actual == "" && selected["HOME"] != "" {
		actual = filepath.Join(selected["HOME"], ".codex")
	}
	if !filepath.IsAbs(actual) || !filepath.IsAbs(profile) {
		return nil, fmt.Errorf("Codex 后台 profile 需要可核对的绝对路径")
	}
	expectedPath, e1 := filepath.EvalSymlinks(profile)
	actualPath, e2 := filepath.EvalSymlinks(actual)
	if actual == "" || e1 != nil || e2 != nil || expectedPath != actualPath {
		return nil, fmt.Errorf("Codex 后台实际 profile 与指定实例不一致")
	}
	info, err := os.Stat(actualPath)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Codex 后台 profile 不是可核对的状态目录")
	}
	return info, nil
}

func verifyCodexEnvironment(data []byte, profile, key, value string) error {
	selected, err := parseCodexEnvironment(data, key)
	if err != nil {
		return err
	}
	if _, err := verifyCodexProfile(selected, profile); err != nil {
		return err
	}
	if key == "" || value == "" || subtle.ConstantTimeCompare([]byte(selected[key]), []byte(value)) != 1 {
		return fmt.Errorf("Codex 后台未采用指定凭据环境；轮换后请核对并重启该后台，不会退回默认凭据")
	}
	return nil
}
