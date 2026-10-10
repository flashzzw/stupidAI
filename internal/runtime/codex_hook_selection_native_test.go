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
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeHookSelectionAndTrustChanges(t *testing.T) {
	codexNativeHookSelectionFlow(t, false)
}

func codexNativeHookSelectionFlow(t *testing.T, combined bool, recoverOriginal ...string) {
	prefix := "netcap-w0208-hook-selection-"
	if combined {
		prefix = "netcap-w0208-combined-resources-"
	}
	crashPoint := ""
	if len(recoverOriginal) == 1 && strings.HasPrefix(recoverOriginal[0], "crash-") {
		crashPoint = strings.TrimPrefix(recoverOriginal[0], "crash-")
	}
	var root string
	var err error
	if crashPoint != "" {
		root = os.Getenv("NETCAP_RECOVERY_CRASH_ROOT")
		if !filepath.IsAbs(root) {
			t.Fatal("recovery crash fixture requires an isolated absolute root")
		}
	} else {
		root, err = os.MkdirTemp("", prefix)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root, 3*time.Minute)
	if crashPoint != "" {
		t.Cleanup(func() { _ = os.WriteFile(filepath.Join(root, "graceful-cleanup-ran"), []byte("unexpected"), 0600) })
		if err := os.WriteFile(filepath.Join(root, "work", "retained.txt"), []byte("owned recovery artifact; preserve across SIGKILL\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	checks, samples := map[string]bool{}, map[string]any{}
	check := func(name string, value bool) {
		t.Helper()
		checks[name] = value
		if !value {
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
		body, _ := json.MarshalIndent(map[string]any{"root": root, "combined": combined, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/Runtime/Handler; owned hook review and selection; intentional timeouts and unavailable local provider; no successful model/tool/hook execution"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
	})
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, _ := os.ReadFile(configPath)
	write(configPath, strings.Replace(string(config), "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1))
	profileBefore, _ := os.ReadFile(configPath)
	marker := filepath.Join(root, "hook-inputs.jsonl")
	packageAt := func(version string) string {
		source := filepath.Join(root, "source", version)
		write(filepath.Join(source, ".codex-plugin/plugin.json"), fmt.Sprintf(`{"name":"hook-fixture","version":"%s.0.0"}`, version))
		write(filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 ${PLUGIN_ROOT}/entry.py","timeout":1}]}]}}`)
		write(filepath.Join(source, "skills/method/SKILL.md"), "---\nname: method\ndescription: Owned mixed hook fixture method\n---\nNo method use is claimed by this test.\n")
		mcpBody := `import json,sys
for line in sys.stdin:
 r=json.loads(line)
 if 'id' not in r: continue
 if r['method']=='initialize': value={'protocolVersion':r['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'owned-hook-fixture','version':'1'}}
 elif r['method']=='tools/list': value={'tools':[{'name':'identity','description':'Owned test tool, not requested','inputSchema':{'type':'object'}}]}
 elif r['method']=='resources/list': value={'resources':[]}
 elif r['method']=='resources/templates/list': value={'resourceTemplates':[]}
 else: raise RuntimeError('Unexpected tool execution')
 print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':value}),flush=True)
`
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"hook_service": map[string]any{"command": "python3", "args": []string{"-u", "-c", mcpBody}}}})
		write(filepath.Join(source, ".mcp.json"), string(mcp))
		write(filepath.Join(source, "entry.py"), fmt.Sprintf("import json,os,sys\nfrom pathlib import Path\ndata=json.load(sys.stdin)\nwith Path(%q).open('a') as f: f.write(json.dumps({'version':%q,'input':data})+'\\n')\nos.execl('/usr/bin/sleep','sleep','2')\n", marker, version))
		return source
	}
	asset := app.CapabilityAsset{ID: "hooks", Name: "Scoped hooks", Kind: "cli_plugin", Version: "asset-v1", Source: packageAt("1"), Backends: []string{"codex"}}
	assetList := []app.CapabilityAsset{asset}
	directoryAsset := app.CapabilityAsset{}
	if combined {
		source := filepath.Join(root, "home/.agents/skills/directory")
		write(filepath.Join(source, "SKILL.md"), "---\nname: native-directory\ndescription: OWNED_DIRECTORY_DESCRIPTION\n---\nOwned directory method v1.\n")
		directoryAsset = app.CapabilityAsset{ID: "directory", Name: "Native directory", Kind: "skill", Version: "directory-v1", Source: source, CodexSkill: filepath.Join(source, "SKILL.md"), Backends: []string{"codex"}}
		assetList = append(assetList, directoryAsset)
	}
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: assetList}
	var applicationLoss *nativeConfigurationApplicationLoss
	open := func() *app.Application {
		store, e := app.OpenStore(cfg.StateDir)
		if e != nil {
			t.Fatal(e)
		}
		var adapter runtime.Adapter = f.Runtime
		if combined {
			applicationLoss = &nativeConfigurationApplicationLoss{Codex: f.Runtime, received: make(chan nativeApplicationConfigurationReply, 1)}
			adapter = applicationLoss
		}
		var persistence app.Persistence = store
		if crashPoint != "" {
			persistence = &recoveryCrashStore{Persistence: store, fixture: f, point: crashPoint, ready: filepath.Join(root, "checkpoint.json"), config: cfg}
		}
		a, e := app.Open(cfg, persistence, adapter)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := open()
	apply := func(command app.Command) app.CommandReceipt {
		t.Helper()
		var last error
		for end := time.Now().Add(4 * time.Second); time.Now().Before(end); {
			r, e := a.Apply(command)
			if e == nil {
				return r
			}
			last = e
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(last)
		return app.CommandReceipt{}
	}
	find := func(snapshot app.Snapshot, id string) app.Session {
		for _, s := range snapshot.Sessions {
			if s.ID == id {
				return s
			}
		}
		t.Fatal("missing session", id)
		return app.Session{}
	}
	inspect := func() app.CapabilityHookInspection {
		t.Helper()
		items, e := a.ReadCapabilities(context.Background())
		if e != nil || len(items) != len(assetList) {
			t.Fatal(e)
		}
		request := app.CapabilityHookInspectionRequest{AssetID: asset.ID, InstanceID: f.Instance.ID}
		for _, item := range items {
			if item.Asset.ID == asset.ID {
				request.Revision, request.SourceSHA256 = item.Revision, item.Source.SHA256
			}
		}
		r, e := a.InspectCapabilityHooks(context.Background(), request)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	inspection := inspect()
	samples["initial-inspection"] = inspection
	samples["initial-capabilities"], _ = a.ReadCapabilities(context.Background())
	ids := map[string]string{}
	for _, row := range []struct{ label, mode, trust string }{{"A", "enabled", "trusted"}, {"B", "enabled", "untrusted"}, {"C", "disabled", "trusted"}} {
		picks := []app.CapabilitySelection{{AssetID: asset.ID, Mode: row.mode, Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: row.trust}}}
		if combined {
			mode := "enabled"
			if row.label == "B" {
				mode = "disabled"
			}
			picks = append(picks, app.CapabilitySelection{AssetID: directoryAsset.ID, Mode: mode, Version: directoryAsset.Version})
		}
		r := apply(app.Command{ID: "create-" + row.label, Kind: "create", Title: "Hook " + row.label, ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", CapabilitySelections: picks})
		ids[row.label] = r.SessionID
		s := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
			v := find(s, r.SessionID)
			return v.Status == "idle" && len(v.View.PluginEvidence) == 1 && v.View.PluginEvidence[0].Verified && (!combined || len(v.View.SkillEvidence) == 1 && v.View.SkillEvidence[0].Verified)
		}), r.SessionID)
		check(row.label+"_bound_version_and_trust", s.ExecutionSnapshot.PluginSelections[0].Hooks.Trust == row.trust && func() bool {
			for _, ref := range s.ExecutionSnapshot.CapabilityRefs {
				if ref.Asset.ID == asset.ID {
					return ref.Asset.Version == "asset-v1"
				}
			}
			return false
		}())
		check(row.label+"_no_hook_on_create", len(s.View.HookExecutions) == 0)
		check(row.label+"_mixed_components_preserved", len(s.Scope.PluginSelections[0].Components) == 3)
		check(row.label+"_mcp_selection_verified", len(s.View.CapabilityEvidence) == 1 && s.View.CapabilityEvidence[0].Verified && s.View.CapabilityEvidence[0].Mode == row.mode)
	}
	initial := a.Snapshot()
	samples["initial"] = initial
	check("same_asset_has_private_native_keys", find(initial, ids["A"]).Scope.PluginSelections[0].NativeSource != find(initial, ids["B"]).Scope.PluginSelections[0].NativeSource)
	turn := func(label, id, version string, invoke bool) {
		t.Helper()
		before := find(a.Snapshot(), id)
		command := app.Command{ID: label, Kind: "send", SessionID: id, Text: "OWNED " + label + "; provider intentionally unavailable"}
		directoryEnabled, pluginEnabled := false, false
		if combined {
			items := []runtime.SkillInput{}
			for _, selected := range before.Scope.SkillSelections {
				if selected.AssetID == directoryAsset.ID && selected.Mode == "enabled" {
					directoryEnabled = true
					items = append(items, runtime.SkillInput{AssetID: selected.AssetID, Name: selected.Name})
				}
			}
			for _, pack := range before.Scope.PluginSelections {
				if pack.Mode == "enabled" {
					pluginEnabled = true
					items = append(items, runtime.SkillInput{AssetID: pack.AssetID, Name: pack.Name + ":method"})
				}
			}
			if len(items) > 0 {
				command.SkillInput = &app.SkillInputRequest{SnapshotID: before.ExecutionSnapshot.ID, Items: items}
			}
		}
		sent := apply(command)
		var s app.Session
		waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			s = find(snapshot, id)
			for _, op := range snapshot.Operations {
				if op.ID == sent.OperationID {
					return f.HasTurnEvent("error", s.Scope.NativeID, op.Receipt.NativeTurn)
				}
			}
			return false
		})
		apply(app.Command{ID: label + "-interrupt", Kind: "interrupt", SessionID: id})
		s = find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool { v := find(snapshot, id); return v.Status == "idle" }), id)
		hooks := []runtime.HookExecution{}
		for _, h := range s.View.HookExecutions {
			if h.Operation == sent.OperationID {
				hooks = append(hooks, h)
			}
		}
		check(label+"_invocation_matches_selection", len(hooks) == map[bool]int{true: 1, false: 0}[invoke])
		body, _ := os.ReadFile(marker)
		matches := 0
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var row struct {
				Version string `json:"version"`
				Input   struct {
					SessionID string `json:"session_id"`
					Prompt    string `json:"prompt"`
				} `json:"input"`
			}
			_ = json.Unmarshal([]byte(line), &row)
			if row.Input.SessionID == s.Scope.NativeID && row.Input.Prompt == "OWNED "+label+"; provider intentionally unavailable" {
				matches++
				check(label+"_exact_marker_version", row.Version == version)
			}
		}
		check(label+"_marker_matches_selection", matches == map[bool]int{true: 1, false: 0}[invoke])
		if invoke && len(hooks) == 1 {
			h := hooks[0]
			check(label+"_timeout_and_original_asset", h.Status == "error" && h.FailureKind == "timeout" && h.AssetID == asset.ID && h.AssetVersion == "asset-v"+version && h.ExecutionSnapshotID == s.ExecutionSnapshot.ID && h.SourceHash == s.Scope.PluginSelections[0].SourceHash)
		}
		if combined {
			report, e := a.ReadSessionSkillContext(context.Background(), id)
			if e != nil {
				t.Fatal(e)
			}
			var frame *runtime.SkillContextFrame
			for n := range report.Frames {
				if report.Frames[n].InputEvidence != nil && report.Frames[n].InputEvidence.OperationID == sent.OperationID {
					frame = &report.Frames[n]
				}
			}
			if command.SkillInput != nil && frame == nil {
				t.Fatal("missing combined input provenance", label)
			}
			if frame == nil && len(report.Frames) > 0 {
				frame = &report.Frames[len(report.Frames)-1]
			}
			if frame == nil {
				t.Fatal("missing combined context", label)
			}
			directoryFound, pluginFound := false, false
			for _, d := range frame.Descriptions {
				directoryFound = directoryFound || d.Name == "native-directory"
				pluginFound = pluginFound || d.Name == "hook-fixture:method"
			}
			check(label+"_directory_description", directoryFound == directoryEnabled)
			check(label+"_plugin_description", pluginFound == pluginEnabled)
			if command.SkillInput != nil {
				exact := frame.InputEvidence != nil && frame.InputEvidence.SnapshotID == before.ExecutionSnapshot.ID && len(frame.InputEvidence.Items) == len(command.SkillInput.Items)
				if frame.InputEvidence != nil {
					for _, item := range frame.InputEvidence.Items {
						expected := ""
						for _, ref := range before.ExecutionSnapshot.CapabilityRefs {
							if ref.Asset.ID == item.AssetID {
								expected = ref.Asset.Version
							}
						}
						exact = exact && item.Status == "loaded_exact" && item.AssetVersion == expected
					}
				}
				check(label+"_all_original_bodies", exact)
			}
			samples[label+"-context"] = report
		}
		samples[label] = a.Snapshot()
	}
	turn("A-call-v1", ids["A"], "1", true)
	turn("B-untrusted", ids["B"], "1", false)
	turn("C-disabled", ids["C"], "1", false)
	change := func(label, id, mode, trust string, inspection app.CapabilityHookInspection) {
		t.Helper()
		before := find(a.Snapshot(), id)
		request := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{{AssetID: asset.ID, Mode: mode, Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: inspection.InspectionID, Trust: trust}}}}
		preview, e := a.PreviewExecutionChange(id, request)
		if e != nil {
			t.Fatal(e)
		}
		samples[label+"-preview"] = preview
		request.PreviewHash = preview.PreviewHash
		apply(app.Command{ID: label + "-save", Kind: "stage_execution", SessionID: id, ExecutionChange: &request})
		staged := find(a.Snapshot(), id)
		check(label+"_save_preserves_current", staged.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
		base := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
		apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &base})
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			v := find(s, id)
			return v.PendingExecutionChange != nil && v.PendingExecutionChange.Baseline != "" && v.PendingExecutionChange.Status == "pending"
		})
		apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &base})
		after := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
			v := find(s, id)
			return v.PendingExecutionChange == nil && v.Status == "idle"
		}), id)
		check(label+"_same_id_and_paused", after.Scope.NativeID == before.Scope.NativeID && after.Paused && after.ExecutionSnapshot.ID != before.ExecutionSnapshot.ID)
		check(label+"_new_trust_bound", after.Scope.PluginSelections[0].Hooks.Trust == trust)
		samples[label+"-applied"] = a.Snapshot()
		apply(app.Command{ID: label + "-resume", Kind: "resume", SessionID: id})
	}
	change("A-revoke", ids["A"], "enabled", "untrusted", inspection)
	turn("A-after-revoke", ids["A"], "1", false)
	asset.Version, asset.Source = "asset-v2", packageAt("2")
	items, _ := a.ReadCapabilities(context.Background())
	preview, err := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: func() string {
		for _, item := range items {
			if item.Asset.ID == asset.ID {
				return item.Revision
			}
		}
		return ""
	}(), Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	req := preview.Request
	req.PreviewHash = preview.PreviewHash
	apply(app.Command{ID: "register-v2", Kind: "manage_capability", CapabilityChange: &req})
	updated := inspect()
	samples["updated-inspection"] = updated
	change("A-upgrade", ids["A"], "enabled", "trusted", updated)
	turn("A-call-v2", ids["A"], "2", true)
	check("B_keeps_original_untrusted_version", find(a.Snapshot(), ids["B"]).Scope.PluginSelections[0].Hooks.Trust == "untrusted" && func() bool {
		for _, ref := range find(a.Snapshot(), ids["B"]).ExecutionSnapshot.CapabilityRefs {
			if ref.Asset.ID == asset.ID {
				return ref.Asset.Version == "asset-v1"
			}
		}
		return false
	}())
	if combined {
		combinedChange := func(label, mode, trust string) {
			before := find(a.Snapshot(), ids["A"])
			request := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{
				{AssetID: directoryAsset.ID, Mode: mode, Version: directoryAsset.Version},
				{AssetID: asset.ID, Mode: "enabled", Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: updated.InspectionID, Trust: trust}},
			}}
			preview, e := a.PreviewExecutionChange(ids["A"], request)
			if e != nil {
				t.Fatal(e)
			}
			samples[label+"-preview"] = preview
			check(label+"_both_diffs", len(preview.Changes) == 2)
			request.PreviewHash = preview.PreviewHash
			apply(app.Command{ID: label + "-save", Kind: "stage_execution", SessionID: ids["A"], ExecutionChange: &request})
			staged := find(a.Snapshot(), ids["A"])
			check(label+"_save_preserves_current", staged.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
			base := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
			apply(app.Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: ids["A"], ExecutionChange: &base})
			waitNativeApplication(t, a, func(s app.Snapshot) bool {
				v := find(s, ids["A"])
				return v.PendingExecutionChange != nil && v.PendingExecutionChange.Status == "pending" && v.PendingExecutionChange.Baseline != ""
			})
			calls := nativeConfigurationCallCount(t, f, before.Scope.NativeID)
			if label == "combined-revoke" {
				applicationLoss.armed.Store(true)
			}
			applied := apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: ids["A"], ExecutionChange: &base})
			after := find(waitNativeApplication(t, a, func(s app.Snapshot) bool {
				v := find(s, ids["A"])
				return v.PendingExecutionChange == nil && v.Status == "idle"
			}), ids["A"])
			check(label+"_same_id_and_candidate", after.Scope.NativeID == before.Scope.NativeID && after.ExecutionSnapshot.ID == staged.PendingExecutionChange.Snapshot.ID && after.Paused)
			check(label+"_one_configuration_apply", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
			if label == "combined-revoke" {
				select {
				case lost := <-applicationLoss.received:
					samples["application-receipt-loss"] = lost
					check("application_loss_same_operation", lost.Intent.ID == applied.OperationID && lost.Receipt.NativeID == before.Scope.NativeID)
				default:
					t.Fatal("application receipt not dropped")
				}
				again := apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: ids["A"], ExecutionChange: &base})
				check("application_loss_retry_keeps_operation", again.OperationID == applied.OperationID && nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
			}
			samples[label+"-applied"] = a.Snapshot()
			apply(app.Command{ID: label + "-resume", Kind: "resume", SessionID: ids["A"]})
		}
		combinedChange("combined-revoke", "disabled", "untrusted")
		turn("A-combined-revoked", ids["A"], "2", false)
		oldBody, e := os.ReadFile(directoryAsset.CodexSkill)
		if e != nil {
			t.Fatal(e)
		}
		write(directoryAsset.CodexSkill, strings.Replace(string(oldBody), "method v1", "method v2", 1))
		directoryAsset.Version = "directory-v2"
		items, e := a.ReadCapabilities(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		revision := ""
		for _, item := range items {
			if item.Asset.ID == directoryAsset.ID {
				revision = item.Revision
			}
		}
		directoryPreview, e := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: "update", AssetID: directoryAsset.ID, BaseRevision: revision, Asset: &directoryAsset})
		if e != nil {
			t.Fatal(e)
		}
		registration := directoryPreview.Request
		registration.PreviewHash = directoryPreview.PreviewHash
		apply(app.Command{ID: "register-directory-v2", Kind: "manage_capability", CapabilityChange: &registration})
		combinedChange("combined-upgrade", "enabled", "trusted")
		turn("A-combined-upgraded", ids["A"], "2", true)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	waitNativeApplication(t, a, func(s app.Snapshot) bool { return find(s, ids["A"]).Status == "idle" })
	turn("A-cold-v2", ids["A"], "2", true)
	check("historical_hook_keeps_original_asset_version", func() bool {
		for _, h := range find(a.Snapshot(), ids["A"]).View.HookExecutions {
			if h.AssetVersion == "asset-v1" {
				return true
			}
		}
		return false
	}())
	if combined {
		before := find(a.Snapshot(), ids["A"])
		review := inspect()
		request := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{
			{AssetID: directoryAsset.ID, Mode: "disabled", Version: directoryAsset.Version},
			{AssetID: asset.ID, Mode: "enabled", Version: asset.Version, HookReview: &app.CapabilityHookReview{InspectionID: review.InspectionID, Trust: "untrusted"}},
		}}
		preview, e := a.PreviewExecutionChange(ids["A"], request)
		if e != nil {
			t.Fatal(e)
		}
		samples["native-loss-preview"] = preview
		request.PreviewHash = preview.PreviewHash
		apply(app.Command{ID: "native-loss-save", Kind: "stage_execution", SessionID: ids["A"], ExecutionChange: &request})
		staged := find(a.Snapshot(), ids["A"])
		candidateID := staged.PendingExecutionChange.Snapshot.ID
		base := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
		apply(app.Command{ID: "native-loss-prepare", Kind: "prepare_execution", SessionID: ids["A"], ExecutionChange: &base})
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			v := find(s, ids["A"])
			return v.PendingExecutionChange != nil && v.PendingExecutionChange.Status == "pending" && v.PendingExecutionChange.Baseline != ""
		})
		calls := nativeConfigurationCallCount(t, f, before.Scope.NativeID)
		nativeLoss := f.DropConfigurationReceipt(before.Scope.NativeID)
		command := app.Command{ID: "native-loss-apply", Kind: "apply_execution", SessionID: ids["A"], ExecutionChange: &base}
		accepted := apply(command)
		select {
		case lost := <-nativeLoss:
			samples["native-receipt-loss"] = lost
			var response struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
			}
			if e := json.Unmarshal(lost.Result, &response); e != nil {
				t.Fatal(e)
			}
			check("native_loss_received_real_original_id", response.Thread.ID == before.Scope.NativeID)
		case <-time.After(15 * time.Second):
			t.Fatal("native configuration receipt was not dropped")
		}
		uncertain := waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			for _, op := range snapshot.Operations {
				if op.ID == accepted.OperationID {
					return op.Status == "uncertain"
				}
			}
			return false
		})
		current := find(uncertain, ids["A"])
		check("native_loss_keeps_old_snapshot_and_candidate", current.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID && current.PendingExecutionChange != nil && current.PendingExecutionChange.Snapshot.ID == candidateID && current.ExecutionHold && current.Paused)
		check("native_loss_applied_once", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
		_, e = a.Apply(app.Command{ID: "native-loss-input", Kind: "send", SessionID: ids["A"], Text: "must not send after unknown configuration"})
		check("native_loss_blocks_input", e != nil)
		duplicate := apply(command)
		check("native_loss_duplicate_keeps_operation", duplicate.OperationID == accepted.OperationID)
		_, e = a.Apply(app.Command{ID: "native-loss-reapply", Kind: "apply_execution", SessionID: ids["A"], ExecutionChange: &base})
		check("native_loss_reapply_rejected", e != nil)
		samples["native-loss-uncertain"] = a.Snapshot()
		if e := a.Close(); e != nil {
			t.Fatal(e)
		}
		f.Restart(t)
		a = open()
		restored := waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			v := find(snapshot, ids["A"])
			return v.View.NativeStatus == "notLoaded" && v.PendingExecutionChange != nil
		})
		current = find(restored, ids["A"])
		check("native_loss_restart_keeps_original_identity", current.Scope.NativeID == before.Scope.NativeID && current.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID && current.PendingExecutionChange.Snapshot.ID == candidateID)
		check("native_loss_restart_remains_paused_unknown", current.ExecutionHold && current.Paused && current.Status == "unknown")
		check("native_loss_restart_does_not_replay", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+1)
		var thread struct {
			Thread struct {
				ID    string            `json:"id"`
				Turns []json.RawMessage `json:"turns"`
			} `json:"thread"`
		}
		if e := f.Call(context.Background(), "thread/read", map[string]any{"threadId": before.Scope.NativeID, "includeTurns": true}, &thread); e != nil {
			t.Fatal(e)
		}
		check("native_loss_preserves_native_history", thread.Thread.ID == before.Scope.NativeID && len(thread.Thread.Turns) == len(before.View.Turns))
		samples["native-loss-restored"] = restored
		if len(recoverOriginal) > 0 {
			request := app.ExecutionRecoveryRequest{BaseSnapshotID: current.ExecutionSnapshot.ID, PendingChangeID: current.PendingExecutionChange.ID, OperationID: accepted.OperationID}
			report, err := a.ReadExecutionRecovery(context.Background(), current.ID, request)
			if err != nil {
				t.Fatalf("original configuration recovery unavailable: %v", err)
			}
			samples["original-recovery-check"] = report
			check("original_recovery_conditions_met", report.Satisfied)
			var lostRecovery <-chan runtime.NativeConfigurationReply
			if recoverOriginal[0] == "application-loss" {
				applicationLoss.recoveryArmed.Store(true)
			}
			if recoverOriginal[0] == "native-loss" {
				lostRecovery = f.DropConfigurationReceipt(before.Scope.NativeID)
			}
			if recoverOriginal[0] == "acknowledged-restart" {
				applicationLoss.holdRecovery.Store(true)
			}
			recovery := apply(app.Command{ID: "explicit-original-recovery", Kind: "recover_execution", SessionID: current.ID, ExecutionRecovery: &request})
			if recoverOriginal[0] == "acknowledged-restart" {
				acknowledged := waitNativeApplication(t, a, func(s app.Snapshot) bool {
					for _, op := range s.Operations {
						if op.ID == recovery.OperationID {
							return op.Status == "acknowledged" && op.Receipt.NativeID == before.Scope.NativeID
						}
					}
					return false
				})
				samples["recovery-acknowledged-before-restart"] = acknowledged
				for _, op := range acknowledged.Operations {
					if op.ID == recovery.OperationID {
						check("recovery_acknowledgement_persisted", op.Receipt.ConfigurationRecovery != nil)
					}
				}
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				f.Restart(t)
				a = open()
				deadline := time.Now().Add(8 * time.Second)
				confirmed := false
				for time.Now().Before(deadline) && !confirmed {
					for _, op := range a.Snapshot().Operations {
						if op.ID == recovery.OperationID && op.Status == "succeeded" {
							confirmed = true
						}
					}
					if !confirmed {
						time.Sleep(25 * time.Millisecond)
					}
				}
				samples["recovery-after-acknowledged-restart"] = a.Snapshot()
				check("acknowledged_recovery_reconciled_after_restart", confirmed)
				if !confirmed {
					t.Fatal("saved recovery acknowledgement was not reconciled after restart")
				}
			}
			if lostRecovery != nil {
				select {
				case lost := <-lostRecovery:
					samples["recovery-native-receipt-loss"] = lost
				case <-time.After(15 * time.Second):
					t.Fatal("recovery native receipt was not dropped")
				}
				uncertain := waitNativeApplication(t, a, func(s app.Snapshot) bool {
					for _, op := range s.Operations {
						if op.ID == recovery.OperationID {
							return op.Status == "uncertain"
						}
					}
					return false
				})
				v := find(uncertain, current.ID)
				check("recovery_loss_keeps_original_candidate", v.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID && v.PendingExecutionChange != nil && v.PendingExecutionChange.Snapshot.ID == candidateID && v.PendingExecutionChange.RecoveryOperationID == recovery.OperationID && v.ExecutionHold && v.Paused && v.Status == "unknown")
				check("recovery_loss_called_once", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+2)
				_, err = a.Apply(app.Command{ID: "no-repeat-recovery", Kind: "recover_execution", SessionID: current.ID, ExecutionRecovery: &request})
				check("recovery_loss_blocks_new_recovery", err != nil)
				samples["original-recovery-unknown"] = uncertain
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				f.Restart(t)
				a = open()
				restarted := waitNativeApplication(t, a, func(s app.Snapshot) bool { return find(s, current.ID).View.NativeStatus == "notLoaded" })
				v = find(restarted, current.ID)
				check("recovery_loss_restart_keeps_unknown", v.Status == "unknown" && v.Paused && v.ExecutionHold && v.PendingExecutionChange.RecoveryOperationID == recovery.OperationID)
				check("recovery_loss_restart_no_replay", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+2)
				samples["original-recovery-restarted"] = restarted
			} else {
				result := waitNativeApplication(t, a, func(s app.Snapshot) bool {
					for _, op := range s.Operations {
						if op.ID == recovery.OperationID {
							return op.Status == "succeeded"
						}
					}
					return false
				})
				actual := find(result, current.ID)
				check("original_recovery_keeps_id_snapshot", actual.Scope.NativeID == before.Scope.NativeID && actual.ExecutionSnapshot.ID == before.ExecutionSnapshot.ID)
				check("original_recovery_paused_and_archived", actual.Paused && !actual.ExecutionHold && actual.PendingExecutionChange == nil && len(actual.ExecutionChangeAttempts) == 1 && actual.ExecutionChangeAttempts[0].Change.Snapshot.ID == candidateID)
				check("original_recovery_once", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+2)
				if recoverOriginal[0] == "application-loss" {
					select {
					case lost := <-applicationLoss.received:
						samples["recovery-application-receipt-loss"] = lost
						check("recovery_application_loss_bound", lost.Intent.ID == recovery.OperationID && lost.Receipt.NativeID == before.Scope.NativeID)
					default:
						t.Fatal("recovery application receipt not dropped")
					}
				}
				again := apply(app.Command{ID: "explicit-original-recovery", Kind: "recover_execution", SessionID: current.ID, ExecutionRecovery: &request})
				check("recovery_command_idempotent", again.OperationID == recovery.OperationID && nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+2)
				if recoverOriginal[0] == "acknowledged-restart" {
					check("recovery_current_resources_still_unverified", actual.View.NativeStatus == "notLoaded" && len(actual.View.PluginEvidence) == 1 && !actual.View.PluginEvidence[0].Verified && len(actual.View.SkillEvidence) == 1 && !actual.View.SkillEvidence[0].Verified)
					time.Sleep(900 * time.Millisecond)
					check("recovery_passive_poll_does_not_load", nativeConfigurationCallCount(t, f, before.Scope.NativeID) == calls+2 && find(a.Snapshot(), current.ID).View.NativeStatus == "notLoaded")
				} else {
					check("recovery_original_resources_verified", len(actual.View.PluginEvidence) == 1 && actual.View.PluginEvidence[0].Verified && len(actual.View.SkillEvidence) == 1 && actual.View.SkillEvidence[0].Verified)
				}
				samples["original-recovery-result"] = result
				if recoverOriginal[0] == "normal" || recoverOriginal[0] == "acknowledged-restart" {
					resumed := apply(app.Command{ID: "recovered-explicit-resume", Kind: "resume", SessionID: current.ID})
					resumeSnapshot := waitNativeApplication(t, a, func(s app.Snapshot) bool {
						for _, op := range s.Operations {
							if op.ID == resumed.OperationID {
								return op.Status == "succeeded"
							}
						}
						return false
					})
					samples["original-recovery-explicit-resume"] = resumeSnapshot
					if recoverOriginal[0] == "acknowledged-restart" {
						v := find(resumeSnapshot, current.ID)
						check("explicit_resume_loads_original_configuration", !v.Paused && v.Status == "idle" && v.Scope.NativeID == before.Scope.NativeID && v.View.PluginEvidence[0].Verified && v.View.SkillEvidence[0].Verified)
						check("explicit_resume_preserves_history", len(v.View.Turns) == len(actual.View.Turns))
					}
					turn("A-recovered-original", current.ID, "2", true)
					apply(app.Command{ID: "recovered-pause", Kind: "pause", SessionID: current.ID})
					samples["original-recovery-following-turn"] = a.Snapshot()
				}
			}
		}
	}
	profileAfter, _ := os.ReadFile(configPath)
	check("original_profile_unchanged", string(profileBefore) == string(profileAfter))
	handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + ids["A"], "/api/sessions/" + ids["B"], "/api/sessions/" + ids["C"]} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		responses[path] = append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	data, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids, "samples": samples}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(data))
}
