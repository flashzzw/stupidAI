package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"codex-netcap/internal/codex"
)

// A recovery is a new, explicit establishment of the recorded original
// configuration. It never certifies that the uncertain candidate took effect.
func codexRecoveryIntent(in Intent) (Intent, error) {
	if in.Kind != "recover_execution" || in.ID == "" || in.ConfigurationRecoveryOf == "" || in.ID == in.ConfigurationRecoveryOf || in.ConfigurationTarget == nil {
		return Intent{}, fmt.Errorf("恢复缺少原操作和候选绑定")
	}
	out := in
	out.PreviousScope = in.ConfigurationTarget
	out.ConfigurationTarget = nil
	if err := validateCodexPluginChange(out); err != nil {
		return Intent{}, err
	}
	return out, nil
}
func codexFailedRecoveryIntent(in Intent) (Intent, error) {
	recovered, err := codexRecoveryIntent(in)
	if err != nil {
		return Intent{}, err
	}
	original := recovered.Scope
	return Intent{ID: in.ConfigurationRecoveryOf, Kind: "apply_execution", Scope: *recovered.PreviousScope, PreviousScope: &original, ConfigurationBaseline: in.ConfigurationBaseline, Permission: in.Permission}, nil
}
func (c *Codex) ReadConfigurationRecovery(ctx context.Context, in Intent) (ConfigurationRecoveryCheck, error) {
	if in.Kind != "apply_execution" {
		return ConfigurationRecoveryCheck{}, fmt.Errorf("需要尚待核对的原改配操作")
	}
	if err := validateCodexPluginChange(in); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	if err := VerifyStateNamespace(in.Scope); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, in.Scope.Instance)
	if err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	return c.checkConfigurationRecovery(ctx, p, in)
}
func (c *Codex) checkConfigurationRecovery(ctx context.Context, p codexConnection, failed Intent) (ConfigurationRecoveryCheck, error) {
	if err := validateCodexPluginChange(failed); err != nil {
		return ConfigurationRecoveryCheck{}, err
	}
	original := *failed.PreviousScope
	original.RecoveryPermission = failed.Permission
	out := ConfigurationRecoveryCheck{Version: 1, NativeID: original.NativeID, Operation: failed.ID, Baseline: failed.ConfigurationBaseline, At: time.Now().UTC().Format(time.RFC3339Nano), Source: "codex.configuration_recovery", ReadOnly: true}
	add := func(kind string, ok bool) {
		status := "blocked"
		if ok {
			status = "passed"
		}
		out.Checks = append(out.Checks, RecoveryCondition{Kind: kind, Status: status})
	}
	add("transaction", failed.ID != "" && failed.Kind == "apply_execution")
	connection := p.ConnectionID()
	first, readErr := codex.Read(ctx, p, original.NativeID)
	unloadedErr := codexRecoveryUnloaded(ctx, p, original.NativeID)
	add("thread_unloaded", readErr == nil && first.ID == original.NativeID && first.Status.Type == "notLoaded" && unloadedErr == nil)
	// Inventory and configuration witnesses do not exist for an unloaded
	// thread. Observe all execution scopes without inventing selection evidence.
	neutral := original
	neutral.MCPSelections, neutral.SkillSelections, neutral.PluginSelections = nil, nil, nil
	neutral.NoAutoResume, neutral.ConfigurationBaseline = true, failed.ConfigurationBaseline
	view, viewErr := c.observe(ctx, neutral)
	add("work_clear", viewErr == nil && view.NativeID == original.NativeID && view.NativeStatus == "notLoaded" && configurationIdle(view))
	history, historyErr := configurationBaseline(ctx, p, original.NativeID)
	add("original_history", historyErr == nil && history == failed.ConfigurationBaseline)
	_, configErr := codexRouteConfig(ctx, p, original)
	fingerprint, fingerprintErr := codexConfigurationFingerprint(ctx, p, original)
	routeErr := verifyCodexThreadRoute(ctx, p, original)
	add("original_configuration", configErr == nil && fingerprintErr == nil && routeErr == nil)
	last, lastErr := codex.Read(ctx, p, original.NativeID)
	lastHistory, lastHistoryErr := configurationBaseline(ctx, p, original.NativeID)
	lastFingerprint, lastFingerprintErr := codexConfigurationFingerprint(ctx, p, original)
	lastUnloaded := codexRecoveryUnloaded(ctx, p, original.NativeID)
	add("read_stable", readErr == nil && lastErr == nil && connection == p.ConnectionID() && first.ID == last.ID && first.Status.Type == last.Status.Type && historyErr == nil && lastHistoryErr == nil && history == lastHistory && fingerprintErr == nil && lastFingerprintErr == nil && fingerprint == lastFingerprint && lastUnloaded == nil)
	return out, nil
}
func codexRecoveryUnloaded(ctx context.Context, p codexConnection, id string) error {
	thread, err := codex.Read(ctx, p, id)
	if err != nil || thread.ID != id || thread.Status.Type != "notLoaded" {
		return fmt.Errorf("原线程仍加载或状态无法核对")
	}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 1000; page++ {
		var result struct {
			Data *[]string `json:"data"`
			Next string    `json:"nextCursor"`
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := p.Call(ctx, "thread/loaded/list", params, &result); err != nil || result.Data == nil {
			return fmt.Errorf("原生加载清单不完整")
		}
		for _, loaded := range *result.Data {
			if loaded == "" || loaded == id {
				return fmt.Errorf("原生加载清单未确认原线程未加载")
			}
		}
		if result.Next == "" {
			return nil
		}
		if seen[result.Next] {
			return fmt.Errorf("原生加载清单分页未前进")
		}
		seen[result.Next] = true
		cursor = result.Next
	}
	return fmt.Errorf("原生加载清单超出核对范围")
}
func (c *Codex) recoverConfiguration(ctx context.Context, p codexConnection, in Intent) (Receipt, error) {
	target, err := codexRecoveryIntent(in)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	failed, err := codexFailedRecoveryIntent(in)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	report, err := c.checkConfigurationRecovery(ctx, p, failed)
	if err != nil {
		return Receipt{}, &NotSent{Reason: err.Error()}
	}
	for _, condition := range report.Checks {
		if condition.Status != "passed" {
			return Receipt{}, &NotSent{Reason: "原配置恢复条件未满足：" + condition.Kind}
		}
	}
	receipt, err := c.applyPluginExecution(ctx, p, target)
	if err != nil {
		return receipt, err
	}
	if c.configurationRecoveries == nil {
		c.configurationRecoveries = map[string]codexRecoveryWitness{}
	}
	c.configurationRecoveries[in.ID] = codexRecoveryWitness{ConnectionID: p.ConnectionID(), InstanceID: in.Scope.Instance.ID, IntentHash: codexRecoveryHash(in)}
	// Preserve a verified historical result in the Application's existing
	// durable receipt. A raw resume reply alone does not attest all selections.
	fingerprint, err := codexConfigurationFingerprint(ctx, p, in.Scope)
	if err != nil {
		return receipt, err
	}
	verified, err := c.reconcilePluginExecution(ctx, p, target, receipt)
	if err != nil {
		return receipt, err
	}
	current, err := codexConfigurationFingerprint(ctx, p, in.Scope)
	if err != nil || current != fingerprint {
		return receipt, fmt.Errorf("恢复确认期间原配置来源变化或无法读取")
	}
	if verified.State == "finished" && verified.Status == "succeeded" && verified.Evidence.Complete {
		receipt.ConfigurationRecovery = &ConfigurationRecoveryAcknowledgement{Version: 1, IntentHash: codexRecoveryHash(in), Fingerprint: fingerprint, At: verified.Evidence.At}
	}
	return receipt, nil
}
func (c *Codex) reconcileConfigurationRecovery(ctx context.Context, p codexConnection, in Intent, receipt Receipt) (Resolution, error) {
	target, err := codexRecoveryIntent(in)
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "codex.configuration_recovery", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "原配置恢复缺少本操作的当前连接建立依据；保持暂停，不重放"}}
	if err != nil {
		return out, err
	}
	witness := c.configurationRecoveries[in.ID]
	if witness.ConnectionID != p.ConnectionID() || witness.IntentHash != codexRecoveryHash(in) {
		if receipt.ConfigurationRecovery != nil {
			return c.reconcileAcknowledgedConfigurationRecovery(ctx, p, in, receipt)
		}
		scope := in.Scope
		scope.NoAutoResume = true
		scope.ConfigurationBaseline = in.ConfigurationBaseline
		out.View, err = c.observe(ctx, scope)
		out.View.Status, out.View.ExecutionClear = "unknown", false
		return out, err
	}
	out, err = c.reconcilePluginExecution(ctx, p, target, receipt)
	out.Evidence.Source = "codex.configuration_recovery"
	if out.State == "finished" && out.Status == "succeeded" && out.Evidence.Complete {
		out.Evidence.Detail = "按原 ID 建立原配置、权限和能力选择并核对原历史；失败候选保留，自动化仍暂停"
	}
	return out, err
}

// ConfigurationRecoveryAcknowledgement records a fully checked historical
// establishment, not current loaded configuration or permission to send input.
// It is carried only in Runtime receipts persisted by Application.
type ConfigurationRecoveryAcknowledgement struct {
	Version     int    `json:"version"`
	IntentHash  string `json:"intent_hash"`
	Fingerprint string `json:"fingerprint"`
	At          string `json:"at"`
}

func (c *Codex) reconcileAcknowledgedConfigurationRecovery(ctx context.Context, p codexConnection, in Intent, receipt Receipt) (Resolution, error) {
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "codex.configuration_recovery_acknowledgement", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "持久恢复回执尚未通过原身份、来源、历史和未加载核对；不重放"}}
	ack := receipt.ConfigurationRecovery
	at, err := time.Parse(time.RFC3339Nano, ack.At)
	if err != nil || ack.Version != 1 || ack.IntentHash != codexRecoveryHash(in) || receipt.NativeID != in.Scope.NativeID || at.After(time.Now().Add(time.Second)) {
		return out, fmt.Errorf("持久恢复确认与原操作不一致")
	}
	if in.CreatedAt != "" {
		created, err := time.Parse(time.RFC3339Nano, in.CreatedAt)
		if err != nil || at.Before(created) {
			return out, fmt.Errorf("持久恢复确认早于原操作")
		}
	}
	failed, err := codexFailedRecoveryIntent(in)
	if err != nil {
		return out, err
	}
	report, err := c.checkConfigurationRecovery(ctx, p, failed)
	if err != nil {
		return out, err
	}
	for _, condition := range report.Checks {
		if condition.Status != "passed" {
			out.Evidence.Detail += "：" + condition.Kind
			return out, nil
		}
	}
	fingerprint, err := codexConfigurationFingerprint(ctx, p, in.Scope)
	if err != nil || fingerprint != ack.Fingerprint {
		return out, fmt.Errorf("持久恢复确认后原配置来源或约束已变化")
	}
	scope := in.Scope
	scope.NoAutoResume, scope.ConfigurationBaseline = true, in.ConfigurationBaseline
	out.View, err = c.observe(ctx, scope)
	if err != nil || out.View.NativeID != scope.NativeID || out.View.NativeStatus != "notLoaded" {
		return out, err
	}
	// The full configured observation above deliberately leaves unloaded
	// selections unverified. Recheck the execution facts without treating that
	// missing inventory as evidence of a running operation.
	neutral := scope
	neutral.MCPSelections, neutral.SkillSelections, neutral.PluginSelections = nil, nil, nil
	clear, err := c.pluginChangeIdle(ctx, p, neutral, in.ConfigurationBaseline)
	if err != nil || clear.NativeStatus != "notLoaded" {
		return out, err
	}
	if err := codexRecoveryUnloaded(ctx, p, scope.NativeID); err != nil {
		return out, err
	}
	lastFingerprint, err := codexConfigurationFingerprint(ctx, p, in.Scope)
	if err != nil || lastFingerprint != fingerprint {
		return out, fmt.Errorf("持久恢复核对期间原配置来源或约束发生变化")
	}
	out.State, out.Status, out.Evidence.Complete = "finished", "succeeded", true
	out.Evidence.Detail = "已保存的原配置恢复确认、原历史与来源核对一致；原线程当前未加载，能力仍未核对，自动化保持暂停"
	out.View.Status, out.View.ExecutionClear = "unknown", false
	return out, nil
}

type codexRecoveryWitness struct{ ConnectionID, InstanceID, IntentHash string }

func codexRecoveryHash(in Intent) string {
	body, _ := json.Marshal(in)
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
