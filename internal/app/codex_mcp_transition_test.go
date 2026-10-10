package app

import (
	"context"
	"reflect"
	"testing"

	"codex-netcap/internal/runtime"
)

type mcpTransitionFixture struct {
	*applicationConfigurationFixture
}

func (f *mcpTransitionFixture) ReadCodexMCPDefinitions(_ context.Context, scope runtime.Scope) ([]runtime.MCPSelection, error) {
	return fixtureMCPLaunchBindings(scope)
}

func TestCodexSharedMCPTransitionsPreserveVersionsAcrossLostReceiptAndRestart(t *testing.T) {
	for _, change := range []string{"added", "version", "inherit"} {
		t.Run(change, func(t *testing.T) {
			c, _ := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","args":["./server.py"]}}}`)
			keepConfig, _ := sharedMCPConfig(t, `{"mcpServers":{"keep":{"command":"python3"}}}`)
			keep := keepConfig.CapabilityAssets[0]
			keep.ID = "keep"
			c.CapabilityAssets = append(c.CapabilityAssets, keep)
			f := &mcpTransitionFixture{applicationConfigurationFixture: &applicationConfigurationFixture{fixtureRuntime: newFixture()}}
			a := openFixture(t, c, f)
			picks := []CapabilitySelection{{AssetID: "keep", Mode: "enabled"}}
			if change != "added" {
				picks = append(picks, CapabilitySelection{AssetID: "shared-mcp", Mode: "enabled"})
			}
			created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: picks})
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("last command error: %v", err)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(created.SessionID)
			pick := CapabilitySelection{AssetID: "shared-mcp", Mode: "enabled", Version: "v1"}
			if change == "inherit" {
				pick.Mode, pick.Version = "inherit", ""
			}
			if change == "version" {
				nextConfig, _ := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","args":["./server.py","v2"]}}}`)
				next := nextConfig.CapabilityAssets[0]
				next.Version = "v2"
				current := libraryRead(t, a, pick.AssetID)
				libraryChange(t, a, "library-v2", CapabilityChangeRequest{Action: "update", AssetID: pick.AssetID, BaseRevision: current.Revision, Asset: &next})
				pick.Version = "v2"
			}
			request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{pick}}
			preview, err := a.PreviewExecutionChange(created.SessionID, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Changes) != 1 || preview.Changes[0].AssetID != "shared-mcp" {
				t.Fatal("transition changed an omitted MCP")
			}
			request.PreviewHash = preview.PreviewHash
			if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &request}); err != nil {
				t.Fatal(err)
			}
			staged, _ := a.ReadExecution(created.SessionID)
			if staged.PendingChange == nil || !reflect.DeepEqual(staged.Snapshot, before.Snapshot) {
				t.Fatal("staging promoted an unverified MCP version")
			}
			for _, old := range before.Snapshot.MCPSelections {
				if old.AssetID != "keep" {
					continue
				}
				found := false
				for _, next := range staged.PendingChange.Snapshot.MCPSelections {
					found = found || reflect.DeepEqual(old, next)
				}
				if !found {
					t.Fatal("omitted MCP was rebound")
				}
			}
			ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
			var prepared CommandReceipt
			waitTest(t, func() bool {
				prepared, err = a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &ref})
				return err == nil
			})
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == prepared.OperationID {
						return op.Status == "succeeded"
					}
				}
				return false
			})
			f.durable = func(in runtime.Intent) {
				state, err := a.store.Load()
				if err != nil {
					t.Error(err)
					return
				}
				session := state.Sessions[created.SessionID]
				if in.PreviousScope == nil || !reflect.DeepEqual(*in.PreviousScope, session.Scope) || !reflect.DeepEqual(in.Scope.MCPSelections, staged.PendingChange.Snapshot.MCPSelections) || session.ExecutionSnapshot.ID != before.Snapshot.ID {
					t.Error("MCP native transition preceded the durable original and candidate bindings")
				}
			}
			var applied CommandReceipt
			waitTest(t, func() bool {
				applied, err = a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref})
				return err == nil
			})
			waitTest(t, func() bool { return f.applications.Load() == 1 && a.Snapshot().Sessions[0].Status == "unknown" })
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openFixture(t, c, f)
			restored, _ := a.ReadExecution(created.SessionID)
			if !reflect.DeepEqual(restored.Snapshot, before.Snapshot) || !reflect.DeepEqual(restored.PendingChange.Snapshot, staged.PendingChange.Snapshot) || !restored.Hold {
				t.Fatal("restart guessed a current MCP version")
			}
			f.resolveApply.Store(true)
			waitTest(t, func() bool { read, _ := a.ReadExecution(created.SessionID); return read.PendingChange == nil })
			after, _ := a.ReadExecution(created.SessionID)
			if !reflect.DeepEqual(after.Snapshot, staged.PendingChange.Snapshot) || len(after.History) != 1 || !reflect.DeepEqual(after.History[0], before.Snapshot) || f.applications.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("reconciliation replaced history or repeated an input/change")
			}
			if retry, err := a.Apply(Command{ID: "apply", Kind: "apply_execution", SessionID: created.SessionID, ExecutionChange: &ref}); err != nil || retry != applied {
				t.Fatal("same command did not retain its receipt")
			}
			for _, op := range a.Snapshot().Operations {
				if op.ID == created.OperationID && op.ExecutionSnapshotID != before.Snapshot.ID {
					t.Fatal("old input was reassigned to the new version")
				}
			}
		})
	}
}
