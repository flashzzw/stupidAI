//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeHookSourceAdoptionMixed(t *testing.T)    { nativeHookSourceAdoption(t, "mixed") }
func TestCodexNativeHookSourceAdoptionWorktree(t *testing.T) { nativeHookSourceAdoption(t, "worktree") }
func TestCodexNativeHookSourceAdoptionColdPending(t *testing.T) {
	nativeHookSourceAdoption(t, "cold-pending")
}

func TestCodexNativeHookSourceAdoptionMixedColdPending(t *testing.T) {
	nativeHookSourceAdoption(t, "mixed-cold-pending")
}
func TestCodexNativeHookSourceAdoptionMixedColdPrepared(t *testing.T) {
	nativeHookSourceAdoption(t, "mixed-cold-prepared")
}

func hookAdoptionMixedAssets(t *testing.T, root, marker string, write func(string, string)) []app.CapabilityAsset {
	t.Helper()
	pack := filepath.Join(root, "source/bundle")
	loose := filepath.Join(root, "source/loose")
	write(filepath.Join(pack, ".codex-plugin/plugin.json"), `{"name":"adoption-bundle","version":"1.0.0"}`)
	write(filepath.Join(pack, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 ${PLUGIN_ROOT}/entry.py","timeout":1}]}]}}`)
	write(filepath.Join(pack, "entry.py"), fmt.Sprintf("import json,os,sys\nfrom pathlib import Path\ndata=json.load(sys.stdin)\nwith Path(%q).open('a') as f:f.write(json.dumps({'version':'pack','input':data})+'\\n')\nos.execl('/usr/bin/sleep','sleep','2')\n", marker))
	write(filepath.Join(pack, "skills/method/SKILL.md"), "---\nname: method\ndescription: OWNED_BUNDLE_METHOD_DESCRIPTION\n---\nOWNED_BUNDLE_METHOD_BODY\n")
	mcpBody := `import json,sys
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r:continue
 if r['method']=='initialize':value={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-adoption','version':'1'}}
 elif r['method']=='tools/list':value={'tools':[{'name':'identity','description':'Owned tool; not invoked','inputSchema':{'type':'object'}}]}
 elif r['method']=='resources/list':value={'resources':[]}
 elif r['method']=='resources/templates/list':value={'resourceTemplates':[]}
 else:raise RuntimeError('Unexpected MCP invocation')
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':value}),flush=True)
`
	mcp, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"adoption_service": map[string]any{"command": "python3", "args": []string{"-u", "-c", mcpBody}}}})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(pack, ".mcp.json"), string(mcp))
	write(filepath.Join(loose, "SKILL.md"), "---\nname: loose-method\ndescription: OWNED_LOOSE_METHOD_DESCRIPTION\n---\nOWNED_LOOSE_METHOD_BODY\n")
	return []app.CapabilityAsset{{ID: "bundle", Name: "Owned bundle", Kind: "cli_plugin", Version: "v1", Source: pack, Backends: []string{"codex"}}, {ID: "loose", Name: "Owned independent Skill", Kind: "skill", Version: "v1", Source: loose, Backends: []string{"codex"}}}
}

func hookAdoptionMixedSelections(t *testing.T, a *app.Application, instance runtime.Instance) []app.CapabilitySelection {
	t.Helper()
	assets, err := a.ReadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Asset.ID == "bundle" {
			result, err := a.InspectCapabilityHooks(context.Background(), app.CapabilityHookInspectionRequest{AssetID: "bundle", InstanceID: instance.ID, Revision: asset.Revision, SourceSHA256: asset.Source.SHA256})
			if err != nil {
				t.Fatal(err)
			}
			return []app.CapabilitySelection{{AssetID: "bundle", Mode: "enabled", HookReview: &app.CapabilityHookReview{InspectionID: result.InspectionID, Trust: "trusted"}}, {AssetID: "loose", Mode: "enabled"}}
		}
	}
	t.Fatal("bundle unavailable")
	return nil
}

func hookAdoptionWorktree(t *testing.T, root string, write func(string, string)) (string, string) {
	t.Helper()
	main := filepath.Join(root, "main-repo")
	linked := filepath.Join(root, "linked")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Owned fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Owned fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("owned git fixture: %s: %v", out, err)
		}
	}
	git("init", "--quiet", main)
	git("-C", main, "commit", "--quiet", "--allow-empty", "-m", "Owned worktree fixture")
	git("-C", main, "worktree", "add", "--quiet", "--detach", linked)
	cwd := filepath.Join(linked, "child")
	write(filepath.Join(linked, ".codex/hooks.json"), `{"hooks":{}}`)
	write(filepath.Join(cwd, ".codex/hooks.json"), `{"hooks":{}}`)
	return cwd, filepath.Join(main, ".codex/hooks.json")
}

func TestCodexNativeHookSourceAdoptionMixedColdLoss(t *testing.T) {
	nativeHookSourceAdoption(t, "mixed-cold-loss")
}

func TestCodexNativeColdPluginPolicyBoundary(t *testing.T) {
	nativeHookSourceAdoption(t, "mixed-cold-policy-probe")
}

// Establish the fixed native protocol boundary before connecting production.
// Every resource and process belongs to this fixture; no model input is sent.
func hookAdoptionColdPolicyProbe(t *testing.T, f *runtime.NativeApplicationFixture, current app.Session) map[string]any {
	t.Helper()
	results := map[string]any{}
	call := func(label, method string, params map[string]any) map[string]any {
		t.Helper()
		var out map[string]any
		if err := f.Call(context.Background(), method, params, &out); err != nil {
			t.Fatal(label, err)
		}
		results[label] = out
		return out
	}
	scope := current.Scope
	before := call("before", "thread/read", map[string]any{"threadId": scope.NativeID, "includeTurns": true})
	overrides := map[string]any{}
	ids := []string{}
	for _, pack := range scope.PluginSelections {
		overrides["plugins."+pack.NativeSource+".enabled"] = false
		ids = append(ids, pack.NativeSource)
	}
	base := map[string]any{"threadId": scope.NativeID, "excludeTurns": true, "cwd": scope.Cwd, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "sandbox": "read-only", "approvalPolicy": "on-request", "config": overrides}
	boot := call("bootstrap", "thread/resume", base)
	if len(boot["disabledPluginIds"].([]any)) != 0 {
		t.Fatal("unexpected initial native policy")
	}
	inventory := call("bootstrap_mcp", "mcpServerStatus/list", map[string]any{"threadId": scope.NativeID, "limit": 100})
	if len(inventory["data"].([]any)) != 0 {
		t.Fatal("provisional excluded plugins still loaded MCP")
	}
	call("policy", "thread/settings/update", map[string]any{"threadId": scope.NativeID, "disabledPluginIds": ids})
	call("detached", "thread/unsubscribe", map[string]any{"threadId": scope.NativeID})
	base["config"] = map[string]any{}
	resumed := call("final", "thread/resume", base)
	got := []string{}
	for _, id := range resumed["disabledPluginIds"].([]any) {
		got = append(got, id.(string))
	}
	slices.Sort(got)
	slices.Sort(ids)
	if !slices.Equal(got, ids) {
		t.Fatal("final resume lost policy")
	}
	finalInventory := call("final_mcp", "mcpServerStatus/list", map[string]any{"threadId": scope.NativeID, "limit": 100})
	if len(finalInventory["data"].([]any)) != 0 {
		t.Fatal("retired MCP reappeared")
	}
	after := call("after", "thread/read", map[string]any{"threadId": scope.NativeID, "includeTurns": true})
	if !reflect.DeepEqual(before["thread"].(map[string]any)["turns"], after["thread"].(map[string]any)["turns"]) {
		t.Fatal("policy sequence changed original history")
	}
	results["passed"] = true
	return results
}

func TestCodexNativeColdPolicyRecovery(t *testing.T) {
	for _, boundary := range []string{"bootstrap", "policy", "unsubscribe", "final"} {
		t.Run(boundary, func(t *testing.T) { nativeHookSourceAdoption(t, "mixed-cold-recover-"+boundary) })
	}
}

func TestCodexNativeColdRecoveryReplyLoss(t *testing.T) {
	for _, boundary := range []string{"bootstrap", "policy", "unsubscribe", "final"} {
		t.Run(boundary, func(t *testing.T) { nativeHookSourceAdoption(t, "mixed-cold-double-"+boundary) })
	}
}
