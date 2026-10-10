package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexDirectorySkillMappingAdmission(t *testing.T) {
	for _, scenario := range []string{"directory", "file", "symlink", "shared-backends", "hardlink", "other-entry", "nested-skill", "managed", "other-backend", "relative-path"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			cfg.CapabilityAssets = nil
			root, other := t.TempDir(), t.TempDir()
			entry := filepath.Join(root, "SKILL.md")
			body := []byte("---\nname: method\ndescription: Owned method\n---\nOwned.\n")
			if err := os.WriteFile(entry, body, 0600); err != nil {
				t.Fatal(err)
			}
			asset := CapabilityAsset{ID: "method", Name: "Method", Kind: "skill", Version: "v1", Source: root, Backends: []string{"codex"}, CodexSkill: entry}
			switch scenario {
			case "file":
				asset.Source = entry
			case "symlink", "hardlink":
				asset.CodexSkill = filepath.Join(other, "SKILL.md")
				link := os.Symlink
				if scenario == "hardlink" {
					link = os.Link
				}
				if err := link(entry, asset.CodexSkill); err != nil {
					t.Fatal(err)
				}
			case "other-entry":
				asset.CodexSkill = filepath.Join(other, "SKILL.md")
				if err := os.WriteFile(asset.CodexSkill, body, 0600); err != nil {
					t.Fatal(err)
				}
			case "nested-skill":
				if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "nested/SKILL.md"), body, 0600); err != nil {
					t.Fatal(err)
				}
			case "managed":
				asset.Management = "managed"
			case "shared-backends":
				asset.Backends = []string{"codex", "claude"}
			case "other-backend":
				asset.Backends = []string{"claude"}
			case "relative-path":
				asset.CodexSkill = "relative/SKILL.md"
			}
			f := newFixture()
			a := openFixture(t, cfg, f)
			preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
			invalidRegistration := scenario == "managed" || scenario == "other-backend" || scenario == "relative-path"
			if invalidRegistration {
				if err == nil {
					t.Fatal("invalid native mapping registered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			request := preview.Request
			request.PreviewHash = preview.PreviewHash
			if _, err = a.Apply(Command{ID: "register", Kind: "manage_capability", CapabilityChange: &request}); err != nil {
				t.Fatal(err)
			}
			_, err = a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: asset.ID, Mode: "enabled"}}})
			valid := scenario == "directory" || scenario == "file" || scenario == "symlink" || scenario == "shared-backends"
			if (err == nil) != valid {
				t.Fatalf("wrong source admission: %v", err)
			}
			if !valid && f.creates.Load() != 0 {
				t.Fatal("invalid original resource dispatched native create")
			}
		})
	}
}

func TestCodexDirectorySkillUpgradeChecksEveryUnchangedDependency(t *testing.T) {
	cfg := codexPluginFixture(t)
	cfg.CapabilityAssets = nil
	for _, id := range []string{"first", "second"} {
		root := t.TempDir()
		entry := filepath.Join(root, "SKILL.md")
		if err := os.WriteFile(entry, []byte("---\nname: "+id+"\ndescription: Owned\n---\nOriginal.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg.CapabilityAssets = append(cfg.CapabilityAssets, CapabilityAsset{ID: id, Name: id, Kind: "skill", Version: "v1", Source: root, Backends: []string{"codex"}, CodexSkill: entry})
	}
	a := openFixture(t, cfg, newFixture())
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "first", Mode: "enabled"}, {AssetID: "second", Mode: "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Scope.NativeID != "" })
	snapshot := a.Snapshot().Sessions[0].ExecutionSnapshot
	asset := cfg.CapabilityAssets[0]
	asset.Version = "v2"
	if err = os.WriteFile(asset.CodexSkill, []byte("---\nname: first\ndescription: Owned v2\n---\nChanged.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	items, _ := a.ReadCapabilities(context.Background())
	revision := ""
	for _, item := range items {
		if item.Asset.ID == asset.ID {
			revision = item.Revision
		}
	}
	libraryChange(t, a, "upgrade-library", CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: revision, Asset: &asset})
	request := ExecutionChangeRequest{BaseSnapshotID: snapshot.ID, Selections: []CapabilitySelection{{AssetID: asset.ID, Mode: "enabled", Version: "v2"}}}
	if _, err = a.PreviewExecutionChange(r.SessionID, request); err != nil {
		t.Fatalf("explicit replacement could not retire old bytes: %v", err)
	}
	if err = verifyExecutionSources(a.Snapshot().Sessions[0]); err == nil {
		t.Fatal("ordinary input lost original source gate")
	}
	if err = os.WriteFile(cfg.CapabilityAssets[1].CodexSkill, []byte("changed unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.PreviewExecutionChange(r.SessionID, request); err == nil {
		t.Fatal("upgrade ignored an unchanged dependency's drift")
	}
	if digest(snapshot) != digest(a.Snapshot().Sessions[0].ExecutionSnapshot) {
		t.Fatal("preview mutated active snapshot")
	}
}

func TestCodexDirectorySkillCreationKeepsNativeEntry(t *testing.T) {
	cfg := codexPluginFixture(t)
	root := t.TempDir()
	entry := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(entry, []byte("---\nname: method\ndescription: Owned method\n---\nUse the original method.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"id": "method", "name": "Method", "kind": "skill", "version": "v1", "source": root, "backends": []string{"codex"}, "codex_skill": entry})
	var asset CapabilityAsset
	if err := json.Unmarshal(data, &asset); err != nil {
		t.Fatal(err)
	}
	cfg.CapabilityAssets = []CapabilityAsset{asset}
	a := openFixture(t, cfg, newFixture())
	r, err := a.Apply(Command{ID: "native-skill", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "method", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Snapshot.PluginSelections) != 0 || len(read.Snapshot.SkillSelections) != 1 {
		t.Fatal("native directory mapping was replaced by private plugin delivery")
	}
	skill := read.Snapshot.SkillSelections[0]
	if skill.Name != "method" || skill.NativeKey != entry || skill.DiscoveryPath != entry || skill.SourcePath != entry || skill.ResourceRoot != root || skill.ResourceHash != read.Snapshot.CapabilityRefs[0].Source.SHA256 {
		t.Fatal("native entry or complete original resource binding lost")
	}
}
