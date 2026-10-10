package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type claudeGitOutput struct{ data bytes.Buffer }

func (b *claudeGitOutput) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > 16<<10 {
		return 0, fmt.Errorf("Git settings roots output exceeded limit")
	}
	return b.data.Write(p)
}

// The pinned CLI uses cwd project settings and a canonical primary-repository
// local store, merged with the legacy cwd-local file. The public SDK resolver
// omits that Git root callback; do not use its source list as a complete graph.
func claudeLocalSettingsPaths(ctx context.Context, scope Scope) ([]string, error) {
	if !filepath.IsAbs(scope.Cwd) {
		return nil, fmt.Errorf("Claude 项目设置需要已绑定的绝对工作目录")
	}
	cwd, err := filepath.EvalSymlinks(scope.Cwd)
	if err != nil {
		return nil, fmt.Errorf("Claude 项目设置工作目录无法核对")
	}
	paths := []string{filepath.Join(cwd, ".claude/settings.json"), filepath.Join(cwd, ".claude/settings.local.json")}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, "git", "-C", cwd, "rev-parse", "--path-format=absolute", "--is-bare-repository", "--show-toplevel", "--git-common-dir", "--absolute-git-dir")
	// Only normalize diagnostic language; preserve the native Git environment
	// and lookup semantics. Exit 128 alone does not prove repository absence.
	cmd.Env, cmd.Dir = append(claudeEnv(scope.Instance), "LC_ALL=C"), cwd
	cmd.WaitDelay = time.Second
	var output claudeGitOutput
	var diagnostic claudeGitOutput
	cmd.Stdout = &output
	cmd.Stderr = &diagnostic
	err = cmd.Run()
	if check.Err() != nil {
		return nil, fmt.Errorf("Claude Git 设置来源读取已取消或超时")
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 128 && output.data.Len() == 0 && strings.TrimSpace(diagnostic.data.String()) == "fatal: not a git repository (or any of the parent directories): .git" {
			return paths, nil
		}
		return nil, fmt.Errorf("Claude Git 设置来源无法核对；不推断本地来源缺席")
	}
	lines := strings.Split(strings.TrimSuffix(output.data.String(), "\n"), "\n")
	if len(lines) != 4 || lines[0] != "false" || !filepath.IsAbs(lines[1]) || !filepath.IsAbs(lines[2]) || !filepath.IsAbs(lines[3]) {
		return nil, fmt.Errorf("Claude Git 设置来源格式尚未核对")
	}
	common, err := filepath.EvalSymlinks(lines[2])
	if err != nil || filepath.Base(common) != ".git" {
		return nil, fmt.Errorf("Claude 外置或非标准 Git 设置根尚未核对")
	}
	primary := filepath.Dir(common)
	top, topErr := filepath.EvalSymlinks(lines[1])
	gitDir, gitErr := filepath.EvalSymlinks(lines[3])
	within, relErr := filepath.Rel(top, cwd)
	if topErr != nil || gitErr != nil || relErr != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("Claude Git 工作区与工作目录的对应关系尚未核对")
	}
	if gitDir == common {
		if top != primary {
			return nil, fmt.Errorf("Claude 外置 Git 元数据与本地设置根的对应关系尚未核对")
		}
	} else {
		worktreeKey, relErr := filepath.Rel(filepath.Join(common, "worktrees"), gitDir)
		marker, markerErr := os.Lstat(filepath.Join(top, ".git"))
		if relErr != nil || worktreeKey == "." || worktreeKey == ".." || strings.ContainsRune(worktreeKey, filepath.Separator) || markerErr != nil || !marker.Mode().IsRegular() {
			return nil, fmt.Errorf("Claude worktree 元数据与本地设置根的对应关系尚未核对")
		}
	}
	if primary == cwd {
		return paths, nil
	}
	// Match the native POSIX ownership/home boundary instead of treating every
	// ancestor, including an intermediate worktree, as an active local source.
	home := ""
	for _, entry := range cmd.Env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			home = value
		}
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("Claude 本地设置 HOME 边界无法核对")
	}
	if primary == home {
		return paths, nil
	}
	for _, path := range []string{primary, filepath.Join(primary, ".git"), filepath.Join(primary, ".claude")} {
		info, e := os.Lstat(path)
		if path == filepath.Join(primary, ".claude") && os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, fmt.Errorf("Claude 本地设置根归属无法核对：%s", path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, fmt.Errorf("Claude 本地设置根的文件归属不可用")
		}
		if stat.Uid != uint32(os.Geteuid()) {
			return paths, nil
		}
	}
	return append(paths, filepath.Join(primary, ".claude/settings.local.json")), nil
}
