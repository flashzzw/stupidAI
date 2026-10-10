package runtime

import (
	"context"
	"fmt"
)

// Alias preserves the existing Claude source wire contract.
type ClaudeSettingsSource = FileSource

func ReadClaudeLocalSettingsSources(ctx context.Context, scope Scope) ([]ClaudeSettingsSource, error) {
	if scope.Instance.Kind != "claude" || scope.Purpose != "" {
		return nil, fmt.Errorf("本地设置版本只支持普通 Claude 执行")
	}
	paths, err := claudeLocalSettingsPaths(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make([]ClaudeSettingsSource, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source, _, err := readClaudeSettingsFile(path, true)
		if err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, nil
}

func readClaudeSettingsFile(path string, optional bool) (ClaudeSettingsSource, []byte, error) {
	return readSourceFile(path, optional, "Claude settings")
}
