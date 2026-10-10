//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeScopedPluginSelection(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-codex-scoped-plugin-")
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
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "results": results, "processes": f.Records, "passed": !t.Failed(), "scope": "production scoped plugin cache through Runtime/Application/SQLite/Handler; native stdio MCP initialization and synthetic-history cold resume; no model or tool calls"}, "", "  ")
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
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"plugin_service": transport(version)}})
		write(filepath.Join(source, ".mcp.json"), string(mcp))
		assets = append(assets, app.CapabilityAsset{ID: "plugin-" + version, Name: "Plugin version " + version, Kind: "cli_plugin", Version: "asset-v" + version, Source: source, Backends: []string{"codex"}})
	}
	f.Restart(t)
	profileBefore, _ := os.ReadFile(configPath)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}, CapabilityAssets: assets[:1]}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ids := []string{}
	nativeIDs := map[string]string{}
	for i, item := range []struct{ label, asset, mode string }{{"version-one", "plugin-1", "enabled"}, {"disabled", "plugin-1", "disabled"}, {"version-two", "plugin-1", "enabled"}} {
		if i == 2 {
			catalog, err := a.ReadCapabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			updated := assets[0]
			updated.Version, updated.Source = "asset-v2", assets[1].Source
			request := app.CapabilityChangeRequest{Action: "update", AssetID: "plugin-1", BaseRevision: catalog[0].Revision, Asset: &updated}
			preview, err := a.PreviewCapabilityChange(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.PreviewHash = preview.PreviewHash
			if _, err = a.Apply(app.Command{ID: "library-update", Kind: "manage_capability", CapabilityChange: &request}); err != nil {
				t.Fatal(err)
			}
			pinned, err := a.ReadExecution(ids[0])
			if err != nil {
				t.Fatal(err)
			}
			check("library_update_keeps_old_execution", pinned.Snapshot.ID == results["version-one"].Snapshot.ID && pinned.Snapshot.CapabilityRefs[0].Asset.Version == "asset-v1" && pinned.Snapshot.PluginSelections[0].NativeSource == results["version-one"].Snapshot.PluginSelections[0].NativeSource)
		}
		r, err := a.Apply(app.Command{ID: fmt.Sprintf("create-%d", i), Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: item.label, CapabilitySelections: []app.CapabilitySelection{{AssetID: item.asset, Mode: item.mode}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.SessionID)
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, ss := range s.Sessions {
				if ss.ID == r.SessionID {
					return ss.Status == "idle" && len(ss.View.PluginEvidence) == 1
				}
			}
			return false
		})
		read, err := a.ReadExecution(r.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		results[item.label] = read
		pack := read.Snapshot.PluginSelections[0]
		wantSource := assets[0].Source
		if i == 2 {
			wantSource = assets[1].Source
		}
		check(item.label+"_private_native_source", strings.HasPrefix(pack.NativeSource, "fixture@netcap-") && pack.SourcePath == wantSource)
		check(item.label+"_plugin_selection_verified", len(read.PluginEvidence) == 1 && read.PluginEvidence[0].Verified && read.PluginEvidence[0].NativeSource == pack.NativeSource)
		check(item.label+"_no_usage", len(read.CapabilityEvidence) == 2 && len(read.CapabilityEvidence[0].UsedTools) == 0 && len(read.CapabilityEvidence[1].UsedTools) == 0)
		for _, e := range read.CapabilityEvidence {
			if e.Server == "plugin_service" {
				check(item.label+"_bound_mcp", e.AssetID == item.asset && e.Verified && e.PluginID == pack.NativeSource && e.Mode == item.mode)
			}
			if e.Server == "baseline" {
				check(item.label+"_inherited_preserved", e.Mode == "inherit" && e.Source == "configuration" && e.RuntimeStatus == "connected")
			}
		}
		for _, ss := range a.Snapshot().Sessions {
			if ss.ID == r.SessionID {
				nativeIDs[item.label] = ss.Scope.NativeID
			}
		}
	}
	// A second asset version uses a separate cache identity and must not update
	// an already-loaded thread, even when the native manifest version is equal.
	for _, label := range []string{"version-one", "version-two"} {
		var inventory struct {
			Data []struct {
				Name     string `json:"name"`
				PluginID string `json:"pluginId"`
				Info     struct {
					Version string `json:"version"`
				} `json:"serverInfo"`
			} `json:"data"`
		}
		if err := f.Call(context.Background(), "mcpServerStatus/list", map[string]any{"threadId": nativeIDs[label]}, &inventory); err != nil {
			t.Fatal(err)
		}
		for _, s := range inventory.Data {
			if s.Name == "plugin_service" {
				want := "1"
				if label == "version-two" {
					want = "2"
				}
				check(label+"_actual_definition", s.Info.Version == want && s.PluginID == results[label].Snapshot.PluginSelections[0].NativeSource)
			}
		}
	}
	check("versions_have_distinct_cache_roots", results["version-one"].Snapshot.PluginSelections[0].CachePath != results["version-two"].Snapshot.PluginSelections[0].CachePath)
	profileAfter, _ := os.ReadFile(configPath)
	check("profile_unchanged", string(profileAfter) == string(profileBefore))
	trace, _ := os.ReadFile(filepath.Join(root, "mcp.jsonl"))
	check("initialize_and_list_no_tools", strings.Contains(string(trace), "initialize") && strings.Contains(string(trace), "tools/list") && !strings.Contains(string(trace), "tools/call"))
	token := strings.Repeat("c", 64)
	handler := console.Handler(a, "127.0.0.1:9465", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, p := range append([]string{"/api/workspace", "/api/snapshot"}, "/api/sessions/"+ids[0], "/api/sessions/"+ids[1], "/api/sessions/"+ids[2]) {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9465"+p, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[p] = append([]byte(nil), w.Body.Bytes()...)
	}
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(b))
	check("handler_includes_cache_and_origin", strings.Contains(string(responses["/api/sessions/"+ids[0]]), `"cache_path"`) && strings.Contains(string(responses["/api/sessions/"+ids[0]]), `"native_source":"fixture@netcap-`))
	// Persist an explicitly synthetic item to exercise real Runtime cold resume.
	if err := f.Call(context.Background(), "thread/inject_items", map[string]any{"threadId": nativeIDs["version-one"], "items": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "SYNTHETIC PLUGIN HISTORY; no model or tool call"}}}}}, nil); err != nil {
		t.Fatal(err)
	}
	var scope runtime.Scope
	for _, ss := range a.Snapshot().Sessions {
		if ss.ID == ids[0] {
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
	for i, id := range ids {
		check(fmt.Sprintf("sqlite_selection_%d", i), len(saved.Sessions[id].Scope.PluginSelections) == 1 && saved.Sessions[id].Scope.PluginSelections[0].NativeSource == saved.Sessions[id].ExecutionSnapshot.PluginSelections[0].NativeSource)
	}
	f.Restart(t)
	scope.RecoveryPermission = "read_only"
	v, err := f.Runtime.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	check("runtime_cold_resume_retains_id_and_plugin", v.NativeID == scope.NativeID && len(v.PluginEvidence) == 1 && v.PluginEvidence[0].Verified)
	noModel, noInstall := true, true
	for _, p := range f.Records {
		for _, c := range p.Calls {
			if c["method"] == "turn/start" {
				noModel = false
			}
			if c["method"] == "plugin/install" || c["method"] == "config/value/write" || c["method"] == "config/mcpServer/reload" {
				noInstall = false
			}
		}
	}
	check("no_model_turns", noModel)
	check("no_native_install_or_global_reload", noInstall)
}
