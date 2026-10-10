package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"codex-netcap/internal/config"
	"github.com/google/uuid"
)

type claudeControlFixture struct {
	Instance Instance `json:"instance"`
	State    string   `json:"state"`
	Native   string   `json:"native"`
	Input    string   `json:"input"`
	Submit   bool     `json:"submit"`
}

func newClaudeControlFixture(t *testing.T) claudeControlFixture {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node bridge fixture required")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(repo, "bridges/claude/dist/main.js")
	if _, err := os.Stat(bridge); err != nil {
		t.Skip("build the Claude bridge before its transport tests")
	}
	root := t.TempDir()
	wrapper := filepath.Join(root, "node-fixture")
	program := "#!/bin/sh\nexec " + config.ShellQuote(node) + " --experimental-test-module-mocks --import " + config.ShellQuote(filepath.Join(repo, "tests/claude_control_fixture.mjs")) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	return claudeControlFixture{Instance: Instance{ID: "owned", Kind: "claude", Profile: root, Bridge: bridge, NodeExecutable: wrapper},
		State: filepath.Join(root, "state"), Native: uuid.NewString(), Input: uuid.NewString(), Submit: true}
}

func startClaudeControlFixture(ctx context.Context, f claudeControlFixture) (*claudeTransport, error) {
	p, err := connectClaude(ctx, f.Instance, f.State)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"native_id": f.Native, "source": f.Native, "cwd": f.Instance.Profile,
		"executable": "/unused/fixture", "model": "fixture", "endpoint": "http://127.0.0.1:1", "permission": "read_only", "api_key": "fixture"}
	if err = p.call(ctx, "create", params, nil); err == nil {
		err = p.call(ctx, "send", map[string]any{"native_id": f.Native, "operation": f.Input}, nil)
	}
	if err == nil && f.Submit {
		err = p.call(ctx, "respond", map[string]any{"native_id": f.Native}, nil)
	}
	if err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

// A real, separately killable Go owner uses the production transport and main
// entrypoint. Only Session is substituted, keeping this feedback loop local.
func TestClaudeControllerProcessHelper(t *testing.T) {
	path := os.Getenv("NETCAP_CLAUDE_CONTROLLER_FIXTURE")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	var f claudeControlFixture
	if err != nil || json.Unmarshal(data, &f) != nil {
		t.Fatal("invalid owned fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := startClaudeControlFixture(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	var snapshot map[string]any
	if err = p.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &snapshot); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(snapshot)
	fmt.Println(string(data))
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestClaudeControllerSurvivesGoCrashWithoutReplayingInput(t *testing.T) {
	for _, submitted := range []bool{true, false} {
		t.Run(fmt.Sprintf("approval_submitted_%t", submitted), func(t *testing.T) {
			testClaudeControllerGoCrash(t, submitted)
		})
	}
}

func testClaudeControllerGoCrash(t *testing.T, submitted bool) {
	f := newClaudeControlFixture(t)
	f.Submit = submitted
	path := filepath.Join(f.Instance.Profile, "fixture.json")
	data, _ := json.Marshal(f)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	cmd := exec.Command(self, "-test.run=^TestClaudeControllerProcessHelper$")
	cmd.Env = append(os.Environ(), "NETCAP_CLAUDE_CONTROLLER_FIXTURE="+path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = in.Close()
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- line }()
	var before map[string]any
	select {
	case line := <-ready:
		if err := json.Unmarshal([]byte(line), &before); err != nil {
			t.Fatalf("controller fixture did not start: %q", line)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("controller fixture startup timed out")
	}
	pid := int(before["bridge_pid"].(float64))
	birth := processBirth(pid)
	t.Cleanup(func() {
		if birth != "" && processBirth(pid) == birth {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	waited = true
	if err == nil || cmd.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("original Go owner did not actually crash", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var recovered *claudeTransport
	for ctx.Err() == nil {
		recovered, err = connectClaude(ctx, f.Instance, f.State)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("cannot reconnect original controller", err)
	}
	defer recovered.close()
	read := func() map[string]any {
		t.Helper()
		var snapshot map[string]any
		if err := recovered.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	after := read()
	if after["bridge_pid"] != before["bridge_pid"] || after["connection"] != before["connection"] || after["live"] != true || after["active"] != f.Input {
		t.Fatalf("Go crash closed/replaced the original query: before=%v after=%v", before, after)
	}
	wantExecutions := float64(0)
	if submitted {
		wantExecutions = 1
	}
	if after["executions"] != wantExecutions || len(after["inputs"].([]any)) != 1 {
		t.Fatal("reconnect replayed the original input or tool", after)
	}
	priorPending, _ := json.Marshal(before["pending"])
	currentPending, _ := json.Marshal(after["pending"])
	if string(priorPending) != string(currentPending) {
		t.Fatal("original approval identity or state changed", after)
	}
	if !submitted {
		if err := recovered.call(ctx, "respond", map[string]any{"native_id": f.Native}, nil); err != nil {
			t.Fatal("could not answer the original pending request", err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.State, "release"), []byte("finish original tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for len(after["results"].([]any)) == 0 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
		after = read()
	}
	results := after["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["output"] != "中文完整结果" || results[0].(map[string]any)["operation"] != f.Input {
		t.Fatal("original result did not remain available after reconnect", after)
	}
	starts, _ := os.ReadFile(filepath.Join(f.State, "bridge-starts.jsonl"))
	if len(strings.FieldsFunc(string(starts), func(r rune) bool { return r == '\n' })) != 1 {
		t.Fatal("reconnect started a second bridge")
	}
}

func TestClaudeControllerRejectsSecondOwnerAndInstanceDrift(t *testing.T) {
	f := newClaudeControlFixture(t)
	// Exercise the production directory-FD addressing with a path too long to
	// pass directly to bind/connect, including Windows-relevant Unicode/spaces.
	f.State = filepath.Join(f.Instance.Profile, strings.Repeat("原实例 ", 25))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := startClaudeControlFixture(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if other, err := connectClaude(ctx, f.Instance, f.State); err == nil {
		other.close()
		t.Fatal("a second live controller acquired the same instance")
	}
	var before map[string]any
	if err := p.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &before); err != nil {
		t.Fatal("rejected controller disturbed original owner", err)
	}
	p.detach()
	changed := f.Instance
	changed.Profile = filepath.Join(f.Instance.Profile, "other-profile")
	if other, err := connectClaude(ctx, changed, f.State); err == nil {
		other.close()
		t.Fatal("another profile reused the original controller")
	}
	var original *claudeTransport
	for ctx.Err() == nil {
		original, err = connectClaude(ctx, f.Instance, f.State)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("original binding could not reconnect", err)
	}
	defer original.close()
	var after map[string]any
	if err := original.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &after); err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(before)
	y, _ := json.Marshal(after)
	if string(x) != string(y) {
		t.Fatal("rejected binding changed the original execution")
	}
}

func TestClaudeControllerWaitsForAcceptedCommandBeforeReattaching(t *testing.T) {
	f := newClaudeControlFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := startClaudeControlFixture(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	gate := filepath.Join(f.State, "interrupt-release")
	defer os.WriteFile(gate, []byte("cleanup original command"), 0600)
	finished := make(chan error, 1)
	go func() { finished <- p.call(ctx, "interrupt", map[string]any{"native_id": f.Native}, nil) }()
	for ctx.Err() == nil {
		if _, err := os.Stat(filepath.Join(f.State, "interrupt-started")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("original command never reached the bridge")
	}
	p.detach()
	if err := <-finished; err == nil {
		t.Fatal("lost reply was reported as delivered")
	}
	if other, err := connectClaude(ctx, f.Instance, f.State); err == nil {
		other.detach()
		t.Fatal("new controller overlapped an accepted, unsettled command")
	}
	if err := os.WriteFile(gate, []byte("settle original command"), 0600); err != nil {
		t.Fatal(err)
	}
	var recovered *claudeTransport
	for ctx.Err() == nil {
		recovered, err = connectClaude(ctx, f.Instance, f.State)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("settled original controller could not reconnect", err)
	}
	defer recovered.close()
	var snapshot map[string]any
	if err := recovered.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["interrupts"] != float64(1) || snapshot["executions"] != float64(1) || snapshot["active"] != f.Input {
		t.Fatal("accepted command or tool was replayed after disconnect", snapshot)
	}
}

func TestClaudeControllerRejectsUnverifiedPeer(t *testing.T) {
	for _, mode := range []string{"pid", "birth", "binding", "state-directory", "capability", "socket-replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newClaudeControlFixture(t)
			if err := os.Mkdir(f.State, 0700); err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(filepath.Join(f.State, "bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			directory, err := os.Open(f.State)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			listener, err := net.Listen("unix", fmt.Sprintf("/proc/self/fd/%d/controller.sock", directory.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			path := filepath.Join(f.State, "controller.sock")
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					served <- err
					return
				}
				defer connection.Close()
				line, err := bufio.NewReader(connection).ReadBytes('\n')
				var request map[string]any
				if err != nil || json.Unmarshal(line, &request) != nil {
					served <- fmt.Errorf("invalid handshake fixture: %v", err)
					return
				}
				hello := claudeHello{Version: 1, SDK: "0.3.280", PersistentControl: 1}
				hello.Controller.Version, hello.Controller.PID = 1, os.Getpid()
				hello.Controller.Birth, hello.Controller.StateDir = processBirth(os.Getpid()), f.State
				hello.Controller.Binding = claudeControllerBinding(f.Instance, f.State)
				switch mode {
				case "pid":
					hello.Controller.PID++
				case "birth":
					hello.Controller.Birth += "0"
				case "binding":
					hello.Controller.Binding = strings.Repeat("0", 64)
				case "state-directory":
					hello.Controller.StateDir = filepath.Dir(f.State)
				case "capability":
					hello.PersistentControl = 0
				case "socket-replaced":
					if err := os.Rename(path, path+".old"); err != nil {
						served <- err
						return
					}
				}
				response, _ := json.Marshal(map[string]any{"v": 1, "id": request["id"], "result": hello})
				_, err = connection.Write(append(response, '\n'))
				if err == nil {
					_, err = io.Copy(io.Discard, connection)
				}
				served <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			p, err := connectClaude(ctx, f.Instance, f.State)
			if p != nil {
				p.detach()
			}
			if err == nil {
				t.Fatal("unverified peer acquired execution control")
			}
			select {
			case err := <-served:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("unverified peer was not disconnected")
			}
			if _, err := os.Stat(filepath.Join(f.State, "bridge-starts.jsonl")); !os.IsNotExist(err) {
				t.Fatal("failed verification started another bridge")
			}
		})
	}
}

func TestClaudeControllerRetainsOriginalLaunchEnvironment(t *testing.T) {
	f := newClaudeControlFixture(t)
	const key = "NETCAP_CLAUDE_CONTROL_ENV_TEST"
	f.Instance.EnvAllow = []string{key}
	t.Setenv(key, "original")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	p, err := startClaudeControlFixture(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	engine := &Claude{clients: map[string]*claudeTransport{f.Instance.ID: p}}
	t.Setenv(key, "changed")
	if _, err := engine.client(ctx, f.Instance); err == nil {
		t.Fatal("live bridge silently reused an older launch environment")
	}
	p.detach()
	if other, err := connectClaude(ctx, f.Instance, f.State); err == nil {
		other.detach()
		t.Fatal("reconnect claimed a different launch environment")
	}
	t.Setenv(key, "original")
	// Display labels do not change the process's execution identity.
	renamed := f.Instance
	renamed.Name = "renamed display label"
	var recovered *claudeTransport
	for ctx.Err() == nil {
		recovered, err = connectClaude(ctx, renamed, f.State)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("original environment could not reconnect", err)
	}
	defer recovered.close()
	var snapshot map[string]any
	if err := recovered.call(ctx, "snapshot", map[string]any{"native_id": f.Native}, &snapshot); err != nil || snapshot["active"] != f.Input || snapshot["executions"] != float64(1) {
		t.Fatal("environment rejection replaced or replayed original work", err, snapshot)
	}
}

func TestClaudeControllerLegacyStdioStillClosesOnEOF(t *testing.T) {
	f := newClaudeControlFixture(t)
	if err := os.Mkdir(f.State, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(f.Instance.NodeExecutable, f.Instance.Bridge, f.State)
	cmd.Env = claudeEnv(f.Instance)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &claudeTransport{cmd: cmd, input: in, pending: map[string]chan bridgeResponse{}, done: make(chan struct{}), exited: make(chan struct{})}
	go p.read(out)
	go func() { _ = cmd.Wait(); close(p.exited); p.fail(io.EOF) }()
	defer p.close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hello, err := p.hello(ctx, map[string]any{})
	if err != nil || hello.PersistentControl != 0 {
		t.Fatal("plain stdio entrypoint changed its handshake", err)
	}
	err = p.call(ctx, "create", map[string]any{"native_id": f.Native, "source": f.Native, "cwd": f.Instance.Profile,
		"executable": "/unused/fixture", "model": "fixture", "endpoint": "http://127.0.0.1:1", "permission": "read_only", "api_key": "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.close()
	closed, err := os.ReadFile(filepath.Join(f.State, "closed.jsonl"))
	if err != nil || !strings.Contains(string(closed), f.Native) || cmd.ProcessState.ExitCode() != 0 {
		t.Fatal("plain stdio EOF did not close its owned session", err)
	}
	if _, err := os.Stat(filepath.Join(f.State, "controller.sock")); !os.IsNotExist(err) {
		t.Fatal("plain stdio unexpectedly created a persistent endpoint")
	}
}
