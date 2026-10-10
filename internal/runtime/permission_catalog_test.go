package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func permissionInventoryFixture(t *testing.T) claudePermissionInventory {
	t.Helper()
	var inventory claudePermissionInventory
	if err := json.Unmarshal([]byte(`{"version":1,"available":true,"at":"2026-10-10T00:00:00Z","cwd":"/owned","managed_only":true,
		"items":[{"behavior":"deny","source":"userSettings","rule":"Read(./private/**)","editability":"persistent","not_in_effect":false},
		{"behavior":"deny","source":"userSettings","rule":"Read(./private/**)","editability":"readonly","not_in_effect":true},
		{"behavior":"ask","source":"futureSource","rule":"Bash(echo 中文🦊\n\u202e)","editability":"readonly","not_in_effect":false}],
		"directories":[{"path":"/owned/extra","source":"session"}],"errors":[{"file":"/owned/settings.json","field":"permissions.allow"}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func permissionCatalogFixture(t *testing.T, inventory claudePermissionInventory) PermissionRuleCatalog {
	t.Helper()
	out, err := claudePermissionCatalog(Scope{NativeID: "original", Cwd: "/owned"}, "b2a7d1cc-f517-441a-bbae-a2baf4731cca", strings.Repeat("a", 64), inventory)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClaudePermissionReportPreservesNativeConflictsAndSourceUncertainty(t *testing.T) {
	inventory := permissionInventoryFixture(t)
	out := permissionCatalogFixture(t, inventory)
	if out.Complete || !out.ManagedOnly || out.NativeID != "original" || out.ConflictCount != 1 || len(out.Items) != 3 ||
		!out.Items[0].Conflicting || !out.Items[1].Conflicting || out.Items[2].Conflicting || out.Items[0].NotInEffect || !out.Items[1].NotInEffect {
		t.Fatal("native contradiction or scope lost", out)
	}
	if out.Items[2].Source != "futureSource" || out.Items[2].Rule != "Bash(echo 中文🦊\n\u202e)" ||
		len(out.Errors) != 1 || out.Directories[0].Source != "session" || !claudeSettingsFileSHA.MatchString(out.SampleHash) {
		t.Fatal("native spelling, source or skipped configuration lost")
	}
	slices.Reverse(inventory.Items)
	inventory.At = "2026-10-10T00:00:10Z"
	if permissionCatalogFixture(t, inventory).SampleHash != out.SampleHash {
		t.Fatal("ordering or observation time incorrectly indicates a rule change")
	}
}

func TestClaudePermissionReportDigestTracksEachObservableConstraint(t *testing.T) {
	for _, mode := range []string{"rule", "source", "behavior", "ignored", "location", "duplicate", "directory", "directory-source", "error", "managed-only"} {
		t.Run(mode, func(t *testing.T) {
			inventory := permissionInventoryFixture(t)
			before := permissionCatalogFixture(t, inventory)
			switch mode {
			case "rule":
				inventory.Items[0].Rule = "Read(./another/**)"
			case "source":
				inventory.Items[0].Source = "projectSettings"
			case "behavior":
				inventory.Items[0].Behavior = "allow"
			case "ignored":
				*inventory.Items[0].NotInEffect = true
			case "location":
				inventory.Items[0].Editability = "readonly"
			case "duplicate":
				inventory.Items = append(inventory.Items, inventory.Items[0])
			case "directory":
				inventory.Directories[0].Path = "/owned/other"
			case "directory-source":
				inventory.Directories[0].Source = "cliArg"
			case "error":
				inventory.Errors[0].Field = "permissions.deny"
			case "managed-only":
				*inventory.ManagedOnly = false
			}
			if permissionCatalogFixture(t, inventory).SampleHash == before.SampleHash {
				t.Fatal("changed native constraint has the old digest")
			}
		})
	}
}

func TestClaudePermissionReportRejectsIncompleteWireAndAcceptsKnownEmpty(t *testing.T) {
	for _, mode := range []string{"empty", "missing-rows", "missing-directories", "missing-errors", "missing-policy", "missing-ignored", "bad-cwd", "bad-date", "bad-version", "bad-source", "bad-rule", "bad-behavior", "bad-location", "bad-directory", "bad-error"} {
		t.Run(mode, func(t *testing.T) {
			inventory := permissionInventoryFixture(t)
			switch mode {
			case "empty":
				inventory.Items = inventory.Items[:0]
				inventory.Directories, inventory.Errors = []PermissionDirectory{}, []PermissionSettingsError{}
			case "missing-rows":
				inventory.Items = nil
			case "missing-directories":
				inventory.Directories = nil
			case "missing-errors":
				inventory.Errors = nil
			case "missing-policy":
				inventory.ManagedOnly = nil
			case "missing-ignored":
				inventory.Items[0].NotInEffect = nil
			case "bad-cwd":
				inventory.Cwd = "/another"
			case "bad-date":
				inventory.At = "today"
			case "bad-version":
				inventory.Version = 2
			case "bad-source":
				inventory.Items[0].Source = "user\x00Settings"
			case "bad-rule":
				inventory.Items[0].Rule = string([]byte{0xff})
			case "bad-behavior":
				inventory.Items[0].Behavior = "maybe"
			case "bad-location":
				inventory.Items[0].Editability = "unknown"
			case "bad-directory":
				inventory.Directories[0].Path = "relative"
			case "bad-error":
				inventory.Errors[0].File = ""
			}
			out, err := claudePermissionCatalog(Scope{NativeID: "original", Cwd: "/owned"}, "b2a7d1cc-f517-441a-bbae-a2baf4731cca", strings.Repeat("a", 64), inventory)
			if (err == nil) != (mode == "empty") {
				t.Fatal("wrong native report acceptance", mode, err)
			}
			if err != nil && len(out.Items) != 0 {
				t.Fatal("partial report returned as evidence")
			}
		})
	}
}

func TestClaudePermissionCatalogDoesNotStartTransport(t *testing.T) {
	profile := t.TempDir()
	state := filepath.Join(t.TempDir(), "not-created")
	c := NewClaude(state)
	defer c.Close()
	_, err := c.ReadPermissionCatalog(context.Background(), Scope{NativeID: "original", Cwd: profile, StateNamespace: profile, Instance: Instance{ID: "claude", Kind: "claude", Profile: profile}})
	if err == nil {
		t.Fatal("missing native query accepted")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("read started a bridge")
	}
}
