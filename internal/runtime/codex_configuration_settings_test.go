package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexConfigurationSettingsComparison(t *testing.T) {
	for _, mode := range []string{"same", "effort", "hidden-effort", "hidden-other", "provider", "permissions", "nested-effort", "other-origin", "layer-order", "layer-metadata", "format", "mode", "unset", "unknown-effort", "unknown-effort-changed"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind, scope.Instance.Profile = "codex", t.TempDir()
			scope.Cwd = t.TempDir()
			userPath := filepath.Join(scope.Instance.Profile, "config.toml")
			projectPath := filepath.Join(scope.Cwd, ".codex", "config.toml")
			if err := os.MkdirAll(filepath.Dir(projectPath), 0700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{userPath, projectPath} {
				if err := os.WriteFile(path, []byte("PRIVATE_FILE"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			version := "sha256:" + strings.Repeat("a", 64)
			userName := map[string]any{"type": "user", "file": userPath, "profile": nil}
			projectName := map[string]any{"type": "project", "dotCodexFolder": filepath.Dir(projectPath)}
			userValues := map[string]any{"model_reasoning_effort": "low", "model": "PRIVATE_HIDDEN", "api_key": "PRIVATE_TOKEN"}
			projectValues := map[string]any{"model_reasoning_effort": "high", "model": "PRIVATE_MODEL"}
			config := map[string]any{"model_reasoning_effort": "high", "model": "PRIVATE_MODEL", "api_key": "PRIVATE_TOKEN"}
			if mode == "unknown-effort-changed" {
				config["model_reasoning_effort"] = "PRIVATE_OLD"
			}
			user := map[string]any{"name": userName, "version": version, "config": userValues, "disabledReason": nil}
			project := map[string]any{"name": projectName, "version": version, "config": projectValues, "disabledReason": nil}
			modelOrigin := map[string]any{"name": projectName, "version": version}
			origins := map[string]any{"model": modelOrigin, "model_reasoning_effort": map[string]any{"name": projectName, "version": version}, "api_key": map[string]any{"name": userName, "version": version}}
			response := map[string]any{"config": config, "layers": []any{project, user}, "origins": origins}
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return response, nil
				case "configRequirements/read":
					return map[string]any{"requirements": nil}, nil
				default:
					t.Fatalf("unexpected mutation %s", method)
					return nil, nil
				}
			}
			read := func() ConfigurationCatalog {
				t.Helper()
				out, err := c.ReadConfigurationCatalog(context.Background(), scope)
				if err != nil {
					t.Fatal(err)
				}
				b, _ := json.Marshal(out)
				if strings.Contains(string(b), "PRIVATE_") {
					t.Fatal("configuration values leaked")
				}
				return out
			}
			before := read()
			want := ConfigurationSettingsDifference{Status: "changed"}
			switch mode {
			case "same":
				want.Status = "unchanged"
			case "effort":
				config["model_reasoning_effort"], projectValues["model_reasoning_effort"] = "medium", "medium"
				project["version"] = "sha256:" + strings.Repeat("b", 64)
				modelOrigin["version"], origins["model_reasoning_effort"].(map[string]any)["version"] = project["version"], project["version"]
				want.EffortChanged, want.EffortSourcesChanged = true, true
			case "hidden-effort":
				userValues["model_reasoning_effort"] = "medium"
				want.EffortSourcesChanged = true
			case "hidden-other":
				userValues["model"] = "PRIVATE_CHANGED"
				want.OtherSourcesChanged = true
			case "provider", "permissions", "nested-effort":
				key := map[string]string{"provider": "model_provider", "permissions": "approval_policy", "nested-effort": "profiles"}[mode]
				config[key], userValues[key] = map[string]any{"model_reasoning_effort": "PRIVATE_NESTED"}, map[string]any{"model_reasoning_effort": "PRIVATE_NESTED"}
				want.OtherValuesChanged, want.OtherSourcesChanged = true, true
			case "other-origin":
				modelOrigin["name"] = userName
				want.OtherSourcesChanged = true
			case "layer-order":
				response["layers"] = []any{user, project}
				want.OtherSourcesChanged, want.EffortSourcesChanged = true, true
			case "layer-metadata":
				user["future_metadata"] = "PRIVATE_METADATA"
				want.OtherSourcesChanged = true
			case "format":
				if err := os.WriteFile(userPath, []byte("PRIVATE_FORMAT_CHANGED"), 0600); err != nil {
					t.Fatal(err)
				}
				want.Status = "unchanged"
			case "mode":
				if err := os.Chmod(userPath, 0640); err != nil {
					t.Fatal(err)
				}
				want.Status = "unchanged"
			case "unset":
				config["model_reasoning_effort"] = nil
				delete(projectValues, "model_reasoning_effort")
				delete(userValues, "model_reasoning_effort")
				delete(origins, "model_reasoning_effort")
				want.EffortChanged, want.EffortSourcesChanged = true, true
			case "unknown-effort", "unknown-effort-changed":
				config["model_reasoning_effort"] = "PRIVATE_UNKNOWN"
				want.EffortChanged = true
			}
			after := read()
			got := CompareConfigurationSettings(before.Settings, after.Settings)
			if got.Before == nil || got.After == nil {
				t.Fatal("missing before/after")
			}
			want.Before, want.After = got.Before, got.After
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%+v, want %+v", got, want)
			}
			if mode == "unset" && (got.After.State != "unset" || got.After.Value != "") {
				t.Fatal("invented resolved default")
			}
			if strings.HasPrefix(mode, "unknown-effort") && (got.After.State != "unrecognized" || got.After.Value != "") {
				t.Fatal("unrecognized value disclosed")
			}
			if mode == "format" || mode == "mode" {
				if reflect.DeepEqual(before.Layers, after.Layers) {
					t.Fatal("file-only difference lost")
				}
			}
			// A persisted old snapshot still verifies its original evidence; a
			// new read cannot populate its missing settings behind the scenes.
			saved, err := c.ReadCodexConfigurationSources(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Settings == nil {
				t.Fatal("new source snapshot lacks comparison evidence")
			}
			scope.CodexConfigurationSources = saved
			if err := verifyCodexConfigurationSources(context.Background(), f, scope); err != nil {
				t.Fatal(err)
			}
			saved.Settings = nil
			if err := verifyCodexConfigurationSources(context.Background(), f, scope); err != nil {
				t.Fatal(err)
			}
			if saved.Settings != nil || CompareConfigurationSettings(saved.Settings, after.Settings).Status != "unrecorded" {
				t.Fatal("legacy evidence backfilled")
			}
		})
	}
}

func TestCodexConfigurationSettingsRejectInvalidEvidence(t *testing.T) {
	for _, mode := range []string{"version", "hash", "state", "unknown-value"} {
		t.Run(mode, func(t *testing.T) {
			hash := strings.Repeat("a", 64)
			saved := &ConfigurationSettings{Version: 1, ReasoningEffort: ConfigurationEffort{State: "set", Value: "low"}, EffortValueSHA256: hash, EffortSourcesSHA256: hash, OtherConfigurationSHA256: hash, OtherOriginsSHA256: hash, OtherLayersSHA256: hash}
			original := *saved
			switch mode {
			case "version":
				saved.Version++
			case "hash":
				saved.OtherLayersSHA256 = "invalid"
			case "state":
				saved.ReasoningEffort.State = "effective"
			case "unknown-value":
				saved.ReasoningEffort = ConfigurationEffort{State: "unrecognized", Value: "PRIVATE_VALUE"}
			}
			if diff := CompareConfigurationSettings(saved, &original); diff.Status != "unsupported" || diff.Before != nil || diff.After != nil {
				t.Fatal("invalid evidence accepted or disclosed")
			}
		})
	}
}
