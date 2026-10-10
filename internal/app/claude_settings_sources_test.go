package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLocalSettingsVersionRejectsBenignDrift(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	path := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
	writePluginMetadata(t, path, `{"env":{"NETCAP_SETTING":"v1"}}`)
	f := newFixture()
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(id)
	body, _ := json.Marshal(before.Snapshot)
	if !strings.Contains(string(body), `"claude_local_settings"`) {
		t.Error("snapshot does not record local settings versions")
	}
	writePluginMetadata(t, path, `{"env":{"NETCAP_SETTING":"v2"}}`)
	read, err := a.ReadExecution(id)
	if err != nil || read.Status != "drifted" {
		t.Errorf("changed local settings not reported: %s %v", read.Status, err)
	}
	if _, err := a.Apply(Command{ID: "must-not-send", Kind: "send", SessionID: id, Text: "MUST NOT SEND"}); err == nil {
		t.Error("unrecorded local version accepted for input")
	}
	after, _ := a.ReadExecution(id)
	if digest(before.Snapshot) != digest(after.Snapshot) {
		t.Error("historical snapshot rewritten")
	}
	if f.sends.Load() != 0 {
		t.Error("source check called provider")
	}
	if err := os.WriteFile(path, []byte(`{"env":{"NETCAP_SETTING":"v1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	restored, _ := a.ReadExecution(id)
	if restored.Status != "unchanged" {
		t.Error("original source was not recognized after restore")
	}
}

func TestClaudeLocalSettingsVersionBoundaries(t *testing.T) {
	for _, mode := range []string{"add", "delete", "permissions", "retarget", "parent-link", "malformed", "graph"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			base := c.Projects[0].Cwd
			if mode == "graph" {
				c.Projects[0].Cwd = filepath.Join(base, "nested")
				if err := os.Mkdir(c.Projects[0].Cwd, 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
			const original = `{"env":{"NETCAP_VERSION":"v1"}}`
			if mode != "add" && mode != "graph" {
				writePluginMetadata(t, path, original)
			}
			targetA, targetB := filepath.Join(t.TempDir(), "a.json"), filepath.Join(t.TempDir(), "b.json")
			if mode == "retarget" {
				writePluginMetadata(t, targetA, original)
				writePluginMetadata(t, targetB, original)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(targetA, path); err != nil {
					t.Fatal(err)
				}
			}
			parentA, parentB := t.TempDir(), t.TempDir()
			if mode == "parent-link" {
				writePluginMetadata(t, filepath.Join(parentA, "settings.local.json"), original)
				writePluginMetadata(t, filepath.Join(parentB, "settings.local.json"), original)
				if err := os.RemoveAll(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parentA, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
			f := newFixture()
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(id)
			if mode == "add" {
				if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatal("capture created settings directory")
				}
			}
			switch mode {
			case "add":
				writePluginMetadata(t, path, original)
			case "delete":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
			case "retarget":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(targetB, path); err != nil {
					t.Fatal(err)
				}
			case "parent-link":
				if err := os.Remove(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parentB, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				writePluginMetadata(t, path, `{"PRIVATE_PARSE_INPUT":`)
			case "graph":
				cmd := exec.Command("git", "init", "--quiet", base)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("fixture git: %v %s", err, out)
				}
			}
			after, err := a.ReadExecution(id)
			if err != nil || after.Status != "drifted" || digest(before.Snapshot) != digest(after.Snapshot) {
				t.Fatalf("version drift: %+v %v", after, err)
			}
			if mode == "graph" {
				found := false
				for _, s := range after.Sources {
					found = found || s.Status == "added" && s.Path == filepath.Join(base, ".claude/settings.local.json")
				}
				if !found {
					t.Fatal("added source path hidden")
				}
			}
			if _, err := a.Apply(Command{ID: "reject-" + mode, Kind: "send", SessionID: id, Text: "MUST NOT SEND"}); err == nil {
				t.Fatal("source drift accepted")
			}
			if f.sends.Load() != 0 {
				t.Fatal("drift invoked provider")
			}
			encoded, _ := json.Marshal(after)
			if strings.Contains(string(encoded), "PRIVATE_PARSE_INPUT") {
				t.Fatal("source content leaked")
			}
		})
	}
}

func TestClaudeLocalSettingsVersionCannotUpgradeThroughCapabilityChange(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	path := filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json")
	writePluginMetadata(t, path, `{"env":{"NETCAP_VERSION":"v1"}}`)
	a := openFixture(t, c, newFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(id)
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}}
	if _, err := a.PreviewExecutionChange(id, request); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, path, `{"env":{"NETCAP_VERSION":"v2"}}`)
	if _, err := a.PreviewExecutionChange(id, request); err == nil {
		t.Fatal("capability preview silently upgraded local sources")
	}
	writePluginMetadata(t, path, `{"env":{"NETCAP_VERSION":"v1"}}`)
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	read, _ := a.ReadExecution(id)
	if read.PendingChange == nil || digest(read.PendingChange.Snapshot.ClaudeLocalSettings) != digest(before.Snapshot.ClaudeLocalSettings) {
		t.Fatal("candidate lost original local versions")
	}
}

func TestClaudeLocalSettingsVersionDoesNotInventLegacyRecord(t *testing.T) {
	c, _ := claudeSkillFixture(t)
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
	s.ExecutionSnapshot.ClaudeLocalSettings = nil
	s.Scope.SettingsGuardVersion = 0
	s.Scope.ClaudeSettingsSources = nil
	state.Sessions[id] = s
	if err := db.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := openFixture(t, c, newFixture())
	read, err := legacy.ReadExecution(id)
	if err != nil || read.Snapshot.ClaudeLocalSettings != nil {
		t.Fatal("legacy source evidence fabricated")
	}
	for _, source := range read.Sources {
		if strings.Contains(source.Name, "本地设置") {
			t.Fatal("legacy local version claimed")
		}
	}
}
