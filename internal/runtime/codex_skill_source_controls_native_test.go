//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeSkillSourceControls(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0207-source-controls-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	checks, inventories := map[string]bool{}, map[string]app.SkillDiscovery{}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "inventories": inventories, "processes": f.Records, "passed": !t.Failed(), "scope": "native Codex 0.158.0 stdio, production Application/SQLite/Handler discovery; no model/turn, method Gate, managed or plugin source control, proxy/peer or Windows"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
	})
	entry := filepath.Join(root, "home/.agents/skills/method/SKILL.md")
	project := filepath.Join(root, "work/.agents/skills/project/SKILL.md")
	doc := func(name string) string {
		return fmt.Sprintf("---\nname: %s\ndescription: Owned discovery method\n---\nBODY_NOT_DISCOVERY_DATA\n", name)
	}
	write(entry, doc("method"))
	write(project, doc("project-method"))
	asset := app.CapabilityAsset{ID: "method", Name: "Owned method", Kind: "skill", Version: "v1", Source: filepath.Dir(entry), Backends: []string{"codex"}, CodexSkill: entry}
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	originalProfile, _ := os.ReadFile(configPath)
	stateDir := filepath.Join(root, "application")
	store, err := app.OpenStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(app.Config{Version: 1, StateDir: stateDir, Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}}, CapabilityAssets: []app.CapabilityAsset{asset}}, store, f.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ids := []string{}
	for index, label := range []string{"Selected method", "Inherited methods"} {
		cmd := app.Command{ID: fmt.Sprintf("create-%d", index), Kind: "create", Title: label, ProjectID: "project", RouteID: f.Route.ID, Permission: "read_only"}
		if len(ids) == 0 {
			cmd.CapabilitySelections = []app.CapabilitySelection{{AssetID: asset.ID, Mode: "disabled"}}
		}
		r, e := a.Apply(cmd)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, r.SessionID)
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, session := range s.Sessions {
				if session.ID == r.SessionID {
					return session.Status == "idle"
				}
			}
			return false
		})
		if _, e = a.Apply(app.Command{ID: fmt.Sprintf("pause-%d", index), Kind: "takeover", SessionID: r.SessionID}); e != nil {
			t.Fatal(e)
		}
	}
	handler := console.Handler(a, "127.0.0.1:9467", strings.Repeat("c", 64), os.DirFS("."))
	request := func(method, path string) json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:9467"+path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:9467")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("Handler %s: %d %s", path, w.Code, w.Body.String())
		}
		return append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	read := func(phase string) app.SkillDiscovery {
		t.Helper()
		var out app.SkillDiscovery
		if err := json.Unmarshal(request("POST", "/api/sessions/"+ids[0]+"/skills/discover"), &out); err != nil {
			t.Fatal(err)
		}
		inventories[phase] = out
		return out
	}
	control := func(out app.SkillDiscovery, path string) string {
		for _, row := range out.Controls {
			if row.Path == path {
				return row.Control
			}
		}
		return "absent"
	}
	before := a.Snapshot()
	initial := read("initial")
	check("native_user_and_repo_control_hints", initial.Complete && control(initial, entry) == "native_directory" && control(initial, project) == "native_directory")
	check("discovery_does_not_change_application_state", reflect.DeepEqual(before, a.Snapshot()))
	check("saved_disabled_keeps_version_and_snapshot", len(initial.SavedSelections) == 1 && initial.SavedSelections[0].Mode == "disabled" && initial.SavedSelections[0].Version == "v1" && initial.SavedSelections[0].Discovered && initial.SnapshotID != "")
	directoryEnabled := false
	for _, row := range initial.Skills {
		if row.Path == entry {
			directoryEnabled = row.Enabled
		}
	}
	check("native_directory_enabled_is_not_saved_selection", directoryEnabled)
	items, err := a.ReadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	asset.Version = "v2"
	preview, err := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: items[0].Revision, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	change := preview.Request
	change.PreviewHash = preview.PreviewHash
	if _, err = a.Apply(app.Command{ID: "register-v2", Kind: "manage_capability", CapabilityChange: &change}); err != nil {
		t.Fatal(err)
	}
	updated := read("updated-library")
	check("library_v2_does_not_relabel_saved_v1", updated.SnapshotID == initial.SnapshotID && updated.SavedSelections[0].Version == "v1")
	write(project, doc("method"))
	ambiguous := read("ambiguous")
	check("native_duplicate_names_block_independent_hint", control(ambiguous, entry) == "ambiguous" && control(ambiguous, project) == "ambiguous")
	write(project, doc("project-method"))
	alias := filepath.Join(root, "work/.agents/skills/alias/SKILL.md")
	write(alias, "temporary")
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(entry, alias); err != nil {
		t.Fatal(err)
	}
	aliased := read("aliased")
	check("native_file_alias_not_independently_selectable", control(aliased, entry) == "ambiguous" && control(aliased, alias) == "ambiguous")
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "work/.agents/skills/broken/SKILL.md")
	write(broken, "---\nname: broken\n---\nBROKEN_BODY_NOT_DISCOVERY_DATA\n")
	partial := read("partial")
	check("native_partial_inventory_keeps_controls_unknown", !partial.Complete && control(partial, entry) == "unknown" && control(partial, project) == "unknown")
	check("partial_keeps_saved_binding", len(partial.SavedSelections) == 1 && partial.SavedSelections[0].Version == "v1")
	write(broken, doc("repaired-method"))
	repaired := read("repaired")
	check("native_repair_refreshes_control_hints", repaired.Complete && control(repaired, entry) == "native_directory")
	if err = os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	missing := read("missing")
	check("removed_source_keeps_missing_saved_selection", missing.Complete && control(missing, entry) == "absent" && len(missing.SavedSelections) == 1 && !missing.SavedSelections[0].Discovered && missing.SavedSelections[0].Version == "v1")
	_, err = a.Apply(app.Command{ID: "drifted-send", Kind: "send", SessionID: ids[0], Text: "must not reach native model"})
	check("discovery_does_not_authorize_drifted_input", err != nil)
	responses := map[string]json.RawMessage{}
	for _, path := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + ids[0], "/api/sessions/" + ids[1]} {
		responses[path] = request("GET", path)
	}
	b, _ := json.MarshalIndent(map[string]any{"responses": responses, "inventories": inventories, "sessions": ids}, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(b))
	check("no_body_in_discovery", !strings.Contains(string(b), "BODY_NOT_DISCOVERY_DATA"))
	afterProfile, _ := os.ReadFile(configPath)
	check("profile_unchanged", string(afterProfile) == string(originalProfile))
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	check("no_model_or_native_config_writes", func() bool {
		for _, record := range f.Records {
			for _, call := range record.Calls {
				method, _ := call["method"].(string)
				if method == "turn/start" || strings.Contains(method, "config/write") {
					return false
				}
			}
		}
		return true
	}())
}
