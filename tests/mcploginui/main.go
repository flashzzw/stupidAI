// Browser transport fixture: JSON lines -> authenticated production Handler.
// No listener, native CLI, OAuth flow, or provider. Runtime responses are
// controlled; Application preparation, private files and SQLite are real.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

type fixtureRuntime struct{ creates, inputs atomic.Int32 }

func (f *fixtureRuntime) Describe(_ context.Context, i runtime.Instance) (runtime.Description, error) {
	return runtime.Description{Kind: i.Kind, Version: "controlled UI fixture", Compatibility: "supported", Capabilities: map[string]string{"create": "supported", "history": "supported", "send": "supported", "recovery": "supported", "resume": "supported", "probe": "unverified"}}, nil
}
func (f *fixtureRuntime) Execute(_ context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind != "create" {
		f.inputs.Add(1)
		return runtime.Receipt{}, fmt.Errorf("fixture accepts no native input")
	}
	f.creates.Add(1)
	if in.Scope.Instance.ID == "claude-b" {
		return runtime.Receipt{}, &runtime.NotSent{Reason: "MCP identity 需要 OAuth 认证（受控夹具）"}
	}
	return runtime.Receipt{NativeID: "controlled-original-a"}, nil
}
func (f *fixtureRuntime) Observe(_ context.Context, s runtime.Scope) (runtime.View, error) {
	return runtime.View{NativeID: s.NativeID, Status: "idle", ExecutionClear: true, Messages: []runtime.Message{}, Pending: []runtime.Pending{}, CapabilityEvidence: []runtime.CapabilityEvidence{
		{Server: "identity", Mode: "enabled", RuntimeStatus: "needs-auth", Source: "dynamic", Tools: []string{}, At: "2026-10-02T00:00:00Z", Detail: "受控来源样例：MCP identity 需要 OAuth 认证"},
		{Server: "inherited-user", Mode: "inherit", RuntimeStatus: "connected", Source: "user", Tools: []string{"read"}, UsedTools: []string{"past_call"}, At: "2026-10-01T00:00:00Z", Detail: "受控历史样例；未运行工具"},
		{Server: "plugin:fixture:docs", Mode: "inherit", RuntimeStatus: "failed", Source: "plugin", Tools: []string{}, At: "2026-10-02T00:00:00Z", Detail: "受控样例：插件服务连接失败"},
	}}, nil
}
func (f *fixtureRuntime) ObserveFresh(c context.Context, s runtime.Scope) (runtime.View, error) {
	return f.Observe(c, s)
}
func (f *fixtureRuntime) Reconcile(context.Context, runtime.Intent, runtime.Receipt) (runtime.Resolution, error) {
	return runtime.Resolution{State: "unknown"}, nil
}
func (f *fixtureRuntime) Close() error { return nil }

type request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
	Action string          `json:"action"`
}
type harness struct {
	config       app.Config
	a            *app.Application
	f            *fixtureRuntime
	handler      http.Handler
	sessions     map[string]string
	root, source string
}

const host = "127.0.0.1:9453"
const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const definition = `{"mcpServers":{"identity":{"type":"http","url":"https://example.invalid/mcp"},"stream":{"type":"sse","url":"https://example.invalid/sse"},"local":{"command":"never-execute-fixture"},"helper":{"type":"http","url":"https://example.invalid/helper","headersHelper":"./authenticate.sh"}}}`

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func (h *harness) open() error {
	s, err := app.OpenStore(h.config.StateDir)
	if err != nil {
		return err
	}
	h.a, err = app.Open(h.config, s, h.f)
	if err != nil {
		s.Close()
		return err
	}
	h.handler = console.Handler(h.a, host, token, os.DirFS("web/dist"))
	return nil
}
func (h *harness) call(r request) (int, json.RawMessage) {
	req := httptest.NewRequest(r.Method, "http://"+host+r.Path, strings.NewReader(string(r.Body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+host)
	req.AddCookie(&http.Cookie{Name: "netcap_session", Value: token})
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}
func (h *harness) export() map[string]any {
	responses := map[string]json.RawMessage{}
	paths := []string{"/api/workspace", "/api/snapshot"}
	for _, id := range h.sessions {
		paths = append(paths, "/api/sessions/"+id, "/api/sessions/"+id+"/execution")
	}
	for _, p := range paths {
		_, body := h.call(request{Method: "GET", Path: p})
		responses[p] = body
	}
	dirs, _ := filepath.Glob(filepath.Join(h.config.StateDir, "mcp-login", "login-*"))
	return map[string]any{"responses": responses, "sessions": h.sessions, "root": h.root, "counts": map[string]any{"fixture_creates": h.f.creates.Load(), "native_inputs": h.f.inputs.Load(), "operations": len(h.a.Snapshot().Operations), "login_directories": len(dirs)}, "scope": "production Application/SQLite/Handler, controlled runtime, no HTTP listener or native login"}
}
func newHarness(root string) (*harness, error) {
	h := &harness{root: root, source: filepath.Join(root, "mcp.json"), sessions: map[string]string{}, f: &fixtureRuntime{}}
	if err := write(h.source, []byte(definition)); err != nil {
		return nil, err
	}
	h.config = app.Config{Version: 1, StateDir: filepath.Join(root, "state"), Projects: []app.Project{{ID: "project", Name: "认证准备受控验证", Cwd: root}}, CapabilityAssets: []app.CapabilityAsset{{ID: "shared", Name: "共享认证服务（受控）", Kind: "mcp", Version: "v1", Source: h.source, Backends: []string{"claude"}}}}
	for _, letter := range []string{"a", "b"} {
		profile := filepath.Join(root, "profile-"+letter)
		bridge := filepath.Join(profile, "bridge.js")
		if err := write(bridge, []byte("throw new Error('must not execute');")); err != nil {
			return nil, err
		}
		h.config.Instances = append(h.config.Instances, runtime.Instance{ID: "claude-" + letter, Name: "Claude " + strings.ToUpper(letter), Kind: "claude", Executable: "/fixture/claude", Profile: profile, Bridge: bridge})
		h.config.Routes = append(h.config.Routes, runtime.Route{ID: "route-" + letter, InstanceID: "claude-" + letter, Model: "fixture"})
	}
	if err := h.open(); err != nil {
		return nil, err
	}
	for _, letter := range []string{"a", "b"} {
		receipt, err := h.a.Apply(app.Command{ID: "create-" + letter, Kind: "create", Title: "OAuth " + strings.ToUpper(letter) + " 受控验证", ProjectID: "project", RouteID: "route-" + letter, Permission: "read_only", CapabilitySelections: []app.CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}})
		if err != nil {
			return nil, err
		}
		h.sessions[letter] = receipt.SessionID
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready := true
		for _, s := range h.a.Snapshot().Sessions {
			if s.Status == "starting" {
				ready = false
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("fixture sessions did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Stop fixture polling before capturing source/history presentation.
	for _, id := range h.sessions {
		if _, err := h.a.Apply(app.Command{ID: "takeover-" + id, Kind: "takeover", SessionID: id}); err != nil {
			return nil, err
		}
	}
	current, err := h.a.ReadExecution(h.sessions["a"])
	if err != nil {
		return nil, err
	}
	change := app.ExecutionChangeRequest{BaseSnapshotID: current.Snapshot.ID, Selections: []app.CapabilitySelection{{AssetID: "shared", Mode: "disabled"}}}
	preview, err := h.a.PreviewExecutionChange(h.sessions["a"], change)
	if err != nil {
		return nil, err
	}
	change.PreviewHash = preview.PreviewHash
	if _, err = h.a.Apply(app.Command{ID: "stage-a", Kind: "stage_execution", SessionID: h.sessions["a"], ExecutionChange: &change}); err != nil {
		return nil, err
	}
	return h, nil
}
func main() {
	root := flag.String("root", "", "new, owned directory for persistent fixture data")
	flag.Parse()
	if *root == "" {
		panic("--root required")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		panic(err)
	}
	if _, err = os.Stat(absolute); !os.IsNotExist(err) {
		panic("fixture root must not exist")
	}
	h, err := newHarness(absolute)
	if err != nil {
		panic(err)
	}
	defer func() { _ = h.a.Close() }()
	log, err := os.OpenFile(filepath.Join(absolute, "exchanges.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	defer log.Close()
	out := json.NewEncoder(os.Stdout)
	ready, _ := json.Marshal(h.export())
	if err = write(filepath.Join(absolute, "ready.json"), ready); err != nil {
		panic(err)
	}
	_ = out.Encode(map[string]any{"ready": absolute})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var r request
		if err = json.Unmarshal(scanner.Bytes(), &r); err != nil {
			panic(err)
		}
		status := 200
		body := json.RawMessage(`{}`)
		switch r.Action {
		case "quit":
			_ = out.Encode(map[string]any{"stopped": true})
			return
		case "restart":
			if err = h.a.Close(); err == nil {
				err = h.open()
			}
			if err != nil {
				panic(err)
			}
		case "drift":
			if err = write(h.source, []byte(`{"mcpServers":{}}`)); err != nil {
				panic(err)
			}
		case "restore":
			if err = write(h.source, []byte(definition)); err != nil {
				panic(err)
			}
		case "":
			status, body = h.call(r)
		default:
			panic("unknown fixture action")
		}
		reply := map[string]any{"status": status, "body": body, "state": h.export()}
		if err = json.NewEncoder(log).Encode(map[string]any{"request": r, "response": reply}); err != nil {
			panic(err)
		}
		encoded, _ := json.Marshal(reply)
		if err = write(filepath.Join(absolute, "latest.json"), encoded); err != nil {
			panic(err)
		}
		_ = out.Encode(map[string]any{"status": status, "response_file": filepath.Join(absolute, "latest.json")})
	}
	if err = scanner.Err(); err != nil {
		panic(err)
	}
}
