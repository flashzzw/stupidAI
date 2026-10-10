//go:build linux && nativecodex

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
	"testing"
	"time"

	"codex-netcap/internal/codex"
)

// NativeApplicationFixture exposes the existing opt-in stdio transport only
// to external tests of Application. Production binaries gain no bypass API.
type NativeApplicationFixture struct {
	Runtime  *Codex
	Root     string
	Instance Instance
	Route    Route
	Records  []*nativeStdioRecord
	ctx      context.Context
	exe      string
	env      []string
	p        *nativeStdio
}

func NewNativeApplicationFixture(t *testing.T, permission string) *NativeApplicationFixture {
	return NewNativeApplicationFixtureAt(t, permission, t.TempDir())
}

func NewNativeApplicationFixtureAt(t *testing.T, permission, root string, timeout ...time.Duration) *NativeApplicationFixture {
	return nativeApplicationFixtureAt(t, permission, root, true, timeout...)
}

func ReopenNativeApplicationFixture(t *testing.T, permission, root string) *NativeApplicationFixture {
	return nativeApplicationFixtureAt(t, permission, root, false)
}

func nativeApplicationFixtureAt(t *testing.T, permission, root string, initialize bool, timeout ...time.Duration) *NativeApplicationFixture {
	t.Helper()
	f := &NativeApplicationFixture{Root: root, exe: os.Getenv("NETCAP_NATIVE_CODEX_TEST")}
	if !filepath.IsAbs(f.exe) {
		t.Fatal("NETCAP_NATIVE_CODEX_TEST must select the fixed native executable")
	}
	f.env = []string{"PATH=" + os.Getenv("PATH"), "NETCAP_FIXTURE_KEY=synthetic-only"}
	for key, dir := range map[string]string{"HOME": "home", "CODEX_HOME": "codex", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		path := filepath.Join(f.Root, dir)
		if initialize {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("missing fixture directory %s: %v", path, err)
		}
		f.env = append(f.env, key+"="+path)
	}
	if initialize {
		if err := os.Mkdir(filepath.Join(f.Root, "work"), 0700); err != nil {
			t.Fatal(err)
		}
		defaultSandbox := "read-only"
		if permission == "read_only" {
			defaultSandbox = "workspace-write"
		}
		configuration := fmt.Sprintf(`model="gpt-6-astra"
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
		if err := os.WriteFile(filepath.Join(f.Root, "codex/config.toml"), []byte(configuration), 0600); err != nil {
			t.Fatal(err)
		}
	}
	duration := 60 * time.Second
	if len(timeout) > 1 {
		t.Fatal("fixture accepts one timeout")
	}
	if len(timeout) == 1 {
		duration = timeout[0]
	}
	if duration <= 0 || duration > 3*time.Minute {
		t.Fatal("invalid bounded fixture timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	t.Cleanup(cancel)
	f.ctx = ctx
	version := exec.CommandContext(ctx, f.exe, "--version")
	version.Env = f.env
	out, err := version.Output()
	if err != nil || strings.TrimSpace(string(out)) != "codex-cli 0.158.0" {
		t.Fatalf("unexpected CLI version: %q %v", out, err)
	}
	proxy := os.Getenv("NETCAP_NATIVE_CODEX_PROXY_TEST")
	if !filepath.IsAbs(proxy) {
		t.Fatal("NETCAP_NATIVE_CODEX_PROXY_TEST must select the fixed 0.157.1 proxy")
	}
	f.Instance = Instance{ID: "native-application", Kind: "codex", Executable: proxy, Version: "0.157.1", Profile: filepath.Join(f.Root, "codex")}
	f.Route = Route{ID: "fixture", InstanceID: f.Instance.ID, Provider: "fixture", Model: "gpt-6-astra", Endpoint: "http://127.0.0.1:9/v1", CredentialRef: "profile:fixture"}
	f.Restart(t)
	return f
}

type NativeProcessCheckpoint struct {
	PID   int             `json:"pid"`
	Birth string          `json:"birth"`
	Trace json.RawMessage `json:"trace"`
}

type nativeTraceContextKey struct{}

// NativeTraceContext attributes fixture calls to one caller while background
// Application observations continue to use the same real stdio connection.
func NativeTraceContext(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, nativeTraceContextKey{}, label)
}

func (p NativeProcessCheckpoint) MatchesLiveProcess() bool {
	return p.Birth != "" && processBirth(p.PID) == p.Birth
}

// Checkpoint freezes the real protocol trace at a stopped application boundary.
func (f *NativeApplicationFixture) Checkpoint() (NativeProcessCheckpoint, error) {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	f.p.record.Events = append([]codex.Message(nil), f.p.allEvents...)
	trace, err := json.Marshal(f.Records)
	return NativeProcessCheckpoint{PID: f.p.cmd.Process.Pid, Birth: processBirth(f.p.cmd.Process.Pid), Trace: trace}, err
}

// Counter assertions should not repeatedly encode the full config/history
// trace. Keep full records for evidence, but read only the matching requests.
func (f *NativeApplicationFixture) CountMethod(method string) int {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	n := 0
	for _, record := range f.Records {
		for _, call := range record.Calls {
			if call["method"] == method {
				n++
			}
		}
	}
	return n
}

func (f *NativeApplicationFixture) CountConfigurationResumes(nativeID string) (int, error) {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	n := 0
	for _, record := range f.Records {
		for _, call := range record.Calls {
			if call["method"] != "thread/resume" {
				continue
			}
			b, err := json.Marshal(call["params"])
			if err != nil {
				return 0, err
			}
			var params struct {
				ThreadID string          `json:"threadId"`
				Config   json.RawMessage `json:"config"`
			}
			if err := json.Unmarshal(b, &params); err != nil {
				return 0, err
			}
			if params.ThreadID == nativeID && len(params.Config) > 0 {
				n++
			}
		}
	}
	return n, nil
}

// HasTurnEvent checks the live notification stream without serializing the
// entire accumulated protocol trace on each polling tick. Full evidence is
// still captured by Checkpoint/Close at the required boundaries.
func (f *NativeApplicationFixture) HasTurnEvent(method, threadID, turnID string) bool {
	if threadID == "" || turnID == "" {
		return false
	}
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	for i := len(f.p.allEvents) - 1; i >= 0; i-- {
		event := f.p.allEvents[i]
		if event.Method != method {
			continue
		}
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(event.Params, &params) != nil {
			continue
		}
		if params.TurnID == "" {
			params.TurnID = params.Turn.ID
		}
		if params.ThreadID == threadID && params.TurnID == turnID {
			return true
		}
	}
	return false
}

func (f *NativeApplicationFixture) Restart(t *testing.T) {
	t.Helper()
	if f.Runtime != nil {
		_ = f.Runtime.Close()
	}
	record := &nativeStdioRecord{}
	f.Records = append(f.Records, record)
	f.p = startNativeStdio(t, f.ctx, f.exe, f.Root, fmt.Sprintf("application-%d", len(f.Records)), f.env, record)
	f.Runtime = NewCodex()
	f.Runtime.clients[f.Instance.ID] = f.p
}

// Call serializes fixture setup/inspection with production Runtime reads.
func (f *NativeApplicationFixture) Call(ctx context.Context, method string, params, out any) error {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	return f.p.Call(ctx, method, params, out)
}

// DropCreateReceipt consumes a genuine native reply at the transport boundary,
// then closes that connection without delivering the reply to Codex.Execute.
func (f *NativeApplicationFixture) DropCreateReceipt(listed bool) (<-chan Receipt, chan struct{}) {
	p := &lostNativeCreate{nativeStdio: f.p, created: make(chan Receipt, 1), release: make(chan struct{}), listed: listed}
	f.Runtime.clients[f.Instance.ID] = p
	return p.created, p.release
}

type lostNativeCreate struct {
	*nativeStdio
	created chan Receipt
	release chan struct{}
	listed  bool
}

func (p *lostNativeCreate) Call(ctx context.Context, method string, params, out any) error {
	if method != "thread/start" {
		return p.nativeStdio.Call(ctx, method, params, out)
	}
	var started codex.Started
	if err := p.nativeStdio.Call(ctx, method, params, &started); err != nil {
		return err
	}
	if err := p.nativeStdio.Call(ctx, "thread/inject_items", map[string]any{"threadId": started.Thread.ID, "items": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "SYNTHETIC DURABLE HISTORY; no model turn"}}}}}, nil); err != nil {
		return err
	}
	if p.listed {
		if err := p.nativeStdio.Call(ctx, "thread/goal/set", map[string]any{"threadId": started.Thread.ID, "objective": "SYNTHETIC PAUSED GOAL; never execute", "status": "paused"}, nil); err != nil {
			return err
		}
	}
	p.created <- Receipt{NativeID: started.Thread.ID}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	_ = p.Close()
	return io.ErrUnexpectedEOF
}

// DropConfigurationReceipt loses a real resume reply before Runtime can attest
// the configuration. Closing this owned stdio fixture also exits its daemon;
// this does not model a still-running shared production daemon.
func (f *NativeApplicationFixture) DropConfigurationReceipt(nativeID string) <-chan NativeConfigurationReply {
	return f.dropPluginChangeReply(nativeID, "thread/resume", 1)
}

// DropPluginChangeReceipt discards a real reply at the named cold transition
// boundary. Runtime observes EOF only after native has completed that RPC.
func (f *NativeApplicationFixture) DropPluginChangeReceipt(nativeID, boundary string) <-chan NativeConfigurationReply {
	switch boundary {
	case "bootstrap":
		return f.dropPluginChangeReply(nativeID, "thread/resume", 1)
	case "policy":
		return f.dropPluginChangeReply(nativeID, "thread/settings/update", 1)
	case "unsubscribe":
		return f.dropPluginChangeReply(nativeID, "thread/unsubscribe", 1)
	case "final":
		return f.dropPluginChangeReply(nativeID, "thread/resume", 2)
	default:
		panic("unknown owned native loss boundary")
	}
}

func (f *NativeApplicationFixture) dropPluginChangeReply(nativeID, method string, occurrence int) <-chan NativeConfigurationReply {
	f.Runtime.mu.Lock()
	defer f.Runtime.mu.Unlock()
	p := &lostNativeConfiguration{nativeStdio: f.p, nativeID: nativeID, method: method, remaining: occurrence, received: make(chan NativeConfigurationReply, 1)}
	f.Runtime.clients[f.Instance.ID] = p
	return p.received
}

type NativeConfigurationReply struct {
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}
type lostNativeConfiguration struct {
	*nativeStdio
	nativeID  string
	received  chan NativeConfigurationReply
	dropped   bool
	method    string
	remaining int
}

func (p *lostNativeConfiguration) Call(ctx context.Context, method string, params, out any) error {
	raw := mustNativeJSON(params)
	var request struct {
		ThreadID string          `json:"threadId"`
		Config   json.RawMessage `json:"config"`
	}
	_ = json.Unmarshal(raw, &request)
	if p.dropped || method != p.method || request.ThreadID != p.nativeID || (method == "thread/resume" && len(request.Config) == 0) {
		return p.nativeStdio.Call(ctx, method, params, out)
	}
	p.remaining--
	if p.remaining > 0 {
		return p.nativeStdio.Call(ctx, method, params, out)
	}
	var result json.RawMessage
	if err := p.nativeStdio.Call(ctx, method, params, &result); err != nil {
		return err
	}
	p.dropped = true
	p.received <- NativeConfigurationReply{Params: raw, Result: result}
	return io.ErrUnexpectedEOF
}
