package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexUserHookSnapshotPersistsAndRejectsDrift(t *testing.T) {
	c, _ := executionFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	const original = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"PRIVATE_HOOK_V1"}]}]}}`
	writePluginMetadata(t, path, original)
	f := newFixture()
	a := openFixture(t, c, f)
	id := createExecution(t, a).SessionID
	before, err := a.ReadExecution(id)
	if err != nil || before.Snapshot.CodexUserHooks == nil || before.Snapshot.CodexUserHooks.File.Path != path {
		t.Fatal("missing user hook version", err)
	}
	if digest(a.Snapshot().Sessions[0].Scope.CodexUserHooks) != digest(before.Snapshot.CodexUserHooks) {
		t.Fatal("native binding omitted hook version")
	}
	encoded, _ := json.Marshal(before)
	if strings.Contains(string(encoded), "PRIVATE_HOOK_V1") {
		t.Fatal("hook body leaked")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, path, strings.Replace(original, "V1", "V2", 1))
	a = openFixture(t, c, f)
	after, err := a.ReadExecution(id)
	if err != nil || after.Status != "drifted" || digest(before.Snapshot) != digest(after.Snapshot) {
		t.Fatal("restart lost source version", err)
	}
	if _, err := a.Apply(Command{ID: "reject", Kind: "send", SessionID: id, Text: "MUST NOT SEND"}); err == nil {
		t.Fatal("drift accepted")
	}
	if f.sends.Load() != 0 {
		t.Fatal("source check dispatched input")
	}
	writePluginMetadata(t, path, original)
	restored, err := a.ReadExecution(id)
	if err != nil || restored.Status != "unchanged" {
		t.Fatal("original file not recognized", err)
	}
}

func TestCodexUserHookSnapshotAbsenceAndLegacyBinding(t *testing.T) {
	for _, scenario := range []string{"new", "legacy", "scope-tamper"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := executionFixture(t)
			a := openFixture(t, c, newFixture())
			id := createExecution(t, a).SessionID
			before, _ := a.ReadExecution(id)
			if before.Snapshot.CodexUserHooks == nil || before.Snapshot.CodexUserHooks.File.Kind != "absent" {
				t.Fatal("absence not recorded")
			}
			path := filepath.Join(c.Instances[0].Profile, "hooks.json")
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("capture created a hook file")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := OpenStore(c.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			state, err := db.Load()
			if err != nil {
				t.Fatal(err)
			}
			session := state.Sessions[id]
			if scenario == "legacy" {
				session.ExecutionSnapshot.CodexUserHooks = nil
				session.Scope.CodexUserHooks = nil
			}
			if scenario == "scope-tamper" {
				session.Scope.CodexUserHooks = nil
			}
			state.Sessions[id] = session
			if err := db.Save(state); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			writePluginMetadata(t, path, `{"hooks":{}}`)
			a = openFixture(t, c, newFixture())
			read, err := a.ReadExecution(id)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "legacy" {
				if read.Snapshot.CodexUserHooks != nil || read.Status != "unchanged" {
					t.Fatal("legacy evidence invented")
				}
			} else if read.Status != "drifted" && read.BindingError == "" {
				t.Fatal("source or binding drift hidden")
			}
		})
	}
}

func TestCodexUserHookVersionCannotUpgradeThroughPluginChange(t *testing.T) {
	c := codexPluginFixture(t)
	path := filepath.Join(c.Instances[0].Profile, "hooks.json")
	const original = `{"hooks":{}}`
	writePluginMetadata(t, path, original)
	a := openFixture(t, c, newFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}}
	if _, err := a.PreviewExecutionChange(id, request); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	if _, err := a.PreviewExecutionChange(id, request); err == nil {
		t.Fatal("plugin preview adopted changed user hook file")
	}
	writePluginMetadata(t, path, original)
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(id)
	if err != nil || read.PendingChange == nil || digest(read.PendingChange.Snapshot.CodexUserHooks) != digest(before.Snapshot.CodexUserHooks) {
		t.Fatal("saved candidate changed user hook version", err)
	}
}
