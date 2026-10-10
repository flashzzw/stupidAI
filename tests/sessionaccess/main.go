// A controlled runtime behind the production HTTP handler and built UI.
// This fixture verifies access presentation, not native CLI behavior.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

type controlledRuntime struct {
	mu                       sync.Mutex
	creates, sends, previews int
	historyRevision          int
	observationMode          string
}

func (f *controlledRuntime) Describe(_ context.Context, i runtime.Instance) (runtime.Description, error) {
	return runtime.Description{Kind: i.Kind, Version: "controlled fixture", Compatibility: "supported", Capabilities: map[string]string{
		"create": "supported", "history": "supported", "send": "supported", "resume": "supported", "recovery": "supported",
	}}, nil
}
func (f *controlledRuntime) Execute(_ context.Context, in runtime.Intent) (runtime.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.Kind == "create" {
		f.creates++
		return runtime.Receipt{NativeID: controlledNativeID}, nil
	}
	f.sends++
	return runtime.Receipt{}, fmt.Errorf("fixture accepts no model input")
}
func (f *controlledRuntime) Observe(_ context.Context, s runtime.Scope) (runtime.View, error) {
	return runtime.View{Executor: controlledExecutor(), NativeID: s.NativeID, Status: "idle", ExecutionClear: true,
		Messages: []runtime.Message{{ID: "fixture-message", Role: "assistant", Text: "受控执行器记录；没有调用原生 CLI 或模型。", Status: "completed"}},
		Pending:  []runtime.Pending{}, Evidence: runtime.Evidence{Source: "controlled.access.fixture", At: "2026-10-01T00:00:00Z", Complete: true, Detail: "受控原身份与空闲证据；不代表真实 CLI 验收。"}}, nil
}

func controlledExecutor() *runtime.ExecutorEvidence {
	return &runtime.ExecutorEvidence{Source: "controlled.peer.fixture", At: "2026-10-01T00:00:00Z", PID: 4242, Birth: "fixture-start", OS: "linux", UID: "1000", User: "fixture-user", WSLDistro: "Ubuntu-fixture", Executable: "/fixture/native-codex", Detail: "受控后台环境样例；不是实际 WSL、CLI 或用户身份验收。"}
}
func (f *controlledRuntime) ObserveFresh(ctx context.Context, s runtime.Scope) (runtime.View, error) {
	f.mu.Lock()
	f.previews++
	f.mu.Unlock()
	if !s.NoAutoResume {
		return runtime.View{}, fmt.Errorf("preview did not disable automatic resume")
	}
	return f.Observe(ctx, s)
}
func (f *controlledRuntime) Reconcile(context.Context, runtime.Intent, runtime.Receipt) (runtime.Resolution, error) {
	return runtime.Resolution{}, fmt.Errorf("no native execution in this fixture")
}
func (f *controlledRuntime) Close() error { return nil }

func main() {
	infoFile := flag.String("info", "", "owned output path for browser connection information")
	dump := flag.String("dump", "", "export production Handler responses without a listening socket")
	flag.Parse()
	if err := run(*infoFile, *dump); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(infoFile, dump string) error {
	root, err := os.MkdirTemp("", "netcap-access-browser-")
	if err != nil {
		return err
	}
	profile := filepath.Join(root, "profile")
	if err = os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(profile, "config.toml"), []byte("model = 'fixture'\n"), 0600); err != nil {
		return err
	}
	store, err := app.OpenStore(filepath.Join(root, "state"))
	if err != nil {
		return err
	}
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_ = os.Setenv("NETCAP_ACCESS_FIXTURE_OBSERVER", strings.Repeat("b", 64))
	c := app.Config{Version: 1, StateDir: filepath.Join(root, "state"),
		Instances: []runtime.Instance{{ID: "codex-fixture", Name: "Codex 来源测试", Kind: "codex", Profile: profile, Executable: "/fixture/codex-proxy", Socket: filepath.Join(root, "original-backend.sock"), Version: "0.158.0"}},
		Routes:    []runtime.Route{{ID: "fixture-route", InstanceID: "codex-fixture", Model: "fixture"}},
		Projects:  []app.Project{{ID: "fixture-project", Name: "接续来源测试", Cwd: root}},
		Observers: []app.ObserverConfig{{ID: "fixture-hooks", InstanceID: "codex-fixture", ProjectID: "fixture-project", Format: "hook", TokenRef: "env:NETCAP_ACCESS_FIXTURE_OBSERVER"}}}
	f := &controlledRuntime{}
	a, err := app.Open(c, store, f)
	if err != nil {
		store.Close()
		return err
	}
	defer a.Close()
	r, err := a.Apply(app.Command{ID: "create-fixture", Kind: "create", ProjectID: "fixture-project", RouteID: "fixture-route", Title: "原会话来源预览", Permission: "read_only"})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.Snapshot().Sessions[0].Status != "idle" {
		if time.Now().After(deadline) {
			return fmt.Errorf("fixture did not become idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = a.Apply(app.Command{ID: "takeover-fixture", Kind: "takeover", SessionID: r.SessionID}); err != nil {
		return err
	}
	observed, err := a.IngestObservation("fixture-hooks", strings.Repeat("b", 64), app.ObservationDelivery{EventID: "observer-fixture", OccurredAt: "2026-10-01T00:00:00Z", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"external-record"}`)})
	if err != nil {
		return err
	}
	if dump != "" {
		handler := console.Handler(a, "127.0.0.1:9451", token, os.DirFS("web/dist"))
		responses := map[string]json.RawMessage{}
		before := a.Snapshot()
		for _, path := range []string{"/api/workspace", "/api/sessions/" + r.SessionID, "/api/sessions/" + observed.SessionIDs[0], "/api/sessions/" + r.SessionID + "/access/preview", "/api/sessions/" + observed.SessionIDs[0] + "/access/preview"} {
			method, body := "GET", ""
			if strings.HasSuffix(path, "/preview") {
				method, body = "POST", "{}"
			}
			request := httptest.NewRequest(method, "http://127.0.0.1:9451"+path, strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 {
				return fmt.Errorf("%s returned %d", path, response.Code)
			}
			responses[path] = append(json.RawMessage{}, response.Body.Bytes()...)
		}
		nativeChecks, err := dumpNativeResponses(handler, responses)
		if err != nil {
			return err
		}
		historyChecks, err := dumpNativeHistoryResponses(handler, responses, f)
		if err != nil {
			return err
		}
		for name, passed := range historyChecks {
			nativeChecks[name] = passed
		}
		bindingChecks, err := dumpNativeBindingResponses(handler, responses)
		if err != nil {
			return err
		}
		for name, passed := range bindingChecks {
			nativeChecks[name] = passed
		}
		observationChecks, err := dumpNativeObservationResponses(handler, responses, f)
		if err != nil {
			return err
		}
		for name, passed := range observationChecks {
			nativeChecks[name] = passed
		}
		var sessionResponse app.SessionRead
		if err = json.Unmarshal(responses["/api/sessions/"+r.SessionID], &sessionResponse); err != nil {
			return err
		}
		accessContext := sessionResponse.Session.Access.Context
		nativeChecks["saved_binding_context"] = accessContext.Basis == "saved_binding" && accessContext.Binding.Profile == profile && accessContext.Binding.Executable == "/fixture/codex-proxy" && accessContext.RegistryStatus == "matching"
		var previewResponse app.SessionAccessPreview
		if err = json.Unmarshal(responses["/api/sessions/"+r.SessionID+"/access/preview"], &previewResponse); err != nil {
			return err
		}
		sample := previewResponse.Access.Context.Executor
		nativeChecks["backend_sample_context"] = sample != nil && sample.UID == "1000" && sample.WSLDistro == "Ubuntu-fixture" && sample.Source == "controlled.peer.fixture"
		var observedResponse app.SessionRead
		if err = json.Unmarshal(responses["/api/sessions/"+observed.SessionIDs[0]], &observedResponse); err != nil {
			return err
		}
		nativeChecks["events_have_no_backend_environment"] = observedResponse.Session.Access.Context.Executor == nil && observedResponse.Session.Access.Context.Basis == "recorded_context"
		var discoveryResponse app.NativeDiscoveryResult
		if err = json.Unmarshal(responses["/api/native-sessions/discover"], &discoveryResponse); err != nil {
			return err
		}
		nativeChecks["discovery_selected_configuration"] = discoveryResponse.Context.Basis == "selected_configuration" && discoveryResponse.Context.Binding.Profile == profile && discoveryResponse.Context.Binding.Permission == "unverified"
		after := a.Snapshot()
		if len(before.Operations) != len(after.Operations) {
			return fmt.Errorf("preview added an operation")
		}
		f.mu.Lock()
		checks := map[string]bool{"single_create": f.creates == 1, "no_native_input": f.sends == 0, "only_managed_preview_read_native": f.previews == 1, "no_new_operations": len(before.Operations) == len(after.Operations)}
		f.mu.Unlock()
		for name, passed := range nativeChecks {
			checks[name] = passed
		}
		for _, s := range after.Sessions {
			if s.ID == r.SessionID {
				checks["control_unchanged"] = s.Paused && s.ControlMode == "human_takeover"
			}
		}
		for name, passed := range checks {
			if !passed {
				return fmt.Errorf("fixture check failed: %s", name)
			}
		}
		data, _ := json.MarshalIndent(map[string]any{"scope": "production Handler + controlled runtime, no HTTP listener or real CLI", "root": root, "session_id": r.SessionID, "observed_id": observed.SessionIDs[0], "responses": responses, "checks": checks}, "", "  ")
		if err = os.WriteFile(dump, data, 0600); err != nil {
			return err
		}
		fmt.Println("PASS production Handler responses and", len(checks), "read-only invariants")
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: console.Handler(a, listener.Addr().String(), token, os.DirFS("web/dist")), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	info := map[string]any{"url": "http://" + listener.Addr().String(), "token": token, "root": root, "session_id": r.SessionID, "observed_id": observed.SessionIDs[0], "pid": os.Getpid(), "scope": "production API/UI with controlled runtime; not native CLI"}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err = os.WriteFile(infoFile, data, 0600); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Println("READY", listener.Addr().String())
	select {
	case <-ctx.Done():
	case err = <-finished:
		if err != http.ErrServerClosed {
			return err
		}
	}
	f.mu.Lock()
	counts, _ := json.MarshalIndent(map[string]int{"creates": f.creates, "native_inputs": f.sends, "readonly_previews": f.previews}, "", "  ")
	f.mu.Unlock()
	return os.WriteFile(filepath.Join(root, "counts.json"), counts, 0600)
}
