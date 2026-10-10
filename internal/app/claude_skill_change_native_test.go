//go:build linux && nativeclaude

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Reuse the existing native fixture; all actions go through Application's
// caller API and its real SQLite/Runtime/Session path, without HTTP listeners.
func nativeSkillChanges(t *testing.T, a *Application, c Config, sessions map[string]string, check func(string, bool), results map[string]any) {
	t.Helper()
	id := sessions["a"]
	initial, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	var nativeID string
	browserCases := map[string]any{}
	browserState := func() map[string]any {
		t.Helper()
		read, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: read}
	}
	for _, s := range a.Snapshot().Sessions {
		if s.ID == id {
			nativeID = s.Scope.NativeID
		}
	}
	waitOperation := func(operation string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			for _, op := range a.Snapshot().Operations {
				if op.ID != operation {
					continue
				}
				if op.Status == "succeeded" {
					return
				}
				if op.Status == "not_sent" || op.Status == "failed" {
					t.Fatalf("operation %s: %s", op.Intent.Kind, op.Error)
				}
			}
			if time.Now().After(deadline) {
				body, _ := json.Marshal(a.Snapshot())
				t.Fatalf("native operation did not finish: %s", body)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	change := func(label string, pick CapabilitySelection) {
		t.Helper()
		before, err := a.ReadExecution(id)
		if err != nil {
			t.Fatal(err)
		}
		states := map[string]any{"initial": browserState()}
		r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{pick}}
		preview, err := a.PreviewExecutionChange(id, r)
		if err != nil {
			t.Fatal(err)
		}
		r.PreviewHash = preview.PreviewHash
		saved, err := a.Apply(Command{ID: label + "-save", Kind: "stage_execution", SessionID: id, ExecutionChange: &r})
		if err != nil {
			t.Fatal(err)
		}
		staged, err := a.ReadExecution(id)
		if err != nil {
			t.Fatal(err)
		}
		states["staged"] = browserState()
		check(label+"_save_preserves_active", digest(staged.Snapshot) == digest(before.Snapshot))
		check(label+"_skill_diff", len(preview.Changes) == 1 && preview.Changes[0].Kind == "skill")
		ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
		prepared, err := a.Apply(Command{ID: label + "-prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref})
		if err != nil {
			t.Fatal(err)
		}
		waitOperation(prepared.OperationID)
		states["prepared"] = browserState()
		applied, err := a.Apply(Command{ID: label + "-apply", Kind: "apply_execution", SessionID: id, ExecutionChange: &ref})
		if err != nil {
			t.Fatal(err)
		}
		waitOperation(applied.OperationID)
		states["applied"] = browserState()
		after, err := a.ReadExecution(id)
		if err != nil {
			t.Fatal(err)
		}
		check(label+"_exact_candidate_promoted", digest(after.Snapshot) == digest(staged.PendingChange.Snapshot) && after.PendingChange == nil)
		for _, s := range a.Snapshot().Sessions {
			if s.ID == id {
				check(label+"_same_id_and_paused", s.Scope.NativeID == nativeID && s.Paused && !s.ExecutionHold && digest(s.Scope.SkillSelections) == digest(after.Snapshot.SkillSelections))
				check(label+"_history_retained", digest(s.ExecutionHistory[len(s.ExecutionHistory)-1]) == digest(before.Snapshot))
			}
		}
		results[label] = after
		browserCases[label] = map[string]any{"states": states, "preview": preview, "selection": pick, "receipts": map[string]any{"stage_execution": saved, "prepare_execution": prepared, "apply_execution": applied}}
	}
	change("disable", CapabilitySelection{AssetID: "shared", Mode: "disabled"})
	disabled, _ := a.ReadExecution(id)
	check("disabled_verified", len(disabled.SkillEvidence) == 1 && disabled.SkillEvidence[0].Verified && !disabled.SkillEvidence[0].DescriptionListed)
	change("enable", CapabilitySelection{AssetID: "shared", Mode: "enabled"})
	enabled, _ := a.ReadExecution(id)
	check("enabled_verified", len(enabled.SkillEvidence) == 1 && enabled.SkillEvidence[0].Verified && enabled.SkillEvidence[0].DescriptionListed)
	change("inherit", CapabilitySelection{AssetID: "shared", Mode: "inherit"})
	inherited, _ := a.ReadExecution(id)
	check("inherit_releases_selection_and_reference", len(inherited.Snapshot.SkillSelections) == 0 && len(inherited.Snapshot.CapabilityRefs) == 0)
	change("add", CapabilitySelection{AssetID: "shared", Mode: "enabled", Version: "v1"})
	beforeUpdate, _ := a.ReadExecution(id)
	v2 := filepath.Join(filepath.Dir(c.StateDir), "skill-v2")
	if err := os.MkdirAll(v2, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2, "SKILL.md"), []byte("---\nname: display-method-v2\ndescription: explicit version two\n---\nPRIVATE_V2_METHOD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := libraryRead(t, a, "shared")
	updated := entry.Asset
	updated.Version, updated.Source, updated.Management = "v2", v2, "managed"
	libraryChange(t, a, "publish-skill-v2", CapabilityChangeRequest{Action: "update", AssetID: "shared", BaseRevision: entry.Revision, Asset: &updated})
	stillPinned, _ := a.ReadExecution(id)
	check("library_update_does_not_upgrade", digest(beforeUpdate.Snapshot) == digest(stillPinned.Snapshot))
	change("upgrade", CapabilitySelection{AssetID: "shared", Mode: "enabled", Version: "v2"})
	upgraded, _ := a.ReadExecution(id)
	check("explicit_version_and_source", upgraded.Snapshot.CapabilityRefs[0].Asset.Version == "v2" && upgraded.Snapshot.SkillSelections[0].ResourceRoot != beforeUpdate.Snapshot.SkillSelections[0].ResourceRoot && upgraded.SkillEvidence[0].Verified)
	sibling, _ := a.ReadExecution(sessions["b"])
	check("sibling_remains_disabled", sibling.Snapshot.SkillSelections[0].Mode == "disabled" && sibling.SkillEvidence[0].Verified)
	check("sibling_stays_pinned", sibling.Snapshot.CapabilityRefs[0].Asset.Version == "v1")
	current, _ := a.ReadExecution(id)
	check("added_keeps_asset_namespace", current.Snapshot.SkillSelections[0].PluginName == initial.Snapshot.SkillSelections[0].PluginName)
	// Export native caller data for a browser exercising real preview and save.
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot()}
	for _, sid := range sessions {
		read, err := a.ReadSession(sid, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		responses["/api/sessions/"+sid] = read
	}
	data, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions, "cases": browserCases}, "", "  ")
	if err := os.WriteFile(filepath.Join(filepath.Dir(c.StateDir), "change-browser-responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
