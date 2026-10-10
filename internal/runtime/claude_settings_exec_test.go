package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClaudeSettingsExecChild(t *testing.T) {
	encoded := os.Getenv("NETCAP_SETTINGS_EXEC_CHILD")
	if encoded == "" {
		t.Skip("subprocess entry")
	}
	var args []string
	if err := json.Unmarshal([]byte(encoded), &args); err != nil {
		os.Exit(18)
	}
	handled, err := RunClaudeSettingsFileExec(args)
	if !handled || err != nil {
		fmt.Fprintln(os.Stderr, "settings exec rejected")
		os.Exit(19)
	}
	os.Exit(20)
}

func settingsConsumerCommand(t *testing.T, fd *os.File, source FileSource, program string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	metadata, _ := json.Marshal(source)
	args, _ := json.Marshal([]string{"__claude-settings-exec-v1", string(metadata), "/usr/bin/python3", "-c", program})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClaudeSettingsExecChild$")
	cmd.Env = append(os.Environ(), "NETCAP_SETTINGS_EXEC_CHILD="+string(args))
	cmd.ExtraFiles = []*os.File{fd}
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	return cmd, stderr
}

func TestClaudeSettingsExecPreservesPIDAndUseLock(t *testing.T) {
	m, scope := settingsFileFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	use, err := m.retain(ctx, id, scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
	if err != nil {
		t.Fatal(err)
	}
	defer use.file.Close()
	cmd, stderr := settingsConsumerCommand(t, use.file, use.record.Artifact.File,
		"import os,json,sys; s=os.fstat(3); print(json.dumps({'pid':os.getpid(),'inode':s.st_ino,'device':s.st_dev}),flush=True); sys.stdin.read()")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); cmd.Process.Kill() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatal("consumer did not exec", stderr.String())
	}
	var observed struct {
		PID    int    `json:"pid"`
		Inode  uint64 `json:"inode"`
		Device uint64 `json:"device"`
	}
	if json.Unmarshal(scanner.Bytes(), &observed) != nil || observed.PID != cmd.Process.Pid ||
		observed.Inode != use.record.FileID.Inode || observed.Device != use.record.FileID.Device {
		t.Fatal("exec changed process or file identity", observed)
	}
	use.file.Close()
	if err := m.releaseRetained(ctx, id, scope); !errors.Is(err, errClaudeSettingsFileBusy) {
		t.Fatal("consumer did not retain the shared use lock after parent close", err)
	}
	input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	if err := m.releaseRetained(ctx, id, scope); err != nil {
		t.Fatal("stopped consumer did not release its lock", err)
	}
}

func TestClaudeSettingsExecRejectsInvalidTransfer(t *testing.T) {
	for _, mode := range []string{"hash", "unlinked", "replacement", "mode", "descriptor"} {
		t.Run(mode, func(t *testing.T) {
			m, scope := settingsFileFixture(t)
			use, err := m.retain(context.Background(), uuid.NewString(), scope, []ClaudePluginSetting{{SourceKey: "selected@local"}})
			if err != nil {
				t.Fatal(err)
			}
			defer use.file.Close()
			source := use.record.Artifact.File
			fd := use.file
			switch mode {
			case "hash":
				source.SHA256 = strings.Repeat("0", 64)
			case "unlinked":
				if err := os.Remove(source.Path); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				body, _ := os.ReadFile(source.Path)
				if err := os.Rename(source.Path, source.Path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source.Path, body, 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(source.Path, 0644); err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				fd, err = os.Open(scope.Instance.SettingsFile)
				if err != nil {
					t.Fatal(err)
				}
				defer fd.Close()
			}
			cmd, stderr := settingsConsumerCommand(t, fd, source, "print('UNEXPECTED_NATIVE_START')")
			stdout := new(bytes.Buffer)
			cmd.Stdout = stdout
			err = cmd.Run()
			var failure *exec.ExitError
			if !errors.As(err, &failure) || failure.ExitCode() != 19 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "settings exec rejected") {
				t.Fatal("invalid transfer started its command", err, stdout.String(), stderr.String())
			}
		})
	}
}
