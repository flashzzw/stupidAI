package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexConfigurationCatalogBoundedAndPrivate(t *testing.T) {
	for _, mode := range []string{"valid", "omitted-disabled", "missing-layers", "missing-origins", "missing-config", "invalid-disabled", "unknown-kind", "wrong-profile", "duplicate-layer", "orphan-origin", "disabled-origin", "bad-version", "native-error", "requirements-error", "policy-drift", "file-drift", "config-drift", "hidden-layer-drift"} {
		t.Run(mode, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Instance.Kind, scope.Instance.Profile = "codex", t.TempDir()
			path := filepath.Join(scope.Instance.Profile, "config.toml")
			if err := os.WriteFile(path, []byte("PRIVATE_FILE_BODY"), 0600); err != nil {
				t.Fatal(err)
			}
			version := "sha256:" + strings.Repeat("a", 64)
			name := map[string]any{"type": "user", "file": path, "profile": nil}
			layer := map[string]any{"name": name, "version": version, "config": map[string]any{"model": "PRIVATE_MODEL"}, "disabledReason": nil}
			origin := map[string]any{"name": name, "version": version}
			response := map[string]any{"config": map[string]any{"model": "PRIVATE_MODEL"}, "layers": []any{layer}, "origins": map[string]any{"model": origin, "PRIVATE_CUSTOM_KEY": origin}}
			switch mode {
			case "missing-layers":
				delete(response, "layers")
			case "missing-origins":
				delete(response, "origins")
			case "missing-config":
				delete(response, "config")
			case "omitted-disabled":
				delete(layer, "disabledReason")
			case "invalid-disabled":
				layer["disabledReason"] = false
			case "unknown-kind":
				name["type"] = "future"
			case "wrong-profile":
				name["file"] = filepath.Join(t.TempDir(), "config.toml")
			case "duplicate-layer":
				response["layers"] = []any{layer, layer}
			case "orphan-origin":
				origin["name"] = map[string]any{"type": "sessionFlags"}
			case "disabled-origin":
				layer["disabledReason"] = "PRIVATE_DIAGNOSTIC"
			case "bad-version":
				layer["version"] = "PRIVATE_BAD_VERSION"
			}
			reads, policies := 0, 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					reads++
					if params["cwd"] != scope.Cwd || params["includeLayers"] != true || len(params) != 2 {
						t.Fatal("scope overridden")
					}
					if mode == "native-error" {
						return nil, errors.New("PRIVATE_RPC_BODY")
					}
					if reads == 2 {
						switch mode {
						case "file-drift":
							if err := os.WriteFile(path, []byte("PRIVATE_CHANGED_BODY"), 0600); err != nil {
								t.Fatal(err)
							}
						case "config-drift":
							response["config"] = map[string]any{"model": "PRIVATE_CHANGED_MODEL"}
						case "hidden-layer-drift":
							layer["private_future_field"] = "PRIVATE_CHANGED_LAYER"
						}
					}
					return response, nil
				case "configRequirements/read":
					policies++
					if mode == "requirements-error" {
						return nil, errors.New("PRIVATE_POLICY_BODY")
					}
					if mode == "policy-drift" && policies == 2 {
						return map[string]any{"requirements": map[string]any{"allowManagedHooksOnly": true}}, nil
					}
					return map[string]any{"requirements": nil}, nil
				default:
					t.Fatalf("catalog dispatched %s", method)
					return nil, nil
				}
			}
			out, err := c.ReadConfigurationCatalog(context.Background(), scope)
			if (err == nil) != (mode == "valid" || mode == "omitted-disabled") {
				t.Fatalf("unexpected catalog acceptance: %+v %v", out, err)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), "PRIVATE_") || err != nil && strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("source/policy contents leaked")
			}
			if err == nil && (out.Complete || out.Scope != "disk_configuration" || out.OtherOriginCount != 1 || len(out.Origins) != 1 || len(out.Layers) != 1 || out.Layers[0].File.SHA256 == "" || reads != 2 || policies != 2) {
				t.Fatalf("lost coverage/identity/file evidence: %+v", out)
			}
		})
	}
}
