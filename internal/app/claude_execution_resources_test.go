package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeResourceCandidateKeepsSixteenAssetLimitForPartialReplacement(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	initial, changes := []CapabilitySelection{}, []CapabilitySelection{}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("asset%d", i)
		path := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"mcpServers":{"server%d":{"command":"python3"}}}`, i)), 0600); err != nil {
			t.Fatal(err)
		}
		c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: id, Name: id, Kind: "mcp", Version: "v1", Source: path, Backends: []string{"claude"}})
		if i < 16 {
			initial = append(initial, CapabilitySelection{AssetID: id, Mode: "enabled"})
		}
		if i < 4 {
			changes = append(changes, CapabilitySelection{AssetID: id, Mode: "inherit"})
		}
		if i >= 16 {
			changes = append(changes, CapabilitySelection{AssetID: id, Mode: "enabled"})
		}
	}
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: initial})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: changes}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.ReadExecution(created.SessionID)
	if len(saved.PendingChange.Snapshot.MCPSelections) != 16 {
		t.Fatal("replacement exceeded or reduced the requested sixteen services")
	}
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: saved.PendingChange.ID}
	if _, err := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: created.SessionID, ExecutionChange: &ref}); err != nil {
		t.Fatal("valid eight-asset patch could not prepare:", err)
	}
}

func TestClaudeResourceCandidateAddsPinsUpgradesAndReleases(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	asset := func(id, version, servers string) CapabilityAsset {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(path, []byte(`{"mcpServers":`+servers+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		return CapabilityAsset{ID: id, Name: id, Kind: "mcp", Version: version, Source: path, Backends: []string{"claude"}}
	}
	first := asset("shared", "v1", `{"one":{"command":"python3"},"retired":{"command":"python3"}}`)
	second := asset("other", "r1", `{"two":{"type":"http","url":"http://127.0.0.1:1/mcp"}}`)
	c.CapabilityAssets = []CapabilityAsset{first, second}
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture()}
	f.resolveApply.Store(true)
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	apply := func(key string, picks []CapabilitySelection) ExecutionRead {
		t.Helper()
		before, _ := a.ReadExecution(sid)
		r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: picks}
		preview, err := a.PreviewExecutionChange(sid, r)
		if err != nil {
			t.Fatal(err)
		}
		r.PreviewHash = preview.PreviewHash
		if _, err = a.Apply(Command{ID: key + "stage", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
			t.Fatal(err)
		}
		staged, _ := a.ReadExecution(sid)
		if digest(staged.Snapshot) != digest(before.Snapshot) {
			t.Fatal("save changed current version")
		}
		ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
		for _, kind := range []string{"prepare_execution", "apply_execution"} {
			var receipt CommandReceipt
			waitTest(t, func() bool {
				receipt, err = a.Apply(Command{ID: key + kind, Kind: kind, SessionID: sid, ExecutionChange: &ref})
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
		after, _ := a.ReadExecution(sid)
		if digest(after.Snapshot) != digest(staged.PendingChange.Snapshot) || digest(after.History[len(after.History)-1]) != digest(before.Snapshot) {
			t.Fatal("promotion changed pinned candidate/history")
		}
		return after
	}
	added := apply("add", []CapabilitySelection{{AssetID: "other", Mode: "enabled", Version: "r1"}, {AssetID: "shared", Mode: "enabled", Version: "v1"}})
	if len(added.Snapshot.MCPSelections) != 3 || len(added.Snapshot.CapabilityRefs) != 2 {
		t.Fatal("new asset services missing")
	}
	updated := asset("shared", "v2", `{"one":{"command":"node"},"replacement":{"command":"node"}}`)
	current := libraryRead(t, a, "shared")
	libraryChange(t, a, "update", CapabilityChangeRequest{Action: "update", AssetID: "shared", BaseRevision: current.Revision, Asset: &updated})
	unchanged, _ := a.ReadExecution(sid)
	if digest(unchanged.Snapshot) != digest(added.Snapshot) {
		t.Fatal("library upgraded existing snapshot")
	}
	upgraded := apply("upgrade", []CapabilitySelection{{AssetID: "shared", Mode: "enabled", Version: "v2"}})
	if upgraded.Snapshot.CapabilityRefs[1].Asset.Version != "v2" || upgraded.Snapshot.CapabilityRefs[0].Asset.Version != "r1" {
		t.Fatal("version replacement changed unrelated asset")
	}
	for _, s := range upgraded.Snapshot.MCPSelections {
		if s.Server == "retired" {
			t.Fatal("retired explicit server kept")
		}
	}
	restored := apply("inherit", []CapabilitySelection{{AssetID: "shared", Mode: "inherit"}, {AssetID: "other", Mode: "inherit"}})
	if len(restored.Snapshot.MCPSelections) != 0 || len(restored.Snapshot.CapabilityRefs) != 0 {
		t.Fatal("explicit dependencies not released")
	}
	for _, picks := range [][]CapabilitySelection{
		{{AssetID: "shared", Mode: "enabled", Version: "v1"}},
		{{AssetID: "shared", Mode: "inherit", Version: "v2"}},
		{{AssetID: "missing", Mode: "enabled"}},
		{{AssetID: "shared", Mode: "enabled"}, {AssetID: "shared", Mode: "disabled"}},
	} {
		if _, err := a.PreviewExecutionChange(sid, ExecutionChangeRequest{BaseSnapshotID: restored.Snapshot.ID, Selections: picks}); err == nil {
			t.Fatalf("invalid selection admitted: %+v", picks)
		}
	}
	alias := asset("alias", "v1", `{"one":{"command":"python3"}}`)
	libraryChange(t, a, "register-alias", CapabilityChangeRequest{Action: "register", AssetID: "alias", Asset: &alias})
	if _, err := a.PreviewExecutionChange(sid, ExecutionChangeRequest{BaseSnapshotID: restored.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}, {AssetID: "alias", Mode: "enabled"}}}); err == nil {
		t.Fatal("same-name assets accepted")
	}
	r := ExecutionChangeRequest{BaseSnapshotID: restored.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled", Version: "v2"}}}
	preview, err := a.PreviewExecutionChange(sid, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage-drift", Kind: "stage_execution", SessionID: sid, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.ReadExecution(sid)
	if err := os.WriteFile(updated.Source, []byte(`{"mcpServers":{"one":{"command":"changed"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ref := ExecutionChangeRequest{BaseSnapshotID: restored.Snapshot.ID, PendingChangeID: saved.PendingChange.ID}
	if _, err := a.Apply(Command{ID: "prepare-drift", Kind: "prepare_execution", SessionID: sid, ExecutionChange: &ref}); err == nil {
		t.Fatal("changed candidate source accepted")
	}
	after, _ := a.ReadExecution(sid)
	if digest(after.PendingChange.Snapshot) != digest(saved.PendingChange.Snapshot) {
		t.Fatal("validation recaptured candidate instead of rejecting drift")
	}
}
