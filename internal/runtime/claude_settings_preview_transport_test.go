//go:build linux && nativeclaude

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestClaudeSettingsPreviewCrashHelper(t *testing.T) {
	root := os.Getenv("NETCAP_SETTINGS_PREVIEW_CRASH_ROOT")
	if root == "" {
		t.Skip("owned child helper")
	}
	body, err := os.ReadFile(filepath.Join(root, "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scope Scope
	if json.Unmarshal(body, &scope) != nil {
		t.Fatal("invalid helper scope")
	}
	m, err := newClaudeSettingsFiles(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	use, err := m.create(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
	if err != nil {
		t.Fatal(err)
	}
	defer use.file.Close()
	body, _ = json.Marshal(use.record.Artifact)
	if err := os.WriteFile(filepath.Join(root, "artifact.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	scope.Instance.SettingsFile = use.record.Artifact.File.Path
	if _, err := runClaudeSettingsValidation(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}}, use.file, nil); err == nil {
		t.Fatal("faulted worker returned success")
	}
}

func TestClaudeSettingsPreviewOrphanFileLock(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node transport required")
	}
	repository, _ := filepath.Abs("../..")
	if _, err := os.Stat(filepath.Join(repository, "bridges/claude/dist/settings_validation.js")); err != nil {
		t.Skip("build the Claude bridge first")
	}
	for _, mode := range []string{"worker-loss", "go-parent-loss"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp(os.Getenv("NETCAP_SETTINGS_PREVIEW_ROOT"), "netcap-settings-preview-orphan-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("Evidence directory:", root)
			for _, name := range []string{"state", "work", "custom", "profile"} {
				if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := filepath.Join(root, "custom/original.json")
			if err := os.WriteFile(original, []byte(`{"enabledPlugins":{"sibling@local":true}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cli := filepath.Join(root, "controlled-native")
			code := fmt.Sprintf(`#!/usr/bin/python3
import json,os,signal,sys,time
from pathlib import Path
root=Path(%q)
if '--version' in sys.argv:
 print('2.1.280 (Claude Code)');raise SystemExit
assert '--bare' in sys.argv and '--no-session-persistence' in sys.argv
settings=sys.argv[sys.argv.index('--settings')+1]
assert os.stat(settings).st_ino==os.fstat(3).st_ino
signal.signal(signal.SIGTERM,signal.SIG_IGN)
for line in sys.stdin:
 r=json.loads(line);sub=r['request']['subtype']
 if sub=='initialize':value={}
 elif sub=='get_settings':value={'effective':json.loads(Path(settings).read_text()),'sources':[]}
 elif sub=='get_hooks_listing':value={'bareMode':{}}
 elif sub=='mcp_status':
  (root/'consumers.json').write_text(json.dumps({'worker':os.getppid(),'native':os.getpid(),'inode':os.fstat(3).st_ino}))
  while True:time.sleep(1)
 else:raise AssertionError(sub)
 print(json.dumps({'type':'control_response','response':{'subtype':'success','request_id':r['request_id'],'response':value}}),flush=True)
`, root)
			if err := os.WriteFile(cli, []byte(code), 0700); err != nil {
				t.Fatal(err)
			}
			scope := Scope{Cwd: filepath.Join(root, "work"), Route: Route{Model: "fixture"}, Instance: Instance{Kind: "claude", Profile: filepath.Join(root, "profile"), SettingsFile: original, NodeExecutable: node, Executable: cli, Bridge: filepath.Join(repository, "bridges/claude/dist/main.js")}}
			body, _ := json.Marshal(scope)
			if err := os.WriteFile(filepath.Join(root, "scope.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			m, err := newClaudeSettingsFiles(filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			var use *claudeSettingsFileUse
			var helper *exec.Cmd
			var finished chan error
			if mode == "go-parent-loss" {
				helper = exec.Command(os.Args[0], "-test.run=^TestClaudeSettingsPreviewCrashHelper$", "-test.v")
				helper.Env = append(os.Environ(), "NETCAP_SETTINGS_PREVIEW_CRASH_ROOT="+root)
				log, err := os.OpenFile(filepath.Join(root, "helper.log"), os.O_WRONLY|os.O_CREATE, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				helper.Stdout, helper.Stderr = log, log
				if err := helper.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = helper.Process.Kill() })
			} else {
				use, err = m.create(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
				if err != nil {
					t.Fatal(err)
				}
				defer use.file.Close()
				scope.Instance.SettingsFile = use.record.Artifact.File.Path
				finished = make(chan error, 1)
				go func() {
					_, err := runClaudeSettingsValidation(context.Background(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}}, use.file, nil)
					finished <- err
				}()
			}
			var consumers struct {
				Worker int    `json:"worker"`
				Native int    `json:"native"`
				Inode  uint64 `json:"inode"`
			}
			for end := time.Now().Add(5 * time.Second); consumers.Native == 0; time.Sleep(10 * time.Millisecond) {
				data, _ := os.ReadFile(filepath.Join(root, "consumers.json"))
				_ = json.Unmarshal(data, &consumers)
				if time.Now().After(end) {
					t.Fatal("controlled native consumer did not reach pause")
				}
			}
			t.Cleanup(func() {
				_ = syscall.Kill(-consumers.Worker, syscall.SIGKILL)
				_ = syscall.Kill(consumers.Native, syscall.SIGKILL)
			})
			var artifact ClaudeDerivedSettingsFile
			if helper != nil {
				data, _ := os.ReadFile(filepath.Join(root, "artifact.json"))
				if json.Unmarshal(data, &artifact) != nil {
					t.Fatal("helper artifact missing")
				}
				_ = helper.Process.Kill()
				_ = helper.Wait()
				if count, err := m.recover(context.Background()); count != 0 || err != nil {
					t.Fatal("Go loss reclaimed a live child file", count, err)
				}
			} else {
				artifact = use.record.Artifact
			}
			if err := syscall.Kill(consumers.Worker, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			if finished != nil {
				select {
				case err := <-finished:
					if err == nil {
						t.Fatal("dead worker accepted")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("worker loss did not finish")
				}
				use.file.Close()
			}
			info, err := os.Stat(fmt.Sprintf("/proc/%d/fd/3", consumers.Native))
			if err != nil || fileIdentity(info).Inode != consumers.Inode {
				t.Fatal("native consumer lost the inherited file", err)
			}
			if err := m.remove(context.Background(), artifact.ID); !errors.Is(err, errClaudeSettingsFileBusy) {
				t.Fatal("orphan consumer was not protected", err)
			}
			if count, err := m.recover(context.Background()); count != 0 || err != nil {
				t.Fatal("recovery deleted live consumer file", count, err)
			}
			if err := syscall.Kill(consumers.Native, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				err = m.remove(context.Background(), artifact.ID)
				if err == nil {
					break
				}
				if !errors.Is(err, errClaudeSettingsFileBusy) || time.Now().After(end) {
					t.Fatal("orphan file not reclaimable after consumer exits", err)
				}
			}
			if _, err := os.Lstat(artifact.File.Path); !os.IsNotExist(err) {
				t.Fatal("released file remains")
			}
			result, _ := json.MarshalIndent(map[string]any{"mode": mode, "artifact": artifact, "consumers": consumers, "consumer_fd_matches": true, "recovery_skipped_live_file": true, "released_after_consumer_exit": true, "passed": !t.Failed()}, "", "  ")
			if err := os.WriteFile(filepath.Join(root, "result.json"), result, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
