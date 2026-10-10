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

func TestCodexNativeDirectorySkillSelection(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0207-native-skill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks, samples := map[string]bool{}, map[string]any{}
	write := func(p, body string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	check := func(name string, v bool) {
		t.Helper()
		checks[name] = v
		if !v {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application/Runtime/Handler; native directory Skill paths and connection-scoped configuration; interrupted unavailable local provider; no successful model/tool/hook or method Gate"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
	})
	sources := map[string]string{"user": filepath.Join(root, "home/.agents/skills/user"), "project": filepath.Join(root, "work/.agents/skills/project"), "retained": filepath.Join(root, "home/.agents/skills/retained")}
	for name, source := range sources {
		guide := " Read [guide](guide.md)."
		if name == "project" {
			guide = ""
		}
		write(filepath.Join(source, "SKILL.md"), fmt.Sprintf("---\nname: native-%s\ndescription: OWNED_%s_DESCRIPTION\n---\nUse only this owned method.%s\n", name, name, guide))
		write(filepath.Join(source, "guide.md"), "Owned relative guide.")
	}
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	config, _ := os.ReadFile(configPath)
	write(configPath, string(config)+fmt.Sprintf("\n[[skills.config]]\npath=%q\nenabled=false\n", filepath.Join(sources["retained"], "SKILL.md")))
	originalProfile, _ := os.ReadFile(configPath)
	f.Restart(t)
	assets := []app.CapabilityAsset{}
	for _, name := range []string{"user", "project"} {
		assets = append(assets, app.CapabilityAsset{ID: name, Name: "Native " + name, Kind: "skill", Version: "v1", Source: sources[name], Backends: []string{"codex"}, CodexSkill: filepath.Join(sources[name], "SKILL.md")})
	}
	assets[1].Source = assets[1].CodexSkill
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: append([]app.CapabilityAsset(nil), assets...)}
	open := func() *app.Application {
		store, e := app.OpenStore(cfg.StateDir)
		if e != nil {
			t.Fatal(e)
		}
		a, e := app.Open(cfg, store, f.Runtime)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := open()
	apply := func(cmd app.Command) app.CommandReceipt {
		t.Helper()
		var last error
		for end := time.Now().Add(4 * time.Second); time.Now().Before(end); {
			r, e := a.Apply(cmd)
			if e == nil {
				return r
			}
			last = e
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(last)
		return app.CommandReceipt{}
	}
	find := func(s app.Snapshot, id string) app.Session {
		for _, v := range s.Sessions {
			if v.ID == id {
				return v
			}
		}
		t.Fatal("missing session", id)
		return app.Session{}
	}
	ready := func(id string) app.Session {
		return find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			v := find(snapshot, id)
			if v.Status != "idle" || len(v.View.SkillEvidence) != len(v.Scope.SkillSelections) {
				return false
			}
			for _, row := range v.View.SkillEvidence {
				if !row.Verified {
					return false
				}
			}
			return true
		}), id)
	}
	ids := map[string]string{}
	for _, row := range []struct{ label, user, project string }{{"A", "enabled", "disabled"}, {"B", "disabled", "enabled"}, {"C", "disabled", "enabled"}} {
		r := apply(app.Command{ID: "create-" + row.label, Kind: "create", Title: "Directory " + row.label, ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only", CapabilitySelections: []app.CapabilitySelection{{AssetID: "user", Mode: row.user}, {AssetID: "project", Mode: row.project}}})
		ids[row.label] = r.SessionID
		s := ready(r.SessionID)
		check(row.label+"_native_bindings", len(s.Scope.PluginSelections) == 0 && len(s.Scope.SkillSelections) == 2)
		check(row.label+"_source_and_configuration_verified", len(s.View.SkillEvidence) == 2 && s.View.SkillEvidence[0].Verified && s.View.SkillEvidence[1].Verified)
	}
	samples["initial"] = a.Snapshot()
	samples["initial-capabilities"], _ = a.ReadCapabilities(context.Background())
	turn := func(label, id string, userEnabled, projectEnabled bool, explicit string) {
		t.Helper()
		s := ready(id)
		cmd := app.Command{ID: label, Kind: "send", SessionID: id, Text: "OWNED " + label + "; provider intentionally unavailable"}
		if explicit != "" {
			cmd.SkillInput = &app.SkillInputRequest{SnapshotID: s.ExecutionSnapshot.ID, Items: []runtime.SkillInput{{AssetID: explicit, Name: "native-" + explicit}}}
		}
		sent := apply(cmd)
		nativeTurn := ""
		waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			for _, op := range snapshot.Operations {
				if op.ID == sent.OperationID {
					nativeTurn = op.Receipt.NativeTurn
				}
			}
			point, e := f.Checkpoint()
			if e != nil {
				return false
			}
			var records []struct {
				Events []struct {
					Method string `json:"method"`
					Params struct {
						ThreadID string `json:"threadId"`
						TurnID   string `json:"turnId"`
					} `json:"params"`
				} `json:"events"`
			}
			_ = json.Unmarshal(point.Trace, &records)
			for _, record := range records {
				for _, event := range record.Events {
					if event.Method == "error" && event.Params.ThreadID == s.Scope.NativeID && nativeTurn != "" && event.Params.TurnID == nativeTurn {
						return true
					}
				}
			}
			return false
		})
		apply(app.Command{ID: label + "-interrupt", Kind: "interrupt", SessionID: id})
		ready(id)
		report, e := a.ReadSessionSkillContext(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		var frame *runtime.SkillContextFrame
		for n := range report.Frames {
			if report.Frames[n].TurnID == nativeTurn {
				frame = &report.Frames[n]
			}
		}
		if frame == nil {
			t.Fatal("missing native context", label)
		}
		found := map[string]bool{}
		for _, d := range frame.Descriptions {
			found[d.Name] = true
		}
		check(label+"_user_description", found["native-user"] == userEnabled)
		check(label+"_project_description", found["native-project"] == projectEnabled)
		check(label+"_retained_disabled", !found["native-retained"])
		if explicit != "" {
			check(label+"_original_body_and_version", frame.InputEvidence != nil && frame.InputEvidence.OperationID == sent.OperationID && frame.InputEvidence.SnapshotID == s.ExecutionSnapshot.ID && len(frame.InputEvidence.Items) == 1 && frame.InputEvidence.Items[0].Status == "loaded_exact" && frame.InputEvidence.Items[0].Path == filepath.Join(sources[explicit], "SKILL.md") && frame.InputEvidence.Items[0].AssetVersion == func() string {
				for _, asset := range assets {
					if asset.ID == explicit {
						return asset.Version
					}
				}
				return ""
			}())
		}
		samples[label] = report
	}
	turn("A-initial", ids["A"], true, false, "user")
	turn("B-initial", ids["B"], false, true, "project")
	turn("C-initial", ids["C"], false, true, "project")
	s := ready(ids["B"])
	_, err = a.Apply(app.Command{ID: "disabled-input", Kind: "send", SessionID: s.ID, Text: "never sent", SkillInput: &app.SkillInputRequest{SnapshotID: s.ExecutionSnapshot.ID, Items: []runtime.SkillInput{{AssetID: "user", Name: "native-user"}}}})
	check("disabled_explicit_input_rejected", err != nil)
	change := func(label, id, assetID, mode string) {
		t.Helper()
		before := find(a.Snapshot(), id)
		pick := app.CapabilitySelection{AssetID: assetID, Mode: mode}
		if mode != "inherit" {
			for _, asset := range assets {
				if asset.ID == assetID {
					pick.Version = asset.Version
				}
			}
		}
		request := app.ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, Selections: []app.CapabilitySelection{pick}}
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
		waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			v := find(snapshot, id)
			return v.PendingExecutionChange != nil && v.PendingExecutionChange.Status == "pending" && v.PendingExecutionChange.Baseline != ""
		})
		apply(app.Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &base})
		after := find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			v := find(snapshot, id)
			return v.Status == "idle" && v.PendingExecutionChange == nil
		}), id)
		check(label+"_same_id_and_history", after.Scope.NativeID == before.Scope.NativeID && after.ExecutionSnapshot.PreviousID == before.ExecutionSnapshot.ID && after.Paused)
		samples[label+"-applied"] = a.Snapshot()
		apply(app.Command{ID: label + "-resume", Kind: "resume", SessionID: id})
	}
	change("A-disable", ids["A"], "user", "disabled")
	turn("A-disabled", ids["A"], false, false, "")
	change("A-enable", ids["A"], "user", "enabled")
	turn("A-enabled", ids["A"], true, false, "user")
	change("B-inherit", ids["B"], "user", "inherit")
	turn("B-inherited", ids["B"], true, true, "project")
	check("inherit_releases_reference", len(ready(ids["B"]).Scope.SkillSelections) == 1 && len(ready(ids["B"]).ExecutionSnapshot.CapabilityRefs) == 1)
	originalA := ready(ids["A"]).Scope.NativeID
	originalB := ready(ids["B"]).Scope.NativeID
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	ready(ids["A"])
	ready(ids["B"])
	check("cold_keeps_original_ids", ready(ids["A"]).Scope.NativeID == originalA && ready(ids["B"]).Scope.NativeID == originalB)
	turn("A-cold", ids["A"], true, false, "user")
	turn("B-cold", ids["B"], true, true, "project")
	change("B-last-inherit", ids["B"], "project", "inherit")
	turn("B-all-inherited", ids["B"], true, true, "")
	check("last_inherit_releases_all_bindings", len(ready(ids["B"]).Scope.SkillSelections) == 0 && len(ready(ids["B"]).ExecutionSnapshot.CapabilityRefs) == 0)
	ready(ids["A"])
	beforeUpgrade := ready(ids["A"])
	userEntry := filepath.Join(sources["user"], "SKILL.md")
	oldBody, _ := os.ReadFile(userEntry)
	write(userEntry, strings.Replace(string(oldBody), "Use only this owned method.", "Use only version two of this owned method.", 1))
	assets[0].Version = "v2"
	items, _ := a.ReadCapabilities(context.Background())
	var revision string
	for _, item := range items {
		if item.Asset.ID == "user" {
			revision = item.Revision
		}
	}
	upgrade, e := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: "update", AssetID: "user", BaseRevision: revision, Asset: &assets[0]})
	if e != nil {
		t.Fatal(e)
	}
	registration := upgrade.Request
	registration.PreviewHash = upgrade.PreviewHash
	apply(app.Command{ID: "register-user-v2", Kind: "manage_capability", CapabilityChange: &registration})
	check("library_update_preserves_existing_snapshot", find(a.Snapshot(), ids["A"]).ExecutionSnapshot.ID == beforeUpgrade.ExecutionSnapshot.ID)
	change("A-upgrade", ids["A"], "user", "enabled")
	turn("A-version-two", ids["A"], true, false, "user")
	stale := find(waitNativeApplication(t, a, func(snapshot app.Snapshot) bool { return find(snapshot, ids["C"]).Status == "unknown" }), ids["C"])
	oldVersion := ""
	for _, ref := range stale.ExecutionSnapshot.CapabilityRefs {
		if ref.Asset.ID == "user" {
			oldVersion = ref.Asset.Version
		}
	}
	_, e = a.Apply(app.Command{ID: "stale-C-input", Kind: "send", SessionID: ids["C"], Text: "must not send from changed original resource"})
	check("sibling_keeps_old_version_and_blocks_drift", oldVersion == "v1" && e != nil)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	ready(ids["A"])
	turn("A-version-two-cold", ids["A"], true, false, "user")
	history, e := a.ReadSessionSkillContext(context.Background(), ids["A"])
	if e != nil {
		t.Fatal(e)
	}
	oldInput := false
	for _, frame := range history.Frames {
		if frame.InputEvidence != nil {
			for _, item := range frame.InputEvidence.Items {
				oldInput = oldInput || item.AssetID == "user" && item.AssetVersion == "v1" && item.Status == "loaded_exact"
			}
		}
	}
	check("historical_body_keeps_original_version", oldInput)
	samples["final-history"] = history
	afterProfile, _ := os.ReadFile(configPath)
	check("profile_unchanged", string(afterProfile) == string(originalProfile))
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
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": ids, "samples": samples}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(b))
}
