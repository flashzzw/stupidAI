package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeMCPSelectionCapturesSharedDefinitionWithoutCredentials(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, ".mcp.json")
	body := `{"mcpServers":{"identity":{"command":"python3","args":["./server.py"],"env":{"API_KEY":"${TEST_MCP_KEY}"}},"second":{"command":"python3"}}}`
	os.WriteFile(source, []byte(body), 0600)
	os.WriteFile(filepath.Join(dir, "server.py"), []byte("fixture script"), 0700)
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared-mcp", Name: "Shared MCP", Kind: "mcp", Version: "v1", Source: dir, Backends: []string{"codex", "claude"}}}
	a := openFixture(t, c, newFixture())
	receipt, err := a.Apply(Command{ID: "selected", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared-mcp", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Snapshot.CapabilityRefs) != 1 || len(read.Snapshot.MCPSelections) != 2 {
		t.Fatal("selection not recorded with its source")
	}
	for _, selection := range read.Snapshot.MCPSelections {
		if selection.SourcePath != source || selection.Mode != "enabled" {
			t.Fatal("wrong MCP binding")
		}
	}
	data, _ := json.Marshal(read.Snapshot)
	if string(data) == body {
		t.Fatal("definition in snapshot")
	}
	var snapshot map[string]any
	json.Unmarshal(data, &snapshot)
	for _, raw := range snapshot["mcp_selections"].([]any) {
		if len(raw.(map[string]any)) != 4 {
			t.Fatal("MCP binding includes execution payload")
		}
	}
}

func TestClaudeRemoteMCPSelectionValidatesDefinitionAndKeepsOnlyReferences(t *testing.T) {
	for _, tc := range []struct {
		name, definition string
		valid            bool
	}{
		{"http", `{"type":"http","url":"https://example.test/mcp","headers":{"Authorization":"Bearer ${TOKEN}"}}`, true},
		{"sse", `{"type":"sse","url":"http://127.0.0.1:1234/sse"}`, true},
		{"http_helper", `{"type":"http","url":"https://example.test/mcp","headersHelper":"auth-tool --profile $ACCOUNT"}`, true},
		{"sse_helper", `{"type":"sse","url":"https://example.test/sse","headersHelper":"./project-auth.sh"}`, true},
		{"helper_number", `{"type":"http","url":"https://example.test/mcp","headersHelper":42}`, false},
		{"helper_empty", `{"type":"http","url":"https://example.test/mcp","headersHelper":"  "}`, false},
		{"helper_nul", `{"type":"http","url":"https://example.test/mcp","headersHelper":"auth\u0000tool"}`, false},
		{"invalid_scheme", `{"type":"http","url":"file:///private"}`, false},
		{"missing_type", `{"url":"https://example.test/mcp"}`, false},
		{"mixed", `{"type":"sse","url":"https://example.test","command":"python3"}`, false},
		{"headers_type", `{"type":"http","url":"https://example.test","headers":{"Authorization":42}}`, false},
		{"newline", `{"type":"http","url":"https://example.test","headers":{"X-Key":"value\r\nnext"}}`, false},
		{"unsupported_oauth", `{"type":"http","url":"https://example.test","oauth":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Instances[0].Kind = "claude"
			c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
			if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(source, []byte(`{"mcpServers":{"remote":`+tc.definition+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			c.CapabilityAssets = []CapabilityAsset{{ID: "remote", Name: "Remote MCP", Kind: "mcp", Version: "v1", Source: source, Backends: []string{"claude"}}}
			a := openFixture(t, c, newFixture())
			receipt, err := a.Apply(Command{ID: "selected", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "remote", Mode: "enabled"}}})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if !tc.valid {
				if len(a.Snapshot().Sessions) != 0 {
					t.Fatal("invalid definition created session")
				}
				return
			}
			read, err := a.ReadExecution(receipt.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if len(read.Snapshot.MCPSelections) != 1 || read.Snapshot.MCPSelections[0].SourcePath != source {
				t.Fatal("missing source reference")
			}
			data, _ := json.Marshal(read.Snapshot)
			if strings.Contains(string(data), "Authorization") || strings.Contains(string(data), "https://example.test") {
				t.Fatal("remote connection payload copied into snapshot")
			}
		})
	}
}
func TestClaudeMCPRejectsNameCollisionWrongKindAndUnsupportedTransport(t *testing.T) {
	for _, tc := range []string{"collision", "kind", "transport", "unknown", "args", "env", "type"} {
		t.Run(tc, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Instances[0].Kind = "claude"
			c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
			if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			body := `{"mcpServers":{"same":{"command":"python3"}}}`
			if tc == "transport" {
				body = `{"mcpServers":{"same":{"type":"unknown","url":"https://example.test"}}}`
			}
			if tc == "unknown" {
				body = `{"mcpServers":{"same":{"command":"python3","cwd":"/ignored"}}}`
			}
			if tc == "args" {
				body = `{"mcpServers":{"same":{"command":"python3","args":[42]}}}`
			}
			if tc == "env" {
				body = `{"mcpServers":{"same":{"command":"python3","env":{"KEY":42}}}}`
			}
			if tc == "type" {
				body = `{"mcpServers":{"same":{"command":"python3","type":42}}}`
			}
			source := filepath.Join(dir, "mcp.json")
			os.WriteFile(source, []byte(body), 0600)
			asset := CapabilityAsset{ID: "first", Name: "First", Kind: "mcp", Version: "v1", Source: source, Backends: []string{"claude"}}
			if tc == "kind" {
				asset.Kind = "skill"
			}
			c.CapabilityAssets = []CapabilityAsset{asset}
			selections := []CapabilitySelection{{AssetID: "first", Mode: "enabled"}}
			if tc == "collision" {
				other := asset
				other.ID = "second"
				c.CapabilityAssets = append(c.CapabilityAssets, other)
				selections = append(selections, CapabilitySelection{AssetID: "second", Mode: "disabled"})
			}
			a := openFixture(t, c, newFixture())
			if _, err := a.Apply(Command{ID: "bad", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: selections}); err == nil {
				t.Fatal("unsupported selection accepted")
			}
			if len(a.Snapshot().Sessions) > 0 {
				t.Fatal("rejected selection created session")
			}
		})
	}
}
