package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestCodexPortableHooksRemainUnavailableWithoutNativeDefinitions(t *testing.T) {
	for _, layout := range []string{"root", "inline", "fallback"} {
		t.Run(layout, func(t *testing.T) {
			cfg := codexPluginFixture(t)
			cfg.Instances[0].ServerExecutable = "/owned/server"
			root := cfg.CapabilityAssets[0].Source
			identity := map[string]any{"$schema": agentPluginSchema, "name": "fixture-plugin", "version": "1.2.3"}
			path := "hooks/hooks.json"
			overlay := map[string]any{"name": "ignored-identity", "version": "9.9.9", "hooks": "./ignored-hooks.json"}
			if layout == "inline" {
				path = "hooks/selected.json"
				identity["extensions"] = map[string]any{"com.openai": map[string]any{"hooks": "./" + path}}
			}
			if layout == "fallback" {
				path = "hooks/selected.json"
				overlay["hooks"] = "./" + path
			}
			write := func(name string, value any) {
				t.Helper()
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(root, name)
				if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(p, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("plugin.json", identity)
			if layout == "root" {
				if err := os.Remove(filepath.Join(root, ".codex-plugin/plugin.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				write(".codex-plugin/plugin.json", overlay)
			}
			write(path, map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "true", "timeout": 1}}}}}})
			write("ignored-hooks.json", map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "missing-ignored-hook"}}}}}})
			f := &capabilityHookReader{fixtureRuntime: newFixture()}
			f.read = func(_ context.Context, i runtime.Instance, p runtime.PluginHookPackage) (runtime.PluginHookInspection, error) {
				return fixtureHookInspection(i, p), nil
			}
			a := openFixture(t, cfg, f)
			item := libraryRead(t, a, "plugin")
			_, err := a.InspectCapabilityHooks(context.Background(), CapabilityHookInspectionRequest{AssetID: "plugin", InstanceID: cfg.Instances[0].ID, Revision: item.Revision, SourceSHA256: item.Source.SHA256})
			if err == nil || f.calls.Load() != 0 {
				t.Fatal("portable hooks reached native inspection without a verified contract")
			}
			for _, mode := range []string{"enabled", "disabled"} {
				_, err := a.Apply(Command{ID: layout + mode, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: mode, HookReview: &CapabilityHookReview{InspectionID: "unverified", Trust: "trusted"}}}})
				if err == nil || f.creates.Load() != 0 {
					t.Fatal("portable hook selection created an execution")
				}
			}
		})
	}
}
