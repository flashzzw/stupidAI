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

func TestCodexNativeMCPOrigins(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-codex-mcp-origins-")
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
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "results": results, "processes": f.Records, "passed": !t.Failed(), "scope": "production Runtime/Application/SQLite/Handler, owned native stdio MCP initialization only; no model or tool call"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), b, 0600)
	})
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := filepath.Join(root, "server.py")
	write(server, `import sys,json
from pathlib import Path
for line in sys.stdin:
    req=json.loads(line)
    with Path(sys.argv[1]).open('a') as f:f.write(json.dumps({'source':sys.argv[2],'method':req.get('method')})+'\n')
    if 'id' not in req:continue
    method=req.get('method')
    if method=='initialize':result={'protocolVersion':req['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':sys.argv[2],'version':'1'}}
    elif method=='tools/list':result={'tools':[{'name':'identity','description':'Owned identity','inputSchema':{'type':'object','properties':{}}}]}
    elif method=='resources/list':result={'resources':[]}
    elif method=='resources/templates/list':result={'resourceTemplates':[]}
    else:raise RuntimeError('unexpected MCP method')
    print(json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}),flush=True)
`)
	configPath := filepath.Join(root, "codex/config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]string{"-u", server, filepath.Join(root, "mcp.jsonl"), "configuration"})
	write(configPath, strings.Replace(string(content), "plugins=false", "plugins=true\nplugin_sharing=false", 1)+"\n[mcp_servers.baseline]\ncommand=\"python3\"\nargs="+string(args)+"\n")
	cwd := filepath.Join(root, "work")
	plugin := filepath.Join(cwd, "plugins/fixture")
	write(filepath.Join(plugin, ".codex-plugin/plugin.json"), `{"name":"fixture","version":"1.0.0"}`)
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"plugin_service": map[string]any{"command": "python3", "args": []string{"-u", server, filepath.Join(root, "mcp.jsonl"), "plugin"}}}})
	write(filepath.Join(plugin, ".mcp.json"), string(mcp))
	market := filepath.Join(cwd, ".agents/plugins/marketplace.json")
	write(market, `{"name":"owned","plugins":[{"name":"fixture","source":{"source":"local","path":"./plugins/fixture"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Productivity"}]}`)
	f.Restart(t)
	if err := f.Call(context.Background(), "plugin/install", map[string]any{"pluginName": "fixture", "marketplacePath": market}, nil); err != nil {
		t.Fatal(err)
	}
	profileBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: cwd}}, CapabilityAssets: []app.CapabilityAsset{{ID: "baseline", Name: "User MCP baseline", Kind: "mcp", Version: "v1", Source: configPath, CodexMCP: "baseline", Backends: []string{"codex"}}}}
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
	for i, mode := range []string{"enabled", "disabled"} {
		r, err := a.Apply(app.Command{ID: fmt.Sprintf("create-%d", i), Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: "MCP origins " + mode, CapabilitySelections: []app.CapabilitySelection{{AssetID: "baseline", Mode: mode}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.SessionID)
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, session := range s.Sessions {
				if session.ID == r.SessionID {
					return session.Status == "idle" && len(session.View.CapabilityEvidence) == 2
				}
			}
			return false
		})
		if _, err := a.Apply(app.Command{ID: "takeover-" + mode, Kind: "takeover", SessionID: r.SessionID}); err != nil {
			t.Fatal(err)
		}
		read, err := a.ReadExecution(r.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		results[mode] = read
		for _, e := range read.CapabilityEvidence {
			if e.Server == "baseline" {
				check(mode+"_configuration_source", e.Source == "configuration" && e.PluginID == "" && e.AssetID == "baseline" && e.Verified && e.Mode == mode)
			}
			if e.Server == "plugin_service" {
				check(mode+"_plugin_source_and_full_id", e.Source == "plugin" && e.PluginID == "fixture@owned" && e.AssetID == "" && e.Mode == "inherit" && e.RuntimeStatus == "connected")
				check(mode+"_native_plugin_tools_visible", len(e.Tools) == 1 && e.Tools[0] == "identity")
			}
			check(mode+"_"+e.Server+"_no_claimed_usage", len(e.UsedTools) == 0)
		}
	}
	profileAfter, err := os.ReadFile(configPath)
	check("product_did_not_write_profile", err == nil && string(profileAfter) == string(profileBefore))
	trace, err := os.ReadFile(filepath.Join(root, "mcp.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	check("real_initialize_and_tools_list", strings.Contains(string(trace), `"method": "initialize"`) && strings.Contains(string(trace), `"method": "tools/list"`))
	check("no_tool_call", !strings.Contains(string(trace), "tools/call"))
	check("no_model_turn", func() bool {
		for _, p := range f.Records {
			for _, c := range p.Calls {
				if c["method"] == "turn/start" {
					return false
				}
			}
		}
		return true
	}())
	token := strings.Repeat("c", 64)
	handler := console.Handler(a, "127.0.0.1:9464", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids[0], "/api/sessions/" + ids[1]} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:9464"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[path] = append([]byte(nil), w.Body.Bytes()...)
	}
	check("handler_preserves_plugin_id", strings.Contains(string(responses["/api/sessions/"+ids[0]]), `"plugin_id":"fixture@owned"`))
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "browser-responses.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		found := false
		for _, e := range saved.Sessions[id].View.CapabilityEvidence {
			if e.Server == "plugin_service" {
				found = e.PluginID == "fixture@owned" && e.Source == "plugin" && e.AssetID == ""
			}
		}
		check(fmt.Sprintf("sqlite_keeps_plugin_origin_%d", i), found)
	}
}
