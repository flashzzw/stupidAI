//go:build linux && nativeclaude

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeSkillCatalog(t *testing.T, a *Application, c Config, sessions map[string]string, check func(string, bool), results map[string]any) {
	t.Helper()
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot()}
	for _, label := range []string{"a", "b"} {
		id := sessions[label]
		before, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := a.ReadSessionSkillCatalog(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		after, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		check(label+"_original_identity", catalog.NativeID == before.Session.Scope.NativeID && catalog.Cwd == before.Session.Scope.Cwd)
		check(label+"_coverage_explicit", !catalog.Complete && len(catalog.Limitations) == 3)
		directoryUser, directoryProject := false, false
		for _, entry := range catalog.Directories.Entries {
			if entry.Name == "user-method" {
				directoryUser = entry.Path == filepath.Join(before.Session.Scope.Instance.Profile, "skills/user-method/SKILL.md") && entry.Source == "userSettings" && entry.NativeKey == "user-method"
			}
			if entry.Name == "inherited-method" {
				directoryProject = entry.Path == filepath.Join(before.Session.Scope.Cwd, ".claude/skills/inherited/SKILL.md") && entry.Source == "projectSettings" && entry.NativeKey == "inherited"
			}
		}
		check(label+"_bound_directory_paths", directoryUser && directoryProject)
		check(label+"_directory_source_separate", catalog.Directories.Source == "claude.bound_directories" && catalog.Directories.Complete && len(catalog.Directories.Entries) == 2)
		check(label+"_bindings_and_ownership_unchanged", digest(before.Session.Scope) == digest(after.Session.Scope) && digest(before.Session.ExecutionSnapshot) == digest(after.Session.ExecutionSnapshot) && before.Session.Generation == after.Session.Generation && before.Session.ControlMode == after.Session.ControlMode && len(before.Operations) == len(after.Operations))
		user, project, bound, builtin := false, false, false, false
		for _, item := range catalog.Items {
			if item.Name == "user-method" {
				user = item.Source == "userSettings" && item.Control == "requires_binding" && item.BoundPath == ""
			}
			if item.Name == "inherited-method" {
				project = item.Source == "projectSettings" && item.Control == "requires_binding" && item.BoundPath == ""
			}
			if item.AssetID == "shared" {
				bound = item.Control == "asset_bound" && item.Source == "plugin" && item.BoundPath == before.Session.Scope.SkillSelections[0].DiscoveryPath
			}
			if item.Name == "claude-api" {
				builtin = item.Source == "built-in" && item.Control == "unavailable" && !item.Ambiguous
			}
		}
		check(label+"_inherited_sources_visible", user && project)
		check(label+"_native_builtin_identified", builtin)
		check(label+"_selected_binding_only_when_reported", bound == (label == "a"))
		encoded, _ := json.Marshal(catalog)
		check(label+"_no_method_body", !strings.Contains(string(encoded), "PRIVATE_"))
		results[label+"_catalog"] = catalog
		responses["/api/sessions/"+id] = after
		responses["/api/sessions/"+id+"/skills/catalog"] = catalog
	}
	// Disk failures remain independent of the current native query's catalog.
	// These are owned, unselected test entries; no query reload is requested.
	aID := sessions["a"]
	before, err := a.ReadSession(aID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	userRoot := filepath.Join(before.Session.Scope.Instance.Profile, "skills")
	broken := filepath.Join(userRoot, "malformed")
	if err = os.MkdirAll(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(broken, "SKILL.md"), []byte("---\nname: [PRIVATE_PARSE_INPUT\n---\nPRIVATE_BODY"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(userRoot, "alias")
	if err = os.Symlink(filepath.Join(userRoot, "user-method"), alias); err != nil {
		t.Fatal(err)
	}
	partial, err := a.ReadSessionSkillCatalog(context.Background(), aID)
	if err != nil {
		t.Fatal(err)
	}
	check("directory_errors_do_not_discard_native_items", !partial.Directories.Complete && len(partial.Directories.Errors) > 0 && len(partial.Items) > 0)
	encoded, _ := json.Marshal(partial)
	check("directory_errors_omit_body_and_parse_input", !strings.Contains(string(encoded), "PRIVATE_"))
	responses["directory-partial"] = partial
	if err = os.Remove(filepath.Join(broken, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := a.ReadSessionSkillCatalog(context.Background(), aID)
	if err != nil {
		t.Fatal(err)
	}
	aliases := 0
	for _, entry := range ambiguous.Directories.Entries {
		if entry.Name == "user-method" && entry.Control == "ambiguous" {
			aliases++
		}
	}
	check("directory_aliases_are_not_independently_selectable", ambiguous.Directories.Complete && aliases == 2)
	responses["directory-ambiguous"] = ambiguous
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadSession(aID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	check("directory_refresh_does_not_change_binding_or_operations", digest(before.Session.Scope) == digest(after.Session.Scope) && digest(before.Session.ExecutionSnapshot) == digest(after.Session.ExecutionSnapshot) && len(before.Operations) == len(after.Operations))
	// Full native responses are replayed by the built UI; no listener is added.
	data, err := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(c.StateDir), "catalog-browser-responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
