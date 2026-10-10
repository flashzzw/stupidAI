package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSettingsAdoptionCandidate(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	path := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
	writePluginMetadata(t, path, `{"env":{"NETCAP_SETTINGS_TEST":"v1"}}`)
	a := openFixture(t, c, newFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(id)
	writePluginMetadata(t, path, `{"env":{"NETCAP_SETTINGS_TEST":"v2"}}`)
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptLocalSettings: true}
	preview, err := a.PreviewExecutionChange(id, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.LocalSettingsChanges) != 1 || preview.LocalSettingsChanges[0].Before.Path != path || preview.LocalSettingsChanges[0].After.SHA256 == preview.LocalSettingsChanges[0].Before.SHA256 {
		t.Fatalf("missing file diff: %+v", preview)
	}
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: id, ExecutionChange: &r}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(id)
	if digest(staged.Snapshot) != digest(before.Snapshot) || !staged.PendingChange.AdoptLocalSettings || digest(staged.PendingChange.Snapshot.ClaudeLocalSettings) == digest(before.Snapshot.ClaudeLocalSettings) {
		t.Fatal("save must preserve active version and capture exact candidate")
	}
	if _, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: id, Text: "must not send"}); err == nil {
		t.Fatal("saving candidate unblocked input")
	}
	writePluginMetadata(t, path, `{"env":{"NETCAP_SETTINGS_TEST":"v3"}}`)
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	if _, err := a.Apply(Command{ID: "prepare", Kind: "prepare_execution", SessionID: id, ExecutionChange: &ref}); err == nil {
		t.Fatal("changed candidate accepted")
	}
	if _, err := a.Apply(Command{ID: "discard", Kind: "discard_execution", SessionID: id, ExecutionChange: &ref}); err != nil {
		t.Fatal(err)
	}
	read, _ := a.ReadExecution(id)
	if read.PendingChange != nil || digest(read.Snapshot) != digest(before.Snapshot) {
		t.Fatal("discard changed current version")
	}
}

func TestClaudeSettingsAdoptionKeepsIndependentSources(t *testing.T) {
	for _, source := range []string{"profile", "custom", "skill", "identity", "unchanged"} {
		t.Run(source, func(t *testing.T) {
			c, resource := claudeSkillFixture(t)
			local := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
			profile := filepath.Join(c.Instances[0].Profile, "settings.json")
			custom := filepath.Join(t.TempDir(), "custom.json")
			c.Instances[0].SettingsFile = custom
			for _, path := range []string{local, profile, custom} {
				writePluginMetadata(t, path, `{}`)
			}
			a := openFixture(t, c, newFixture())
			created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(created.SessionID)
			if source != "unchanged" {
				writePluginMetadata(t, local, `{"env":{"NETCAP_SETTINGS_TEST":"v2"}}`)
			}
			switch source {
			case "profile":
				writePluginMetadata(t, profile, `{"env":{"NETCAP_PROFILE_TEST":"changed"}}`)
			case "custom":
				writePluginMetadata(t, custom, `{"env":{"NETCAP_CUSTOM_TEST":"changed"}}`)
			case "skill":
				writePluginMetadata(t, filepath.Join(resource, "SKILL.md"), "---\nname: displayed-method\ndescription: changed method\n---\nchanged\n")
			case "identity":
				writePluginMetadata(t, local, `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
			}
			request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, AdoptLocalSettings: true}
			if _, err := a.PreviewExecutionChange(created.SessionID, request); err == nil {
				t.Fatal("local adoption accepted an unchanged version or unrelated source drift")
			}
			after, _ := a.ReadExecution(created.SessionID)
			if digest(after.Snapshot) != digest(before.Snapshot) || after.PendingChange != nil {
				t.Fatal("rejected preview modified the saved execution")
			}
		})
	}
}

func TestClaudeSettingsAdoptionRejectsLegacyWithoutNativeBinding(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	local := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
	writePluginMetadata(t, local, `{}`)
	a := openFixture(t, c, newFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
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
	s := state.Sessions[id]
	s.Scope.ClaudeSettingsSources = nil
	s.ExecutionSnapshot.ClaudeLocalSettings.Version = 1
	state.Sessions[id] = s
	if err := db.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := openFixture(t, c, newFixture())
	writePluginMetadata(t, local, `{"env":{"NETCAP_SETTINGS_TEST":"v2"}}`)
	_, err = legacy.PreviewExecutionChange(id, ExecutionChangeRequest{BaseSnapshotID: s.ExecutionSnapshot.ID, AdoptLocalSettings: true})
	if err == nil || !strings.Contains(err.Error(), "尚无原生本地文件版本绑定") {
		t.Fatalf("legacy adoption: %v", err)
	}
}
