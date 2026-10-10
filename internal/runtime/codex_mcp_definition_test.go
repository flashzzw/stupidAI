package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mcpEnvironmentFixture struct {
	*nativeFixture
	values map[string]string
	reads  int
	err    error
}

func (f *mcpEnvironmentFixture) MCPEnvironment(_ context.Context, _ Instance, names []string) (map[string]string, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	values := map[string]string{}
	for _, name := range names {
		if value, exists := f.values[name]; exists {
			values[name] = value
		}
	}
	return values, nil
}

func mcpDefinitionScope(t *testing.T, definition string) Scope {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	body := []byte(`{"mcpServers":{"identity":` + definition + `}}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return Scope{Instance: Instance{ID: "codex", Kind: "codex", Profile: filepath.Dir(path), EnvAllow: []string{"TOKEN", "OPTIONAL"}},
		NativeID: "native", Cwd: "/owned/project", MCPSelections: []MCPSelection{{AssetID: "shared", Server: "identity", Mode: "enabled", SourcePath: path,
			Definition: &MCPDefinition{Version: 1, SHA256: hex.EncodeToString(sum[:])}}}}
}

func emptyMCPConfig(method string, params map[string]any) (any, error) {
	if method != "config/read" {
		return nil, errors.New("unexpected native write")
	}
	return map[string]any{"config": map[string]any{"mcp_servers": map[string]any{}}, "layers": []any{}}, nil
}

func TestCodexMCPDefinitionPinsDaemonReferencesWithoutCopyingValues(t *testing.T) {
	scope := mcpDefinitionScope(t, `{"command":"python3","args":["scripts/server.py"],"env":{"SECRET":"${TOKEN}","FALLBACK":"${OPTIONAL:-owned}"}}`)
	f := &mcpEnvironmentFixture{nativeFixture: &nativeFixture{call: emptyMCPConfig}, values: map[string]string{"TOKEN": "PRIVATE_DAEMON_TOKEN"}}
	selections, configs, err := codexMCPDefinitionConfigs(context.Background(), f, scope, true)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(selections)
	if strings.Contains(string(metadata), "PRIVATE_DAEMON_TOKEN") || strings.Contains(string(metadata), "python3") || len(selections[0].Definition.Environment) != 2 {
		t.Fatal("source payload or credentials entered the binding")
	}
	if configs["identity"]["cwd"] != scope.Cwd || configs["identity"]["args"].([]string)[0] != filepath.Join(filepath.Dir(scope.MCPSelections[0].SourcePath), "scripts/server.py") {
		t.Fatal("resource-relative script and execution directory were conflated")
	}
	env := configs["identity"]["env"].(map[string]string)
	if env["SECRET"] != "PRIVATE_DAEMON_TOKEN" || env["FALLBACK"] != "owned" {
		t.Fatal("explicit daemon reference or missing-value fallback was not resolved")
	}
	scope.MCPSelections = selections
	if err := ValidateCodexMCPDefinitions(scope); err != nil {
		t.Fatal(err)
	}
	overrides := map[string]any{"unrelated": true}
	if err := addCodexMCPOverrides(context.Background(), f, scope, overrides); err != nil || overrides["unrelated"] != true || overrides["mcp_servers.identity.enabled"] != true {
		t.Fatalf("valid shared definition was not applied: %v", err)
	}
	f.values["TOKEN"] = "ROTATED_DAEMON_TOKEN"
	if err := addCodexMCPOverrides(context.Background(), f, scope, map[string]any{}); err == nil || strings.Contains(err.Error(), "ROTATED") {
		t.Fatal("rotated environment was accepted or disclosed")
	}
}

func TestCodexMCPDisabledDefinitionDoesNotReadUnusedCredentials(t *testing.T) {
	scope := mcpDefinitionScope(t, `{"command":"python3","env":{"SECRET":"${MISSING_TOKEN}"}}`)
	scope.MCPSelections[0].Mode = "disabled"
	scope.Instance.EnvAllow = nil
	f := &mcpEnvironmentFixture{nativeFixture: &nativeFixture{call: emptyMCPConfig}, err: errors.New("must not read unused credentials")}
	selections, configs, err := codexMCPDefinitionConfigs(context.Background(), f, scope, true)
	if err != nil || f.reads != 0 || configs["identity"]["enabled"] != false || len(configs["identity"]["env"].(map[string]string)) != 0 {
		t.Fatalf("disabled definition required credential access: %v", err)
	}
	scope.MCPSelections = selections
	if err := ValidateCodexMCPDefinitions(scope); err != nil {
		t.Fatal(err)
	}
}

func TestCodexMCPDefinitionRejectsUnsupportedOrAmbiguousSource(t *testing.T) {
	for name, definition := range map[string]string{
		"args-null":        `{"command":"python3","args":null}`,
		"env-null":         `{"command":"python3","env":null}`,
		"unknown-field":    `{"command":"python3","unreviewed":true}`,
		"remote":           `{"type":"http","url":"https://example.test/mcp"}`,
		"invalid-env-name": `{"command":"python3","env":{"BAD=NAME":"x"}}`,
		"nested-fallback":  `{"command":"python3","env":{"KEY":"${TOKEN:-${OTHER}}"}}`,
		"nul":              `{"command":"python3","args":["\u0000"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			scope := mcpDefinitionScope(t, definition)
			f := &mcpEnvironmentFixture{nativeFixture: &nativeFixture{call: emptyMCPConfig}}
			if _, _, err := codexMCPDefinitionConfigs(context.Background(), f, scope, true); err == nil {
				t.Fatal("unsupported definition was silently reinterpreted")
			}
		})
	}
}

func TestCodexMCPDefinitionRefusesSourceDriftAndMissingBindingEvidence(t *testing.T) {
	for _, mode := range []string{"file-change", "missing-environment-proof", "wrong-environment-key", "missing-variable", "disallowed-variable", "reader-failure", "native-collision"} {
		t.Run(mode, func(t *testing.T) {
			scope := mcpDefinitionScope(t, `{"command":"python3","env":{"KEY":"${TOKEN}"}}`)
			f := &mcpEnvironmentFixture{nativeFixture: &nativeFixture{call: emptyMCPConfig}, values: map[string]string{"TOKEN": "private"}}
			selections, _, err := codexMCPDefinitionConfigs(context.Background(), f, scope, true)
			if err != nil {
				t.Fatal(err)
			}
			scope.MCPSelections = selections
			switch mode {
			case "file-change":
				if err := os.WriteFile(selections[0].SourcePath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-environment-proof":
				selections[0].Definition.Environment = nil
			case "wrong-environment-key":
				selections[0].Definition.Environment[0].Name = "OTHER"
			case "missing-variable":
				delete(f.values, "TOKEN")
			case "disallowed-variable":
				scope.Instance.EnvAllow = nil
			case "reader-failure":
				f.err = errors.New("peer unavailable")
			case "native-collision":
				f.nativeFixture.call = func(string, map[string]any) (any, error) {
					return mcpConfig(scope, "user", "config.toml", map[string]any{"command": "inherited"}), nil
				}
			}
			if err := addCodexMCPOverrides(context.Background(), f, scope, map[string]any{}); err == nil {
				t.Fatal("unverified definition entered native overrides")
			}
		})
	}
}

func TestCodexMCPEnvironmentReferencesRejectDuplicateProcessVariables(t *testing.T) {
	for _, key := range []string{"CODEX_HOME", "HOME", "TOKEN"} {
		t.Run(key, func(t *testing.T) {
			data := []byte("CODEX_HOME=/owned/profile\x00HOME=/owned/home\x00TOKEN=private\x00" + key + "=duplicate-private\x00")
			values, err := parseCodexEnvironment(data, "TOKEN")
			if err == nil || values != nil || strings.Contains(err.Error(), "private") {
				t.Fatal("ambiguous process environment was accepted or disclosed")
			}
		})
	}
}
