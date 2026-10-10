package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeSkillCatalogExplainsSourcesWithoutInventingPaths(t *testing.T) {
	var inventory claudeSkillInventory
	if err := json.Unmarshal([]byte(`{"available":true,"at":"2026-10-02T00:00:00Z","commands":[{"name":"user-method","description":"user metadata","aliases":[]},{"name":"project-method","aliases":[]},{"name":"plugin:method","aliases":["method"]},{"name":"managed-method","aliases":[]},{"name":"custom-method","aliases":[]},{"name":"help","builtin":true,"aliases":[]}],"descriptions":[{"name":"user-method","source":"userSettings"},{"name":"project-method","source":"projectSettings"},{"name":"plugin:method","source":"plugin","plugin_name":"plugin"},{"name":"managed-method","source":"policySettings"},{"name":"custom-method","source":"futureSource"}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	scope := Scope{NativeID: "original", Cwd: "/project", SkillSelections: []SkillSelection{{AssetID: "shared", Name: "plugin:method", Source: "plugin", PluginName: "plugin", Delivery: "local_plugin", Mode: "enabled", DiscoveryPath: "/bound/method/SKILL.md"}}}
	catalog, err := claudeSkillCatalog(scope, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Complete || len(catalog.Items) != 6 || catalog.NativeID != "original" || len(catalog.Limitations) != 3 {
		t.Fatalf("wrong catalog coverage: %+v", catalog)
	}
	for _, item := range catalog.Items {
		switch item.Name {
		case "user-method", "project-method":
			if item.Control != "requires_binding" || item.BoundPath != "" {
				t.Fatal("unregistered source was bound by display name")
			}
		case "managed-method", "custom-method":
			if item.Control != "unavailable" || item.BoundPath != "" {
				t.Fatal("unverified exclusion claimed")
			}
		case "plugin:method":
			if item.Control != "asset_bound" || item.BoundPath != "/bound/method/SKILL.md" || item.AssetID != "shared" || len(item.Aliases) != 1 {
				t.Fatal("saved binding or native alias lost")
			}
		case "help":
			if item.DescriptionListed || item.Source != "builtin" || item.Control != "unavailable" {
				t.Fatal("builtin command promoted to Skill")
			}
		}
	}
}

func TestClaudeSkillCatalogRetainsConflictsAndRejectsMalformedMetadata(t *testing.T) {
	for _, mode := range []string{"duplicate-source", "alias-conflict", "builtin-conflict", "builtin-source", "missing-array", "long-description", "unknown-source"} {
		t.Run(mode, func(t *testing.T) {
			var inv claudeSkillInventory
			_ = json.Unmarshal([]byte(`{"available":true,"commands":[{"name":"method","aliases":["alias"]}],"descriptions":[{"name":"method","source":"userSettings"}]}`), &inv)
			switch mode {
			case "duplicate-source":
				inv.Descriptions = append(inv.Descriptions, claudeSkillDescription{Name: "method", Source: "projectSettings"})
			case "alias-conflict":
				inv.Commands = append(inv.Commands, claudeSkillCommand{Name: "alias"})
			case "builtin-conflict":
				inv.Commands[0].Builtin = true
			case "builtin-source":
				inv.Commands[0].Builtin = true
				inv.Descriptions[0].Source = "built-in"
			case "missing-array":
				inv.Commands = nil
			case "long-description":
				inv.Commands[0].Description = string(make([]byte, 4001))
			case "unknown-source":
				inv.Descriptions[0].Source = "new-native-source"
			}
			catalog, err := claudeSkillCatalog(Scope{}, inv)
			if mode == "missing-array" || mode == "long-description" {
				if err == nil {
					t.Fatal("invalid metadata accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range catalog.Items {
				if item.Name == "method" {
					if mode == "builtin-source" {
						if item.Source != "built-in" || item.Ambiguous || item.Control != "unavailable" {
							t.Fatal("native built-in Skill misreported as conflict")
						}
					} else if mode == "unknown-source" {
						if item.Source != "new-native-source" || item.Control != "unavailable" {
							t.Fatal("unknown source misclassified")
						}
					} else if !item.Ambiguous || item.Control != "ambiguous" {
						t.Fatal("conflict hidden")
					}
				}
			}
			if mode == "duplicate-source" && len(catalog.Items) != 2 {
				t.Fatal("two reported sources collapsed")
			}
		})
	}
}

func TestClaudeSkillCatalogDoesNotStartMissingTransport(t *testing.T) {
	profile := t.TempDir()
	state := filepath.Join(t.TempDir(), "not-created")
	c := NewClaude(state)
	defer c.Close()
	_, err := c.ReadSkillCatalog(context.Background(), Scope{NativeID: "original", Cwd: profile, StateNamespace: profile, Instance: Instance{ID: "claude", Kind: "claude", Profile: profile}})
	if err == nil {
		t.Fatal("missing query accepted")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("catalog created a Runtime transport directory")
	}
}

func TestClaudeSkillCatalogDoesNotGuessBetweenSavedAssets(t *testing.T) {
	inv := claudeSkillInventory{Available: true, Commands: []claudeSkillCommand{{Name: "method"}}, Descriptions: []claudeSkillDescription{{Name: "method", Source: "userSettings"}}}
	scope := Scope{SkillSelections: []SkillSelection{{AssetID: "a", Name: "method", Source: "userSettings", DiscoveryPath: "/a/SKILL.md"}, {AssetID: "b", Name: "method", Source: "userSettings", DiscoveryPath: "/b/SKILL.md"}}}
	catalog, err := claudeSkillCatalog(scope, inv)
	if err != nil || len(catalog.Items) != 1 {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
	item := catalog.Items[0]
	if !item.Ambiguous || item.Control != "ambiguous" || item.AssetID != "" || item.BoundPath != "" {
		t.Fatal("ambiguous saved assets were bound by name")
	}
}
