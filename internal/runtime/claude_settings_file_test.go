package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func settingsFileFixture(t *testing.T) (*claudeSettingsFiles, Scope) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"state", "work", "custom", "profile"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "custom", "original.json")
	if err := os.WriteFile(path, []byte(`{"env":{"SENTINEL":"PRIVATE_SETTINGS_VALUE"},"permissions":{"deny":["Read(/protected/**)"]},"enabledPlugins":{"sibling@local":true},"large":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := newClaudeSettingsFiles(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{SessionID: "fixture-session", NativeID: "fixture-native", Cwd: filepath.Join(root, "work"),
		Instance: Instance{ID: "claude", Kind: "claude", Profile: filepath.Join(root, "profile"), SettingsFile: path}}
	return m, scope
}

func TestClaudeSettingsFileLifecycle(t *testing.T) {
	m, scope := settingsFileFixture(t)
	ctx := context.Background()
	original, _ := os.ReadFile(scope.Instance.SettingsFile)
	use, err := m.create(ctx, scope, []ClaudePluginSetting{{SourceKey: "selected@local", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	defer use.file.Close()
	id, path := use.record.Artifact.ID, use.record.Artifact.File.Path
	data, _ := os.ReadFile(path)
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || string(object["large"]) != "9007199254740993" ||
		string(object["permissions"]) != `{"deny":["Read(/protected/**)"]}` ||
		string(object["enabledPlugins"]) != `{"selected@local":false,"sibling@local":true}` ||
		filepath.Dir(path) != filepath.Dir(scope.Instance.SettingsFile) {
		t.Fatal("composition changed source values or the original settings directory")
	}
	receipt, _ := os.ReadFile(filepath.Join(m.root, id+".json"))
	if strings.Contains(string(receipt), "PRIVATE_SETTINGS_VALUE") || strings.Contains(string(receipt), "9007199254740993") {
		t.Fatal("settings values leaked into the receipt")
	}
	reopened, err := newClaudeSettingsFiles(filepath.Dir(m.root))
	if err != nil {
		t.Fatal(err)
	}
	other, err := reopened.acquire(ctx, id, scope)
	if err != nil || other.record.Artifact != use.record.Artifact {
		t.Fatalf("saved binding could not be reopened: %v", err)
	}
	defer other.file.Close()
	if err := m.remove(ctx, id); !errors.Is(err, errClaudeSettingsFileBusy) {
		t.Fatal("active file was not protected by its use lock", err)
	}
	use.file.Close()
	if err := m.remove(ctx, id); !errors.Is(err, errClaudeSettingsFileBusy) {
		t.Fatal("another holder was ignored", err)
	}
	other.file.Close()
	if err := reopened.remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("owned file was not removed")
	}
	if err := reopened.remove(ctx, id); err != nil {
		t.Fatal("release is not idempotent", err)
	}
	if use, err := reopened.acquire(ctx, id, scope); err == nil {
		use.file.Close()
		t.Fatal("released record was reused")
	}
	after, _ := os.ReadFile(scope.Instance.SettingsFile)
	if string(after) != string(original) {
		t.Fatal("original settings were modified")
	}
}

func TestClaudeSettingsFileRejectsInvalidSources(t *testing.T) {
	for _, mode := range []string{"null-plugin", "invalid-json", "large-file", "readonly-parent", "shared-parent", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			t.Cleanup(func() { _ = os.Chmod(filepath.Dir(scope.Instance.SettingsFile), 0700) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "null-plugin":
				os.WriteFile(scope.Instance.SettingsFile, []byte(`{"enabledPlugins":{"other@local":null}}`), 0600)
			case "invalid-json":
				os.WriteFile(scope.Instance.SettingsFile, []byte(`{`), 0600)
			case "large-file":
				os.WriteFile(scope.Instance.SettingsFile, []byte(strings.Repeat("x", (2<<20)+1)), 0600)
			case "readonly-parent":
				os.Chmod(filepath.Dir(scope.Instance.SettingsFile), 0500)
			case "shared-parent":
				os.Chmod(filepath.Dir(scope.Instance.SettingsFile), 0770)
			case "cancelled":
				cancel()
			}
			use, err := m.create(ctx, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err == nil {
				use.file.Close()
				t.Fatal("invalid source was composed")
			}
			files, _ := filepath.Glob(filepath.Join(filepath.Dir(scope.Instance.SettingsFile), ".netcap-settings-*.json"))
			if len(files) != 0 {
				t.Fatal("invalid source created an external artifact")
			}
		})
	}
}

func TestClaudeSettingsFileDriftAndRecoveryIsolation(t *testing.T) {
	for _, mode := range []string{"source-bytes", "source-mode", "private-bytes", "private-mode", "replacement", "foreign-scope"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			ctx := context.Background()
			use, err := m.create(ctx, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			use.file.Close()
			path := use.record.Artifact.File.Path
			incoming := scope
			switch mode {
			case "source-bytes":
				os.WriteFile(scope.Instance.SettingsFile, []byte(`{}`), 0600)
			case "source-mode":
				os.Chmod(scope.Instance.SettingsFile, 0640)
			case "private-bytes":
				os.WriteFile(path, []byte(`{"foreign":true}`), 0600)
			case "private-mode":
				os.Chmod(path, 0644)
			case "replacement":
				os.Rename(path, path+".moved")
				os.WriteFile(path, []byte(`{"foreign":true}`), 0600)
			case "foreign-scope":
				incoming.NativeID = "different-native"
			}
			if reopened, err := m.acquire(ctx, use.record.Artifact.ID, incoming); err == nil {
				reopened.file.Close()
				t.Fatal("drift or foreign scope accepted")
			}
			protected := strings.HasPrefix(mode, "private-") || mode == "replacement"
			err = m.remove(ctx, use.record.Artifact.ID)
			if protected && (err == nil || func() bool { _, e := os.Lstat(path); return os.IsNotExist(e) }()) {
				t.Fatal("modified/foreign file was removed")
			}
			if !protected && err != nil {
				t.Fatal("source change prevented cleanup of the unchanged owned copy", err)
			}
		})
	}
	t.Run("one-bad-file-does-not-stop-other-cleanup", func(t *testing.T) {
		m, scope := settingsFileFixture(t)
		ctx := context.Background()
		uses := []*claudeSettingsFileUse{}
		for i := 0; i < 2; i++ {
			use, err := m.create(ctx, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			use.file.Close()
			uses = append(uses, use)
		}
		sort.Slice(uses, func(i, j int) bool { return uses[i].record.Artifact.ID < uses[j].record.Artifact.ID })
		os.WriteFile(uses[0].record.Artifact.File.Path, []byte(`{"keep":true}`), 0600)
		count, err := m.recover(ctx)
		if count != 1 || err == nil {
			t.Fatalf("cleanup did not isolate the damaged file: %d, %v", count, err)
		}
		if _, err := os.Lstat(uses[1].record.Artifact.File.Path); !os.IsNotExist(err) {
			t.Fatal("independent owned copy was left behind")
		}
	})
}

func TestClaudeSettingsFileRejectsInvalidReceipts(t *testing.T) {
	for _, mode := range []string{"scope", "hash", "no-selection", "duplicate-selection", "foreign-original"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			use, err := m.create(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			use.file.Close()
			r := use.record
			switch mode {
			case "scope":
				r.Scope = "invalid"
			case "hash":
				r.Artifact.File.SHA256 = strings.Repeat("z", 64)
			case "no-selection":
				r.Settings = nil
			case "duplicate-selection":
				r.Settings = append(r.Settings, r.Settings[0])
			case "foreign-original":
				r.Artifact.Original.Path = filepath.Join(scope.Cwd, "foreign.json")
			}
			data, _ := json.Marshal(r)
			if err := os.WriteFile(filepath.Join(m.root, r.Artifact.ID+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.remove(context.Background(), r.Artifact.ID); err == nil {
				t.Fatal("invalid receipt authorized removal")
			}
			if _, err := os.Lstat(r.Artifact.File.Path); err != nil {
				t.Fatal("corrupt receipt deleted the file", err)
			}
		})
	}
}

func TestClaudeSettingsFileCrashHelper(t *testing.T) {
	root := os.Getenv("NETCAP_SETTINGS_CRASH_ROOT")
	if root == "" {
		t.Skip("owned crash helper")
	}
	m, err := newClaudeSettingsFiles(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	m.checkpoint = func(stage string) {
		if stage == os.Getenv("NETCAP_SETTINGS_CRASH_POINT") {
			fmt.Println("CHECKPOINT " + stage)
			time.Sleep(time.Hour)
		}
	}
	scope := Scope{Cwd: filepath.Join(root, "work"), Instance: Instance{Kind: "claude", SettingsFile: filepath.Join(root, "custom/original.json")}}
	if use, err := m.create(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}}); err != nil {
		t.Fatal(err)
	} else {
		use.file.Close()
		if err := m.remove(context.Background(), use.record.Artifact.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeSettingsFileCrashRecovery(t *testing.T) {
	for _, point := range []string{"recorded_before_link", "linked_before_ready", "unlinked_before_release", "recorded_release_before_archive"} {
		t.Run(point, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			root := filepath.Dir(scope.Cwd)
			cmd := exec.Command(os.Args[0], "-test.run=^TestClaudeSettingsFileCrashHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "NETCAP_SETTINGS_CRASH_ROOT="+root, "NETCAP_SETTINGS_CRASH_POINT="+point)
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			ready := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					if scanner.Text() == "CHECKPOINT "+point {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("child did not reach the filesystem boundary")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("owned child checkpoint timed out")
			}
			cmd.Process.Kill()
			cmd.Wait()
			count, err := m.recover(context.Background())
			want := 1
			if point == "recorded_release_before_archive" {
				want = 0
			}
			if err != nil || count != want {
				t.Fatalf("interrupted publication did not recover: %d %v", count, err)
			}
			files, _ := filepath.Glob(filepath.Join(filepath.Dir(scope.Instance.SettingsFile), ".netcap-settings-*.json"))
			if len(files) != 0 {
				t.Fatal("interrupted publication left a private file")
			}
			active, _ := filepath.Glob(filepath.Join(m.root, "*.json"))
			if len(active) != 0 {
				t.Fatal("released receipts still occupy the active recovery index")
			}
		})
	}
}
