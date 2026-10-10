package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCodexUserHookVersionsDetectFileChanges(t *testing.T) {
	for _, scenario := range []string{"content", "mode", "delete", "create", "link-target", "dangling", "directory", "fifo", "oversize", "legacy", "wrong-version", "wrong-profile"} {
		t.Run(scenario, func(t *testing.T) {
			scope := Scope{Instance: Instance{Kind: "codex", Profile: t.TempDir()}}
			path := filepath.Join(scope.Instance.Profile, "hooks.json")
			body := []byte(`{"hooks":{}}`)
			write := func(name string, b []byte) {
				t.Helper()
				if err := os.WriteFile(name, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "create" {
				write(path, body)
			}
			if scenario == "link-target" {
				if err := os.Rename(path, path+".first"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".first", path); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			scope.CodexUserHooks, err = ReadCodexUserHooks(scope)
			if err != nil || VerifyCodexUserHooks(scope) != nil {
				t.Fatal("initial source failed", err)
			}
			switch scenario {
			case "content", "create":
				write(path, []byte(`{"hooks":{"Stop":[]}}`))
			case "mode":
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "wrong-version":
				scope.CodexUserHooks.Version = 2
			case "wrong-profile":
				scope.Instance.Profile = t.TempDir()
			case "legacy":
				scope.CodexUserHooks = nil
				write(path, []byte("changed unrecorded source"))
			case "link-target", "dangling", "directory", "fifo", "oversize":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "link-target":
					write(path+".second", body)
					err = os.Symlink(path+".second", path)
				case "dangling":
					err = os.Symlink(path+".missing", path)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				case "oversize":
					write(path, []byte(strings.Repeat("x", (2<<20)+1)))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := VerifyCodexUserHooks(scope); (err == nil) != (scenario == "legacy") {
				t.Fatalf("wrong source verdict: %v", err)
			}
		})
	}
}

func TestCodexUserHookDriftRejectsDispatchBeforeConnecting(t *testing.T) {
	scope := Scope{Instance: Instance{Kind: "codex", Profile: t.TempDir()}}
	var err error
	scope.CodexUserHooks, err = ReadCodexUserHooks(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope.Instance.Profile, "hooks.json"), []byte(`{"hooks":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewCodex()
	defer c.Close()
	for _, kind := range []string{"create", "send", "respond", "prepare_execution", "apply_execution", "recover_execution"} {
		_, err := c.Execute(context.Background(), Intent{Kind: kind, Scope: scope})
		if err == nil || !strings.Contains(err.Error(), "用户 hook") || len(c.clients) != 0 {
			t.Fatalf("%s connected or hid source mismatch: %v", kind, err)
		}
	}
}
