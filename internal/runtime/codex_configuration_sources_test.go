package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexConfigurationSourceAdmission(t *testing.T) {
	for _, scenario := range []string{"unchanged", "bytes", "mode", "missing", "merged-value", "new-layer", "bad-version", "wrong-cwd", "policy-tightened", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind, scope.Instance.Profile, scope.Cwd = "codex", t.TempDir(), t.TempDir()
			path := filepath.Join(scope.Instance.Profile, "config.toml")
			projectWrite(t, path, "model='fixture'\n")
			name := map[string]any{"type": "user", "file": path, "profile": nil}
			version := "sha256:" + strings.Repeat("1", 64)
			response := map[string]any{"config": map[string]any{"model": "fixture"}, "origins": map[string]any{"model": map[string]any{"name": name, "version": version}}, "layers": []any{map[string]any{"name": name, "version": version, "config": map[string]any{"model": "fixture"}}}}
			policy := false
			f.call = func(method string, params map[string]any) (any, error) {
				if method == "config/read" {
					return response, nil
				}
				if method == "configRequirements/read" {
					if policy {
						return map[string]any{"requirements": map[string]any{"allowManagedHooksOnly": true}}, nil
					}
					return map[string]any{"requirements": nil}, nil
				}
				t.Fatalf("source guard reached unrelated RPC %s", method)
				return nil, nil
			}
			var err error
			scope.CodexConfigurationSources, err = c.ReadCodexConfigurationSources(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "bytes":
				projectWrite(t, path, "# changed bytes\nmodel='fixture'\n")
			case "mode":
				if err := os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "merged-value":
				response["config"] = map[string]any{"model": "changed"}
			case "new-layer":
				response["layers"] = append(response["layers"].([]any), map[string]any{"name": map[string]any{"type": "sessionFlags"}, "version": version, "config": map[string]any{}})
			case "bad-version":
				scope.CodexConfigurationSources.Version = 99
			case "wrong-cwd":
				scope.CodexConfigurationSources.Cwd = "/other"
			case "policy-tightened":
				policy = true
			case "legacy":
				scope.CodexConfigurationSources = nil
			}
			f.methods = nil
			err = verifyCodexConfigurationSources(context.Background(), f, scope)
			pass := scenario == "unchanged" || scenario == "policy-tightened" || scenario == "legacy"
			if (err == nil) != pass {
				t.Fatalf("incorrect source admission: %v", err)
			}
			if scenario == "legacy" && len(f.methods) != 0 {
				t.Fatal("legacy snapshot acquired new checks")
			}
			if !pass {
				_, err = c.Execute(context.Background(), Intent{Kind: "send", Scope: scope, Permission: "read_only", Text: "must not dispatch"})
				var notSent *NotSent
				if !errors.As(err, &notSent) {
					t.Fatal("source drift not refused before send", err)
				}
			}
		})
	}
}

func TestCodexHookConfigurationTransitionOnlyEmptyLayers(t *testing.T) {
	for _, scenario := range []string{"add", "remove", "file-present", "nonempty", "other-layer-change", "reordered-common", "changed-value", "changed-origin", "unrelated-path", "legacy-upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			empty := configurationDigest(map[string]any{})
			base := ConfigurationLayer{ID: "user", Kind: "user", Version: "sha256:" + empty, SHA256: "original"}
			system := ConfigurationLayer{ID: "system", Kind: "system", Version: "sha256:" + empty, SHA256: "system"}
			extra := ConfigurationLayer{ID: "project", Kind: "project", Version: "sha256:" + empty, ConfigurationSHA256: empty, File: &FileSource{Path: "/work/.codex/config.toml", Kind: "absent"}}
			old := Scope{CodexConfigurationSources: &CodexConfigurationSources{Version: 1, Profile: "/profile", Cwd: "/work", ConfigurationData: ConfigurationData{ConfigurationSHA256: "same", OriginsSHA256: "same", LayersSHA256: "old", Layers: []ConfigurationLayer{base, system}}}}
			target := old
			copy := *old.CodexConfigurationSources
			copy.Layers = []ConfigurationLayer{extra, base, system}
			copy.LayersSHA256 = "new"
			target.CodexConfigurationSources = &copy
			target.CodexProjectHooks = &CodexProjectHookSources{Files: []CodexProjectHookFile{{Declared: true, File: FileSource{Path: "/work/.codex/hooks.json"}}}}
			switch scenario {
			case "remove":
				old, target = target, old
			case "file-present":
				target.CodexConfigurationSources.Layers[0].File.Kind = "file"
			case "nonempty":
				target.CodexConfigurationSources.Layers[0].ConfigurationSHA256 = "changed"
			case "other-layer-change":
				target.CodexConfigurationSources.Layers[1].SHA256 = "hidden-metadata-changed"
			case "reordered-common":
				target.CodexConfigurationSources.Layers = []ConfigurationLayer{extra, system, base}
			case "changed-value":
				target.CodexConfigurationSources.ConfigurationSHA256 = "different"
			case "changed-origin":
				target.CodexConfigurationSources.OriginsSHA256 = "different"
			case "unrelated-path":
				target.CodexProjectHooks = nil
			case "legacy-upgrade":
				old.CodexConfigurationSources = nil
			}
			if CodexHookConfigurationTransition(old, target) != (scenario == "add" || scenario == "remove") {
				t.Fatal("configuration transition widened")
			}
		})
	}
}
