package runtime

import (
	"slices"
	"time"
)

// ConfigurationFailure is diagnostic evidence, independent of the native
// transaction outcome. Detail is derived locally, never copied from CLI errors.
type ConfigurationFailure struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
	Baseline  string `json:"baseline"`
	Stage     string `json:"stage"`
	Reason    string `json:"reason"`
	Cleanup   string `json:"cleanup"`
	At        string `json:"at"`
	Detail    string `json:"detail,omitempty"`
}

func claudeConfigurationFailure(in Intent, s claudeSnapshot) *ConfigurationFailure {
	f, c := s.ConfigurationFailure, s.Configuration
	if f == nil || c == nil || in.ID == "" || in.ConfigurationBaseline == "" || f.Version != 1 || f.Operation != in.ID || f.Baseline != in.ConfigurationBaseline || f.Operation != c.Operation || f.Baseline != c.Baseline || !claudeChangeBinding(in, s) {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, f.At); err != nil {
		return nil
	}
	stages := map[string]string{"open_original": "重开原查询", "check_original_components": "核对原能力组件", "close_original": "关闭原查询", "check_original_history": "核对原历史", "open_candidate": "启动候选配置", "check_settings": "核对候选设置", "check_candidate_history": "核对候选历史", "check_components": "核对能力组件", "check_idle": "核对空闲状态", "check_sources": "复核来源文件", "commit": "保存原生结果"}
	stage, ok := stages[f.Stage]
	if !ok || (f.Cleanup != "confirmed" && f.Cleanup != "unconfirmed") || (f.Reason != "stage_failed" && !(f.Stage == "check_settings" && (f.Reason == "settings_parse_errors" || f.Reason == "policy_unreadable"))) {
		return nil
	}
	if !slices.Equal(c.Target, in.Scope.MCPSelections) || !slices.Equal(c.SkillTarget, in.Scope.SkillSelections) || !EqualPluginSelections(c.PluginTarget, in.Scope.PluginSelections) {
		return nil
	}
	previousSources := in.Scope.ClaudeSettingsSources
	if in.PreviousScope != nil && c.SettingsTarget == nil && c.InstalledTarget == nil && c.InstalledPrevious == nil {
		return nil
	}
	if (c.InstalledTarget != nil || c.InstalledPrevious != nil) && (in.PreviousScope == nil || !claudeInstalledChangeScopes(*in.PreviousScope, in.Scope) && !claudeInstalledChangeScopes(in.Scope, *in.PreviousScope) && !claudeSettingsAdoptionScopes(*in.PreviousScope, in.Scope) ||
		!equalClaudeInstalledSettings(c.InstalledTarget, in.Scope.ClaudeInstalledSettings) || !equalClaudeInstalledSettings(c.InstalledPrevious, in.PreviousScope.ClaudeInstalledSettings)) {
		return nil
	}
	if c.SettingsTarget != nil {
		if in.PreviousScope == nil || !claudeSettingsAdoptionScopes(*in.PreviousScope, in.Scope) || !slices.Equal(c.SettingsTarget, in.Scope.ClaudeSettingsSources) || !slices.Equal(c.SettingsPrevious, in.PreviousScope.ClaudeSettingsSources) {
			return nil
		}
		previousSources = in.PreviousScope.ClaudeSettingsSources
	}
	previous := slices.Equal(s.Config.MCPSelections, c.Previous) && slices.Equal(s.Config.SkillSelections, c.SkillPrevious) && EqualPluginSelections(s.Config.PluginSelections, c.PluginPrevious) && slices.Equal(s.Config.SettingsSources, previousSources) && equalClaudeInstalledSettings(s.Config.InstalledSettings, c.InstalledPrevious)
	target := slices.Equal(s.Config.MCPSelections, c.Target) && slices.Equal(s.Config.SkillSelections, c.SkillTarget) && EqualPluginSelections(s.Config.PluginSelections, c.PluginTarget) && slices.Equal(s.Config.SettingsSources, in.Scope.ClaudeSettingsSources) && equalClaudeInstalledSettings(s.Config.InstalledSettings, c.InstalledTarget)
	if !previous && !target {
		return nil
	}
	copy := *f
	copy.Detail = stage + "失败；实际配置仍待核对，不自动回滚或重放。"
	if f.Reason == "settings_parse_errors" {
		copy.Detail = "候选设置存在原生解析错误，部分设置被跳过；实际配置仍待核对。"
	}
	if f.Reason == "policy_unreadable" {
		copy.Detail = "候选的原生策略来源无法读取；实际配置仍待核对。"
	}
	return &copy
}
