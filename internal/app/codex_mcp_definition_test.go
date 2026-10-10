package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

type mcpDefinitionFixture struct {
	*fixtureRuntime
	read func(runtime.Scope) ([]runtime.MCPSelection, error)
}

func (f *mcpDefinitionFixture) ReadCodexMCPDefinitions(_ context.Context, scope runtime.Scope) ([]runtime.MCPSelection, error) {
	if f.read != nil {
		return f.read(scope)
	}
	for _, selection := range scope.MCPSelections {
		if selection.Definition != nil {
			selection.Definition.Environment = []runtime.MCPEnvironmentBinding{}
		}
	}
	return scope.MCPSelections, nil
}

func sharedMCPConfig(t *testing.T, definitionJSON string) (Config, string) {
	t.Helper()
	c := fixtureConfig(t)
	root := t.TempDir()
	definition := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(definition, []byte(definitionJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server.py"), []byte("# shared native MCP fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared-mcp", Name: "Shared MCP", Kind: "mcp", Version: "v1", Source: root, Backends: []string{"codex", "claude"}}}
	return c, definition
}

func TestCodexSharedMCPAssetCanBeSelectedWithoutProfileMapping(t *testing.T) {
	c, definition := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","args":["./server.py"]}}}`)
	a := openFixture(t, c, &mcpDefinitionFixture{fixtureRuntime: newFixture()})
	receipt, err := a.Apply(Command{ID: "shared-definition", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only",
		CapabilitySelections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(receipt.SessionID)
	if err != nil || len(read.Snapshot.MCPSelections) != 1 || len(read.Snapshot.CapabilityRefs) != 1 {
		t.Fatalf("shared definition did not retain its source: %+v %v", read, err)
	}
	selection := read.Snapshot.MCPSelections[0]
	if selection.AssetID != "shared-mcp" || selection.Server != "identity" || selection.Mode != "enabled" || selection.SourcePath != definition {
		t.Fatalf("wrong shared selection: %+v", selection)
	}
	encoded, _ := json.Marshal(read.Snapshot)
	if strings.Contains(string(encoded), "python3") || strings.Contains(string(encoded), "server.py") {
		t.Fatal("shared execution payload copied into snapshot")
	}
}

func TestCodexSharedMCPRejectsUnboundMetadataBeforeCreatingExecution(t *testing.T) {
	for _, mode := range []string{"reader-unavailable", "read-error", "missing-server", "extra-server", "asset", "server", "mode", "path", "missing-definition", "version", "hash", "missing-environment", "extra-environment", "wrong-environment", "invalid-hash"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","env":{"KEY":"${TOKEN}"}}}}`)
			f := &mcpDefinitionFixture{fixtureRuntime: newFixture()}
			f.read = func(scope runtime.Scope) ([]runtime.MCPSelection, error) {
				selection := &scope.MCPSelections[0]
				selection.Definition.Environment = []runtime.MCPEnvironmentBinding{{Name: "TOKEN", Present: true, SHA256: strings.Repeat("a", 64)}}
				switch mode {
				case "read-error":
					return nil, errors.New("owned daemon unavailable")
				case "missing-server":
					return nil, nil
				case "extra-server":
					return append(scope.MCPSelections, *selection), nil
				case "asset":
					selection.AssetID = "other"
				case "server":
					selection.Server = "other"
				case "mode":
					selection.Mode = "disabled"
				case "path":
					selection.SourcePath += ".other"
				case "missing-definition":
					selection.Definition = nil
				case "version":
					selection.Definition.Version++
				case "hash":
					selection.Definition.SHA256 = strings.Repeat("b", 64)
				case "missing-environment":
					selection.Definition.Environment = nil
				case "extra-environment":
					selection.Definition.Environment = append(selection.Definition.Environment, selection.Definition.Environment[0])
				case "wrong-environment":
					selection.Definition.Environment[0].Name = "OTHER"
				case "invalid-hash":
					selection.Definition.Environment[0].SHA256 = "unknown"
				}
				return scope.MCPSelections, nil
			}
			var adapter runtime.Adapter = f
			if mode == "reader-unavailable" {
				adapter = f.fixtureRuntime
			}
			a := openFixture(t, c, adapter)
			_, err := a.Apply(Command{ID: "unbound", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only",
				CapabilitySelections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "enabled"}}})
			if err == nil || f.creates.Load() != 0 || len(a.Snapshot().Sessions) != 0 || len(a.Snapshot().Operations) != 0 {
				t.Fatal("unbound metadata created a session or native intent")
			}
		})
	}
}

func TestCodexSharedMCPBindingsPrecedeNativeIntentAndSurviveRestart(t *testing.T) {
	c, _ := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","env":{"KEY":"${TOKEN}"}}}}`)
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &mcpDefinitionFixture{fixtureRuntime: newFixture()}
	bindings := []runtime.MCPEnvironmentBinding{{Name: "TOKEN", Present: true, SHA256: strings.Repeat("a", 64)}}
	f.read = func(scope runtime.Scope) ([]runtime.MCPSelection, error) {
		scope.MCPSelections[0].Definition.Environment = queryCopy(bindings)
		return scope.MCPSelections, nil
	}
	f.before = func(intent runtime.Intent) {
		state, err := db.Load()
		if err != nil {
			t.Error(err)
			return
		}
		snapshot := state.Sessions[intent.Scope.SessionID].ExecutionSnapshot
		if snapshot == nil || !reflect.DeepEqual(snapshot.MCPSelections, intent.Scope.MCPSelections) ||
			!reflect.DeepEqual(snapshot.MCPSelections[0].Definition.Environment, bindings) || state.Operations[intent.ID].ExecutionSnapshotID != snapshot.ID {
			t.Error("native dispatch preceded the durable source and environment binding")
		}
	}
	a, err := Open(c, db, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	receipt, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only",
		CapabilitySelections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	original, _ := a.ReadExecution(receipt.SessionID)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.before = nil
	f.read = func(runtime.Scope) ([]runtime.MCPSelection, error) {
		return nil, errors.New("restart must not rebind credentials")
	}
	a = openFixture(t, c, f)
	restored, err := a.ReadExecution(receipt.SessionID)
	if err != nil || !reflect.DeepEqual(restored.Snapshot, original.Snapshot) {
		t.Fatal("restart replaced the bound source or environment version")
	}
	restored.Snapshot.MCPSelections[0].Definition.Environment[0].SHA256 = "caller mutation"
	again, _ := a.ReadExecution(receipt.SessionID)
	if !reflect.DeepEqual(again.Snapshot, original.Snapshot) {
		t.Fatal("query caller changed the persisted environment binding")
	}
}

func TestCodexSharedMCPActivationBindsOnlyNewSnapshotAndRejectsStalePreview(t *testing.T) {
	c, _ := sharedMCPConfig(t, `{"mcpServers":{"identity":{"command":"python3","env":{"KEY":"${TOKEN}"}},"second":{"command":"python3","env":{"KEY":"${TOKEN}"}}}}`)
	f := &mcpDefinitionFixture{fixtureRuntime: newFixture()}
	stamp := strings.Repeat("a", 64)
	var captures []runtime.Scope
	f.read = func(scope runtime.Scope) ([]runtime.MCPSelection, error) {
		captures = append(captures, queryCopy(scope))
		for i := range scope.MCPSelections {
			selection := &scope.MCPSelections[i]
			selection.Definition.Environment = []runtime.MCPEnvironmentBinding{}
			if selection.Mode == "enabled" {
				selection.Definition.Environment = append(selection.Definition.Environment, runtime.MCPEnvironmentBinding{Name: "TOKEN", Present: true, SHA256: stamp})
			}
		}
		return scope.MCPSelections, nil
	}
	a := openFixture(t, c, f)
	receipt, err := a.Apply(Command{ID: "create-disabled", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only",
		CapabilitySelections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	original, _ := a.ReadExecution(receipt.SessionID)
	request := ExecutionChangeRequest{BaseSnapshotID: original.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "enabled"}}}
	preview, err := a.PreviewExecutionChange(receipt.SessionID, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 2 || len(captures[1].MCPSelections) != 2 || len(a.Snapshot().Operations) != 1 || f.creates.Load() != 1 {
		t.Fatal("activation did not bind both servers with a read-only preview")
	}
	request.PreviewHash = preview.PreviewHash
	stamp = strings.Repeat("b", 64)
	if _, err := a.Apply(Command{ID: "stale-stage", Kind: "stage_execution", SessionID: receipt.SessionID, ExecutionChange: &request}); err == nil {
		t.Fatal("credential change between preview and save reused the old preview")
	}
	beforeStage, _ := a.ReadExecution(receipt.SessionID)
	if beforeStage.PendingChange != nil || !reflect.DeepEqual(beforeStage.Snapshot, original.Snapshot) {
		t.Fatal("rejected activation changed the original execution")
	}
	preview, err = a.PreviewExecutionChange(receipt.SessionID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	command := Command{ID: "stage-current", Kind: "stage_execution", SessionID: receipt.SessionID, ExecutionChange: &request}
	saved, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	afterStage, _ := a.ReadExecution(receipt.SessionID)
	if afterStage.PendingChange == nil || !reflect.DeepEqual(afterStage.Snapshot, original.Snapshot) {
		t.Fatal("staging activation replaced the original disabled snapshot")
	}
	for _, selection := range afterStage.PendingChange.Snapshot.MCPSelections {
		if selection.Mode != "enabled" || selection.Definition.Environment[0].SHA256 != stamp {
			t.Fatal("candidate lacks the explicitly bound current environment")
		}
	}
	count := len(captures)
	stamp = strings.Repeat("c", 64)
	if retry, err := a.Apply(command); err != nil || retry != saved || len(captures) != count {
		t.Fatal("retry rebound the saved activation to a different credential")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	restored, _ := a.ReadExecution(receipt.SessionID)
	if !reflect.DeepEqual(restored.PendingChange, afterStage.PendingChange) || !reflect.DeepEqual(restored.Snapshot, original.Snapshot) || len(captures) != count {
		t.Fatal("restart lost or rebound the explicit activation candidate")
	}
}
