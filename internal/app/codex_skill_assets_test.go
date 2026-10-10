package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSharedSkillCreationKeepsAssetIdentityAndResourceVersion(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			root := t.TempDir()
			entry := filepath.Join(root, "SKILL.md")
			body := "---\nname: method\ndescription: Shared method\n---\nUse this owned method.\n"
			if kind == "directory" {
				body += "Read [guide](references/guide.md).\n"
				if err := os.Mkdir(filepath.Join(root, "references"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "references/guide.md"), []byte("Owned guide"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(entry, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			source := root
			if kind == "file" {
				source = entry
			}
			cfg.CapabilityAssets = []CapabilityAsset{{ID: "shared", Name: "Shared method", Kind: "skill", Version: "asset-v1", Source: source, Backends: []string{"codex", "claude"}}}
			f := newFixture()
			a := openFixture(t, cfg, f)
			var previous string
			for _, mode := range []string{"enabled", "disabled"} {
				r, err := a.Apply(Command{ID: mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: mode}}})
				if err != nil {
					t.Fatal(err)
				}
				read, err := a.ReadExecution(r.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if len(read.Snapshot.PluginSelections) != 1 || len(read.Snapshot.CapabilityRefs) != 1 {
					t.Fatal("shared Skill binding missing")
				}
				pack, ref := read.Snapshot.PluginSelections[0], read.Snapshot.CapabilityRefs[0]
				if ref.Asset.Kind != "skill" || ref.Asset.Version != "asset-v1" || ref.Source.Resolved != source || pack.AssetID != "shared" || pack.Mode != mode || !strings.HasPrefix(pack.NativeSource, "netcap-skill-") || pack.NativeSource == previous {
					t.Fatal("shared asset identity/version or private namespace lost")
				}
				if len(pack.Components) != 1 || pack.Components[0].Kind != "skill" || !strings.HasSuffix(pack.Components[0].NativeName, ":method") {
					t.Fatal("shared method not exposed as one Skill")
				}
				if kind == "directory" {
					found := false
					for _, file := range pack.Files {
						found = found || file.Path == "skills/method/references/guide.md" && file.Mode == 0640
					}
					if !found {
						t.Fatal("relative guide or permissions lost")
					}
				}
				if _, err := os.Stat(pack.CachePath); !os.IsNotExist(err) {
					t.Fatal("planning materialized a native cache")
				}
				if mode == "enabled" {
					waitTest(t, func() bool {
						for _, session := range a.Snapshot().Sessions {
							if session.ID == r.SessionID {
								return session.Status == "idle" && session.Scope.NativeID != ""
							}
						}
						return false
					})
					for _, next := range []string{"disabled", "inherit"} {
						preview, err := a.PreviewExecutionChange(r.SessionID, ExecutionChangeRequest{BaseSnapshotID: read.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "shared", Mode: next}}})
						if err != nil || len(preview.Changes) != 1 || preview.Changes[0].Kind != "skill" || preview.Changes[0].Name != "method" || preview.Changes[0].BeforeVersion != "asset-v1" {
							t.Fatalf("Skill change mislabeled or lost original version: %+v %v", preview, err)
						}
					}
				}
				previous = pack.NativeSource
			}
			if f.sends.Load() != 0 {
				t.Fatal("selection sent model input")
			}
		})
	}
}
