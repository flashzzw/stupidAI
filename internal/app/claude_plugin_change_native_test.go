//go:build linux && nativeclaude

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

// Reuse the existing native fixture; all actions go through Application's
// caller API and its real SQLite/Runtime/Session path, without HTTP listeners.
func TestClaudeNativeApplicationPluginChanges(t *testing.T) {
	claudeNativeApplicationPlugins(t, false, true)
}

func nativePluginChanges(t *testing.T, a *Application, c Config, sessions map[string]string, check func(string, bool), results map[string]any) {
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
		check(label+"_plugin_diff", len(preview.Changes) == 1 && preview.Changes[0].Kind == "cli_plugin")
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
				check(label+"_same_id_and_paused", s.Scope.NativeID == nativeID && s.Paused && !s.ExecutionHold && digest(s.Scope.PluginSelections) == digest(after.Snapshot.PluginSelections))
				check(label+"_history_retained", digest(s.ExecutionHistory[len(s.ExecutionHistory)-1]) == digest(before.Snapshot))
			}
		}
		results[label] = after
		browserCases[label] = map[string]any{"states": states, "preview": preview, "selection": pick, "receipts": map[string]any{"stage_execution": saved, "prepare_execution": prepared, "apply_execution": applied}}
	}
	change("disable", CapabilitySelection{AssetID: "plugin", Mode: "disabled"})
	disabled, _ := a.ReadExecution(id)
	check("disabled_verified", len(disabled.PluginEvidence) == 1 && disabled.PluginEvidence[0].Verified && disabled.PluginEvidence[0].Mode == "disabled")
	change("enable", CapabilitySelection{AssetID: "plugin", Mode: "enabled"})
	enabled, _ := a.ReadExecution(id)
	check("enabled_verified", len(enabled.PluginEvidence) == 1 && enabled.PluginEvidence[0].Verified && enabled.PluginEvidence[0].Mode == "enabled")
	change("inherit", CapabilitySelection{AssetID: "plugin", Mode: "inherit"})
	inherited, _ := a.ReadExecution(id)
	check("inherit_releases_selection_and_reference", len(inherited.Snapshot.PluginSelections) == 0 && len(inherited.Snapshot.CapabilityRefs) == 0)
	change("add", CapabilitySelection{AssetID: "plugin", Mode: "enabled", Version: "asset-v1"})
	beforeUpdate, _ := a.ReadExecution(id)
	v2 := filepath.Join(filepath.Dir(c.StateDir), "plugin-v2")
	if err := os.MkdirAll(v2, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range initial.Snapshot.PluginSelections[0].Files {
		path := filepath.Join(v2, file.Path)
		if file.Kind == "directory" {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		body, err := os.ReadFile(filepath.Join(initial.Snapshot.PluginSelections[0].SourcePath, file.Path))
		if err != nil {
			t.Fatal(err)
		}
		if file.Path == ".claude-plugin/plugin.json" {
			body = []byte(`{"name":"fixture-plugin","version":"2.0.0"}`)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := libraryRead(t, a, "plugin")
	updated := entry.Asset
	updated.Version, updated.Source, updated.Management = "asset-v2", v2, "managed"
	libraryChange(t, a, "publish-plugin-v2", CapabilityChangeRequest{Action: "update", AssetID: "plugin", BaseRevision: entry.Revision, Asset: &updated})
	stillPinned, _ := a.ReadExecution(id)
	check("library_update_does_not_upgrade", digest(beforeUpdate.Snapshot) == digest(stillPinned.Snapshot))
	change("upgrade", CapabilitySelection{AssetID: "plugin", Mode: "enabled", Version: "asset-v2"})
	upgraded, _ := a.ReadExecution(id)
	check("explicit_version_and_source", upgraded.Snapshot.CapabilityRefs[0].Asset.Version == "asset-v2" && upgraded.Snapshot.PluginSelections[0].SourcePath != beforeUpdate.Snapshot.PluginSelections[0].SourcePath && upgraded.PluginEvidence[0].Verified)
	sibling, _ := a.ReadExecution(sessions["b"])
	check("sibling_remains_disabled", sibling.Snapshot.PluginSelections[0].Mode == "disabled" && sibling.PluginEvidence[0].Verified)
	check("sibling_stays_pinned", sibling.Snapshot.CapabilityRefs[0].Asset.Version == "asset-v1")
	current, _ := a.ReadExecution(id)
	check("upgrade_keeps_plugin_name", current.Snapshot.PluginSelections[0].Name == initial.Snapshot.PluginSelections[0].Name)
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
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = Open(c, store, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	restored, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	check("restart_keeps_snapshot_and_version_history", digest(restored.Snapshot) == digest(current.Snapshot) && digest(restored.History) == digest(current.History))
	resumed, err := a.Apply(Command{ID: "resume-upgraded", Kind: "resume", SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	waitOperation(resumed.OperationID)
	for _, s := range a.Snapshot().Sessions {
		if s.ID == id {
			check("restart_keeps_identity_without_claiming_live_plugin", s.Scope.NativeID == nativeID && len(s.View.PluginEvidence) == 1 && !s.View.PluginEvidence[0].Verified && s.Scope.PluginSelections[0].Version == "2.0.0")
		}
	}
	change("cold-disable", CapabilitySelection{AssetID: "plugin", Mode: "disabled"})
	cold, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	check("cold_apply_reopens_and_verifies_target", len(cold.PluginEvidence) == 1 && cold.PluginEvidence[0].Verified && cold.PluginEvidence[0].Mode == "disabled")
	results["restored"], _ = a.ReadExecution(id)
}
