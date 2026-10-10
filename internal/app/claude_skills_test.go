package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func claudeSkillFixture(t *testing.T) (Config, string) {
	t.Helper()
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "shared-resource")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: displayed-method\ndescription: fixture method\n---\nPRIVATE_METHOD_BODY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.Instances[0].Profile, "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("not a Skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(root, "native-key")); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared", Name: "Shared method", Kind: "skill", Version: "v1", Source: source, Backends: []string{"claude"}}}
	return c, source
}

func TestClaudeExternalSkillBindingIsIsolatedAndReadOnly(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			c, source := claudeSkillFixture(t)
			if err := os.Remove(filepath.Join(c.Instances[0].Profile, "skills/native-key")); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				c.CapabilityAssets[0].Source = filepath.Join(source, "SKILL.md")
			}
			a := openFixture(t, c, newFixture())
			var bindings []runtime.SkillSelection
			for _, mode := range []string{"enabled", "disabled"} {
				r, err := a.Apply(Command{ID: mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: mode}}})
				if err != nil {
					t.Fatal(err)
				}
				read, err := a.ReadExecution(r.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				s := read.Snapshot.SkillSelections[0]
				if s.Delivery != "local_plugin" || s.Source != "plugin" || s.ResourceKind != kind || s.SourcePath != filepath.Join(source, "SKILL.md") || s.Mode != mode || s.Name != s.PluginName+":displayed-method" || len(s.ResourceHash) != 64 {
					t.Fatalf("wrong delivery: %+v", s)
				}
				if _, err := os.Lstat(filepath.Dir(filepath.Dir(s.DiscoveryPath))); !os.IsNotExist(err) {
					t.Fatal("capture materialized the resource")
				}
				bindings = append(bindings, s)
			}
			if bindings[0].PluginName == bindings[1].PluginName || bindings[0].DiscoveryPath == bindings[1].DiscoveryPath {
				t.Fatal("shared asset shares delivery namespace")
			}
			if bindings[0].ResourceRoot != bindings[1].ResourceRoot || bindings[0].ResourceHash != bindings[1].ResourceHash {
				t.Fatal("shared source was copied or changed")
			}
		})
	}
}

func TestClaudeSkillSelectionRecordsAliasAndKeepsOriginalBinding(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			c, source := claudeSkillFixture(t)
			f := newFixture()
			a := openFixture(t, c, f)
			r, err := a.Apply(Command{ID: "select", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: mode}}})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			s := a.Snapshot().Sessions[0]
			read, err := a.ReadExecution(r.SessionID)
			if err != nil || len(read.Snapshot.SkillSelections) != 1 || len(read.Snapshot.CapabilityRefs) != 1 {
				t.Fatalf("missing selection: %+v %v", read, err)
			}
			pick := read.Snapshot.SkillSelections[0]
			if pick.Name != "displayed-method" || pick.NativeKey != "native-key" || pick.Source != "userSettings" || pick.Mode != mode || pick.SourcePath != filepath.Join(source, "SKILL.md") || pick.DiscoveryPath != filepath.Join(c.Instances[0].Profile, "skills/native-key/SKILL.md") {
				t.Fatalf("wrong bound alias: %+v", pick)
			}
			if digest(s.Scope.SkillSelections) != digest(read.Snapshot.SkillSelections) || verifyExecutionBinding(s) != nil {
				t.Fatal("scope lost snapshot selection")
			}
			b, _ := json.Marshal(read.Snapshot)
			if strings.Contains(string(b), "PRIVATE_METHOD_BODY") {
				t.Fatal("body leaked into snapshot")
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openFixture(t, c, f)
			after, _ := a.ReadExecution(r.SessionID)
			if digest(after.Snapshot) != digest(read.Snapshot) {
				t.Fatal("restart replaced original version")
			}
			s.Scope.SkillSelections[0].NativeKey = "wrong"
			if verifyExecutionBinding(s) == nil {
				t.Fatal("modified native key accepted")
			}
		})
	}
}

func TestClaudeSkillSelectionRejectsAmbiguousOrUnavailableSource(t *testing.T) {
	for _, mode := range []string{"same-key", "same-source-two-aliases", "native-hardlink", "external-hardlink", "duplicate-choice", "invalid-mode", "unsupported-backend"} {
		t.Run(mode, func(t *testing.T) {
			c, source := claudeSkillFixture(t)
			pick := CapabilitySelection{AssetID: "shared", Mode: "disabled"}
			picks := []CapabilitySelection{pick}
			switch mode {
			case "same-key":
				other := filepath.Join(c.Projects[0].Cwd, ".claude/skills/native-key")
				if err := os.MkdirAll(other, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("---\nname: another\ndescription: fixture\n---\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "same-source-two-aliases":
				if err := os.Symlink(source, filepath.Join(c.Instances[0].Profile, "skills/another-key")); err != nil {
					t.Fatal(err)
				}
			case "native-hardlink", "external-hardlink":
				if mode == "external-hardlink" {
					if err := os.Remove(filepath.Join(c.Instances[0].Profile, "skills/native-key")); err != nil {
						t.Fatal(err)
					}
				}
				alias := filepath.Join(c.Projects[0].Cwd, ".claude/skills/other-key")
				if err := os.MkdirAll(alias, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(source, "SKILL.md"), filepath.Join(alias, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			case "duplicate-choice":
				picks = append(picks, pick)
			case "invalid-mode":
				picks[0].Mode = "on"
			case "unsupported-backend":
				c.Instances[0].Kind = "opencode"
			}
			f := newFixture()
			a := openFixture(t, c, f)
			_, err := a.Apply(Command{ID: "bad", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: picks})
			if err == nil || f.creates.Load() != 0 || len(a.Snapshot().Sessions) != 0 {
				t.Fatalf("unverified selection dispatched: %v", err)
			}
		})
	}
}

func TestClaudeSkillIndependentCopyKeepsSourceClassification(t *testing.T) {
	for _, delivery := range []string{"natural", "external"} {
		t.Run(delivery, func(t *testing.T) {
			c, source := claudeSkillFixture(t)
			if delivery == "external" {
				if err := os.Remove(filepath.Join(c.Instances[0].Profile, "skills/native-key")); err != nil {
					t.Fatal(err)
				}
			}
			other := filepath.Join(c.Projects[0].Cwd, ".claude/skills/copied-key")
			if err := os.MkdirAll(other, 0700); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(source, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "SKILL.md"), body, 0600); err != nil {
				t.Fatal(err)
			}
			a := openFixture(t, c, newFixture())
			r, err := a.Apply(Command{ID: "copy-control", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "disabled"}}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := a.ReadExecution(r.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			pick := read.Snapshot.SkillSelections[0]
			if pick.SourcePath != filepath.Join(source, "SKILL.md") || (pick.Delivery == "local_plugin") != (delivery == "external") {
				t.Fatalf("independent copy changed source classification: %+v", pick)
			}
		})
	}
}

func TestClaudeSkillBindingPreservedInMCPChangeCandidate(t *testing.T) {
	c, _ := claudeSkillFixture(t)
	mcp := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"identity":{"command":"fixture"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: "mcp", Name: "MCP", Kind: "mcp", Version: "v1", Source: mcp, Backends: []string{"claude"}})
	a := openFixture(t, c, newFixture())
	r, err := a.Apply(Command{ID: "mixed", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "disabled"}, {AssetID: "mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.PreviewExecutionChange(r.SessionID, ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "mcp", Mode: "disabled", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Apply(Command{ID: "save-mcp", Kind: "stage_execution", SessionID: r.SessionID, ExecutionChange: &ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PreviewHash: preview.PreviewHash, Selections: []CapabilitySelection{{AssetID: "mcp", Mode: "disabled", Version: "v1"}}}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if digest(after.PendingChange.Snapshot.SkillSelections) != digest(before.Snapshot.SkillSelections) || len(after.PendingChange.Snapshot.CapabilityRefs) != 2 || digest(after.Snapshot) != digest(before.Snapshot) {
		t.Fatal("MCP proposal changed the Skill binding or current snapshot")
	}
}
