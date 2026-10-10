package app

import (
	"codex-netcap/internal/runtime"
	"context"
	"fmt"
)

type LocalSettingsChange struct {
	Before runtime.ClaudeSettingsSource `json:"before"`
	After  runtime.ClaudeSettingsSource `json:"after"`
}

func localSettingsAdoption(before, after *ExecutionSnapshot) bool {
	return before != nil && after != nil && before.Instance.Kind == "claude" && before.ClaudeLocalSettings != nil && after.ClaudeLocalSettings != nil && digest(before.ClaudeLocalSettings) != digest(after.ClaudeLocalSettings)
}

func captureLocalSettingsAdoption(session Session, candidate *ExecutionSnapshot) ([]LocalSettingsChange, error) {
	old := session.ExecutionSnapshot.ClaudeLocalSettings
	if session.Scope.Instance.Kind != "claude" || old == nil || old.Version != 2 || old.GuardVersion != 1 {
		return nil, fmt.Errorf("此执行尚无原生本地文件版本绑定，不能直接采纳新设置")
	}
	if err := runtime.ValidateClaudeExecutionSettings(context.Background(), session.Scope); err != nil {
		return nil, err
	}
	next, err := captureClaudeLocalSettings(session.Scope)
	if err != nil {
		return nil, err
	}
	if len(old.Sources) != len(next.Sources) {
		return nil, fmt.Errorf("Git/cwd 设置来源集合变化尚不支持在原 ID 上采纳")
	}
	changes := []LocalSettingsChange{}
	for i, s := range old.Sources {
		if s.Path != next.Sources[i].Path {
			return nil, fmt.Errorf("Git/cwd 设置来源路径变化尚不支持在原 ID 上采纳")
		}
		if s != next.Sources[i] {
			changes = append(changes, LocalSettingsChange{Before: s, After: next.Sources[i]})
		}
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("本地设置文件版本未变化，无需采纳")
	}
	candidate.ClaudeLocalSettings = next
	if session.Scope.ClaudeInstalledSettings != nil {
		proof := candidate.ClaudeInstalledPluginSettings
		binding, startup, err := runtime.AdoptClaudeInstalledSettings(context.Background(), session.Scope, proof.StartupSettings, next.Sources)
		if err != nil {
			return nil, err
		}
		proof.Binding, proof.StartupSettings = binding, startup
	}
	return changes, nil
}
