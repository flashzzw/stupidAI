package runtime

import (
	"context"
	"fmt"
	"slices"

	"codex-netcap/internal/codex"
)

// A cold thread cannot accept settings/update, and 0.158.0 resume has no
// disabledPluginIds parameter. During explicit apply only, load an intermediate
// configuration that excludes every controlled plugin. The caller must then
// write policy, unsubscribe and load the exact target before recording success.
// The bool reports a possibly dispatched resume, including a missing reply.
func (c *Codex) loadColdPluginPolicyBoundary(ctx context.Context, p codexConnection, in Intent, original Scope, policy []string) (bool, error) {
	if in.HookSourceProof != nil {
		if err := c.verifyCodexHookSourceProof(ctx, p, original, in.HookSourceProof); err != nil {
			return false, err
		}
	}
	overrides, err := codexRouteConfig(ctx, p, in.Scope)
	if err != nil {
		return false, err
	}
	intermediate := in.Scope
	if in.Kind != "recover_execution" && CodexConfigurationSourceAdoption(original, in.Scope) {
		// The intermediate load exists only to write exclusion policy. Keep
		// the established effort until the final resume applies the candidate;
		// the original proof and idle checks must remain true in this window.
		intermediate.CodexConfigurationSources = original.CodexConfigurationSources
		overrides["model_reasoning_effort"] = codexRecordedEffort(original)
	}
	controlled := map[string]bool{}
	for _, packs := range [][]PluginSelection{original.PluginSelections, in.Scope.PluginSelections} {
		for _, pack := range packs {
			controlled[pack.NativeSource] = true
			overrides["plugins."+pack.NativeSource+".enabled"] = false
		}
	}
	sandbox, approval, err := codexRecoveryPolicy(in.Scope)
	if err != nil {
		return false, err
	}
	baseline, err := configurationBaseline(ctx, p, in.Scope.NativeID)
	if err != nil || baseline == "" || baseline != in.ConfigurationBaseline {
		return false, fmt.Errorf("冷插件策略加载前原历史已变化或缺少准备依据")
	}
	if err := codexRecoveryUnloaded(ctx, p, in.Scope.NativeID); err != nil {
		return false, err
	}
	var loaded codex.Started
	if err := p.Call(ctx, "thread/resume", map[string]any{"threadId": in.Scope.NativeID, "excludeTurns": true, "cwd": in.Scope.Cwd, "model": in.Scope.Route.Model, "modelProvider": in.Scope.Route.Provider, "sandbox": sandbox, "approvalPolicy": approval, "config": overrides}, &loaded); err != nil {
		c.failed(in.Scope.Instance)
		return true, err
	}
	if loaded.Thread.ID != in.Scope.NativeID || loaded.Model != in.Scope.Route.Model || loaded.Provider != in.Scope.Route.Provider {
		return true, fmt.Errorf("冷插件策略加载的原生身份或路由不匹配")
	}
	if _, err := verifyCodexRecoveryPermission(in.Scope, loaded); err != nil {
		return true, err
	}
	if err := verifyCodexEffort(intermediate, loaded.Thread); err != nil {
		return true, err
	}
	ids, err := decodeCodexPluginIDs(loaded.DisabledPluginIDs)
	if err != nil || !slices.Equal(ids, policy) {
		return true, fmt.Errorf("冷插件策略加载期间原生禁用列表已变化或无法核对")
	}
	rows, complete := codexMCPStatuses(ctx, p, in.Scope.NativeID)
	if !complete {
		return true, fmt.Errorf("冷插件策略加载后 MCP 清单不完整")
	}
	for _, row := range rows {
		id, known := codexMCPOrigin(row.PluginID)
		if !known || controlled[id] {
			return true, fmt.Errorf("冷插件策略加载未确认本次管理的插件已排除")
		}
	}
	return true, nil
}
