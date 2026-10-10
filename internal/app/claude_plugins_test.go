package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codex-netcap/internal/runtime"
)

func claudePluginFixture(t *testing.T) Config {
	t.Helper()
	c, _ := claudeSkillFixture(t)
	root := t.TempDir()
	for path, body := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"fixture-plugin","version":"1.2.3"}`,
		"skills/method/SKILL.md":     "---\nname: method-name\ndescription: fixture method\n---\nPRIVATE_BODY\n",
		"commands/action.md":         "---\ndescription: command\n---\nPRIVATE_BODY\n",
		"agents/check.md":            "---\nname: verifier\ndescription: check\n---\nPRIVATE_BODY\n",
		"hooks/hooks.json":           `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"exit 7"}]}]}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "plugin", Name: "Fixture plugin", Kind: "cli_plugin", Version: "asset-v1", Source: root, Backends: []string{"claude"}}}
	return c
}

func TestClaudePluginCreationRejectsUnverifiableSources(t *testing.T) {
	for _, mode := range []string{"unknown-component", "installed-collision", "duplicate-name", "broken-manifest", "codex"} {
		t.Run(mode, func(t *testing.T) {
			c := claudePluginFixture(t)
			source := c.CapabilityAssets[0].Source
			write := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			picks := []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}
			switch mode {
			case "unknown-component":
				write(filepath.Join(source, "settings.json"), `{}`)
			case "installed-collision":
				write(filepath.Join(c.Instances[0].Profile, "plugins/installed_plugins.json"), `{"plugins":{"fixture-plugin@market":[]}}`)
			case "broken-manifest":
				write(filepath.Join(source, ".claude-plugin/plugin.json"), `{}`)
			case "duplicate-name":
				other := c.CapabilityAssets[0]
				other.ID = "other"
				c.CapabilityAssets = append(c.CapabilityAssets, other)
				picks = append(picks, CapabilitySelection{AssetID: "other", Mode: "disabled"})
			case "codex":
				c.Instances[0].Kind = "codex"
			}
			f := newFixture()
			a := openFixture(t, c, f)
			_, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: picks})
			if err == nil {
				t.Fatal("unverifiable plugin accepted")
			}
			if f.creates.Load() != 0 || f.sends.Load() != 0 {
				t.Fatal("invalid source dispatched native work")
			}
		})
	}
}

func TestClaudePluginSelectionCapturesResourceVersionAndComponents(t *testing.T) {
	c := claudePluginFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	r, err := a.Apply(Command{ID: "plugin-create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Snapshot.PluginSelections) != 1 {
		t.Fatal("plugin was not bound")
	}
	p := read.Snapshot.PluginSelections[0]
	if p.AssetID != "plugin" || p.Name != "fixture-plugin" || p.Version != "1.2.3" || p.Mode != "enabled" || len(p.Files) == 0 || len(p.Components) != 4 || p.SourcePath != c.CapabilityAssets[0].Source || len(p.SourceHash) != 64 {
		t.Fatalf("wrong plugin binding: %+v", p)
	}
	if read.Snapshot.CapabilityRefs[0].Asset.Version != "asset-v1" {
		t.Fatal("native manifest version replaced asset version")
	}
	for _, component := range p.Components {
		if component.Kind == "agent" && component.NativeName != "fixture-plugin:verifier" {
			t.Fatal("agent identity guessed from file name")
		}
	}
	if f.sends.Load() != 0 {
		t.Fatal("selection sent model input")
	}
}

func TestClaudePluginChangePreviewPreservesCurrentBindings(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "inherit", "new"} {
		t.Run(mode, func(t *testing.T) {
			c := claudePluginFixture(t)
			mcp := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"identity":{"command":"fixture"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: "mcp", Name: "MCP", Kind: "mcp", Version: "v1", Source: mcp, Backends: []string{"claude"}})

			a := openFixture(t, c, newFixture())
			picks := []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}
			if mode == "new" {
				picks = nil
			}
			created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: picks})
			if err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
			before, _ := a.ReadExecution(created.SessionID)
			pick := mode
			if pick == "new" {
				pick = "enabled"
			}
			preview, err := a.PreviewExecutionChange(created.SessionID, ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: pick}, {AssetID: "mcp", Mode: "enabled"}}})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "enabled" && (len(preview.Changes) != 2 || !slices.ContainsFunc(preview.Changes, func(change CapabilitySelectionChange) bool {
				return change.Kind == "cli_plugin" && change.After == pick
			})) {
				t.Fatalf("missing explicit plugin change: %+v", preview.Changes)
			}
			after, _ := a.ReadExecution(created.SessionID)
			if digest(after.Snapshot) != digest(before.Snapshot) || after.PendingChange != nil {
				t.Fatal("preview altered the effective binding")
			}
		})
	}
}

func TestClaudePluginBindingSurvivesMCPChangeLostReceiptAndRestart(t *testing.T) {
	claudeMixedChangeLostReceipt(t, false)
}

func TestClaudePluginChangeLostReceiptAndRestart(t *testing.T) {
	claudeMixedChangeLostReceipt(t, true)
}

func claudeMixedChangeLostReceipt(t *testing.T, pluginChange bool) {
	t.Helper()
	c := claudePluginFixture(t)
	mcp := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"identity":{"command":"fixture"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: "mcp", Name: "MCP", Kind: "mcp", Version: "v1", Source: mcp, Backends: []string{"claude"}})
	f := &applicationConfigurationFixture{fixtureRuntime: newFixture()}
	f.resolveApply.Store(true)
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "mixed-create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}, {AssetID: "mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	asset, pluginMode, mcpMode := "mcp", "enabled", "disabled"
	if pluginChange {
		asset, pluginMode, mcpMode = "plugin", "disabled", "enabled"
	}
	f.durable = func(in runtime.Intent) {
		if len(in.Scope.PluginSelections) != 1 || in.Scope.PluginSelections[0].Mode != pluginMode || in.Scope.MCPSelections[0].Mode != mcpMode {
			t.Error("change dispatched the wrong mixed binding")
		}
	}
	r := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: asset, Mode: "disabled"}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	_, err = a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &r})
	if err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(created.SessionID)
	ref := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, PendingChangeID: staged.PendingChange.ID}
	for _, kind := range []string{"prepare_execution", "apply_execution"} {
		var receipt CommandReceipt
		waitTest(t, func() bool {
			receipt, err = a.Apply(Command{ID: kind, Kind: kind, SessionID: created.SessionID, ExecutionChange: &ref})
			return err == nil
		})
		waitTest(t, func() bool {
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID {
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}
	after, _ := a.ReadExecution(created.SessionID)
	if after.PendingChange != nil || digest(after.Snapshot) != digest(staged.PendingChange.Snapshot) || digest(after.History[0]) != digest(before.Snapshot) || after.Snapshot.PluginSelections[0].Mode != pluginMode {
		t.Fatal("lost receipt lost pinned mixed bindings or version history")
	}
	if f.applications.Load() != 1 {
		t.Fatal("lost reply replayed configuration")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	restored, _ := a.ReadExecution(created.SessionID)
	if digest(restored.Snapshot) != digest(after.Snapshot) || restored.Snapshot.PluginSelections[0].Mode != pluginMode || restored.Snapshot.MCPSelections[0].Mode != mcpMode || digest(restored.History) != digest(after.History) {
		t.Fatal("restart lost plugin or MCP binding")
	}
}
