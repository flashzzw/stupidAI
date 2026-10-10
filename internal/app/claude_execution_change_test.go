package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClaudeExecutionChangeUpdatesAllAssetServersAndChecksSources(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"one":{"command":"python3"},"two":{"command":"python3"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared", Name: "Shared", Kind: "mcp", Version: "v1", Source: source, Backends: []string{"claude"}}}
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil || len(preview.Changes) != 2 || preview.Boundary != claudeMCPChangeBoundary {
		t.Fatalf("asset-wide preview: %+v %v", preview, err)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(created.SessionID)
	if !reflect.DeepEqual(staged.Snapshot, before.Snapshot) {
		t.Fatal("saving changed current snapshot")
	}
	for _, s := range staged.PendingChange.Snapshot.MCPSelections {
		if s.Mode != "disabled" {
			t.Fatal("only first server changed")
		}
	}
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	prep, err := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &ref})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == prep.OperationID {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"changed":{"command":"python3"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(Command{ID: "apply-drifted", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref}); err == nil {
		t.Fatal("source drift accepted for application")
	}
	if f.applications.Load() != 0 {
		t.Fatal("source drift reached native apply")
	}
}
