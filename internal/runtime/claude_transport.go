package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type bridgeResponse struct {
	Version int             `json:"v"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		NotSent bool   `json:"not_sent"`
		Message string `json:"message"`
	} `json:"error"`
}

type claudeTransport struct {
	cmd                       *exec.Cmd
	input                     io.WriteCloser
	write                     sync.Mutex
	mu                        sync.Mutex
	pending                   map[string]chan bridgeResponse
	done                      chan struct{}
	exited                    chan struct{}
	err                       error
	once                      sync.Once
	settingsFileConsumer      int
	installedSettings         int
	installedSettingsRecovery int
	originalQueryRecovery     int
}

func claudeEnv(i Instance) []string {
	if i.isolatedHome != "" {
		return probeEnv(i)
	}
	out := []string{}
	for _, key := range append([]string{"PATH", "HOME", "USER", "LANG", "TERM", "SHELL"}, i.EnvAllow...) {
		if key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_RESUME_INTERRUPTED_TURN" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+value)
		}
	}
	return append(out, "CLAUDE_CONFIG_DIR="+i.Profile)
}

func connectClaude(ctx context.Context, i Instance, dir string) (*claudeTransport, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("旧 Claude bridge 仍持有实例，不能创建第二个控制器: %w", err)
	}
	node := i.NodeExecutable
	if node == "" {
		node = "node"
	}
	cmd := exec.Command(node, i.Bridge, dir)
	cmd.Env = claudeEnv(i)
	if executable, err := os.Executable(); err == nil {
		cmd.Env = append(cmd.Env, "NETCAP_CLAUDE_SETTINGS_EXEC="+executable)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The child retains this lock after a Go crash until it has shut down SDK
	// queries. A new controller cannot race the old bridge's EOF cleanup.
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &claudeTransport{cmd: cmd, input: in, pending: map[string]chan bridgeResponse{}, done: make(chan struct{}), exited: make(chan struct{})}
	go p.read(out)
	go func() {
		_ = cmd.Wait()
		close(p.exited)
		p.fail(errors.New("Claude bridge 已退出，原执行需核对"))
	}()
	var hello struct {
		Version                   int    `json:"version"`
		SDK                       string `json:"sdk"`
		SettingsFileConsumer      int    `json:"settings_file_consumer"`
		InstalledSettings         int    `json:"installed_settings"`
		InstalledSettingsRecovery int    `json:"installed_settings_recovery"`
		OriginalQueryRecovery     int    `json:"original_query_recovery"`
	}
	if err = p.call(ctx, "hello", map[string]any{}, &hello); err != nil || hello.Version != 1 || hello.SDK != "0.3.280" {
		p.close()
		return nil, fmt.Errorf("Claude bridge 握手不兼容: %v", err)
	}
	p.settingsFileConsumer = hello.SettingsFileConsumer
	p.installedSettings = hello.InstalledSettings
	p.installedSettingsRecovery = hello.InstalledSettingsRecovery
	p.originalQueryRecovery = hello.OriginalQueryRecovery
	return p, nil
}

func (p *claudeTransport) fail(err error) {
	p.once.Do(func() { p.mu.Lock(); p.err = err; p.mu.Unlock(); close(p.done) })
}

func (p *claudeTransport) read(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	for scanner.Scan() {
		var response bridgeResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.Version != 1 {
			p.fail(errors.New("Claude bridge 返回无效协议"))
			return
		}
		p.mu.Lock()
		ch := p.pending[response.ID]
		delete(p.pending, response.ID)
		p.mu.Unlock()
		if ch != nil {
			ch <- response
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	p.fail(err)
}

func (p *claudeTransport) call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return &NotSent{Reason: err.Error()}
	}
	id := uuid.NewString()
	ch := make(chan bridgeResponse, 1)
	p.mu.Lock()
	if p.err != nil {
		err := p.err
		p.mu.Unlock()
		return &NotSent{Reason: err.Error()}
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	wire, err := json.Marshal(map[string]any{"v": 1, "id": id, "method": method, "params": params})
	if err != nil {
		return &NotSent{Reason: err.Error()}
	}
	p.write.Lock()
	_, err = p.input.Write(append(wire, '\n'))
	p.write.Unlock()
	if err != nil {
		p.fail(err)
		return err
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			if response.Error.NotSent {
				return &NotSent{Reason: response.Error.Message}
			}
			return errors.New(response.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(response.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.err
	}
}

func (p *claudeTransport) close() {
	_ = p.input.Close()
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		// This process group was created above and has never been shared with
		// user-launched CLIs. Its exit still does not prove native task success.
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.exited
	}
}
