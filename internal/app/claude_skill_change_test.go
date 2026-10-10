package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeSkillChangeStagesWithoutChangingEffectiveBinding(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Before != "enabled" || preview.Changes[0].After != "disabled" {
		t.Fatalf("wrong Skill diff: %+v", preview)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(created.SessionID)
	if digest(staged.Snapshot) != digest(before.Snapshot) || staged.PendingChange.Snapshot.SkillSelections[0].Mode != "disabled" || staged.PendingChange.Snapshot.SkillSelections[0].NativeKey != "native-key" {
		t.Fatal("stage lost native key or changed effective configuration")
	}
	if f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("staging dispatched native work")
	}
}

func TestClaudeSkillChangeLostReceiptAndRestartKeepMixedBindings(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	mcp := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"identity":{"command":"fixture"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: "mcp", Name: "MCP", Kind: "mcp", Version: "v1", Source: mcp, Backends: []string{"claude"}})
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture()}
	f.resolveApply.Store(true)
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "mixed-create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}, {AssetID: "mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	_, err = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r})
	if err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(created.SessionID)
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	for _, kind := range []string{"prepare_execution", "apply_execution"} {
		var receipt CommandReceipt
		waitTest(t, func() bool {
			receipt, err = a.Apply(Command{ID: kind, Kind: kind, SessionID: created.SessionID, ExecutionChange: &ref})
			return err == nil
		})
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID {
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}
	after, _ := a.ReadExecution(created.SessionID)
	if after.PendingChange != nil || digest(after.Snapshot) != digest(staged.PendingChange.Snapshot) || digest(after.History[0]) != digest(before.Snapshot) || digest(after.Snapshot.MCPSelections) != digest(before.Snapshot.MCPSelections) {
		t.Fatal("lost receipt lost pinned mixed bindings or version history")
	}
	if f.applications.Load() != 1 {
		t.Fatal("lost reply replayed configuration")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	restored, _ := a.ReadExecution(created.SessionID)
	if digest(restored.Snapshot) != digest(after.Snapshot) || restored.Snapshot.SkillSelections[0].Mode != "disabled" || digest(restored.History) != digest(after.History) {
		t.Fatal("restart lost effective Skill choice")
	}
}
