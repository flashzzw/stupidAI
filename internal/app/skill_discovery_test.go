package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestSessionSkillControlScopes(t *testing.T) {
	for _, scenario := range []string{"user", "repo", "plugin", "system", "admin", "admin-plugin", "unknown-scope", "same-name", "symlink", "hardlink", "missing-during-read", "partial", "error-with-complete", "directory-entry", "over-limit"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := fixtureConfig(t)
			f := &skillFixture{fixtureRuntime: newFixture()}
			a := openFixture(t, cfg, f)
			id := createFixture(t, a).SessionID
			if _, err := a.Apply(Command{ID: "pause", Kind: "takeover", SessionID: id}); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(entry, []byte("BODY_NOT_DISCOVERY_DATA"), 0600); err != nil {
				t.Fatal(err)
			}
			expected := "native_directory"
			f.discover = func(scope runtime.Scope) (runtime.SkillInventory, error) {
				out := runtime.SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", Complete: true, Skills: []runtime.DiscoveredSkill{{Name: "method", Path: entry, Scope: "user", Enabled: false}}}
				switch scenario {
				case "repo", "system", "admin", "unknown-scope":
					out.Skills[0].Scope = scenario
					if scenario == "system" || scenario == "admin" {
						expected = "unsupported"
					} else if scenario == "unknown-scope" {
						expected = "unknown"
					}
				case "plugin":
					out.Skills[0].PluginID = "method@owned-market"
					expected = "plugin_bundle"
				case "admin-plugin":
					out.Skills[0].PluginID = "method@managed-market"
					out.Skills[0].Scope = "admin"
					expected = "unsupported"
				case "same-name", "symlink", "hardlink":
					other := filepath.Join(t.TempDir(), "SKILL.md")
					name := "other-method"
					var err error
					if scenario == "same-name" {
						name = "method"
						err = os.WriteFile(other, []byte("other"), 0600)
					} else if scenario == "symlink" {
						err = os.Symlink(entry, other)
					} else {
						err = os.Link(entry, other)
					}
					if err != nil {
						t.Fatal(err)
					}
					out.Skills = append(out.Skills, runtime.DiscoveredSkill{Name: name, Path: other, Scope: "repo", Enabled: true})
					expected = "ambiguous"
				case "missing-during-read":
					if err := os.Remove(entry); err != nil {
						t.Fatal(err)
					}
					expected = "unknown"
				case "partial", "error-with-complete":
					out.Complete = scenario == "error-with-complete"
					out.Errors = []runtime.SkillScanError{{Detail: "source unavailable"}}
					expected = "unknown"
				case "directory-entry":
					out.Skills[0].Path = filepath.Dir(entry)
					expected = "unknown"
				case "over-limit":
					out.Skills = make([]runtime.DiscoveredSkill, 2049)
				}
				return out, nil
			}
			before := a.Snapshot()
			out, err := a.ReadSessionSkills(context.Background(), id)
			if scenario == "over-limit" {
				if err == nil {
					t.Fatal("oversized adapter result accepted")
				}
				return
			}
			if err != nil || len(out.Controls) == 0 || out.Controls[0].Control != expected || out.Controls[0].Detail == "" {
				t.Fatalf("control: %+v %v", out, err)
			}
			if expected == "ambiguous" && out.Controls[1].Control != expected {
				t.Fatal("only one side of ambiguity marked")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "BODY_NOT_DISCOVERY_DATA") || digest(before) != digest(a.Snapshot()) || f.sends.Load() != 0 {
				t.Fatal("discovery leaked body or changed execution state")
			}
		})
	}
}

func TestSessionSkillDiscoveryKeepsSavedVersionAcrossLibraryUpdate(t *testing.T) {
	cfg := codexPluginFixture(t)
	root := t.TempDir()
	entry := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(entry, []byte("---\nname: method\ndescription: Owned method\n---\nOriginal.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	asset := CapabilityAsset{ID: "method", Name: "Method", Kind: "skill", Version: "v1", Source: root, Backends: []string{"codex"}, CodexSkill: entry}
	cfg.CapabilityAssets = []CapabilityAsset{asset}
	f := &skillFixture{fixtureRuntime: newFixture()}
	a := openFixture(t, cfg, f)
	r, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "method", Mode: "disabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	if _, err = a.Apply(Command{ID: "pause", Kind: "takeover", SessionID: r.SessionID}); err != nil {
		t.Fatal(err)
	}
	before := a.Snapshot().Sessions[0]
	f.discover = func(scope runtime.Scope) (runtime.SkillInventory, error) {
		items, e := a.ReadCapabilities(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		asset.Version = "v2"
		libraryChange(t, a, "register-v2-during-read", CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: items[0].Revision, Asset: &asset})
		return runtime.SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", Complete: true, Skills: []runtime.DiscoveredSkill{{Name: "method", Path: entry, Scope: "user", Enabled: true}}}, nil
	}
	out, err := a.ReadSessionSkills(context.Background(), r.SessionID)
	if err != nil || len(out.SavedSelections) != 1 {
		t.Fatalf("discovery: %+v %v", out, err)
	}
	saved := out.SavedSelections[0]
	if out.SnapshotID != before.ExecutionSnapshot.ID || saved.AssetID != "method" || saved.Version != "v1" || saved.Mode != "disabled" || !saved.Discovered || !out.Skills[0].Enabled {
		t.Fatal("library or directory state relabeled saved selection", out)
	}
	if digest(before) != digest(a.Snapshot().Sessions[0]) {
		t.Fatal("discovery changed saved execution")
	}
	f.discover = func(scope runtime.Scope) (runtime.SkillInventory, error) {
		return runtime.SkillInventory{Cwd: scope.Cwd, Source: "codex.skills/list", Complete: true, Skills: []runtime.DiscoveredSkill{{Name: "method", Path: filepath.Join(root, "other/SKILL.md"), Scope: "user"}}}, nil
	}
	out, err = a.ReadSessionSkills(context.Background(), r.SessionID)
	if err != nil || len(out.SavedSelections) != 1 || out.SavedSelections[0].Discovered || out.SavedSelections[0].Version != "v1" {
		t.Fatal("missing saved path replaced by same name or removed", out, err)
	}
}
