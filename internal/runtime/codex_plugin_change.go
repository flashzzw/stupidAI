package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"codex-netcap/internal/codex"
)

func validateCodexPluginChange(in Intent) error {
	if in.PreviousScope == nil || in.Scope.Instance.Kind != "codex" || in.Scope.NativeID == "" || in.Scope.Purpose != "" || in.ConfigurationBaseline == "" || len(in.Scope.PluginSelections)+len(in.PreviousScope.PluginSelections)+len(in.Scope.SkillSelections)+len(in.PreviousScope.SkillSelections) == 0 && !CodexSourceAdoption(*in.PreviousScope, in.Scope) {
		return fmt.Errorf("插件改配缺少原绑定、原生身份或准备依据")
	}
	previous := *in.PreviousScope
	previous.MCPSelections = in.Scope.MCPSelections
	previous.SkillSelections = in.Scope.SkillSelections
	previous.PluginSelections = in.Scope.PluginSelections
	if CodexSourceAdoption(*in.PreviousScope, in.Scope) {
		previous.CodexUserHooks = in.Scope.CodexUserHooks
		previous.CodexProjectHooks = in.Scope.CodexProjectHooks
		previous.CodexConfigurationSources = in.Scope.CodexConfigurationSources
	}
	a, _ := json.Marshal(previous)
	b, _ := json.Marshal(in.Scope)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("插件改配不能改变执行身份、路由或状态归属")
	}
	return nil
}

func (c *Codex) pluginChangeIdle(ctx context.Context, p codexConnection, scope Scope, baseline string) (View, error) {
	scope.NoAutoResume = true
	scope.ConfigurationBaseline = baseline
	v, err := c.observe(ctx, scope)
	if err != nil {
		return v, err
	}
	if v.NativeID != scope.NativeID || (v.NativeStatus != "idle" && v.NativeStatus != "notLoaded") || !configurationIdle(v) {
		return v, fmt.Errorf("插件改配需要原线程及全部执行范围空闲")
	}
	current, err := configurationBaseline(ctx, p, scope.NativeID)
	if err != nil || current != baseline {
		return v, fmt.Errorf("准备后原历史发生变化或无法完整读取")
	}
	return v, nil
}

func (c *Codex) applyPluginExecution(ctx context.Context, p codexConnection, in Intent) (receipt Receipt, resultErr error) {
	receipt = Receipt{NativeID: in.Scope.NativeID}
	policySent, coldPolicyLoaded := false, false
	defer func() {
		var notSent *NotSent
		if (policySent || coldPolicyLoaded) && errors.As(resultErr, &notSent) {
			resultErr = fmt.Errorf("%s；原生配置或插件策略可能已改变，保留原绑定等待只读核对", notSent.Reason)
		}
	}()
	if err := validateCodexPluginChange(in); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	recovering := in.Kind == "recover_execution"
	original := *in.PreviousScope
	original.directorySkillChange = true
	sourceAdoption := CodexSourceAdoption(original, in.Scope)
	if sourceAdoption && !recovering {
		var err error
		original, err = c.codexHookSourceAdoptionScope(ctx, p, original, in.Scope, in.HookSourceProof)
		if err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	var deactivationErr error
	if !recovering {
		original, deactivationErr = c.managedHookDeactivation(ctx, p, original, in.Scope)
	}
	if deactivationErr != nil {
		return receipt, &NotSent{Reason: deactivationErr.Error()}
	}
	if err := verifyCodexThreadRoute(ctx, p, original); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	originalConfig := original
	if recovering {
		originalConfig = in.Scope
	}
	if _, err := codexRouteConfig(ctx, p, originalConfig); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	initialObservation := original
	if original.hookSourceCold {
		initialObservation.MCPSelections, initialObservation.SkillSelections, initialObservation.PluginSelections = nil, nil, nil
	}
	if recovering {
		initialObservation.MCPSelections, initialObservation.SkillSelections, initialObservation.PluginSelections = nil, nil, nil
		// Recovery establishes the recorded original files, not the uncertain
		// candidate's files. Their presence was checked by the recovery gate.
		initialObservation.CodexUserHooks = in.Scope.CodexUserHooks
		initialObservation.CodexProjectHooks = in.Scope.CodexProjectHooks
		initialObservation.CodexConfigurationSources = in.Scope.CodexConfigurationSources
		initialObservation.directorySkillChange = false
		if err := codexRecoveryUnloaded(ctx, p, in.Scope.NativeID); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	if _, err := c.pluginChangeIdle(ctx, p, initialObservation, in.ConfigurationBaseline); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if err := prepareCodexPluginCaches(in.Scope); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	overrides, err := codexRouteConfig(ctx, p, in.Scope)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	before, err := c.pluginChangeIdle(ctx, p, initialObservation, in.ConfigurationBaseline)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	policyRequired := codexMixedPlugins(original.PluginSelections) || codexMixedPlugins(in.Scope.PluginSelections)
	policy, readErr := c.readPluginPolicy(ctx, p, original)
	if policyRequired && readErr != nil {
		return receipt, &NotSent{Reason: readErr.Error()}
	}
	// An older mixed selection can leave a saved disable even after upgrading
	// to an MCP-only package. Honor any available policy on that path too;
	// legacy MCP-only records still use their existing inventory evidence.
	usePolicy := readErr == nil
	skillChange := len(original.SkillSelections)+len(in.Scope.SkillSelections) > 0
	hookChange := hasCodexHookSelections(original) || hasCodexHookSelections(in.Scope) || sourceAdoption
	hookDetached := before.NativeStatus == "notLoaded"
	var desiredPolicy []string
	transition := original
	if recovering {
		transition = initialObservation
	}
	if skillChange {
		transition.SkillSelections = nil
	}
	if usePolicy {
		desiredPolicy = mergeCodexPluginPolicy(policy.DisabledIDs, in.Scope.PluginSelections, original.PluginSelections)
		raw, _ := json.Marshal(desiredPolicy)
		if _, e := decodeCodexPluginIDs(raw); e != nil {
			return receipt, &NotSent{Reason: e.Error()}
		}
		if !slices.Equal(desiredPolicy, policy.DisabledIDs) {
			if before.NativeStatus == "notLoaded" {
				coldPolicyLoaded, err = c.loadColdPluginPolicyBoundary(ctx, p, in, original, policy.DisabledIDs)
				if err != nil {
					return receipt, &NotSent{Reason: err.Error()}
				}
				transition = initialObservation
				before, err = c.pluginChangeIdle(ctx, p, transition, in.ConfigurationBaseline)
				if err != nil {
					return receipt, &NotSent{Reason: err.Error()}
				}
				if before.NativeStatus != "idle" {
					return receipt, &NotSent{Reason: "冷插件策略加载后原线程尚未确认空闲"}
				}
			}
			policySent = true
			if err = p.Call(ctx, "thread/settings/update", map[string]any{"threadId": in.Scope.NativeID, "disabledPluginIds": desiredPolicy}, nil); err != nil {
				c.failed(in.Scope.Instance)
				return receipt, err
			}
		}
		// Policy and package configuration intentionally differ during apply.
		// Continue checking identity, history and all executions, without treating
		// the old package selection as the new policy's expected inventory.
		transition.PluginSelections = nil
	}
	if (recovering || original.hookSourceCold) && !coldPolicyLoaded && before.NativeStatus != "notLoaded" {
		return receipt, fmt.Errorf("恢复准备期间原线程重新加载，保持未知")
	}
	if before.NativeStatus == "idle" {
		var detached struct {
			Status string `json:"status"`
		}
		if err = p.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": in.Scope.NativeID}, &detached); err != nil {
			c.failed(in.Scope.Instance)
			return receipt, &NotSent{Reason: "未取得当前连接退订回执，尚未发送新插件配置"}
		}
		if !slices.Contains([]string{"unsubscribed", "notSubscribed", "notLoaded"}, detached.Status) {
			return receipt, &NotSent{Reason: "原生未确认当前连接退订，尚未发送新插件配置"}
		}
		hookDetached = detached.Status == "unsubscribed" || detached.Status == "notLoaded"
		// Unsubscription is not a global unload. Poll the original execution
		// again before resuming; another client can still be using the thread.
		if _, err = c.pluginChangeIdle(ctx, p, transition, in.ConfigurationBaseline); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	// Recheck every target cache after the intervening native I/O. Recovery and
	// read-only reconciliation never create or repair these versioned copies.
	overrides, err = codexRouteConfig(ctx, p, in.Scope)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	sandbox, approval, err := codexRecoveryPolicy(in.Scope)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if _, err = c.pluginChangeIdle(ctx, p, transition, in.ConfigurationBaseline); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if usePolicy {
		policy, e := c.readPluginPolicy(ctx, p, original)
		if e != nil || !slices.Equal(policy.DisabledIDs, desiredPolicy) {
			return receipt, &NotSent{Reason: "应用前原生插件策略已变化或无法核对"}
		}
	}
	if hookChange || skillChange {
		// In 0.158.0, a successfully unsubscribed thread can remain in the
		// loaded list with idle status. The verified reconfiguration boundary
		// is this connection's unsubscribe receipt, not global memory eviction.
		if !hookDetached {
			return receipt, &NotSent{Reason: "未取得本连接的 Skill/hook 退订依据，不能用热恢复替换选择"}
		}
		delete(c.hookWitnesses, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
	}
	skillFingerprint := ""
	if skillChange {
		if err = addCodexDirectorySkillOverrides(ctx, p, in.Scope, overrides); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
		delete(c.directorySkillWitnesses, in.Scope.Instance.ID+"/"+in.Scope.NativeID)
		skillFingerprint, err = codexConfigurationFingerprint(ctx, p, in.Scope)
		if err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	hookFingerprint, hookErr := c.prepareSelectedHooks(ctx, p, in.Scope)
	if hookErr == nil && hookChange && !hasCodexHookSelections(in.Scope) {
		// The empty target is a configuration too. Record it only after a
		// valid resume reply, so policy writes cannot masquerade as retirement.
		hookFingerprint, hookErr = codexConfigurationFingerprint(ctx, p, in.Scope)
	}
	if hookErr != nil {
		return receipt, &NotSent{Reason: hookErr.Error()}
	}
	if hookChange || skillChange {
		if _, e := c.pluginChangeIdle(ctx, p, transition, in.ConfigurationBaseline); e != nil {
			return receipt, &NotSent{Reason: "准备能力配置后原历史或占用无法核对，未发送新配置"}
		}
	}
	if sourceAdoption && !recovering && in.HookSourceProof != nil {
		if err := c.verifyCodexHookSourceProof(ctx, p, original, in.HookSourceProof); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	if (recovering || original.hookSourceCold) && !coldPolicyLoaded {
		if err := codexRecoveryUnloaded(ctx, p, in.Scope.NativeID); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	var out codex.Started
	err = p.Call(ctx, "thread/resume", map[string]any{"threadId": in.Scope.NativeID, "excludeTurns": true, "cwd": in.Scope.Cwd, "model": in.Scope.Route.Model, "modelProvider": in.Scope.Route.Provider, "sandbox": sandbox, "approvalPolicy": approval, "config": overrides}, &out)
	if err != nil {
		c.failed(in.Scope.Instance)
		return receipt, err
	}
	if out.Thread.ID != in.Scope.NativeID || out.Model != in.Scope.Route.Model || out.Provider != in.Scope.Route.Provider {
		return receipt, fmt.Errorf("插件改配回执的身份或路由不匹配")
	}
	if _, err = verifyCodexRecoveryPermission(in.Scope, out); err != nil {
		return receipt, err
	}
	if err = verifyCodexEffort(in.Scope, out.Thread); err != nil {
		return receipt, err
	}
	if codexRecordedEffort(in.Scope) != "" {
		actual, e := codex.Read(ctx, p, in.Scope.NativeID)
		if e != nil || actual.ID != in.Scope.NativeID || actual.Status.Type == "notLoaded" {
			return receipt, fmt.Errorf("应用后无法回读原线程设置")
		}
		if e = verifyCodexEffort(in.Scope, actual); e != nil {
			return receipt, e
		}
	}
	if usePolicy {
		ids, e := decodeCodexPluginIDs(out.DisabledPluginIDs)
		if e != nil || !slices.Equal(ids, desiredPolicy) {
			return receipt, fmt.Errorf("插件恢复回执的禁用策略不匹配")
		}
	}
	if err = c.rememberDirectorySkills(ctx, p, in.Scope, in.Scope.NativeID, skillFingerprint); err != nil {
		return receipt, err
	}
	if err = c.rememberSelectedHooks(ctx, p, in.Scope, in.Scope.NativeID, hookFingerprint); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func codexRetiredPluginsAbsent(ctx context.Context, p codexConnection, in Intent) bool {
	retired := map[string]bool{}
	for _, old := range in.PreviousScope.PluginSelections {
		if !slices.ContainsFunc(in.Scope.PluginSelections, func(next PluginSelection) bool {
			return next.NativeSource == old.NativeSource && next.Mode == "enabled"
		}) {
			retired[old.NativeSource] = true
		}
	}
	if len(retired) == 0 {
		return true
	}
	statuses, complete := codexMCPStatuses(ctx, p, in.Scope.NativeID)
	if !complete {
		return false
	}
	for _, row := range statuses {
		id, known := codexMCPOrigin(row.PluginID)
		if !known || retired[id] {
			return false
		}
	}
	return true
}

func (c *Codex) reconcilePluginExecution(ctx context.Context, p codexConnection, in Intent, receipt Receipt) (Resolution, error) {
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "codex.plugin-configuration", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "插件目标版本与撤下来源仍待核对；不会重放改配"}}
	if err := validateCodexPluginChange(in); err != nil {
		return out, err
	}
	if receipt.NativeID != "" && receipt.NativeID != in.Scope.NativeID {
		return out, fmt.Errorf("插件改配回执不是原会话")
	}
	scope := in.Scope
	scope.NoAutoResume = true
	v, err := c.observe(ctx, scope)
	out.View = v
	if err != nil {
		return out, err
	}
	if v.NativeID != scope.NativeID || v.NativeStatus != "idle" || !configurationIdle(v) {
		return out, nil
	}
	if err = verifyCodexThreadRoute(ctx, p, scope); err != nil {
		return out, err
	}
	if _, err = codexRouteConfig(ctx, p, scope); err != nil {
		return out, err
	}
	if CodexSourceAdoption(*in.PreviousScope, scope) || hasCodexHookSelections(*in.PreviousScope) && !hasCodexHookSelections(scope) {
		if err := c.verifyHookConfiguration(ctx, p, scope); err != nil {
			out.Evidence.Detail = "hook 撤去缺少本连接的有效配置建立依据；" + err.Error()
			return out, nil
		}
	}
	if len(in.PreviousScope.SkillSelections)+len(in.Scope.SkillSelections) > 0 {
		key := scope.Instance.ID + "/" + scope.NativeID
		w, ok := c.directorySkillWitnesses[key]
		fingerprint, e := codexConfigurationFingerprint(ctx, p, scope)
		if !ok || e != nil || w.ConnectionID != p.ConnectionID() || w.SelectionHash != codexScopedSelectionHash(scope) || w.Fingerprint != fingerprint {
			out.Evidence.Detail = "原生 Skill 改配缺少本连接的建立依据，不重放或推断已恢复继承"
			return out, nil
		}
	}
	if !codexRetiredPluginsAbsent(ctx, p, in) {
		out.Evidence.Detail = "已撤下插件仍存在或来源清单不完整"
		return out, nil
	}
	if codexMixedPlugins(in.Scope.PluginSelections) || codexMixedPlugins(in.PreviousScope.PluginSelections) {
		policy, e := c.readPluginPolicy(ctx, p, scope)
		if e != nil || !policy.matches(scope.PluginSelections) {
			out.Evidence.Detail = "目标整包原生策略尚未只读核对"
			return out, nil
		}
		for _, old := range in.PreviousScope.PluginSelections {
			active := slices.ContainsFunc(scope.PluginSelections, func(next PluginSelection) bool {
				return next.NativeSource == old.NativeSource && next.Mode == "enabled"
			})
			if !active && !slices.Contains(policy.DisabledIDs, old.NativeSource) {
				out.Evidence.Detail = "已撤下版本尚未由整包策略排除"
				return out, nil
			}
		}
	}
	baseline, err := configurationBaseline(ctx, p, scope.NativeID)
	if err != nil || baseline != in.ConfigurationBaseline {
		out.Evidence.Detail = "改配前后原历史不一致或无法核对"
		return out, nil
	}
	out.State, out.Status = "finished", "succeeded"
	out.Receipt.NativeID = scope.NativeID
	out.Evidence.Complete = true
	out.Evidence.Detail = "原 ID、历史、路由、固定副本、当前组件及撤下来源已核对；未重放改配"
	return out, nil
}
