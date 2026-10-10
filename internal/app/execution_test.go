package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func executionFixture(t *testing.T) (Config, string) {
	t.Helper()
	c := fixtureConfig(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture skill definition"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tool.py"), []byte("fixture-secret-not-for-snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared", Name: "Shared fixture", Kind: "skill", Version: "v1", Source: source, Backends: []string{"codex", "claude"}}}
	return c, source
}

func createExecution(t *testing.T, a *Application) CommandReceipt {
	t.Helper()
	r, err := a.Apply(Command{ID: "create-execution", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return r
}

func TestExecutionSnapshotPrecedesNativeWriteAndSurvivesRegistryChanges(t *testing.T) {
	c, _ := executionFixture(t)
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	f.before = func(in runtime.Intent) {
		state, err := db.Load()
		if err != nil {
			t.Error(err)
			return
		}
		snapshot := state.Sessions[in.Scope.SessionID].ExecutionSnapshot
		if snapshot == nil || snapshot.ID == "" || state.Operations[in.ID].ExecutionSnapshotID != snapshot.ID {
			t.Error("native write preceded durable snapshot/reference")
		}
	}
	a, err := Open(c, db, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	sid := createExecution(t, a).SessionID
	read, err := a.ReadExecution(sid)
	if err != nil || read.Status != "unchanged" || len(read.Snapshot.CapabilityRefs) != 1 {
		t.Fatalf("missing recorded execution: %+v %v", read, err)
	}
	original, _ := json.Marshal(read.Snapshot)
	if strings.Contains(string(original), "fixture-secret-not-for-snapshot") || read.Snapshot.Coverage != "recorded_sources_only" {
		t.Fatal("snapshot contains source content or overstates native loading evidence")
	}
	if _, err := a.Apply(Command{ID: "first", Kind: "send", SessionID: sid, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Removing a registry entry does not erase the version already referenced by
	// an execution. Unchanged original files can still be checked and used.
	c.CapabilityAssets = nil
	f.before = nil
	a = openFixture(t, c, f)
	if _, err := a.Apply(Command{ID: "second", Kind: "send", SessionID: sid, Text: "second"}); err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	after, err := a.ReadExecution(sid)
	encoded, _ := json.Marshal(after.Snapshot)
	if err != nil || string(encoded) != string(original) {
		t.Fatal("restart or registry update rewrote startup snapshot")
	}
	for _, op := range a.Snapshot().Operations {
		if op.ExecutionSnapshotID != after.Snapshot.ID {
			t.Fatal("later operation did not retain the original snapshot reference")
		}
	}
}

func TestExecutionReferenceDetectsScriptDriftWithoutRewritingHistory(t *testing.T) {
	c, source := executionFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	sid := createExecution(t, a).SessionID
	before, _ := a.ReadExecution(sid)
	beforeJSON, _ := json.Marshal(before.Snapshot)
	if err := os.WriteFile(filepath.Join(source, "tool.py"), []byte("changed script; same SKILL.md"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadExecution(sid)
	if err != nil || after.Status != "drifted" || after.Sources[1].Status != "changed" {
		t.Fatalf("dependent script drift was hidden: %+v %v", after, err)
	}
	for _, kind := range []string{"send", "resume"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: sid, Text: "must not send"}); err == nil {
			t.Fatalf("%s ignored source drift", kind)
		}
	}
	if f.sends.Load() != 0 {
		t.Fatal("drift caused a native write")
	}
	// Query callers cannot mutate the stored version or approve drift.
	after.Snapshot.CapabilityRefs[0].Asset.Version = "caller mutation"
	again, _ := a.ReadExecution(sid)
	againJSON, _ := json.Marshal(again.Snapshot)
	if string(againJSON) != string(beforeJSON) {
		t.Fatal("source checks or query mutation overwrote historical snapshot")
	}
	if a.Workspace().Sessions[0].ExecutionSnapshot != nil {
		t.Fatal("workspace list carried the complete execution source record")
	}
}

func TestExecutionReferencesRejectInvalidBindingsBeforeNativeCreate(t *testing.T) {
	for _, mode := range []string{"missing", "disabled", "incompatible", "duplicate", "nested-symlink"} {
		t.Run(mode, func(t *testing.T) {
			c, source := executionFixture(t)
			refs := []string{"shared"}
			switch mode {
			case "missing":
				refs = []string{"unknown"}
			case "disabled":
				c.CapabilityAssets[0].Disabled = true
			case "incompatible":
				c.CapabilityAssets[0].Backends = []string{"claude"}
			case "duplicate":
				refs = append(refs, "shared")
			case "nested-symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(source, "external")); err != nil {
					t.Fatal(err)
				}
			}
			f := newFixture()
			a := openFixture(t, c, f)
			_, err := a.Apply(Command{ID: "invalid", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: refs})
			if err == nil || f.creates.Load() != 0 || len(a.Snapshot().Sessions) != 0 {
				t.Fatal("invalid reference created a native or persisted session")
			}
		})
	}
}

type changedSourceStore struct {
	Persistence
	path string
}

func (s *changedSourceStore) Save(state State) error {
	if err := s.Persistence.Save(state); err != nil {
		return err
	}
	for _, op := range state.Operations {
		if op.Status == "prepared" {
			return os.WriteFile(s.path, []byte("changed after intent commit"), 0600)
		}
	}
	return nil
}

func TestExecutionSourceRecheckedAfterIntentBeforeDispatch(t *testing.T) {
	c, source := executionFixture(t)
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture()
	a, err := Open(c, &changedSourceStore{Persistence: db, path: filepath.Join(source, "tool.py")}, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{"shared"}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		for _, op := range a.Snapshot().Operations {
			if op.ID == r.OperationID {
				return op.Status == "not_sent"
			}
		}
		return false
	})
	if f.creates.Load() != 0 {
		t.Fatal("native create used sources changed after intent commit")
	}
}

func TestExecutionSnapshotDoesNotInventLegacyEvidence(t *testing.T) {
	c := fixtureConfig(t)
	db, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state := newState()
	state.Sessions["legacy"] = Session{ID: "legacy", ControlMode: "observed"}
	if err := db.Save(state); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a := openFixture(t, c, newFixture())
	read, err := a.ReadExecution("legacy")
	if err != nil || read.Snapshot != nil || read.Status != "unrecorded" {
		t.Fatal("legacy session acquired fabricated startup evidence")
	}
}

func TestExecutionSnapshotTracksInitiallyAbsentClaudeSettings(t *testing.T) {
	c, _ := executionFixture(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a := openFixture(t, c, newFixture())
	sid := createExecution(t, a).SessionID
	read, _ := a.ReadExecution(sid)
	if read.Snapshot.ProfileSource.Kind != "absent" {
		t.Fatal("missing optional settings were recorded as loaded configuration")
	}
	if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "settings.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	read, _ = a.ReadExecution(sid)
	if read.Status != "drifted" || read.Sources[0].Status != "changed" {
		t.Fatal("newly added native configuration did not change recorded sources")
	}
}

func TestNativeSelectionsAreDurableAndReferencesStayPassive(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			c, _ := executionFixture(t)
			c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: "identity", Name: "Identity", Kind: "mcp", Version: "v1", Source: filepath.Join(c.Instances[0].Profile, "config.toml"), Backends: []string{"codex"}, CodexMCP: "identity"})
			f := newFixture()
			a := openFixture(t, c, f)
			command := Command{ID: "selected", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{"identity"}}
			if selected {
				command.CapabilityRefs = nil
				command.CapabilitySelections = []CapabilitySelection{{AssetID: "identity", Mode: "disabled"}}
			}
			receipt, err := a.Apply(command)
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			session := a.Snapshot().Sessions[0]
			read, _ := a.ReadExecution(receipt.SessionID)
			if len(read.Snapshot.CapabilityRefs) != 1 {
				t.Fatal("selection did not automatically record source")
			}
			if selected {
				if len(read.Snapshot.MCPSelections) != 1 || !reflect.DeepEqual(session.Scope.MCPSelections, read.Snapshot.MCPSelections) {
					t.Fatal("native selection lost immutable binding")
				}
				for _, op := range a.Snapshot().Operations {
					if !reflect.DeepEqual(op.Intent.Scope.MCPSelections, session.Scope.MCPSelections) {
						t.Fatal("dispatch omitted native selection")
					}
				}
				mutated := session
				mutated.Scope.MCPSelections = append([]runtime.MCPSelection{}, session.Scope.MCPSelections...)
				mutated.Scope.MCPSelections[0].Mode = "enabled"
				if verifyExecutionBinding(mutated) == nil {
					t.Fatal("scope drift accepted")
				}
			} else if len(read.Snapshot.MCPSelections) != 0 || len(session.Scope.MCPSelections) != 0 {
				t.Fatal("reference-only request enabled native capability")
			}
			if _, err := a.Apply(Command{ID: "late-select", Kind: "send", SessionID: receipt.SessionID, Text: "work", CapabilitySelections: []CapabilitySelection{{AssetID: "identity", Mode: "enabled"}}}); err == nil {
				t.Fatal("in-place selection accepted")
			}
			before, _ := json.Marshal(read.Snapshot)
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openFixture(t, c, newFixture())
			read, _ = a.ReadExecution(receipt.SessionID)
			after, _ := json.Marshal(read.Snapshot)
			if string(before) != string(after) {
				t.Fatal("restart rewrote selection snapshot")
			}
		})
	}
}

func TestNativeSelectionsRejectUnsupportedBindingsBeforeCreation(t *testing.T) {
	for _, mode := range []string{"invalid-mode", "duplicate", "same-server", "different-source", "no-mapping", "claude", "missing"} {
		t.Run(mode, func(t *testing.T) {
			c, source := executionFixture(t)
			asset := CapabilityAsset{ID: "identity", Name: "Identity", Kind: "mcp", Version: "v1", Source: filepath.Join(c.Instances[0].Profile, "config.toml"), Backends: []string{"codex"}, CodexMCP: "identity"}
			selections := []CapabilitySelection{{AssetID: "identity", Mode: "enabled"}}
			switch mode {
			case "invalid-mode":
				selections[0].Mode = "inherit"
			case "duplicate":
				selections = append(selections, selections[0])
			case "same-server":
				alias := asset
				alias.ID = "alias"
				c.CapabilityAssets = append(c.CapabilityAssets, alias)
				selections = append(selections, CapabilitySelection{AssetID: "alias", Mode: "disabled"})
			case "different-source":
				asset.Source = source
			case "no-mapping":
				asset.CodexMCP = ""
			case "claude":
				c.Instances[0].Kind = "claude"
				c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
				_ = os.WriteFile(c.Instances[0].Bridge, nil, 0600)
			case "missing":
				selections[0].AssetID = "missing"
			}
			c.CapabilityAssets = append(c.CapabilityAssets, asset)
			f := newFixture()
			a := openFixture(t, c, f)
			_, err := a.Apply(Command{ID: "invalid", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: selections})
			if err == nil || f.creates.Load() != 0 || len(a.Snapshot().Sessions) != 0 {
				t.Fatal("invalid native selection reached creation")
			}
		})
	}
}
