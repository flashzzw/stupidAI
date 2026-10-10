package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

func claudeSettingsAdoptionScopes(previous, target Scope) bool {
	if previous.Instance.Kind != "claude" || previous.Purpose != "" || previous.SettingsGuardVersion != 1 || len(previous.ClaudeSettingsSources) < 2 || len(previous.ClaudeSettingsSources) != len(target.ClaudeSettingsSources) || slices.Equal(previous.ClaudeSettingsSources, target.ClaudeSettingsSources) {
		return false
	}
	for i, s := range previous.ClaudeSettingsSources {
		if s.Path != target.ClaudeSettingsSources[i].Path {
			return false
		}
	}
	previous.ClaudeSettingsSources = target.ClaudeSettingsSources
	previous.MCPSelections = target.MCPSelections
	previous.SkillSelections = target.SkillSelections
	previous.PluginSelections = target.PluginSelections
	if !claudeInstalledLocalAdoption(previous, target) {
		return false
	}
	previous.ClaudeInstalledSettings = target.ClaudeInstalledSettings
	return reflect.DeepEqual(previous, target)
}

type claudeConfigurationChange struct {
	Recovery               *claudeConfigurationRecovery `json:"recovery,omitempty"`
	Operation              string                       `json:"operation"`
	Baseline               string                       `json:"baseline"`
	Target                 []MCPSelection               `json:"target"`
	Previous               []MCPSelection               `json:"previous"`
	Status                 string                       `json:"status"`
	At                     string                       `json:"at"`
	HistoryDigest          string                       `json:"history_digest"`
	Inventory              claudeMCPInventory           `json:"inventory"`
	SkillTarget            []SkillSelection             `json:"skill_target"`
	SkillPrevious          []SkillSelection             `json:"skill_previous"`
	Skills                 claudeSkillInventory         `json:"skills"`
	PluginTarget           []PluginSelection            `json:"plugin_target"`
	PluginPrevious         []PluginSelection            `json:"plugin_previous"`
	Plugins                claudePluginInventory        `json:"plugins"`
	SettingsTarget         []ClaudeSettingsSource       `json:"settings_target"`
	SettingsPrevious       []ClaudeSettingsSource       `json:"settings_previous"`
	InstalledTarget        *ClaudeInstalledSettings     `json:"installed_target,omitempty"`
	InstalledPrevious      *ClaudeInstalledSettings     `json:"installed_previous,omitempty"`
	OriginalPlugins        *claudePluginInventory       `json:"original_plugins,omitempty"`
	Progress               *claudeConfigurationProgress `json:"progress,omitempty"`
	originalPluginsPresent bool
}

func (c *claudeConfigurationChange) UnmarshalJSON(body []byte) error {
	type record claudeConfigurationChange
	var wire struct {
		record
		OriginalPlugins json.RawMessage `json:"original_plugins"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return err
	}
	*c = claudeConfigurationChange(wire.record)
	c.originalPluginsPresent = wire.OriginalPlugins != nil
	if c.originalPluginsPresent {
		return json.Unmarshal(wire.OriginalPlugins, &c.OriginalPlugins)
	}
	return nil
}

func claudeChangeBinding(in Intent, s claudeSnapshot) bool {
	return in.Scope.NativeID != "" && in.Scope.Purpose == "" && s.Config.ID == in.Scope.NativeID &&
		s.Config.Cwd == in.Scope.Cwd && s.Config.Model == in.Scope.Route.Model && s.Config.Endpoint == in.Scope.Route.Endpoint &&
		s.Config.Permission == in.Permission && s.Config.Executable == in.Scope.Instance.Executable && s.Config.SettingsFile == in.Scope.Instance.SettingsFile && s.Config.SettingsGuardVersion == in.Scope.SettingsGuardVersion
}

func (c *Claude) prepareExecution(ctx context.Context, p *claudeTransport, in Intent) (Receipt, error) {
	out := Receipt{NativeID: in.Scope.NativeID}
	view, snapshot, err := c.observe(ctx, in.Scope)
	if err != nil {
		return out, &NotSent{Reason: err.Error()}
	}
	if !claudeChangeBinding(in, snapshot) || !claudeInstalledConfigMatches(in.Scope, snapshot) || !EqualPluginSelections(snapshot.Config.PluginSelections, in.Scope.PluginSelections) || !configurationIdle(view) || snapshot.ConfigurationBaseline == "" {
		return out, &NotSent{Reason: "Claude 原身份、历史、MCP、工具或输入尚未完整核对为空闲"}
	}
	params := map[string]any{"native_id": in.Scope.NativeID}
	if in.ConfigurationTarget != nil {
		if !claudeSettingsAdoptionScopes(in.Scope, *in.ConfigurationTarget) && !claudeInstalledChangeScopes(in.Scope, *in.ConfigurationTarget) {
			return out, &NotSent{Reason: "本地设置采纳目标与原绑定不一致"}
		}
		if claudeSettingsAdoptionScopes(in.Scope, *in.ConfigurationTarget) {
			params["settings_target"] = in.ConfigurationTarget.ClaudeSettingsSources
		}
		if in.ConfigurationTarget.ClaudeInstalledSettings != nil {
			if p.settingsFileConsumer != 1 || p.installedSettings != 1 {
				return out, &NotSent{Reason: "当前 Claude bridge 不支持已安装插件执行设置协议"}
			}
			params["installed_target"] = in.ConfigurationTarget.ClaudeInstalledSettings
		}
	}
	if err := p.call(ctx, "prepare_execution", params, &out); err != nil {
		return out, err
	}
	if out.ConfigurationBaseline != snapshot.ConfigurationBaseline {
		return Receipt{}, &NotSent{Reason: "准备期间 Claude 原历史发生变化，请重新核对"}
	}
	return out, nil
}

func (c *Claude) applyExecution(ctx context.Context, p *claudeTransport, in Intent) (Receipt, error) {
	out := Receipt{NativeID: in.Scope.NativeID}
	var snapshot claudeSnapshot
	if err := p.call(ctx, "snapshot", map[string]any{"native_id": in.Scope.NativeID}, &snapshot); err != nil {
		return out, &NotSent{Reason: err.Error()}
	}
	if !claudeChangeBinding(in, snapshot) || in.ConfigurationBaseline == "" || snapshot.ConfigurationBaseline != in.ConfigurationBaseline {
		return out, &NotSent{Reason: "Claude 准备后的原历史或绑定已变化，需要重新准备"}
	}
	adopting := in.PreviousScope != nil && claudeSettingsAdoptionScopes(*in.PreviousScope, in.Scope)
	installed := in.PreviousScope != nil && (claudeInstalledChangeScopes(*in.PreviousScope, in.Scope) || adopting && in.Scope.ClaudeInstalledSettings != nil)
	if in.PreviousScope != nil && !adopting && !installed {
		return out, &NotSent{Reason: "Claude 原设置采纳绑定无法核对"}
	}
	previousSources := in.Scope.ClaudeSettingsSources
	if adopting {
		previousSources = in.PreviousScope.ClaudeSettingsSources
	}
	if !slices.Equal(previousSources, snapshot.Config.SettingsSources) {
		return out, &NotSent{Reason: "原生本地设置版本与原操作不一致"}
	}
	original := in.Scope
	if in.PreviousScope != nil {
		original = *in.PreviousScope
	}
	if !claudeInstalledConfigMatches(original, snapshot) {
		return out, &NotSent{Reason: "Claude 原执行设置与准备绑定不一致"}
	}
	original.MCPSelections = snapshot.Config.MCPSelections
	original.SkillSelections = snapshot.Config.SkillSelections
	original.PluginSelections = snapshot.Config.PluginSelections
	if !configurationIdle(projectClaude(original, snapshot)) {
		return out, &NotSent{Reason: "Claude 原会话仍有工具、输入、未决权限或未知改配"}
	}
	credential, err := claudeCredential(in.Scope.Route)
	if err != nil {
		return out, err
	}
	use, err := c.holdInstalledSettings(ctx, p, in.Scope, installed)
	if err != nil {
		return out, &NotSent{Reason: err.Error()}
	}
	if use != nil {
		defer use.file.Close()
	}
	// One explicit request owns close/start/verification. Missing wire replies
	// are reconciled against its durable outcome and never cause a retry here.
	params := map[string]any{"native_id": in.Scope.NativeID, "operation": in.ID, "baseline": in.ConfigurationBaseline, "mcp_selections": append([]MCPSelection{}, in.Scope.MCPSelections...), "skill_selections": append([]SkillSelection{}, in.Scope.SkillSelections...), "plugin_selections": append([]PluginSelection{}, in.Scope.PluginSelections...), "api_key": credential}
	if adopting {
		params["settings_sources"] = in.Scope.ClaudeSettingsSources
	}
	if in.Scope.ClaudeInstalledSettings != nil {
		params["installed_settings"] = in.Scope.ClaudeInstalledSettings
	}
	err = p.call(ctx, "apply_execution", params, &out)
	if err == nil && in.Scope.ClaudeInstalledSettings != nil {
		if !equalClaudeInstalledSettings(out.ClaudeInstalledSettings, in.Scope.ClaudeInstalledSettings) {
			return out, fmt.Errorf("候选已派发，但缺少匹配的执行设置回执；需要核对原操作")
		}
		if err := verifyClaudeInstalledStartup(ctx, in.Scope); err != nil {
			return out, err
		}
	}
	return out, err
}

func (c *Claude) reconcileExecution(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "claude.configuration", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "缺少持久的原生改配结果；只读核对，不重放"}}
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return out, err
	}
	var s claudeSnapshot
	if err = p.call(ctx, "snapshot", map[string]any{"native_id": in.Scope.NativeID}, &s); err != nil {
		return out, err
	}
	if !claudeChangeBinding(in, s) {
		return out, fmt.Errorf("改配核对的 Claude 身份或执行绑定不一致")
	}
	change := s.Configuration
	if change == nil || change.Operation != in.ID || change.Baseline != in.ConfigurationBaseline || !slices.Equal(change.Target, in.Scope.MCPSelections) {
		return out, nil
	}
	if !equalClaudeInstalledSettings(change.InstalledTarget, in.Scope.ClaudeInstalledSettings) ||
		in.PreviousScope != nil && !equalClaudeInstalledSettings(change.InstalledPrevious, in.PreviousScope.ClaudeInstalledSettings) {
		return out, nil
	}
	if failure := claudeConfigurationFailure(in, s); failure != nil && change.Status == "applying" {
		// Diagnostics explain this exact unfinished operation. They never grant
		// success, input authority, rollback or replay, even after child exit.
		actual := in.Scope
		actual.ClaudeSettingsSources = s.Config.SettingsSources
		actual.ClaudeInstalledSettings = s.Config.InstalledSettings
		actual.MCPSelections, actual.SkillSelections, actual.PluginSelections = s.Config.MCPSelections, s.Config.SkillSelections, s.Config.PluginSelections
		out.View = projectClaude(actual, s)
		out.View.ConfigurationFailure = failure
		out.View.Status, out.View.ExecutionClear = "unknown", false
		out.View.Evidence.Complete = false
		out.View.Evidence.Detail, out.Evidence.Detail = failure.Detail, failure.Detail
		if out.View.SettingsGuard != nil {
			guard := *out.View.SettingsGuard
			guard.State, guard.Detail = "unavailable", "候选配置应用失败，当前设置阻止机制不可作为已验证的配置依据。"
			out.View.SettingsGuard = &guard
		}
		return out, nil
	}
	if change.SettingsTarget != nil {
		if !slices.Equal(change.SettingsTarget, in.Scope.ClaudeSettingsSources) || !slices.Equal(s.Config.SettingsSources, in.Scope.ClaudeSettingsSources) {
			return out, nil
		}
	} else if !slices.Equal(s.Config.SettingsSources, in.Scope.ClaudeSettingsSources) {
		return out, nil
	}
	// Records from before Skill changes kept Skill selections immutable. Their
	// active config is the only Skill binding they could have applied.
	skillTarget, skillPrevious := change.SkillTarget, change.SkillPrevious
	if skillTarget == nil && skillPrevious == nil {
		skillTarget, skillPrevious = s.Config.SkillSelections, s.Config.SkillSelections
	}
	if !slices.Equal(skillTarget, in.Scope.SkillSelections) {
		return out, nil
	}
	pluginTarget, pluginPrevious, plugins := change.PluginTarget, change.PluginPrevious, change.Plugins
	if pluginTarget == nil && pluginPrevious == nil {
		// Earlier transactions could not change plugins. Current native evidence
		// can verify that immutable binding, but an offline record cannot invent it.
		pluginTarget, pluginPrevious, plugins = s.Config.PluginSelections, s.Config.PluginSelections, s.Plugins
	}
	if !EqualPluginSelections(pluginTarget, in.Scope.PluginSelections) {
		return out, nil
	}
	actual := in.Scope
	actual.ClaudeInstalledSettings = s.Config.InstalledSettings
	actual.MCPSelections = s.Config.MCPSelections
	actual.SkillSelections = s.Config.SkillSelections
	actual.PluginSelections = s.Config.PluginSelections
	out.View = projectClaude(actual, s)
	if change.Status == "not_sent" && change.InstalledTarget == nil && change.InstalledPrevious == nil && slices.Equal(change.Previous, s.Config.MCPSelections) && slices.Equal(skillPrevious, s.Config.SkillSelections) && EqualPluginSelections(pluginPrevious, s.Config.PluginSelections) {
		out.State, out.Status = "finished", "not_sent"
		out.Evidence.Complete = true
		out.Evidence.Detail = "候选查询已退出，原配置已持久保留；保持暂停，未重放"
	} else if change.Status == "verified" && claudeInstalledConfigMatches(in.Scope, s) && slices.Equal(change.Target, s.Config.MCPSelections) && slices.Equal(skillTarget, s.Config.SkillSelections) && EqualPluginSelections(pluginTarget, s.Config.PluginSelections) && s.HistoryDigest == change.HistoryDigest && configurationIdle(out.View) {
		_, verified := claudeMCPEvidence(in.Scope, claudeSnapshot{MCP: change.Inventory, Plugins: plugins})
		skillsVerified := change.SkillTarget == nil && change.SkillPrevious == nil || claudeSkillChangeVerified(skillTarget, skillPrevious, change.Skills)
		pluginTarget = claudeExecutionPlugins(Scope{PluginSelections: pluginTarget, ClaudeInstalledSettings: change.InstalledTarget})
		pluginPrevious = claudeExecutionPlugins(Scope{PluginSelections: pluginPrevious, ClaudeInstalledSettings: change.InstalledPrevious})
		if verified && skillsVerified && claudePluginChangeVerified(pluginTarget, pluginPrevious, plugins) && claudeReleasedMCPVerified(change) {
			out.State, out.Status = "finished", "succeeded"
			out.Evidence.Complete = true
			out.Evidence.Detail = "原 ID、目标绑定和成功加载清单已有原子持久结果，历史未变化；当前连接另行核对，未重放"
		}
	}
	out.Receipt.NativeID = in.Scope.NativeID
	out.Receipt.ClaudeInstalledSettings = in.Scope.ClaudeInstalledSettings
	delete(c.cache, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
	return out, nil
}

func claudeReleasedMCPVerified(change *claudeConfigurationChange) bool {
	for _, old := range change.Previous {
		if slices.ContainsFunc(change.Target, func(s MCPSelection) bool { return s.Server == old.Server }) {
			continue
		}
		for _, server := range change.Inventory.Servers {
			if server.Name == old.Server && (server.Source == "" || server.Source == "dynamic") {
				return false
			}
		}
	}
	return true
}
