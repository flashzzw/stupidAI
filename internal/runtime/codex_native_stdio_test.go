//go:build linux && nativecodex

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"codex-netcap/internal/codex"
)

// This opt-in transport connects the production Runtime to a real isolated
// CLI. It does not test the Unix proxy, peer identity or credential verifier.
type nativeStdio struct {
	cmd       *exec.Cmd
	input     io.WriteCloser
	output    io.ReadCloser
	log       *os.File
	reply     chan codex.Message
	done      chan struct{}
	wait      chan struct{}
	mu        sync.Mutex
	events    []codex.Message
	allEvents []codex.Message
	requests  map[string]codex.Message
	readErr   error
	seq       int
	record    *nativeStdioRecord
	once      sync.Once
}

type nativeStdioRecord struct {
	Label    string           `json:"label"`
	PID      int              `json:"pid"`
	Exited   bool             `json:"exited"`
	ExitCode int              `json:"exit_code"`
	Calls    []map[string]any `json:"calls"`
	Events   []codex.Message  `json:"events"`
	Stderr   string           `json:"stderr"`
}

func startNativeStdio(t *testing.T, ctx context.Context, executable, root, label string, env []string, record *nativeStdioRecord) *nativeStdio {
	t.Helper()
	log, err := os.Create(filepath.Join(root, label+".stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.CommandContext(ctx, executable, "app-server", "--stdio")
	cmd.Env, cmd.Dir, cmd.Stderr = env, filepath.Join(root, "work"), log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &nativeStdio{cmd: cmd, input: input, output: output, log: log, reply: make(chan codex.Message, 1), done: make(chan struct{}), wait: make(chan struct{}), requests: map[string]codex.Message{}, record: record}
	record.Label, record.PID = label, cmd.Process.Pid
	go func() { _ = cmd.Wait(); close(p.wait) }()
	go p.read()
	t.Cleanup(func() { _ = p.Close() })
	var init struct {
		CodexHome string `json:"codexHome"`
	}
	if err = p.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "netcap_runtime_native_test", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, &init); err != nil {
		t.Fatal(err)
	}
	if init.CodexHome != filepath.Join(root, "codex") {
		t.Fatalf("unexpected native home: %q", init.CodexHome)
	}
	if err = json.NewEncoder(input).Encode(map[string]string{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *nativeStdio) read() {
	defer func() {
		p.mu.Lock()
		if p.readErr == nil {
			p.readErr = io.EOF
		}
		p.mu.Unlock()
		close(p.done)
	}()
	scanner := bufio.NewScanner(p.output)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	for scanner.Scan() {
		var m codex.Message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			p.mu.Lock()
			p.readErr = err
			p.mu.Unlock()
			return
		}
		if len(m.ID) > 0 && m.Method == "" {
			select {
			case p.reply <- m:
			case <-p.wait:
				return
			}
			continue
		}
		p.mu.Lock()
		p.events, p.allEvents = append(p.events, m), append(p.allEvents, m)
		if len(m.ID) > 0 {
			p.requests[string(m.ID)] = m
		}
		if m.Method == "serverRequest/resolved" {
			var resolved struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &resolved) == nil {
				delete(p.requests, string(resolved.RequestID))
			}
		}
		p.mu.Unlock()
	}
	p.mu.Lock()
	p.readErr = scanner.Err()
	if p.readErr == nil {
		p.readErr = io.EOF
	}
	p.mu.Unlock()
}

func (p *nativeStdio) Call(ctx context.Context, method string, params, out any) error {
	p.seq++
	id := json.RawMessage(fmt.Sprint(p.seq))
	record := map[string]any{"method": method, "params": params}
	if label, ok := ctx.Value(nativeTraceContextKey{}).(string); ok {
		record["trace_context"] = label
	}
	p.record.Calls = append(p.record.Calls, record)
	if err := json.NewEncoder(p.input).Encode(codex.Message{ID: id, Method: method, Params: mustNativeJSON(params)}); err != nil {
		return err
	}
	select {
	case m := <-p.reply:
		if string(m.ID) != string(id) {
			return fmt.Errorf("native response ID mismatch")
		}
		if m.Error != nil {
			m.Error.Method = method
			record["error"] = m.Error
			return m.Error
		}
		record["result"] = m.Result
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.readErr
	}
}

func mustNativeJSON(value any) json.RawMessage {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return b
}
func (p *nativeStdio) ConnectionID() string {
	return fmt.Sprintf("%s/%d", p.record.Label, p.record.PID)
}
func (p *nativeStdio) Drain() []codex.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := p.events
	p.events = nil
	return events
}
func (p *nativeStdio) PendingRequest(id json.RawMessage) (codex.Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.requests[string(id)]
	return r, ok && p.readErr == nil
}
func (p *nativeStdio) Respond(_ context.Context, id json.RawMessage, result any) error {
	return json.NewEncoder(p.input).Encode(codex.Message{ID: id, Result: mustNativeJSON(result)})
}
func (p *nativeStdio) Close() error {
	p.once.Do(func() {
		_ = p.input.Close()
		// Signal the exec.Cmd process handle, not a potentially reused raw PID.
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.wait:
		case <-time.After(time.Second):
			_ = p.cmd.Process.Kill()
			<-p.wait
		}
		_ = p.output.Close()
		<-p.done
		_ = p.log.Close()
		stderr, _ := os.ReadFile(p.log.Name())
		p.record.Stderr = string(stderr)
		p.record.Exited, p.record.ExitCode = p.cmd.ProcessState.Exited() || p.cmd.ProcessState.Sys().(syscall.WaitStatus).Signaled(), p.cmd.ProcessState.ExitCode()
		p.mu.Lock()
		p.record.Events = append([]codex.Message(nil), p.allEvents...)
		p.mu.Unlock()
	})
	return nil
}

func TestCodexNativeRuntimePermissionAndRecovery(t *testing.T) {
	executable := os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	if !filepath.IsAbs(executable) {
		t.Fatal("NETCAP_NATIVE_CODEX_TEST must select the fixed 0.158.0 executable")
	}
	for _, permission := range []string{"workspace", "read_only"} {
		t.Run(permission, func(t *testing.T) {
			root := t.TempDir()
			env := []string{"PATH=" + os.Getenv("PATH"), "NETCAP_FIXTURE_KEY=synthetic-only"}
			for key, dir := range map[string]string{"HOME": "home", "CODEX_HOME": "codex", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
				path := filepath.Join(root, dir)
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				env = append(env, key+"="+path)
			}
			if err := os.Mkdir(filepath.Join(root, "work"), 0700); err != nil {
				t.Fatal(err)
			}
			defaultSandbox := "read-only"
			if permission == "read_only" {
				defaultSandbox = "workspace-write"
			}
			config := fmt.Sprintf(`model="gpt-6-astra"
model_provider="fixture"
approval_policy="on-request"
sandbox_mode=%q
[model_providers.fixture]
name="Synthetic unavailable local provider"
base_url="http://127.0.0.1:9/v1"
env_key="NETCAP_FIXTURE_KEY"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
request_max_retries=0
stream_max_retries=0
[features]
apps=false
plugins=false
shell_snapshot=false
`, defaultSandbox)
			if err := os.WriteFile(filepath.Join(root, "codex/config.toml"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			t.Cleanup(cancel)
			version := exec.CommandContext(ctx, executable, "--version")
			version.Env = env
			v, err := version.Output()
			if err != nil || strings.TrimSpace(string(v)) != "codex-cli 0.158.0" {
				t.Fatalf("unverified CLI: %q %v", v, err)
			}
			records := []*nativeStdioRecord{}
			checks := map[string]bool{}
			views := map[string]View{}
			creationHeader := map[string]any{}
			matchesRequestedPolicy := func(view View) bool {
				if view.PermissionEvidence == nil {
					return false
				}
				var sandbox struct {
					Type string `json:"type"`
				}
				var approval string
				if json.Unmarshal(view.PermissionEvidence.Sandbox, &sandbox) != nil || json.Unmarshal(view.PermissionEvidence.ApprovalPolicy, &approval) != nil {
					return false
				}
				want := "workspaceWrite"
				if permission == "read_only" {
					want = "readOnly"
				}
				return sandbox.Type == want && approval == "on-request"
			}
			t.Cleanup(func() {
				if dir := os.Getenv("NETCAP_NATIVE_CODEX_EVIDENCE"); dir != "" {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Error(err)
						return
					}
					report := map[string]any{"version": strings.TrimSpace(string(v)), "permission": permission, "profile_default_sandbox": defaultSandbox, "root": root, "checks": checks, "views": views, "creation_header": creationHeader, "processes": records, "passed": !t.Failed(), "scope": "production Codex Runtime with test-only real stdio transport; no Unix proxy/peer/auth verification; synthetic unavailable loopback provider"}
					b, _ := json.MarshalIndent(report, "", "  ")
					if err := os.WriteFile(filepath.Join(dir, permission+".json"), append(b, '\n'), 0600); err != nil {
						t.Error(err)
					}
				}
			})
			check := func(name string, ok bool) {
				t.Helper()
				checks[name] = ok
				if !ok {
					t.Fatalf("native check failed: %s", name)
				}
				t.Logf("native check passed: %s", name)
			}
			start := func(label string) *nativeStdio {
				r := &nativeStdioRecord{}
				records = append(records, r)
				return startNativeStdio(t, ctx, executable, root, label, env, r)
			}
			p := start("initial")
			c := NewCodex()
			t.Cleanup(func() { _ = c.Close() })
			scope := Scope{Instance: Instance{ID: "native-test", Kind: "codex", Profile: filepath.Join(root, "codex")}, Cwd: filepath.Join(root, "work"), Route: Route{Provider: "fixture", Model: "gpt-6-astra", Endpoint: "http://127.0.0.1:9/v1", CredentialRef: "profile:fixture"}, RecoveryPermission: permission}
			c.clients[scope.Instance.ID] = p
			created, err := c.Execute(ctx, Intent{ID: "synthetic-create", Kind: "create", Scope: scope, Permission: permission})
			check("runtime_create", err == nil && created.NativeID != "")
			scope.NativeID = created.NativeID
			creation, err := c.Reconcile(ctx, Intent{ID: "synthetic-create", Kind: "create", Scope: scope, Permission: permission}, created)
			check("runtime_create_reconciled", err == nil && creation.State == "finished" && creation.Status == "succeeded")
			fresh, err := c.Observe(ctx, scope)
			views["fresh"] = fresh
			if err != nil {
				t.Logf("fresh observe error: %v", err)
			}
			check("fresh_creation_policy", err == nil && fresh.ExecutionClear && matchesRequestedPolicy(fresh) && fresh.PermissionEvidence.Verified && fresh.PermissionEvidence.Source == "codex.thread/start")
			if err = p.Call(ctx, "thread/inject_items", map[string]any{"threadId": scope.NativeID, "items": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "SYNTHETIC HISTORY; no model turn"}}}}}, nil); err != nil {
				t.Fatal(err)
			}
			var originalGoal any
			if err = p.Call(ctx, "thread/goal/set", map[string]any{"threadId": scope.NativeID, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"}, &originalGoal); err != nil {
				t.Fatal(err)
			}
			loaded, err := c.Observe(ctx, scope)
			views["loaded"] = loaded
			if err != nil {
				t.Logf("loaded observe error: %v", err)
			}
			check("loaded_permission", err == nil && loaded.ExecutionClear && matchesRequestedPolicy(loaded) && loaded.PermissionEvidence.Verified && loaded.PermissionEvidence.Source == "codex.thread/resume")
			var goalAfter any
			if err = p.Call(ctx, "thread/goal/get", map[string]any{"threadId": scope.NativeID}, &goalAfter); err != nil {
				t.Fatal(err)
			}
			check("loaded_goal_preserved", reflect.DeepEqual(originalGoal, goalAfter))
			_ = c.Close()
			p = start("restarted")
			c.clients[scope.Instance.ID] = p
			disk, err := codex.Read(ctx, p, scope.NativeID)
			check("same_unloaded_thread", err == nil && disk.ID == scope.NativeID && disk.Status.Type == "notLoaded")
			f, err := os.Open(disk.Path)
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 65536), 1<<20)
			if !scanner.Scan() {
				_ = f.Close()
				t.Fatal("missing native metadata header")
			}
			var header struct {
				Payload map[string]json.RawMessage `json:"payload"`
			}
			err = json.Unmarshal(scanner.Bytes(), &header)
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"id", "session_id", "cwd", "thread_source", "model_provider", "cli_version"} {
				creationHeader[key] = header.Payload[key]
			}
			createIntent := Intent{ID: "synthetic-create", Kind: "create", Scope: scope, Permission: permission}
			createIntent.Scope.NativeID = ""
			withoutReceipt, withoutErr := c.Reconcile(ctx, createIntent, Receipt{})
			withReceipt, withErr := c.Reconcile(ctx, createIntent, created)
			views["cold_creation_with_receipt"], views["cold_creation_without_receipt"] = withReceipt.View, withoutReceipt.View
			t.Logf("cold creation: with receipt=%s (%v, %s); without receipt=%s (%v, %s)", withReceipt.State, withErr, withReceipt.Evidence.Detail, withoutReceipt.State, withoutErr, withoutReceipt.Evidence.Detail)
			check("cold_creation_with_receipt", withErr == nil && withReceipt.State == "finished" && withReceipt.Receipt.NativeID == scope.NativeID)
			check("cold_creation_without_receipt", withoutErr == nil && withoutReceipt.State == "finished" && withoutReceipt.Receipt.NativeID == scope.NativeID)
			restored, err := c.Observe(ctx, scope)
			views["restored"] = restored
			if err != nil {
				t.Logf("cold observe error: %v", err)
			}
			check("runtime_cold_permission", err == nil && restored.NativeID == scope.NativeID && restored.ExecutionClear && matchesRequestedPolicy(restored) && restored.PermissionEvidence.Verified && reflect.DeepEqual(restored.PermissionEvidence.Sandbox, loaded.PermissionEvidence.Sandbox))
			if err = p.Call(ctx, "thread/goal/get", map[string]any{"threadId": scope.NativeID}, &goalAfter); err != nil {
				t.Fatal(err)
			}
			check("cold_goal_preserved", reflect.DeepEqual(originalGoal, goalAfter))
			input := Intent{ID: "synthetic-input", Kind: "send", Scope: scope, Permission: permission, Text: "SYNTHETIC INPUT to unavailable local provider"}
			receipt, err := c.Execute(ctx, input)
			if err != nil {
				t.Logf("send error: %v", err)
			}
			check("runtime_same_id_send", err == nil && receipt.NativeID == scope.NativeID && receipt.NativeTurn != "")
			resolution, err := c.Reconcile(ctx, input, receipt)
			views["first_send_observation"] = resolution.View
			// The native turn/start receipt can precede its user item. Reconcile
			// must wait for that input marker without replaying the operation.
			deadline := time.Now().Add(5 * time.Second)
			for err == nil && resolution.State == "unknown" && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				resolution, err = c.Reconcile(ctx, input, receipt)
			}
			views["running_turn"] = resolution.View
			check("running_turn_correlated", err == nil && resolution.State == "running" && resolution.Receipt.NativeTurn == receipt.NativeTurn && !resolution.View.ExecutionClear)
			before := len(p.record.Calls)
			_, err = c.Execute(ctx, Intent{ID: "duplicate-input", Kind: "send", Scope: scope, Permission: permission, Text: "MUST NOT START"})
			check("second_input_rejected", err != nil)
			mutated := false
			for _, call := range p.record.Calls[before:] {
				if call["method"] == "thread/resume" || call["method"] == "turn/start" {
					mutated = true
				}
			}
			check("running_observation_did_not_resume_or_send", !mutated)
			interrupt := Intent{ID: "interrupt-original", Kind: "interrupt", Scope: scope, Permission: permission, NativeTurn: receipt.NativeTurn, OwnedInputs: []string{input.ID}}
			interrupted, err := c.Execute(ctx, interrupt)
			if err != nil {
				t.Logf("interrupt error: %v", err)
			}
			check("interrupt_original_turn", err == nil && interrupted.NativeTurn == receipt.NativeTurn)
			deadline = time.Now().Add(10 * time.Second)
			for {
				resolution, err = c.Reconcile(ctx, input, receipt)
				if err != nil || resolution.State == "finished" || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			views["interrupted_turn"] = resolution.View
			if err != nil {
				t.Logf("reconcile error: %v", err)
			}
			check("interrupted_turn_correlated", err == nil && resolution.State == "finished" && resolution.Status == "interrupted" && resolution.Receipt.NativeTurn == receipt.NativeTurn)
			check("one_original_turn", len(resolution.View.Turns) == 1 && resolution.View.Turns[0].ID == receipt.NativeTurn)
			if err = p.Call(ctx, "thread/goal/get", map[string]any{"threadId": scope.NativeID}, &goalAfter); err != nil {
				t.Fatal(err)
			}
			check("goal_still_paused", reflect.DeepEqual(originalGoal, goalAfter))
			// An active native Goal may occupy an idle gap without a model turn.
			// Its fixture endpoint is still unavailable; no real model is invoked.
			if err = p.Call(ctx, "thread/goal/set", map[string]any{"threadId": scope.NativeID, "status": "active"}, nil); err != nil {
				t.Fatal(err)
			}
			before = len(p.record.Calls)
			goalView, err := c.Observe(ctx, scope)
			views["active_goal"] = goalView
			goalBusy := false
			for _, execution := range goalView.Executions {
				if execution.Kind == "native_goal" && execution.Status == "running" {
					goalBusy = true
				}
			}
			check("active_goal_occupancy", err == nil && goalBusy && !goalView.ExecutionClear && goalView.Status == "running")
			_, err = c.Execute(ctx, Intent{ID: "goal-conflicting-input", Kind: "send", Scope: scope, Permission: permission, Text: "MUST NOT INTERRUPT GOAL"})
			check("input_during_active_goal_rejected", err != nil)
			mutated = false
			for _, call := range p.record.Calls[before:] {
				if call["method"] == "thread/resume" || call["method"] == "turn/start" {
					mutated = true
				}
			}
			check("active_goal_observation_did_not_resume_or_send", !mutated)
			if err = p.Call(ctx, "thread/goal/set", map[string]any{"threadId": scope.NativeID, "status": "paused"}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
