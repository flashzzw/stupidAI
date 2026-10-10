package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeHookCatalogKeepsSourceAndPolicyUncertainty(t *testing.T) {
	for _, mode := range []string{"valid", "unknown-source", "duplicate", "missing-policy", "missing-rows", "invalid-hash", "invalid-time", "negative-count", "invalid-timeout", "nul-source"} {
		t.Run(mode, func(t *testing.T) {
			var inventory claudeHookInventory
			body := `{"available":true,"at":"2026-10-02T00:00:00Z","items":[{"event":"PreToolUse","matcher":"Read","source":"pluginHook","plugin_name":"fixture@inline","type":"command","disabled":true,"definition_hash":"` + strings.Repeat("a", 64) + `"}],"policy":{"managed_only":true,"policy_unreadable":true,"settings_error_count":1}}`
			if err := json.Unmarshal([]byte(body), &inventory); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unknown-source":
				inventory.Items[0].Source = "futureSource"
			case "duplicate":
				inventory.Items = append(inventory.Items, inventory.Items[0])
			case "missing-policy":
				inventory.Policy = nil
			case "missing-rows":
				inventory.Items = nil
			case "invalid-hash":
				inventory.Items[0].DefinitionHash = "not-a-digest"
			case "invalid-time":
				inventory.At = "yesterday"
			case "negative-count":
				inventory.Policy.SettingsErrorCount = -1
			case "invalid-timeout":
				v := -1.0
				inventory.Items[0].TimeoutSeconds = &v
			case "nul-source":
				inventory.Items[0].Source = "user\x00Settings"
			}
			catalog, err := claudeHookCatalog(Scope{NativeID: "original", Cwd: "/fixture"}, inventory)
			valid := mode == "valid" || mode == "unknown-source" || mode == "duplicate"
			if (err == nil) != valid {
				t.Fatalf("wrong acceptance for %s: %v", mode, err)
			}
			if !valid {
				return
			}
			if catalog.NativeID != "original" || catalog.Complete || !catalog.Policy.PolicyUnreadable || !catalog.Policy.ManagedOnly || catalog.Policy.SettingsErrorCount != 1 || !catalog.Items[0].Disabled {
				t.Fatal("catalog changed native identity, disabled state or uncertainty")
			}
			if mode == "unknown-source" && catalog.Items[0].Source != "futureSource" {
				t.Fatal("unknown source reclassified")
			}
			if mode == "duplicate" && len(catalog.Items) != 2 {
				t.Fatal("native rows silently merged")
			}
		})
	}
}

func TestClaudeHookCatalogDoesNotStartTransport(t *testing.T) {
	profile := t.TempDir()
	state := filepath.Join(t.TempDir(), "not-created")
	c := NewClaude(state)
	defer c.Close()
	_, err := c.ReadHookCatalog(context.Background(), Scope{NativeID: "original", Cwd: profile, StateNamespace: profile, Instance: Instance{ID: "claude", Kind: "claude", Profile: profile}})
	if err == nil {
		t.Fatal("missing native query accepted")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("read created a bridge directory")
	}
}
