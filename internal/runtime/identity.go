package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

// StateNamespace describes the actual local CLI state directory, not the
// display name of an execution profile. WSL host identity is a later boundary.
func StateNamespace(scope Scope) (string, error) {
	if scope.Purpose == "probe" || (scope.Instance.Kind != "codex" && scope.Instance.Kind != "claude") {
		return "", nil
	}
	path, err := filepath.Abs(scope.Instance.Profile)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("原生状态目录无法解析: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("原生状态来源必须是目录")
	}
	return path, nil
}

func VerifyStateNamespace(scope Scope) error {
	// Older records have no saved namespace proof. Preserve their compatibility
	// rather than manufacturing historical evidence from today's symlink target.
	if scope.StateNamespace == "" {
		return nil
	}
	current, err := StateNamespace(scope)
	if err != nil {
		return err
	}
	if current != scope.StateNamespace {
		return fmt.Errorf("原生状态目录归属已变化；保留原会话，不能按配置别名恢复")
	}
	return nil
}
