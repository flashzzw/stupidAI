package runtime

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"
)

type claudeConfigurationRecovery struct {
	OriginalQueryRetry *ClaudeOriginalQueryRecovery `json:"original_query_retry,omitempty"`
	InstalledSettings  *ClaudeInstalledSettings     `json:"installed_settings,omitempty"`
	Operation          string                       `json:"operation"`
	Status             string                       `json:"status"`
	At                 string                       `json:"at"`
	HistoryDigest      string                       `json:"history_digest"`
	Inventory          claudeMCPInventory           `json:"inventory"`
	Skills             claudeSkillInventory         `json:"skills"`
	Plugins            claudePluginInventory        `json:"plugins"`
}

func recoveryOriginalIntent(in Intent) (Intent, error) {
	if in.Kind != "recover_execution" || in.ID == "" || in.ID == in.ConfigurationRecoveryOf || in.ConfigurationRecoveryOf == "" || in.ConfigurationTarget == nil || !claudeSettingsAdoptionScopes(in.Scope, *in.ConfigurationTarget) && !claudeInstalledChangeScopes(in.Scope, *in.ConfigurationTarget) {
		return Intent{}, fmt.Errorf("恢复操作缺少原版本或失败候选绑定")
	}
	previous := in.Scope
	return Intent{ID: in.ConfigurationRecoveryOf, Kind: "apply_execution", Scope: *in.ConfigurationTarget, PreviousScope: &previous, Permission: in.Permission, ConfigurationBaseline: in.ConfigurationBaseline, ClaudeInstalledRecovery: in.ClaudeInstalledRecovery}, nil
}
func (c *Claude) recoverExecution(ctx context.Context, p *claudeTransport, in Intent) (Receipt, error) {
	failed, err := recoveryOriginalIntent(in)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	if in.ClaudeOriginalQueryRecovery != nil && (p.originalQueryRecovery != 1 ||
		!validClaudeOriginalQueryRecovery(in.ClaudeOriginalQueryRecovery, failed.ID, failed.ConfigurationBaseline)) {
		return Receipt{}, &NotSent{Reason: "当前恢复方式或 Claude bridge 不支持候选启动前的原查询恢复"}
	}
	restored, err := claudeRecoveredScope(in)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	startupScope := restored
	if startupScope.ClaudeInstalledSettings == nil {
		startupScope = failed.Scope
	}
	if err := verifyClaudeInstalledStartup(ctx, startupScope); err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	var result struct {
		ConfigurationRecoveryCheck
		Snapshot claudeSnapshot `json:"snapshot"`
	}
	checkParams := map[string]any{"native_id": in.Scope.NativeID, "operation": failed.ID, "baseline": failed.ConfigurationBaseline}
	if in.ClaudeInstalledRecovery != nil {
		if p.installedSettingsRecovery != 1 {
			return Receipt{}, &NotSent{Reason: "当前 Claude bridge 不支持安装设置恢复版本"}
		}
		checkParams["installed_recovery"] = restored.ClaudeInstalledSettings
	}
	if err = p.call(ctx, "configuration_recovery", checkParams, &result); err != nil {
		return Receipt{}, &NotSent{Reason: "无法取得原配置恢复依据"}
	}
	if claudeConfigurationFailure(failed, result.Snapshot) == nil {
		return Receipt{}, &NotSent{Reason: "原失败事务绑定不可核对"}
	}
	if err = ValidateConfigurationRecoveryCheck(failed, result.ConfigurationRecoveryCheck); err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	if err = validateClaudeOriginalQueryRecoveryCheck(failed, result.Snapshot, result.ConfigurationRecoveryCheck); err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	if !reflect.DeepEqual(in.ClaudeOriginalQueryRecovery, result.OriginalQueryRetry) {
		return Receipt{}, &NotSent{Reason: "请先核对并明确确认当前原查询恢复方式"}
	}
	for _, condition := range result.Checks {
		if condition.Status != "passed" {
			return Receipt{}, &NotSent{Reason: "原配置恢复条件未满足"}
		}
	}
	credential, err := claudeCredential(in.Scope.Route)
	if err != nil {
		return Receipt{}, err
	}
	params := map[string]any{"native_id": in.Scope.NativeID, "operation": in.ID, "original_operation": failed.ID, "baseline": in.ConfigurationBaseline, "api_key": credential}
	if in.ClaudeOriginalQueryRecovery != nil {
		params["original_query_retry"] = true
	}
	if in.ClaudeInstalledRecovery != nil {
		use, err := c.holdInstalledSettings(ctx, p, restored, true)
		if err != nil {
			return Receipt{}, &NotSent{Reason: err.Error()}
		}
		defer use.file.Close()
		params["installed_recovery"] = restored.ClaudeInstalledSettings
	}
	var out Receipt
	err = p.call(ctx, "recover_execution", params, &out)
	if err == nil && !reflect.DeepEqual(out.ClaudeOriginalQueryRecovery, in.ClaudeOriginalQueryRecovery) {
		return out, fmt.Errorf("恢复已派发但缺少匹配的原查询恢复方式回执，需要核对原操作")
	}
	if err == nil && in.ClaudeInstalledRecovery != nil {
		if !equalClaudeInstalledSettings(out.ClaudeInstalledSettings, restored.ClaudeInstalledSettings) {
			return out, fmt.Errorf("恢复已派发但缺少匹配的设置版本回执，需要核对原操作")
		}
		if err := verifyClaudeInstalledStartup(ctx, restored); err != nil {
			return out, err
		}
	}
	return out, err
}
func (c *Claude) reconcileRecovery(ctx context.Context, in Intent, receipt Receipt) (Resolution, error) {
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "claude.configuration_recovery", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "原配置恢复结果尚未核对；保持暂停，不重放"}}
	failed, err := recoveryOriginalIntent(in)
	if err != nil {
		return out, err
	}
	restored, err := claudeRecoveredScope(in)
	if err != nil {
		return out, err
	}
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return out, err
	}
	var s claudeSnapshot
	if err = p.call(ctx, "snapshot", map[string]any{"native_id": in.Scope.NativeID}, &s); err != nil {
		return out, err
	}
	change := s.Configuration
	if !claudeChangeBinding(in, s) || change == nil || change.Operation != failed.ID || change.Baseline != in.ConfigurationBaseline || !slices.Equal(change.Previous, in.Scope.MCPSelections) || !slices.Equal(change.SkillPrevious, in.Scope.SkillSelections) || !EqualPluginSelections(change.PluginPrevious, in.Scope.PluginSelections) || !slices.Equal(change.Target, failed.Scope.MCPSelections) || !slices.Equal(change.SkillTarget, failed.Scope.SkillSelections) || !EqualPluginSelections(change.PluginTarget, failed.Scope.PluginSelections) || !equalClaudeInstalledSettings(change.InstalledPrevious, in.Scope.ClaudeInstalledSettings) || !equalClaudeInstalledSettings(change.InstalledTarget, failed.Scope.ClaudeInstalledSettings) {
		return out, fmt.Errorf("恢复回执与原事务或候选绑定不一致")
	}
	if !slices.Equal(in.Scope.ClaudeSettingsSources, failed.Scope.ClaudeSettingsSources) && (change.SettingsTarget == nil || !slices.Equal(change.SettingsPrevious, in.Scope.ClaudeSettingsSources) || !slices.Equal(change.SettingsTarget, failed.Scope.ClaudeSettingsSources)) ||
		slices.Equal(in.Scope.ClaudeSettingsSources, failed.Scope.ClaudeSettingsSources) && (change.SettingsTarget != nil || change.SettingsPrevious != nil) {
		return out, fmt.Errorf("恢复回执与原本地设置版本不一致")
	}
	if failure := claudeRecoveryFailure(in, s); failure != nil {
		actual := restored
		actual.ClaudeInstalledSettings = s.Config.InstalledSettings
		out.View = projectClaude(actual, s)
		out.View.ConfigurationFailure = failure
		out.View.Status, out.View.ExecutionClear, out.View.Evidence.Complete = "unknown", false, false
	}
	if !claudeInstalledConfigMatches(restored, s) || !slices.Equal(s.Config.SettingsSources, in.Scope.ClaudeSettingsSources) || !slices.Equal(s.Config.MCPSelections, in.Scope.MCPSelections) || !slices.Equal(s.Config.SkillSelections, in.Scope.SkillSelections) || !EqualPluginSelections(s.Config.PluginSelections, in.Scope.PluginSelections) {
		return out, nil
	}
	out.View = projectClaude(restored, s)
	out.View.ConfigurationFailure = claudeRecoveryFailure(in, s)
	r := change.Recovery
	if r != nil && reflect.DeepEqual(r.OriginalQueryRetry, in.ClaudeOriginalQueryRecovery) && r.Operation == in.ID && r.Status == "verified" && change.Status == "recovered" && r.HistoryDigest == s.HistoryDigest && configurationIdle(out.View) && mcpRecoveryVerified(restored, change) && claudeRecoveredInstalledBinding(restored, change) {
		out.State, out.Status = "finished", "succeeded"
		out.Evidence.Complete = true
		out.Evidence.Detail = "原 ID 与原配置恢复已有持久核对结果；失败候选保留，自动化仍暂停，未重放候选。"
	} else {
		out.View.Status = "unknown"
		out.View.ExecutionClear = false
		out.View.Evidence.Complete = false
	}
	out.Receipt.NativeID = in.Scope.NativeID
	out.Receipt.ClaudeInstalledSettings = restored.ClaudeInstalledSettings
	if r != nil {
		out.Receipt.ClaudeOriginalQueryRecovery = r.OriginalQueryRetry
	}
	return out, nil
}
func mcpRecoveryVerified(scope Scope, change *claudeConfigurationChange) bool {
	r := change.Recovery
	if r == nil {
		return false
	}
	_, mcp := claudeMCPEvidence(scope, claudeSnapshot{MCP: r.Inventory, Plugins: r.Plugins})
	retired := &claudeConfigurationChange{Target: scope.MCPSelections, Previous: change.Target, Inventory: r.Inventory}
	return mcp && claudeReleasedMCPVerified(retired) && claudeSkillChangeVerified(scope.SkillSelections, change.SkillTarget, r.Skills) && claudePluginChangeVerified(claudeExecutionPlugins(scope), change.PluginTarget, r.Plugins) && claudeRecoveredInheritedPlugins(scope, change, r.Plugins)
}

// The durable recovery proof remains valid as historical evidence after later
// model turns. Current inventories are checked separately for a live query.
func claudeRecoveryProjectionVerified(scope Scope, s claudeSnapshot) bool {
	c := s.Configuration
	if c == nil || c.Recovery == nil || c.Recovery.Operation == "" || c.Recovery.Status != "verified" || len(c.Recovery.HistoryDigest) != 64 || !slices.Equal(scope.MCPSelections, c.Previous) || !slices.Equal(scope.SkillSelections, c.SkillPrevious) || !EqualPluginSelections(scope.PluginSelections, c.PluginPrevious) ||
		!(c.SettingsPrevious == nil && c.SettingsTarget == nil && c.InstalledTarget != nil) && !slices.Equal(scope.ClaudeSettingsSources, c.SettingsPrevious) || !claudeRecoveredInstalledBinding(scope, c) || !mcpRecoveryVerified(scope, c) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, c.Recovery.At); err != nil {
		return false
	}
	if !s.Live {
		return true
	}
	current := *c
	r := *c.Recovery
	r.Inventory, r.Skills, r.Plugins = s.MCP, s.Skills, s.Plugins
	current.Recovery = &r
	return claudeRecoveredInheritedPlugins(scope, c, s.Plugins) && mcpRecoveryVerified(scope, &current)
}
func claudeRecoveryFailure(in Intent, s claudeSnapshot) *ConfigurationFailure {
	c := s.Configuration
	if c == nil || c.Recovery == nil || c.Recovery.Operation != in.ID || c.Status != "applying" || s.ConfigurationRecoveryFailure == nil {
		return nil
	}
	// Reuse the strict diagnostic validator with the recovery's original target.
	copy := *c
	copy.Operation = in.ID
	copy.Target, copy.Previous = c.Previous, c.Target
	copy.SkillTarget, copy.SkillPrevious = c.SkillPrevious, c.SkillTarget
	copy.PluginTarget, copy.PluginPrevious = c.PluginPrevious, c.PluginTarget
	copy.SettingsTarget, copy.SettingsPrevious = c.SettingsPrevious, c.SettingsTarget
	copy.InstalledTarget, copy.InstalledPrevious = c.InstalledPrevious, c.InstalledTarget
	if in.ClaudeInstalledRecovery != nil {
		restored, err := claudeRecoveredScope(in)
		if err != nil || !equalClaudeInstalledSettings(c.Recovery.InstalledSettings, restored.ClaudeInstalledSettings) {
			return nil
		}
		copy.Target, copy.Previous = in.Scope.MCPSelections, in.Scope.MCPSelections
		copy.SkillTarget, copy.SkillPrevious = in.Scope.SkillSelections, in.Scope.SkillSelections
		copy.PluginTarget, copy.PluginPrevious = in.Scope.PluginSelections, in.Scope.PluginSelections
		copy.SettingsTarget, copy.SettingsPrevious = nil, nil
		copy.InstalledTarget, copy.InstalledPrevious = restored.ClaudeInstalledSettings, in.Scope.ClaudeInstalledSettings
		previous := in.Scope
		in.Scope, in.ConfigurationTarget = restored, &previous
	}
	s.Configuration = &copy
	s.ConfigurationFailure = s.ConfigurationRecoveryFailure
	in.PreviousScope = in.ConfigurationTarget
	f := claudeConfigurationFailure(in, s)
	if f != nil {
		f.Detail = "按原配置恢复失败，结果仍待核对；保持暂停，不重复启动。"
	}
	return f
}
