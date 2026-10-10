package runtime

import (
	"fmt"
	"path/filepath"
)

// CodexUserHookSource records the default user hook file, including absence.
// It is a file version, not evidence of native loading or complete hook policy.
type CodexUserHookSource struct {
	Version int        `json:"version"`
	File    FileSource `json:"file"`
}

func ReadCodexUserHooks(scope Scope) (*CodexUserHookSource, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || !filepath.IsAbs(scope.Instance.Profile) {
		return nil, fmt.Errorf("用户 hook 文件版本需要普通 Codex 执行及原配置目录")
	}
	file, _, err := readSourceFile(filepath.Join(scope.Instance.Profile, "hooks.json"), true, "Codex 用户 hook")
	if err != nil {
		return nil, err
	}
	return &CodexUserHookSource{Version: 1, File: file}, nil
}

func VerifyCodexUserHooks(scope Scope) error {
	scope = codexHookSourceScope(scope)
	if scope.CodexUserHooks == nil {
		return nil
	} // Legacy records have no invented baseline.
	if scope.CodexUserHooks.Version != 1 {
		return fmt.Errorf("Codex 用户 hook 文件版本格式尚未核对")
	}
	current, err := ReadCodexUserHooks(scope)
	if err != nil {
		return err
	}
	if *current != *scope.CodexUserHooks {
		return fmt.Errorf("Codex 用户 hook 文件内容、权限、路径或存在状态已变化：%s", current.File.Path)
	}
	return nil
}
