package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestClaudeInstalledSettingsRetirementRequiresOwnedUnusedFile(t *testing.T) {
	for _, scenario := range []string{"owned", "no-publication", "unowned-file", "wrong-scope"} {
		t.Run(scenario, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			settings := []ClaudePluginSetting{{SourceKey: "selected@local"}}
			file, err := PlanClaudeDerivedSettingsFile(scope, settings, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			scope.ClaudeInstalledSettings = &ClaudeInstalledSettings{Version: 1, Artifact: file, Selections: []PluginSelection{{Name: "selected", NativeSource: "selected@local", Mode: "disabled"}}}
			adapter := NewClaude(filepath.Dir(m.root))
			t.Cleanup(func() { _ = adapter.Close() })
			ctx := context.Background()
			if scenario == "unowned-file" {
				if err := os.WriteFile(file.File.Path, []byte("unowned"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := adapter.RetireClaudeInstalledSettings(ctx, scope); err == nil {
					t.Fatal("unowned file was accepted as retired")
				}
				if body, _ := os.ReadFile(file.File.Path); string(body) != "unowned" {
					t.Fatal("unowned file changed")
				}
				return
			}
			if scenario != "no-publication" {
				use, err := m.retain(ctx, file.ID, scope, settings)
				if err != nil {
					t.Fatal(err)
				}
				if err := adapter.RetireClaudeInstalledSettings(ctx, scope); !errors.Is(err, errClaudeSettingsFileBusy) {
					t.Fatal("live consumer did not block retirement", err)
				}
				_ = use.file.Close()
				if scenario == "wrong-scope" {
					foreign := scope
					foreign.NativeID = "another-native-session"
					if err := adapter.RetireClaudeInstalledSettings(ctx, foreign); err == nil {
						t.Fatal("another session retired the file")
					}
				}
			}
			if err := adapter.RetireClaudeInstalledSettings(ctx, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(file.File.Path); !os.IsNotExist(err) {
				t.Fatal("unused file was not retired", err)
			}
			if err := adapter.RetireClaudeInstalledSettings(ctx, scope); err != nil {
				t.Fatal("retirement retry failed", err)
			}
		})
	}
}
