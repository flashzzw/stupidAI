package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestClaudeRetainedSettingsSurviveClosedConsumersAndRestart(t *testing.T) {
	m, scope := settingsFileFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	settings := []ClaudePluginSetting{{SourceKey: "selected@local", Enabled: false}}
	use, err := m.retain(ctx, id, scope, settings)
	if err != nil {
		t.Fatal(err)
	}
	artifact, identity := use.record.Artifact, use.record.FileID
	use.file.Close()
	m, err = newClaudeSettingsFiles(filepath.Dir(m.root))
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := m.recover(ctx); err != nil || removed != 0 {
		t.Fatal("a saved execution version was reclaimed", removed, err)
	}
	if _, err := os.Stat(artifact.File.Path); err != nil {
		t.Fatal("closed query lost its settings", err)
	}
	if err := m.remove(ctx, id); err == nil {
		t.Fatal("ordinary preview cleanup released an execution version")
	}
	retry, err := m.retain(ctx, id, scope, settings)
	if err != nil || retry.record.Artifact != artifact || retry.record.FileID != identity || retry.record.Version != 2 {
		t.Fatal("retry did not retain the original file identity", err)
	}
	defer retry.file.Close()
	foreign := scope
	foreign.NativeID = "another-execution"
	if err := m.releaseRetained(ctx, id, foreign); err == nil {
		t.Fatal("another execution released the saved settings")
	}
	if changed, err := m.retain(ctx, id, scope, []ClaudePluginSetting{{SourceKey: "selected@local", Enabled: true}}); err == nil {
		changed.file.Close()
		t.Fatal("a saved version silently accepted different settings")
	}
	if err := m.releaseRetained(ctx, id, scope); !errors.Is(err, errClaudeSettingsFileBusy) {
		t.Fatal("retirement ignored a live consumer", err)
	}
	retry.file.Close()
	if err := m.releaseRetained(ctx, id, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact.File.Path); !os.IsNotExist(err) {
		t.Fatal("explicit retirement did not reclaim the file", err)
	}
	if err := m.releaseRetained(ctx, id, scope); err != nil {
		t.Fatal("retirement retry was not idempotent", err)
	}
	if again, err := m.retain(ctx, id, scope, settings); err == nil {
		again.file.Close()
		t.Fatal("retired version was recreated")
	}
}

func TestClaudeRetainedSettingsConcurrentRetryHasOneFile(t *testing.T) {
	m, scope := settingsFileFixture(t)
	id := uuid.NewString()
	type result struct {
		use *claudeSettingsFileUse
		err error
	}
	results := make(chan result, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			use, err := m.retain(context.Background(), id, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			results <- result{use, err}
		}()
	}
	workers.Wait()
	close(results)
	var identity claudeFileIdentity
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.use.file.Close()
		if identity.Inode != 0 && result.use.record.FileID != identity {
			t.Fatal("concurrent retry published another inode")
		}
		identity = result.use.record.FileID
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(scope.Instance.SettingsFile), ".netcap-settings-*.json"))
	if len(files) != 1 {
		t.Fatal("concurrent retry published duplicate files", files)
	}
	if err := m.releaseRetained(context.Background(), id, scope); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeRetainedSettingsRejectSourceDriftButAllowRetirement(t *testing.T) {
	m, scope := settingsFileFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	settings := []ClaudePluginSetting{{SourceKey: "selected@local"}}
	use, err := m.retain(ctx, id, scope, settings)
	if err != nil {
		t.Fatal(err)
	}
	use.file.Close()
	if err := os.WriteFile(scope.Instance.SettingsFile, []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if again, err := m.retain(ctx, id, scope, settings); err == nil {
		again.file.Close()
		t.Fatal("cold retry adopted changed original settings")
	}
	if err := m.releaseRetained(ctx, id, scope); err != nil {
		t.Fatal("source drift prevented owned file retirement", err)
	}
	body, _ := os.ReadFile(scope.Instance.SettingsFile)
	if string(body) != `{"changed":true}` {
		t.Fatal("retirement changed the original settings")
	}
}

func TestClaudeRetainedSettingsFinishInterruptedRelease(t *testing.T) {
	m, scope := settingsFileFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	use, err := m.retain(ctx, id, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
	if err != nil {
		t.Fatal(err)
	}
	use.file.Close()
	blocked := filepath.Join(m.root, "released", id+".json")
	m.checkpoint = func(stage string) {
		if stage == "recorded_release_before_archive" {
			if err := os.Mkdir(blocked, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.releaseRetained(ctx, id, scope); err == nil {
		t.Fatal("interrupted archive reported success")
	}
	m.checkpoint = nil
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if removed, err := m.recover(ctx); err != nil || removed != 0 {
		t.Fatal("retired receipt was not archived idempotently", removed, err)
	}
	if _, err := os.Stat(filepath.Join(m.root, id+".json")); !os.IsNotExist(err) {
		t.Fatal("retired receipt still occupies active recovery index", err)
	}
	if err := m.releaseRetained(ctx, id, scope); err != nil {
		t.Fatal(err)
	}
}
