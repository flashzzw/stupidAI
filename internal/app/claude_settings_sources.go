package app

import (
	"context"
	"fmt"
	"slices"

	"codex-netcap/internal/runtime"
)

type ClaudeLocalSettingsSnapshot struct {
	Version      int                            `json:"version"`
	GuardVersion int                            `json:"guard_version,omitempty"`
	Sources      []runtime.ClaudeSettingsSource `json:"sources"`
}

func captureClaudeLocalSettings(scope runtime.Scope) (*ClaudeLocalSettingsSnapshot, error) {
	sources, err := runtime.ReadClaudeLocalSettingsSources(context.Background(), scope)
	if err != nil {
		return nil, err
	}
	return &ClaudeLocalSettingsSnapshot{Version: 2, GuardVersion: 1, Sources: sources}, nil
}

func claudeNativeSettingsSources(snapshot *ExecutionSnapshot) []runtime.ClaudeSettingsSource {
	if snapshot == nil || snapshot.ClaudeLocalSettings == nil || snapshot.ClaudeLocalSettings.Version != 2 {
		return nil
	}
	return queryCopy(snapshot.ClaudeLocalSettings.Sources)
}

func claudeSettingsGuardVersion(snapshot *ExecutionSnapshot) int {
	if snapshot == nil || snapshot.ClaudeLocalSettings == nil {
		return 0
	}
	return snapshot.ClaudeLocalSettings.GuardVersion
}

func checkClaudeLocalSettings(snapshot *ExecutionSnapshot) []SourceCheck {
	saved := snapshot.ClaudeLocalSettings
	if saved == nil {
		return nil
	} // Do not invent local versions for legacy records.
	issue := func(detail string) []SourceCheck {
		return []SourceCheck{{Name: "Claude 项目/本地设置", Path: snapshot.Cwd, Status: "unavailable", Detail: detail}}
	}
	if (saved.Version != 1 && saved.Version != 2) || (saved.GuardVersion != 0 && saved.GuardVersion != 1) || len(saved.Sources) < 2 || len(saved.Sources) > 3 || snapshot.Instance.Kind != "claude" {
		return issue("本地设置版本记录无效或尚不支持")
	}
	current, err := runtime.ReadClaudeLocalSettingsSources(context.Background(), runtime.Scope{Instance: snapshot.Instance, Cwd: snapshot.Cwd})
	if err != nil {
		return issue(err.Error())
	}
	checks := []SourceCheck{}
	paths := func(sources []runtime.ClaudeSettingsSource) []string {
		out := make([]string, 0, len(sources))
		for _, s := range sources {
			out = append(out, s.Path)
		}
		return out
	}
	if !slices.Equal(paths(current), paths(saved.Sources)) {
		checks = append(checks, SourceCheck{Name: "Claude 项目/本地设置来源集合", Path: snapshot.Cwd, Status: "changed", Detail: "Git/cwd 对应的设置路径集合已变化，不能采用未记录的来源：" + snapshot.Cwd})
		for _, source := range current {
			if !slices.Contains(paths(saved.Sources), source.Path) {
				checks = append(checks, SourceCheck{Name: "Claude 新增本地设置来源", Path: source.Path, Status: "added", Detail: "当前发现了未记录的设置路径：" + source.Path})
			}
		}
	}
	for _, source := range saved.Sources {
		item := SourceCheck{Name: "Claude 本地设置文件", Path: source.Path, Status: "unchanged"}
		index := slices.IndexFunc(current, func(s runtime.ClaudeSettingsSource) bool { return s.Path == source.Path })
		if index < 0 {
			item.Status, item.Detail = "changed", "此路径已不在当前 Git/cwd 设置来源集合中："+source.Path
		} else if current[index] != source {
			item.Status, item.Detail = "changed", fmt.Sprintf("记录的文件内容、权限、存在状态或解析路径已变化：%s", source.Path)
		}
		checks = append(checks, item)
	}
	return checks
}
