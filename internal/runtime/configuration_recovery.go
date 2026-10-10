package runtime

import (
	"context"
	"fmt"
	"time"
)

type RecoveryCondition struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
}
type ConfigurationRecoveryCheck struct {
	OriginalQueryRetry *ClaudeOriginalQueryRecovery `json:"original_query_retry,omitempty"`
	InstalledRecovery  *ClaudeInstalledSettings     `json:"installed_recovery,omitempty"`
	Version            int                          `json:"version"`
	NativeID           string                       `json:"native_id"`
	Operation          string                       `json:"operation"`
	Baseline           string                       `json:"baseline"`
	At                 string                       `json:"at"`
	Checks             []RecoveryCondition          `json:"checks"`
	Source             string                       `json:"source"`
	ReadOnly           bool                         `json:"read_only"`
}
type ConfigurationRecoveryReader interface {
	ReadConfigurationRecovery(context.Context, Intent) (ConfigurationRecoveryCheck, error)
}

func (a *Adapters) ReadConfigurationRecovery(ctx context.Context, in Intent) (ConfigurationRecoveryCheck, error) {
	r, err := a.adapter(in.Scope.Instance.Kind)
	if err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	reader, ok := r.(ConfigurationRecoveryReader)
	if !ok {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("此后端尚无配置恢复条件核对入口")
	}
	return reader.ReadConfigurationRecovery(ctx, in)
}
func (c *Claude) ReadConfigurationRecovery(ctx context.Context, in Intent) (ConfigurationRecoveryCheck, error) {
	if in.Kind != "apply_execution" || in.Scope.Instance.Kind != "claude" || in.PreviousScope == nil || in.Scope.Purpose != "" {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("需要原 Claude 设置采纳操作")
	}
	if err := VerifyStateNamespace(in.Scope); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A bridge may be restored to read its journal. This method never opens a
	// native query, reloads resources, sends input or changes the transaction.
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	params := map[string]any{"native_id": in.Scope.NativeID, "operation": in.ID, "baseline": in.ConfigurationBaseline}
	if in.ClaudeInstalledRecovery != nil {
		if p.installedSettingsRecovery != 1 || !ValidClaudeInstalledSettingsRecovery(*in.PreviousScope, in.Scope, in.ClaudeInstalledRecovery) {
			return ConfigurationRecoveryCheck{}, fmt.Errorf("当前恢复计划或 Claude bridge 不支持安装设置恢复版本")
		}
		params["installed_recovery"] = &in.ClaudeInstalledRecovery.Binding
	}
	var result struct {
		ConfigurationRecoveryCheck
		Snapshot claudeSnapshot `json:"snapshot"`
	}
	if err := p.call(ctx, "configuration_recovery", params, &result); err != nil {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("无法读取原生配置恢复条件")
	}
	if claudeConfigurationFailure(in, result.Snapshot) == nil {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("恢复核对缺少匹配的原操作、候选或失败记录")
	}
	if err := ValidateConfigurationRecoveryCheck(in, result.ConfigurationRecoveryCheck); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	if result.OriginalQueryRetry != nil && p.originalQueryRecovery != 1 {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("当前 Claude bridge 没有声明原查询恢复协议")
	}
	if err := validateClaudeOriginalQueryRecoveryCheck(in, result.Snapshot, result.ConfigurationRecoveryCheck); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	startupScope := *in.PreviousScope
	if in.ClaudeInstalledRecovery != nil {
		startupScope.ClaudeInstalledSettings = &in.ClaudeInstalledRecovery.Binding
	} else if startupScope.ClaudeInstalledSettings == nil {
		startupScope = in.Scope
	}
	if err := verifyClaudeInstalledStartup(ctx, startupScope); err != nil {
		for i := range result.Checks {
			if result.Checks[i].Kind == "original_settings" {
				result.Checks[i].Status = "blocked"
			}
		}
	}
	return result.ConfigurationRecoveryCheck, nil
}

func ValidateConfigurationRecoveryCheck(in Intent, out ConfigurationRecoveryCheck) error {
	_, err := time.Parse(time.RFC3339Nano, out.At)
	if out.Version != 1 || !out.ReadOnly || out.Source != in.Scope.Instance.Kind+".configuration_recovery" || out.NativeID != in.Scope.NativeID || out.Operation != in.ID || out.Baseline != in.ConfigurationBaseline || err != nil {
		return fmt.Errorf("恢复条件证据与原操作不一致")
	}
	if in.ClaudeInstalledRecovery == nil && out.InstalledRecovery != nil || in.ClaudeInstalledRecovery != nil && !equalClaudeInstalledSettings(&in.ClaudeInstalledRecovery.Binding, out.InstalledRecovery) {
		return fmt.Errorf("恢复核对没有确认本次安装设置来源版本")
	}
	if out.OriginalQueryRetry != nil && (in.Scope.Instance.Kind != "claude" || in.PreviousScope == nil ||
		in.Scope.ClaudeInstalledSettings == nil && in.PreviousScope.ClaudeInstalledSettings == nil ||
		!validClaudeOriginalQueryRecovery(out.OriginalQueryRetry, in.ID, in.ConfigurationBaseline)) {
		return fmt.Errorf("原查询恢复方式缺少匹配的原失败和安装事务")
	}
	want := map[string]bool{}
	kinds := []string{"transaction", "owned_query_stopped", "work_clear", "original_history", "original_settings", "read_stable"}
	if in.Scope.Instance.Kind == "codex" {
		kinds = []string{"transaction", "thread_unloaded", "work_clear", "original_history", "original_configuration", "read_stable"}
	} else if in.Scope.Instance.Kind != "claude" {
		return fmt.Errorf("恢复条件后端不受支持")
	}
	for _, kind := range kinds {
		want[kind] = false
	}
	for _, c := range out.Checks {
		seen, ok := want[c.Kind]
		if !ok || seen || (c.Status != "passed" && c.Status != "blocked" && c.Status != "unavailable") {
			return fmt.Errorf("恢复条件证据不完整或包含未知检查")
		}
		want[c.Kind] = true
	}
	for _, seen := range want {
		if !seen {
			return fmt.Errorf("恢复条件证据缺失")
		}
	}
	return nil
}
