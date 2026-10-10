package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeNativeReadAllowsOriginalAuthenticationWithoutControl(t *testing.T) {
	for _, overlay := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile", true: "custom_settings"}[overlay], func(t *testing.T) {
			c := fixtureConfig(t)
			c.Instances[0].Kind = "claude"
			c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
			if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(c.Instances[0].Profile, "settings.json")
			if overlay {
				path = filepath.Join(c.Instances[0].Profile, "custom.json")
				c.Instances[0].SettingsFile = path
			}
			body := []byte(`{"apiKeyHelper":"must-not-execute","env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9","ANTHROPIC_API_KEY":"fixture-only"}}`)
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			f := &bindingRuntime{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			request := NativePreviewRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
			preview, err := a.PreviewNative(context.Background(), request)
			if err != nil || len(preview.Sessions) != 1 {
				t.Fatalf("original authenticated source unreadable: %+v %v", preview, err)
			}
			bound, err := a.BindNative(context.Background(), NativeBindingRequest{CommandID: "bind", ProjectID: "project", InstanceID: "instance", NativeID: discoveredID, PreviewToken: preview.BindingToken})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ReadBoundNativeHistory(context.Background(), bound.SessionID, ""); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range []Command{
				{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"},
				{ID: "send", Kind: "send", SessionID: bound.SessionID, Text: "must not send"},
				{ID: "resume", Kind: "resume", SessionID: bound.SessionID},
			} {
				if _, err := a.Apply(cmd); err == nil {
					t.Fatalf("read-only source granted %s", cmd.Kind)
				}
			}
			if f.creates.Load() != 0 || f.sends.Load() != 0 || f.observations.Load() != 0 {
				t.Fatal("read activated an execution")
			}
			if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.ReadBoundNativeHistory(context.Background(), bound.SessionID, ""); err == nil {
				t.Fatal("authenticated source drift was ignored")
			}
		})
	}
}

func TestClaudeNativeReadStillRejectsUnprovenSources(t *testing.T) {
	for _, content := range []string{
		`{"env":{"HOME":"/another"}}`,
		`{"env":{"CLAUDE_CONFIG_DIR":"/another"}}`,
		`{"env":{"CLAUDE_CODE_PROJECT_DIR_NAME":"another"}}`,
		`{"env":{"XDG_CONFIG_HOME":"/another"}}`,
		`{"env":{"ANTHROPIC_API_KEY":null}}`,
		`{"apiKeyHelper":{}}`,
		`null`, `broken`,
	} {
		t.Run(content, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Instances[0].Kind = "claude"
			c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
			if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "settings.json"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			f := &bindingRuntime{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			if _, err := a.PreviewNative(context.Background(), NativePreviewRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}); err == nil {
				t.Fatal("unproven source accepted")
			}
			if f.inspections.Load() != 0 {
				t.Fatal("invalid source reached native reader")
			}
		})
	}
}

func TestClaudeNativeReadRequiresReaderAndVerifiedCLI(t *testing.T) {
	for _, scenario := range []string{"missing_bridge", "missing_node", "missing_profile", "unverified_cli", "unsupported_history"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Instances[0].Kind = "claude"
			c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
			if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing_bridge":
				c.Instances[0].Bridge += ".missing"
			case "missing_node":
				c.Instances[0].NodeExecutable = filepath.Join(c.Instances[0].Profile, "missing-node")
			case "missing_profile":
				c.Instances[0].Profile += ".missing"
			}
			f := &bindingRuntime{fixtureRuntime: newFixture()}
			a := openFixture(t, c, f)
			a.mu.Lock()
			if scenario == "unverified_cli" {
				a.instances[0].Description.Compatibility = "unverified"
			}
			if scenario == "unsupported_history" {
				a.instances[0].Description.Capabilities["history"] = "unsupported"
			}
			a.mu.Unlock()
			if _, err := a.PreviewNative(context.Background(), NativePreviewRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}); err == nil {
				t.Fatal("unavailable reader was accepted")
			}
			if f.inspections.Load() != 0 {
				t.Fatal("unavailable reader was invoked")
			}
		})
	}
}
