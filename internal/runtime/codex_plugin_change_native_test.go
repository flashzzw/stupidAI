//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

// Record the public Execute interval after Application has drained its prior
// display observation. Other sessions may continue their permission sampling.
type nativePluginChangeRecorder struct {
	runtime.Adapter
	fixture  *runtime.NativeApplicationFixture
	mu       sync.Mutex
	prepared map[string]bool
}

func (r *nativePluginChangeRecorder) ReadCodexProjectHookSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	return r.fixture.Runtime.ReadCodexProjectHookSources(ctx, scope)
}

func (r *nativePluginChangeRecorder) ReadCodexConfigurationSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexConfigurationSources, error) {
	return r.fixture.Runtime.ReadCodexConfigurationSources(ctx, scope)
}

func nativePluginCalls(f *runtime.NativeApplicationFixture, method, id string) int {
	checkpoint, err := f.Checkpoint()
	if err != nil {
		return -1
	}
	var records []struct {
		Calls []struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		} `json:"calls"`
	}
	if json.Unmarshal(checkpoint.Trace, &records) != nil {
		return -1
	}
	n := 0
	for _, record := range records {
		for _, call := range record.Calls {
			if call.Method == method && (id == "" || call.Params["threadId"] == id) {
				n++
			}
		}
	}
	return n
}

func (r *nativePluginChangeRecorder) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	if in.Kind != "prepare_execution" {
		return r.Adapter.Execute(ctx, in)
	}
	resumes := nativePluginCalls(r.fixture, "thread/resume", in.Scope.NativeID)
	unsubs := nativePluginCalls(r.fixture, "thread/unsubscribe", in.Scope.NativeID)
	receipt, err := r.Adapter.Execute(ctx, in)
	unchanged := resumes >= 0 && unsubs >= 0 && resumes == nativePluginCalls(r.fixture, "thread/resume", in.Scope.NativeID) && unsubs == nativePluginCalls(r.fixture, "thread/unsubscribe", in.Scope.NativeID)
	r.mu.Lock()
	r.prepared[in.ID] = unchanged
	r.mu.Unlock()
	return receipt, err
}

func TestCodexNativePluginChanges(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-codex-plugin-change-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	results := map[string]app.ExecutionRead{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "results": results, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/SQLite/Runtime plugin version transactions; native MCP initialize/list with synthetic persisted history; no model/tool calls"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), b, 0600)
	})
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body := `import sys,json
from pathlib import Path
for line in sys.stdin:
    req=json.loads(line)
    with Path(sys.argv[1]).open('a') as f:f.write(json.dumps({'version':sys.argv[2],'method':req.get('method')})+'\n')
    if 'id' not in req:continue
    method=req.get('method')
    if method=='initialize':result={'protocolVersion':req['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-plugin','version':sys.argv[2]}}
    elif method=='tools/list':result={'tools':[{'name':'identity','description':'Owned identity','inputSchema':{'type':'object','properties':{}}}]}
    elif method=='resources/list':result={'resources':[]}
    elif method=='resources/templates/list':result={'resourceTemplates':[]}
    else:raise RuntimeError('unexpected MCP method')
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)
`
	transport := func(version string) map[string]any {
		return map[string]any{"command": "python3", "args": []string{"-u", "-c", body, filepath.Join(root, "mcp.jsonl"), version}}
	}
	configPath := filepath.Join(root, "codex/config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(transport("unrelated")["args"])
	write(configPath, strings.Replace(string(content), "plugins=false", "plugins=true\nplugin_sharing=false", 1)+"\n[mcp_servers.baseline]\ncommand=\"python3\"\nargs="+string(args)+"\n")
	cwd := filepath.Join(root, "work")
	assets := []app.CapabilityAsset{}
	for _, version := range []string{"1", "2"} {
		source := filepath.Join(root, "assets", "plugin-"+version)
		write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"fixture","version":"1.0.0"}`)
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"plugin_service_" + version: transport(version)}})
		write(filepath.Join(source, ".mcp.json"), string(mcp))
		assets = append(assets, app.CapabilityAsset{ID: "plugin-" + version, Name: "Plugin version " + version, Kind: "cli_plugin", Version: "asset-v" + version, Source: source, Backends: []string{"codex"}})
	}
	f.Restart(t)
	profileBefore, _ := os.ReadFile(configPath)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}, CapabilityAssets: append(assets[:1:1], app.CapabilityAsset{ID: "baseline", Name: "Baseline MCP", Kind: "mcp", Version: "v1", Source: configPath, CodexMCP: "baseline", Backends: []string{"codex"}})}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &nativePluginChangeRecorder{Adapter: f.Runtime, fixture: f, prepared: map[string]bool{}}
	a, err := app.Open(cfg, store, recorder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ids := map[string]string{}
	nativeIDs := map[string]string{}
	for _, label := range []string{"a", "b", "empty"} {
		picks := []app.CapabilitySelection{{AssetID: "plugin-1", Mode: "enabled"}}
		if label == "a" {
			picks = append(picks, app.CapabilitySelection{AssetID: "baseline", Mode: "enabled"})
		}
		r, err := a.Apply(app.Command{ID: "create-" + label, Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: "Plugin change " + label, CapabilitySelections: picks})
		if err != nil {
			t.Fatal(err)
		}
		ids[label] = r.SessionID
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, ss := range s.Sessions {
				if ss.ID == r.SessionID {
					return ss.Status == "idle" && len(ss.View.PluginEvidence) == 1 && ss.View.PluginEvidence[0].Verified
				}
			}
			return false
		})
		for _, ss := range a.Snapshot().Sessions {
			if ss.ID == r.SessionID {
				nativeIDs[label] = ss.Scope.NativeID
			}
		}
		if err := f.Call(context.Background(), "thread/inject_items", map[string]any{"threadId": nativeIDs[label], "items": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "SYNTHETIC plugin change history; no model or tool call"}}}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	operation := func(id string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			for _, op := range a.Snapshot().Operations {
				if op.ID == id {
					if op.Status == "succeeded" {
						return
					}
					if op.Status == "not_sent" || op.Status == "failed" {
						t.Fatalf("%s: %s", op.Intent.Kind, op.Error)
					}
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("plugin operation did not reach a verified result")
	}
	apply := func(cmd app.Command) app.CommandReceipt {
		t.Helper()
		var out app.CommandReceipt
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var err error
			out, err = a.Apply(cmd)
			if err == nil {
				return out
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("command was not accepted: %s", cmd.Kind)
		return out
	}
	read := func(id string) app.ExecutionRead {
		t.Helper()
		r, err := a.ReadExecution(id)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	serialize := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	count := func(method string, nativeID ...string) int {
		id := ""
		if len(nativeID) > 0 {
			id = nativeID[0]
		}
		return nativePluginCalls(f, method, id)
	}

	state := func(id string) map[string]any {
		t.Helper()
		response, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: response}
	}
	cases := map[string]any{}
	change := func(label, id string, pick app.CapabilitySelection) {
		t.Helper()
		before := read(id)
		states := map[string]any{"initial": state(id)}
		request := app.ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []app.CapabilitySelection{pick}}
		preview, err := a.PreviewExecutionChange(id, request)
		if err != nil {
			t.Fatal(err)
		}
		request.PreviewHash = preview.PreviewHash
		saved := apply(app.Command{ID: label + "-stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request})
		staged := read(id)
		states["staged"] = state(id)
		check(label+"_save_keeps_current", serialize(staged.Snapshot) == serialize(before.Snapshot))
		check(label+"_explicit_plugin_diff", len(preview.Changes) == 1 && preview.Changes[0].Kind == "cli_plugin")
		ref := app.ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
		targetID := nativeIDs["a"]
		if id == ids["empty"] {
			targetID = nativeIDs["empty"]
		}
		unsubs := count("thread/unsubscribe", targetID)
		prepared := apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref})
		operation(prepared.OperationID)
		states["prepared"] = state(id)
		recorder.mu.Lock()
		check(label+"_prepare_is_read_only", recorder.prepared[prepared.OperationID])
		recorder.mu.Unlock()
		applied := apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref})
		operation(applied.OperationID)
		states["applied"] = state(id)
		after := read(id)
		results[label] = after
		check(label+"_promotes_exact_candidate", after.PendingChange == nil && serialize(after.Snapshot) == serialize(staged.PendingChange.Snapshot))
		check(label+"_preserves_history", len(after.History) == len(before.History)+1 && serialize(after.History[len(after.History)-1]) == serialize(before.Snapshot))
		check(label+"_single_detach", count("thread/unsubscribe", targetID) == unsubs+1)
		for _, ss := range a.Snapshot().Sessions {
			if ss.ID == id {
				want := nativeIDs["a"]
				if id == ids["empty"] {
					want = nativeIDs["empty"]
				}
				check(label+"_same_id_paused", ss.Scope.NativeID == want && ss.Paused && !ss.ExecutionHold)
			}
		}
		if id == ids["a"] {
			check(label+"_other_mcp_stays_pinned", serialize(after.Snapshot.MCPSelections) == serialize(before.Snapshot.MCPSelections))
		}
		if pick.Mode == "inherit" {
			check(label+"_releases_plugin_reference", len(after.Snapshot.PluginSelections) == 0 && !strings.Contains(serialize(after.Snapshot.CapabilityRefs), `"id":"plugin-1"`))
		} else {
			check(label+"_verified_target", len(after.PluginEvidence) == 1 && after.PluginEvidence[0].Verified)
		}
		cases[label] = map[string]any{"states": states, "preview": preview, "selection": pick, "receipts": map[string]any{"stage_execution": saved, "prepare_execution": prepared, "apply_execution": applied}}
	}
	change("disable", ids["a"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "disabled"})
	change("enable", ids["a"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "enabled"})
	old := read(ids["a"])
	catalog, err := a.ReadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var revision string
	for _, item := range catalog {
		if item.Asset.ID == "plugin-1" {
			revision = item.Revision
		}
	}
	updated := assets[0]
	updated.Source, updated.Version = assets[1].Source, "asset-v2"
	request := app.CapabilityChangeRequest{Action: "update", AssetID: "plugin-1", BaseRevision: revision, Asset: &updated}
	preview, err := a.PreviewCapabilityChange(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	apply(app.Command{ID: "library-update", Kind: "manage_capability", CapabilityChange: &request})
	check("library_update_keeps_old_execution", serialize(read(ids["a"]).Snapshot) == serialize(old.Snapshot))
	change("upgrade", ids["a"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "enabled", Version: "asset-v2"})
	change("inherit", ids["a"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "inherit"})
	change("add", ids["a"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "enabled", Version: "asset-v2"})
	change("empty-inherit", ids["empty"], app.CapabilitySelection{AssetID: "plugin-1", Mode: "inherit"})
	check("empty_inherit_has_no_explicit_capabilities", len(read(ids["empty"]).Snapshot.PluginSelections) == 0 && len(read(ids["empty"]).Snapshot.MCPSelections) == 0)
	for _, label := range []string{"a", "b", "empty"} {
		var inventory struct {
			Data []struct {
				Name     string  `json:"name"`
				PluginID *string `json:"pluginId"`
				Info     *struct {
					Version string `json:"version"`
				} `json:"serverInfo"`
			} `json:"data"`
		}
		if err := f.Call(context.Background(), "mcpServerStatus/list", map[string]any{"threadId": nativeIDs[label]}, &inventory); err != nil {
			t.Fatal(err)
		}
		versions := []string{}
		for _, row := range inventory.Data {
			if row.PluginID != nil && row.Info != nil {
				versions = append(versions, row.Info.Version)
			}
		}
		want := `["2"]`
		if label == "b" {
			want = `["1"]`
		}
		if label == "empty" {
			want = `[]`
		}
		check(label+"_actual_version_and_exclusion", serialize(versions) == want)
	}
	profileAfter, _ := os.ReadFile(configPath)
	check("profile_configuration_unchanged", string(profileAfter) == string(profileBefore))
	token := strings.Repeat("c", 64)
	handler := console.Handler(a, "127.0.0.1:9466", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, p := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids["a"], "/api/sessions/" + ids["b"]} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9466"+p, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[p] = append([]byte(nil), w.Body.Bytes()...)
	}
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids, "cases": cases}, "", "  ")
	write(filepath.Join(root, "change-browser-responses.json"), string(b))
	var scope runtime.Scope
	for _, ss := range a.Snapshot().Sessions {
		if ss.ID == ids["a"] {
			scope = ss.Scope
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	check("sqlite_keeps_versions_and_history", len(saved.Sessions[ids["a"]].ExecutionHistory) == 5 && saved.Sessions[ids["a"]].Scope.PluginSelections[0].NativeSource == scope.PluginSelections[0].NativeSource)
	f.Restart(t)
	scope.RecoveryPermission = "read_only"
	v, err := f.Runtime.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	check("cold_resume_keeps_original_id_and_current_plugin", v.NativeID == scope.NativeID && len(v.PluginEvidence) == 1 && v.PluginEvidence[0].Verified)
	check("no_model_or_direct_tool_calls", count("turn/start") == 0 && count("mcpServer/tool/call") == 0)
	check("no_global_plugin_install_or_reload", count("plugin/install") == 0 && count("config/mcpServer/reload") == 0 && count("config/value/write") == 0)
}
