package runtime

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func claudeLocalWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeLocalSettingsRoots(t *testing.T) {
	root, repo, worktree := claudeLocalRepo(t)
	// Git skips an empty .git directory; its mere presence is not evidence of
	// a repository whose settings failed to load.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{repo, filepath.Join(repo, "nested"), worktree, filepath.Join(worktree, "nested"), filepath.Join(root, "ordinary")} {
		scope := Scope{Cwd: cwd, Instance: Instance{Kind: "claude", Profile: filepath.Join(root, "profile")}}
		got, err := claudeLocalSettingsPaths(context.Background(), scope)
		want := []string{filepath.Join(cwd, ".claude/settings.json"), filepath.Join(cwd, ".claude/settings.local.json")}
		if cwd != repo && cwd != filepath.Join(root, "ordinary") {
			want = append(want, filepath.Join(repo, ".claude/settings.local.json"))
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s roots: %v %v", cwd, got, err)
		}
	}
	t.Setenv("HOME", repo)
	cwd := filepath.Join(worktree, "nested")
	got, err := claudeLocalSettingsPaths(context.Background(), Scope{Cwd: cwd, Instance: Instance{Kind: "claude"}})
	if err != nil || len(got) != 2 {
		t.Fatalf("home boundary: %v %v", got, err)
	}
}

func TestClaudeLocalSettingsFileBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "bom", "malformed", "array", "env-null", "env-null-value", "env-number", "helper", "credential", "state", "directory", "fifo", "broken-link", "link", "large", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cwd := filepath.Join(root, "cwd")
			profile := filepath.Join(root, "profile")
			for _, p := range []string{cwd, profile} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(cwd, ".claude/settings.local.json")
			body := `{"env":{"NETCAP_SAFE":"preserved"},"permissions":{"deny":["Read(./private)"]}}`
			switch mode {
			case "bom":
				body = "\ufeff" + body
			case "malformed":
				body = `{"PRIVATE_PARSE_INPUT":`
			case "array":
				body = `[]`
			case "env-null":
				body = `{"env":null}`
			case "env-null-value":
				body = `{"env":{"key":null}}`
			case "env-number":
				body = `{"env":{"key":1}}`
			case "helper":
				body = `{"apiKeyHelper":"PRIVATE_HELPER"}`
			case "credential":
				body = `{"env":{"ANTHROPIC_API_KEY":"PRIVATE_VALUE"}}`
			case "state":
				body = `{"env":{"XDG_CONFIG_HOME":"/other"}}`
			case "large":
				body = strings.Repeat("x", (2<<20)+1)
			}
			claudeLocalWrite(t, path, body)
			if mode == "fifo" || mode == "directory" || mode == "broken-link" || mode == "link" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				var err error
				switch mode {
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "broken-link":
					err = os.Symlink(filepath.Join(root, "absent"), path)
				case "link":
					target := filepath.Join(root, "linked.json")
					claudeLocalWrite(t, target, body)
					err = os.Symlink(target, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			err := ValidateClaudeExecutionSettings(ctx, Scope{Cwd: cwd, Instance: Instance{Kind: "claude", Profile: profile}})
			valid := mode == "valid" || mode == "bom" || mode == "link"
			if (err == nil) != valid {
				t.Fatalf("unexpected validation: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("settings content leaked")
			}
			if valid {
				after, e := os.ReadFile(path)
				if e != nil || string(after) != body {
					t.Fatal("settings modified")
				}
			}
		})
	}
}

func TestClaudeLocalSettingsGitFailureDoesNotMeanAbsent(t *testing.T) {
	root, _, worktree := claudeLocalRepo(t)
	bin := filepath.Join(root, "bin")
	claudeLocalWrite(t, filepath.Join(bin, "git"), "#!/bin/sh\nexit 9\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := claudeLocalSettingsPaths(context.Background(), Scope{Cwd: worktree, Instance: Instance{Kind: "claude"}})
	if err == nil {
		t.Fatal("failed Git read hid repository sources")
	}
}

func claudeLocalRepo(t *testing.T, evidenceRoot ...string) (root, repo, worktree string) {
	t.Helper()
	root = t.TempDir()
	if len(evidenceRoot) > 0 {
		root = evidenceRoot[0]
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	repo, worktree = filepath.Join(root, "repo"), filepath.Join(root, "worktree")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
	}
	git("init", "--quiet", repo)
	git("-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	git("-C", repo, "worktree", "add", "--detach", worktree)
	for _, dir := range []string{filepath.Join(repo, "nested"), filepath.Join(worktree, "nested"), filepath.Join(root, "ordinary"), filepath.Join(root, "profile")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestClaudeLocalIdentityRejectedBeforeBridge(t *testing.T) {
	for _, source := range []string{"project", "cwd-local", "primary-local"} {
		t.Run(source, func(t *testing.T) {
			root, repo, worktree := claudeLocalRepo(t)
			cwd := filepath.Join(worktree, "nested")
			path := filepath.Join(cwd, ".claude/settings.json")
			if source == "cwd-local" {
				path = filepath.Join(cwd, ".claude/settings.local.json")
			}
			if source == "primary-local" {
				path = filepath.Join(repo, ".claude/settings.local.json")
			}
			claudeLocalWrite(t, path, `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
			state := filepath.Join(root, "runtime")
			r := NewClaude(state)
			t.Cleanup(func() { _ = r.Close() })
			scope := Scope{Cwd: cwd, Instance: Instance{ID: "claude", Kind: "claude", Profile: filepath.Join(root, "profile"), NodeExecutable: filepath.Join(root, "must-not-launch")}}
			for _, kind := range []string{"create", "send", "respond", "prepare_execution", "apply_execution"} {
				_, err := r.Execute(context.Background(), Intent{ID: "owned", Kind: kind, Scope: scope})
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "覆盖") {
					t.Errorf("%s did not reject original source: %v", kind, err)
				}
			}
			if _, err := os.Stat(filepath.Join(state, "runtimes")); !os.IsNotExist(err) {
				t.Fatal("bridge was prepared before local source rejection")
			}
		})
	}
}

func TestClaudeLocalSettingsGitOutputLimit(t *testing.T) {
	var output claudeGitOutput
	// Exercise io.Copy's ReaderFrom dispatch, as used by os/exec pipe copies.
	_, err := io.Copy(&output, struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 32<<10))})
	if err == nil || output.data.Len() > 16<<10 {
		t.Fatal("Git output exceeded the read budget")
	}
}

func TestClaudeLocalSettingsUnknownGitLayout(t *testing.T) {
	for _, layout := range []string{"bare", "external-dot-git"} {
		t.Run(layout, func(t *testing.T) {
			root, _, _ := claudeLocalRepo(t)
			cwd := filepath.Join(root, "other")
			args := []string{"init", "--quiet", "--bare", cwd}
			if layout == "external-dot-git" {
				metadata := filepath.Join(root, "external/.git")
				if err := os.MkdirAll(filepath.Dir(metadata), 0700); err != nil {
					t.Fatal(err)
				}
				args = []string{"init", "--quiet", "--separate-git-dir", metadata, cwd}
			}
			cmd := exec.Command("git", args...)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture git: %v %s", err, out)
			}
			if _, err := claudeLocalSettingsPaths(context.Background(), Scope{Cwd: cwd, Instance: Instance{Kind: "claude"}}); err == nil {
				t.Fatal("unverified Git layout accepted as a primary repository")
			}
		})
	}
}
