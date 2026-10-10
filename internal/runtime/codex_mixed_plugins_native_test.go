//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"io"
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

func TestCodexNativeMixedPluginSelectionAndChanges(t *testing.T) {
	codexNativePluginSkillFlow(t, "mixed")
}

func TestCodexNativeExplicitSkillInputs(t *testing.T) {
	codexNativePluginSkillFlow(t, "explicit")
}

func TestCodexNativeCustomSkillRoots(t *testing.T) {
	codexNativePluginSkillFlow(t, "custom-roots")
}

func TestCodexNativeSkillOnlyPluginInputs(t *testing.T) {
	codexNativePluginSkillFlow(t, "skill-only")
}

func TestCodexNativeSharedSkillFile(t *testing.T) { codexNativePluginSkillFlow(t, "skill-file") }
func TestCodexNativeSharedSkillDirectory(t *testing.T) {
	codexNativePluginSkillFlow(t, "skill-directory")
}

type nativeSkillReceiptLoss struct {
	*runtime.Codex
	once sync.Once
	lost chan struct {
		Intent  runtime.Intent
		Receipt runtime.Receipt
	}
}

func (r *nativeSkillReceiptLoss) Execute(ctx context.Context, in runtime.Intent) (runtime.Receipt, error) {
	receipt, err := r.Codex.Execute(ctx, in)
	if err != nil || in.Kind != "send" || len(in.SkillInputs) == 0 {
		return receipt, err
	}
	dropped := false
	r.once.Do(func() {
		dropped = true
		r.lost <- struct {
			Intent  runtime.Intent
			Receipt runtime.Receipt
		}{in, receipt}
	})
	if dropped {
		// Native accepted the real turn; Application receives neither identity
		// nor turn ID and must recover the original request without replay.
		return runtime.Receipt{}, io.ErrUnexpectedEOF
	}
	return receipt, nil
}

func codexNativePluginSkillFlow(t *testing.T, variant string) {
	explicitSkills, skillOnly := variant != "mixed", variant == "skill-only"
	customRoots := variant == "custom-roots"
	portableLayout := ""
	if strings.HasPrefix(variant, "portable-") {
		portableLayout = strings.TrimPrefix(variant, "portable-")
	}
	assetSkill := variant == "skill-file" || variant == "skill-directory"
	if assetSkill {
		skillOnly = true
	}
	inputCount := 2
	if assetSkill {
		inputCount = 1
	}
	prefix := "netcap-w0208-mixed-plugin-"
	if explicitSkills {
		prefix = "netcap-w0207-skill-input-"
	}
	if skillOnly {
		prefix = "netcap-w0208-skill-only-"
	}
	if assetSkill {
		prefix = "netcap-w0207-" + variant + "-"
	}
	if customRoots {
		prefix = "netcap-w0208-custom-skills-"
	}
	if portableLayout != "" {
		prefix = "netcap-w0208-portable-" + portableLayout + "-"
	}
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks := map[string]bool{}
	reports := map[string]any{}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"checks": checks, "reports": reports, "processes": f.Records, "passed": !t.Failed(), "explicit_skill_inputs": explicitSkills, "skill_only": skillOnly, "skill_asset": assetSkill, "custom_skill_roots": customRoots, "portable_layout": portableLayout, "scope": "production Application/SQLite/Runtime plugin or shared Skill selection; interrupted unavailable local provider inputs; no successful model/tool/hook execution"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
	})
	config := filepath.Join(root, "codex/config.toml")
	before, _ := os.ReadFile(config)
	write(config, strings.Replace(string(before), "plugins=false", "plugins=true\nplugin_sharing=false", 1))
	before, _ = os.ReadFile(config)
	source := filepath.Join(root, "assets/mixed")
	entryPath := "skills/method/SKILL.md"
	secondPath := "skills/second/SKILL.md"
	if assetSkill {
		entryPath = "SKILL.md"
	} else {
		write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"mixed","version":"1.0.0"}`)
	}
	if customRoots {
		entryPath, secondPath = "methods/grouped-entry/SKILL.md", "helpers/other-entry/SKILL.md"
		write(filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"mixed","version":"1.0.0","skills":["./methods","./helpers"]}`)
		write(filepath.Join(source, "skills/ignored/SKILL.md"), "---\nname: ignored\ndescription: REPLACED_DEFAULT_DESCRIPTION\n---\nREPLACED_DEFAULT_BODY\n")
	}
	methodV1 := "---\nname: method\ndescription: MIXED_NATIVE_DESCRIPTION\n---\nMIXED_NATIVE_BODY\n"
	if variant == "skill-directory" {
		methodV1 += "Read [guide](references/guide.md).\n"
		write(filepath.Join(source, "references/guide.md"), "OWNED_GUIDE_V1\n")
	}
	if customRoots {
		methodV1 += "Read [guide](../../resources/guide.md).\n"
		write(filepath.Join(source, "resources/guide.md"), "OWNED_GUIDE_V1\n")
	}
	write(filepath.Join(source, entryPath), methodV1)
	if explicitSkills && !assetSkill {
		write(filepath.Join(source, secondPath), "---\nname: second\ndescription: Second owned method\n---\nSECOND_NATIVE_BODY\n")
	}
	body := `import json,sys
for line in sys.stdin:
 r=json.loads(line)
 with open(sys.argv[1],'a') as f:f.write(json.dumps({'method':r.get('method')})+'\n')
 if 'id' not in r:continue
 m=r['method']
 if m=='initialize':v={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'mixed','version':'1'}}
 elif m=='tools/list':v={'tools':[{'name':'identity','inputSchema':{'type':'object','properties':{}}}]}
 elif m=='resources/list':v={'resources':[]}
 elif m=='resources/templates/list':v={'resourceTemplates':[]}
 else:raise RuntimeError('unexpected tool execution')
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':v}),flush=True)
`
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"mixed_service": map[string]any{"command": "python3", "args": []string{"-u", "-c", body, filepath.Join(root, "mcp.jsonl")}}}})
	if !skillOnly {
		write(filepath.Join(source, ".mcp.json"), string(mcp))
	}
	if portableLayout != "" {
		writeNativePortablePlugin(t, source, portableLayout, "1", mcp)
	}
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: []app.CapabilityAsset{{ID: "mixed", Name: "Mixed plugin", Kind: "cli_plugin", Version: "asset-v1", Source: source, Backends: []string{"codex"}}}}
	if assetSkill {
		cfg.CapabilityAssets[0].Kind, cfg.CapabilityAssets[0].Name = "skill", "Shared method"
		cfg.CapabilityAssets[0].Backends = []string{"codex", "claude"}
		if variant == "skill-file" {
			cfg.CapabilityAssets[0].Source = filepath.Join(source, "SKILL.md")
		}
	}
	store, err := app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	var adapter runtime.Adapter = f.Runtime
	var loss *nativeSkillReceiptLoss
	if explicitSkills {
		loss = &nativeSkillReceiptLoss{Codex: f.Runtime, lost: make(chan struct {
			Intent  runtime.Intent
			Receipt runtime.Receipt
		}, 1)}
		adapter = loss
	}
	a, err := app.Open(cfg, store, adapter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	apply := func(cmd app.Command) app.CommandReceipt {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var last error
		for time.Now().Before(deadline) {
			r, e := a.Apply(cmd)
			if e == nil {
				return r
			}
			last = e
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: %v", cmd.Kind, last)
		return app.CommandReceipt{}
	}
	session := func(id string) app.Session {
		t.Helper()
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				return s
			}
		}
		t.Fatal("missing session")
		return app.Session{}
	}
	ready := func(id string) {
		t.Helper()
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, ss := range s.Sessions {
				if ss.ID == id {
					return ss.Status == "idle" && ss.Connection == "ready" && (len(ss.Scope.PluginSelections) == 0 || len(ss.View.PluginEvidence) == len(ss.Scope.PluginSelections) && ss.View.PluginEvidence[0].Verified)
				}
			}
			return false
		})
	}
	opDone := func(id string) {
		t.Helper()
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == id {
					if op.Status == "not_sent" || op.Status == "failed" {
						t.Fatalf("%s: %s", op.Intent.Kind, op.Error)
					}
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}
	read := func(id string) app.ExecutionRead {
		t.Helper()
		r, e := a.ReadExecution(id)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	ids := map[string]string{}
	methodNames := map[string]string{}
	inputItems := map[string][]runtime.SkillInput{}
	for _, mode := range []string{"enabled", "disabled"} {
		r := apply(app.Command{ID: "create-" + mode, Kind: "create", ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", Title: mode, CapabilitySelections: []app.CapabilitySelection{{AssetID: "mixed", Mode: mode}}})
		ids[mode] = r.SessionID
		ready(r.SessionID)
		out := read(r.SessionID)
		for _, component := range out.Snapshot.PluginSelections[0].Components {
			if component.Kind == "skill" {
				inputItems[r.SessionID] = append(inputItems[r.SessionID], runtime.SkillInput{AssetID: "mixed", Name: component.NativeName})
			}
			if component.Name == "method" {
				methodNames[r.SessionID] = component.NativeName
			}
		}
		check(mode+"_whole_package_policy_verified", len(out.PluginEvidence) == 1 && out.PluginEvidence[0].Verified && out.PluginEvidence[0].Coverage == "cached_package_and_thread_policy")
		if portableLayout != "" {
			pack := out.Snapshot.PluginSelections[0]
			check(mode+"_canonical_identity_retained", pack.Name == "mixed" && pack.Version == "1.0.0")
			check(mode+"_manifest_source_retained", pack.ManifestSource != nil && pack.ManifestSource.Identity == "plugin.json" && pack.ManifestSource.Layout == "portable" && pack.ManifestSource.Settings == nativePortableSettings(portableLayout))
		}
		if skillOnly {
			check(mode+"_no_invented_mcp", len(out.CapabilityEvidence) == 0 && len(out.PluginEvidence[0].Components) == inputCount && !strings.Contains(out.PluginEvidence[0].Detail, "MCP"))
		}
		if assetSkill {
			check(mode+"_original_skill_asset_retained", out.Snapshot.CapabilityRefs[0].Asset.Kind == "skill" && out.Snapshot.PluginSelections[0].SkillAsset != nil)
		}
		reports[mode+"-created"] = out
	}
	check("independent_private_caches", read(ids["enabled"]).Snapshot.PluginSelections[0].NativeSource != read(ids["disabled"]).Snapshot.PluginSelections[0].NativeSource)
	descriptions := map[string]string{ids["enabled"]: "MIXED_NATIVE_DESCRIPTION", ids["disabled"]: "MIXED_NATIVE_DESCRIPTION"}
	sample := func(label, id string, enabled bool) {
		t.Helper()
		ready(id)
		cmd := app.Command{ID: "input-" + label, Kind: "send", SessionID: id, Text: "Owned mixed plugin preparation; provider intentionally unavailable; do not execute tools"}
		if explicitSkills {
			cmd.SkillInput = &app.SkillInputRequest{SnapshotID: read(id).Snapshot.ID, Items: inputItems[id]}
			if !enabled {
				before := nativePluginCalls(f, "turn/start", session(id).Scope.NativeID)
				_, e := a.Apply(cmd)
				check(label+"_disabled_explicit_request_rejected", e != nil && before == nativePluginCalls(f, "turn/start", session(id).Scope.NativeID))
				cmd.SkillInput = nil
			}
		}
		r := apply(cmd)
		turn := ""
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == r.OperationID {
					if op.Status == "not_sent" {
						t.Fatalf("send rejected: %s", op.Error)
					}
					turn = op.Receipt.NativeTurn
					return turn != ""
				}
			}
			return false
		})
		sid := session(id).Scope.NativeID
		attempted := false
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			checkpoint, e := f.Checkpoint()
			if e != nil {
				t.Fatal(e)
			}
			var records []struct {
				Events []struct {
					Method string `json:"method"`
					Params struct {
						ThreadID string          `json:"threadId"`
						TurnID   string          `json:"turnId"`
						Error    json.RawMessage `json:"error"`
					} `json:"params"`
				} `json:"events"`
			}
			_ = json.Unmarshal(checkpoint.Trace, &records)
			for _, record := range records {
				for _, event := range record.Events {
					if event.Method == "error" && event.Params.ThreadID == sid && event.Params.TurnID == turn && strings.Contains(string(event.Params.Error), "responseStreamDisconnected") {
						attempted = true
					}
				}
			}
			if attempted {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		check(label+"_provider_failure_observed", attempted)
		apply(app.Command{ID: "interrupt-" + label, Kind: "interrupt", SessionID: id})
		ready(id)
		preparedContext, e := a.ReadSessionSkillContext(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		frame := preparedContext.Frames[len(preparedContext.Frames)-1]
		found := false
		for _, d := range frame.Descriptions {
			if d.Name == methodNames[id] && d.Description == descriptions[id] {
				found = true
			}
		}
		check(label+"_actual_description", frame.TurnID == turn && found == enabled)
		if portableLayout != "" {
			ignored := false
			for _, d := range frame.Descriptions {
				ignored = ignored || d.Description == "PORTABLE_IGNORED_SKILL" || strings.HasPrefix(d.Name, "ignored-identity:")
			}
			check(label+"_ignored_overlay_skill_absent", !ignored)
			out := read(id)
			for _, row := range out.CapabilityEvidence {
				ignored = ignored || row.Server == "ignored_mcp" || strings.HasPrefix(row.Server, "selected_service_")
			}
			check(label+"_ignored_mcp_absent", !ignored)
		}
		if customRoots {
			defaultFound := false
			for _, d := range frame.Descriptions {
				defaultFound = defaultFound || d.Name == "mixed:ignored" || d.Description == "REPLACED_DEFAULT_DESCRIPTION"
			}
			check(label+"_replaced_default_skill_absent", !defaultFound)
		}
		if explicitSkills && enabled {
			input := frame.InputEvidence
			check(label+"_request_correlated", input != nil && input.OperationID == r.OperationID && input.SnapshotID == cmd.SkillInput.SnapshotID && len(input.Items) == inputCount)
			loaded, versions, distinct := input != nil && len(input.Items) == inputCount, input != nil && len(input.Items) == inputCount, false
			if input != nil {
				for _, item := range input.Items {
					loaded = loaded && item.Status == "loaded_exact" && len(item.RecordIDs) > 0
					versions = versions && item.AssetVersion == read(id).Snapshot.CapabilityRefs[0].Asset.Version
				}
				if len(input.Items) == 2 {
					distinct = input.Items[0].Name != input.Items[1].Name && input.Items[0].ExpectedSHA256 != input.Items[1].ExpectedSHA256
				}
			}
			check(label+"_exact_body_loaded", loaded)
			check(label+"_request_version_bound", versions)
			if customRoots {
				pack := read(id).Snapshot.PluginSelections[0]
				paths := map[string]string{"mixed:method": "methods/grouped-entry/SKILL.md", "mixed:second": "helpers/other-entry/SKILL.md"}
				if read(id).Snapshot.CapabilityRefs[0].Asset.Version == "asset-v2" {
					paths = map[string]string{"mixed:method": "workflows/new-entry/SKILL.md", "mixed:second": "workflows/second-entry/SKILL.md"}
				}
				bound := input != nil && len(input.Items) == 2
				if input != nil {
					for _, item := range input.Items {
						bound = bound && paths[item.Name] != "" && item.Path == filepath.Join(pack.CachePath, paths[item.Name])
					}
				}
				check(label+"_custom_paths_bound", bound)
			}
			if !assetSkill {
				check(label+"_distinct_skill_bodies", distinct)
			}
			if variant == "skill-directory" || customRoots {
				cache := read(id).Snapshot.PluginSelections[0].CachePath
				guidePath := "skills/method/references/guide.md"
				if customRoots {
					guidePath = "resources/guide.md"
				}
				guide, err := os.ReadFile(filepath.Join(cache, guidePath))
				expected := "OWNED_GUIDE_V1\n"
				if read(id).Snapshot.CapabilityRefs[0].Asset.Version == "asset-v2" {
					expected = "OWNED_GUIDE_V2\n"
				}
				check(label+"_relative_resource_keeps_version", err == nil && string(guide) == expected)
			}
		}
		var native struct {
			Thread struct {
				Turns []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"turns"`
			} `json:"thread"`
		}
		if e = f.Call(context.Background(), "thread/read", map[string]any{"threadId": sid, "includeTurns": true}, &native); e != nil {
			t.Fatal(e)
		}
		terminal := false
		for _, nt := range native.Thread.Turns {
			if nt.ID == turn && nt.Status == "interrupted" {
				terminal = true
			}
		}
		check(label+"_interrupted", terminal)
		reports[label] = map[string]any{"execution": read(id), "context": preparedContext}
	}
	sample("enabled-first", ids["enabled"], true)
	if explicitSkills {
		select {
		case lost := <-loss.lost:
			reports["lost-input-receipt"] = lost
			check("lost_receipt_original_request", lost.Intent.ID != "" && len(lost.Intent.SkillInputs) == inputCount && lost.Receipt.NativeTurn != "")
			matched := false
			for _, op := range a.Snapshot().Operations {
				if op.ID == lost.Intent.ID {
					reports["recovered-input-operation"] = op
					matched = op.Receipt.NativeID == lost.Receipt.NativeID && op.Receipt.NativeTurn == lost.Receipt.NativeTurn && op.Status == "interrupted"
				}
			}
			check("lost_receipt_same_turn_recovered", matched)
			check("lost_receipt_no_replay", nativePluginCalls(f, "turn/start", lost.Receipt.NativeID) == 1)
		default:
			t.Fatal("explicit input receipt was not dropped")
		}
	}
	sample("disabled-first", ids["disabled"], false)
	// An unrelated native disable survives all product changes and new input.
	for _, id := range ids {
		ss := session(id)
		policy := []string{"retained@owned"}
		if ss.Scope.PluginSelections[0].Mode == "disabled" {
			policy = append(policy, ss.Scope.PluginSelections[0].NativeSource)
		}
		if err = f.Call(context.Background(), "thread/settings/update", map[string]any{"threadId": ss.Scope.NativeID, "disabledPluginIds": policy}, nil); err != nil {
			t.Fatal(err)
		}
	}
	change := func(label, id, mode string, version ...string) {
		t.Helper()
		before := read(id)
		nativeID := session(id).Scope.NativeID
		request := app.ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []app.CapabilitySelection{{AssetID: "mixed", Mode: mode}}}
		if len(version) > 0 {
			request.Selections[0].Version = version[0]
		}
		preview, e := a.PreviewExecutionChange(id, request)
		if e != nil {
			t.Fatal(e)
		}
		request.PreviewHash = preview.PreviewHash
		apply(app.Command{ID: label + "-stage", Kind: "stage_execution", SessionID: id, ExecutionChange: &request})
		staged := read(id)
		ref := app.ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
		count := nativePluginCalls(f, "thread/settings/update", nativeID)
		p := apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref})
		opDone(p.OperationID)
		check(label+"_prepare_no_settings_write", count == nativePluginCalls(f, "thread/settings/update", nativeID))
		p = apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref})
		opDone(p.OperationID)
		out := read(id)
		check(label+"_same_id_and_exact_candidate", session(id).Scope.NativeID == nativeID && out.Snapshot.ID == staged.PendingChange.Snapshot.ID && len(out.History) == len(before.History)+1)
		reports[label] = out
		var echo struct {
			IDs []string `json:"disabledPluginIds"`
		}
		if e = f.Call(context.Background(), "thread/resume", map[string]any{"threadId": nativeID, "excludeTurns": true}, &echo); e != nil {
			t.Fatal(e)
		}
		retained := false
		for _, v := range echo.IDs {
			retained = retained || v == "retained@owned"
		}
		check(label+"_unrelated_policy_preserved", retained)
	}
	change("reenable", ids["disabled"], "enabled")
	sample("reenabled-input", ids["disabled"], true)
	change("disable", ids["enabled"], "disabled")
	sample("disabled-input", ids["enabled"], false)
	change("inherit", ids["enabled"], "inherit")
	sample("inherited-input", ids["enabled"], false)
	check("sibling_still_enabled", read(ids["disabled"]).PluginEvidence[0].Verified)
	old := read(ids["disabled"])
	sourceV2 := filepath.Join(root, "assets/mixed-v2")
	if !assetSkill {
		write(filepath.Join(sourceV2, ".codex-plugin/plugin.json"), `{"name":"mixed","version":"1.0.0"}`)
	}
	if customRoots {
		entryPath, secondPath = "workflows/new-entry/SKILL.md", "workflows/second-entry/SKILL.md"
		write(filepath.Join(sourceV2, ".codex-plugin/plugin.json"), `{"name":"mixed","version":"1.0.0","skills":"./workflows"}`)
		write(filepath.Join(sourceV2, "skills/ignored/SKILL.md"), "---\nname: ignored\ndescription: REPLACED_DEFAULT_DESCRIPTION\n---\nREPLACED_DEFAULT_BODY\n")
	}
	methodV2 := "---\nname: method\ndescription: MIXED_NATIVE_DESCRIPTION_V2\n---\nMIXED_NATIVE_BODY_V2\n"
	if variant == "skill-directory" {
		methodV2 += "Read [guide](references/guide.md).\n"
		write(filepath.Join(sourceV2, "references/guide.md"), "OWNED_GUIDE_V2\n")
	}
	if customRoots {
		methodV2 += "Read [guide](../../resources/guide.md).\n"
		write(filepath.Join(sourceV2, "resources/guide.md"), "OWNED_GUIDE_V2\n")
	}
	if explicitSkills {
		methodV2 = strings.ReplaceAll(methodV2, "\n", "\r\n")
		if !assetSkill {
			write(filepath.Join(sourceV2, secondPath), "---\nname: second\ndescription: Second owned method v2\n---\nSECOND_NATIVE_BODY_V2\n")
		}
	}
	write(filepath.Join(sourceV2, entryPath), methodV2)
	if !skillOnly {
		write(filepath.Join(sourceV2, ".mcp.json"), strings.Replace(string(mcp), "mixed_service", "mixed_service_v2", 1))
	}
	if portableLayout != "" {
		writeNativePortablePlugin(t, sourceV2, portableLayout, "2", []byte(strings.Replace(string(mcp), "mixed_service", "mixed_service_v2", 1)))
	}
	catalog, e := a.ReadCapabilities(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	updated := cfg.CapabilityAssets[0]
	updated.Source = sourceV2
	if variant == "skill-file" {
		updated.Source = filepath.Join(sourceV2, "SKILL.md")
	}
	updated.Version = "asset-v2"
	request := app.CapabilityChangeRequest{Action: "update", AssetID: "mixed", BaseRevision: catalog[0].Revision, Asset: &updated}
	preview, e := a.PreviewCapabilityChange(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	request.PreviewHash = preview.PreviewHash
	apply(app.Command{ID: "library-update", Kind: "manage_capability", CapabilityChange: &request})
	check("library_update_keeps_original_snapshot", read(ids["disabled"]).Snapshot.ID == old.Snapshot.ID)
	sample("pinned-old-input", ids["disabled"], true)
	change("upgrade", ids["disabled"], "enabled", "asset-v2")
	descriptions[ids["disabled"]] = "MIXED_NATIVE_DESCRIPTION_V2"
	sample("upgraded-input", ids["disabled"], true)
	check("upgrade_replaces_private_source", read(ids["disabled"]).Snapshot.PluginSelections[0].NativeSource != old.Snapshot.PluginSelections[0].NativeSource)
	if explicitSkills {
		context, e := a.ReadSessionSkillContext(context.Background(), ids["disabled"])
		if e != nil {
			t.Fatal(e)
		}
		oldKept := false
		for _, frame := range context.Frames {
			if frame.InputEvidence != nil && frame.InputEvidence.SnapshotID == old.Snapshot.ID {
				for _, item := range frame.InputEvidence.Items {
					oldKept = oldKept || item.AssetVersion == "asset-v1" && item.Status == "loaded_exact"
				}
			}
		}
		check("old_request_keeps_original_version_after_upgrade", oldKept)
	}
	change("add", ids["enabled"], "enabled", "asset-v2")
	descriptions[ids["enabled"]] = "MIXED_NATIVE_DESCRIPTION_V2"
	sample("added-input", ids["enabled"], true)
	// Capture actual Handler data for browser acceptance without a listener.
	token := strings.Repeat("m", 64)
	handler := console.Handler(a, "127.0.0.1:9467", token, os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/snapshot", "/api/sessions/" + ids["disabled"], "/api/sessions/" + ids["disabled"] + "/skills/context"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "/skills/context") {
			method, body = "POST", "{}"
		}
		req := httptest.NewRequest(method, "http://127.0.0.1:9467"+path, strings.NewReader(body))
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		responses[path] = append([]byte(nil), w.Body.Bytes()...)
	}
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "session": ids["disabled"], "skill_only": skillOnly, "skill_asset": assetSkill, "custom_skill_roots": customRoots, "portable_layout": portableLayout}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(b))
	nativeIDs := map[string]string{}
	for label, id := range ids {
		nativeIDs[label] = session(id).Scope.NativeID
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	store, err = app.OpenStore(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = app.Open(cfg, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	for label, id := range ids {
		ready(id)
		out := read(id)
		check(label+"_cold_restore_keeps_selection", len(out.Snapshot.PluginSelections) == 0 || out.PluginEvidence[0].Verified)
		check(label+"_cold_restore_keeps_native_id", session(id).Scope.NativeID == nativeIDs[label])
		reports[label+"-restored"] = out
		sample("cold-"+label, id, true)
	}
	if assetSkill {
		id := ids["disabled"]
		pack := read(id).Snapshot.PluginSelections[0]
		target := pack.SourcePath
		if pack.SkillAsset.Kind == "file" {
			target = filepath.Dir(target)
		}
		aliases := filepath.Join(cfg.Projects[0].Cwd, ".agents/skills")
		if err := os.MkdirAll(aliases, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(aliases, "new-native-source")); err != nil {
			t.Fatal(err)
		}
		before := nativePluginCalls(f, "turn/start", session(id).Scope.NativeID)
		r, sendErr := a.Apply(app.Command{ID: "refuse-native-alias", Kind: "send", SessionID: id, Text: "Must not send after a source becomes native"})
		// Background observation may withdraw readiness before Apply runs.
		// Both an immediate refusal and a verified not_sent operation are safe.
		if sendErr == nil {
			waitNativeApplication(t, a, func(s app.Snapshot) bool {
				for _, op := range s.Operations {
					if op.ID == r.OperationID {
						return op.Status == "not_sent"
					}
				}
				return false
			})
		} else {
			reports["native-source-send-refusal"] = sendErr.Error()
		}
		check("new_native_source_blocks_input", (sendErr == nil || strings.Contains(sendErr.Error(), "会话尚未空闲")) && before == nativePluginCalls(f, "turn/start", session(id).Scope.NativeID))
		scope := session(id).Scope
		scope.NoAutoResume = true
		mutations := func() int {
			return nativePluginCalls(f, "thread/resume", scope.NativeID) + nativePluginCalls(f, "thread/settings/update", scope.NativeID) + nativePluginCalls(f, "turn/start", scope.NativeID)
		}
		beforeRead := mutations()
		observed, err := f.Runtime.Observe(context.Background(), scope)
		reports["native-source-overlap"] = observed
		check("new_native_source_not_claimed_verified", err == nil && !observed.ExecutionClear && len(observed.PluginEvidence) == 1 && !observed.PluginEvidence[0].Verified)
		check("new_native_source_reason_visible", len(observed.PluginEvidence) == 1 && strings.Contains(observed.PluginEvidence[0].Detail, "原生目录或插件发现"))
		check("new_native_source_observation_read_only", beforeRead == mutations())
	}
	trace, _ := os.ReadFile(filepath.Join(root, "mcp.jsonl"))
	check("no_tool_execution", !strings.Contains(string(trace), "tools/call"))
	if skillOnly {
		_, err := os.Stat(filepath.Join(root, "mcp.jsonl"))
		check("no_mcp_process_started", os.IsNotExist(err))
	}
	after, _ := os.ReadFile(config)
	check("profile_unchanged", string(before) == string(after))
}
