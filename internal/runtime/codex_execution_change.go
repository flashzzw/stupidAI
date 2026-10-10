package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"codex-netcap/internal/codex"
)

func configurationBaseline(ctx context.Context, p codex.Client, id string) (string, error) {
	var out struct {
		Thread struct {
			ID    string          `json:"id"`
			Turns json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if err := p.Call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": true}, &out); err != nil {
		return "", err
	}
	if out.Thread.ID != id {
		return "", fmt.Errorf("准备历史的原生身份不一致")
	}
	var turns []struct {
		Status    string `json:"status"`
		ItemsView string `json:"itemsView"`
	}
	if err := json.Unmarshal(out.Thread.Turns, &turns); err != nil || turns == nil {
		return "", fmt.Errorf("准备历史缺失或未完整落盘")
	}
	for _, turn := range turns {
		if (turn.Status != "completed" && turn.Status != "interrupted" && turn.Status != "failed") || (turn.ItemsView != "" && turn.ItemsView != "full") {
			return "", fmt.Errorf("原生历史不完整或仍有未结束轮次")
		}
	}
	// Keep fields outside the display projection too (tool arguments/results,
	// unknown future item fields). Canonical JSON ignores map key ordering only.
	var full any
	decoder := json.NewDecoder(bytes.NewReader(out.Thread.Turns))
	decoder.UseNumber()
	if err := decoder.Decode(&full); err != nil {
		return "", err
	}
	b, err := json.Marshal(full)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Codex) prepareExecution(ctx context.Context, p codexConnection, in Intent) (Receipt, error) {
	scope := in.Scope
	scope.NoAutoResume = true
	scope.directorySkillChange = true
	receipt := Receipt{NativeID: scope.NativeID}
	if in.ConfigurationTarget != nil && CodexSourceAdoption(in.Scope, *in.ConfigurationTarget) {
		var err error
		scope, err = c.codexHookSourceAdoptionScope(ctx, p, scope, *in.ConfigurationTarget, in.HookSourceProof)
		if err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	} else if in.ConfigurationTarget != nil {
		if !CodexHookDeactivation(in.Scope, *in.ConfigurationTarget) {
			return receipt, &NotSent{Reason: "准备候选不是仅禁用或撤去原 hook，不能按此路径核对"}
		}
		var err error
		scope, err = c.managedHookDeactivation(ctx, p, scope, *in.ConfigurationTarget)
		if err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil || thread.ID != scope.NativeID {
		return receipt, &NotSent{Reason: "准备核对的原生身份不一致或不可读"}
	}
	if err = verifyCodexThreadRoute(ctx, p, scope); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if _, err = codexRouteConfig(ctx, p, scope); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	before, err := configurationBaseline(ctx, p, scope.NativeID)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	observation := scope
	if scope.hookSourceCold {
		observation.MCPSelections, observation.SkillSelections, observation.PluginSelections = nil, nil, nil
		observation.ConfigurationBaseline = before
	}
	view, err := c.observe(ctx, observation)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if view.NativeID != scope.NativeID || (!scope.hookSourceCold && view.NativeStatus != "idle") || (scope.hookSourceCold && view.NativeStatus != "notLoaded") || !configurationIdle(view) {
		return receipt, &NotSent{Reason: fmt.Sprintf("准备改配需核对已加载原线程、原选择和全部执行范围空闲（原生=%s，状态=%s，完整=%t，空闲=%t，未决=%d）", view.NativeStatus, view.Status, view.Evidence.Complete, view.ExecutionClear, len(view.Pending))}
	}
	after, err := configurationBaseline(ctx, p, scope.NativeID)
	if err != nil || before != after {
		return receipt, &NotSent{Reason: "准备核对期间历史发生变化"}
	}
	if scope.hookSourceCold {
		if err := codexRecoveryUnloaded(ctx, p, scope.NativeID); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	if in.HookSourceProof != nil {
		if err := c.verifyCodexHookSourceProof(ctx, p, scope, in.HookSourceProof); err != nil {
			return receipt, &NotSent{Reason: err.Error()}
		}
	}
	receipt.ConfigurationBaseline = before
	return receipt, nil
}

func configurationIdle(view View) bool {
	if view.Status != "idle" || !view.Evidence.Complete || !view.ExecutionClear || len(view.Executions) != 0 {
		return false
	}
	for _, p := range view.Pending {
		if p.Status == "pending" || p.Status == "submitted" || p.Status == "unverified" {
			return false
		}
	}
	return true
}

func (c *Codex) applyExecution(ctx context.Context, p codexConnection, in Intent) (Receipt, error) {
	if in.PreviousScope != nil {
		return c.applyPluginExecution(ctx, p, in)
	}
	receipt := Receipt{NativeID: in.Scope.NativeID}
	if in.Scope.NativeID == "" || in.Scope.Purpose != "" || len(in.Scope.MCPSelections) == 0 {
		return receipt, &NotSent{Reason: "改配只支持已有原生身份的普通 Codex MCP 选择"}
	}
	scope := in.Scope
	scope.NoAutoResume = true
	scope.ConfigurationBaseline = in.ConfigurationBaseline
	baseline, err := configurationBaseline(ctx, p, scope.NativeID)
	if err != nil || in.ConfigurationBaseline == "" || baseline != in.ConfigurationBaseline {
		return receipt, &NotSent{Reason: "缺少准备阶段的空闲依据，或准备后原历史已变化"}
	}
	// All observations before and after a reconfiguration are read-only.
	// In particular, reconciliation must never load either configuration.
	view, err := c.observe(ctx, scope)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	if view.NativeID != scope.NativeID || view.NativeStatus != "notLoaded" {
		return receipt, &NotSent{Reason: "原线程仍已加载；当前版本不能热切换 MCP。保持准备状态，在原线程卸载后重新申请应用；不会停止共享后台"}
	}
	if !configurationIdle(view) {
		return receipt, &NotSent{Reason: "原历史、队列、工具或未决请求尚未确认空闲"}
	}
	if err = verifyCodexThreadRoute(ctx, p, scope); err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	overrides, err := codexRouteConfig(ctx, p, scope)
	if err != nil {
		return receipt, &NotSent{Reason: err.Error()}
	}
	// Recheck after configuration I/O: a separate client may have loaded the
	// thread. A later race is caught by actual inventory reconciliation.
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil || thread.ID != scope.NativeID || thread.Status.Type != "notLoaded" {
		return receipt, &NotSent{Reason: "应用前原线程状态已变化，保持原绑定待核对"}
	}
	sandbox := "read-only"
	if in.Permission == "workspace" {
		sandbox = "workspace-write"
	}
	var out codex.Started
	err = p.Call(ctx, "thread/resume", map[string]any{"threadId": scope.NativeID, "excludeTurns": true,
		"model": scope.Route.Model, "modelProvider": scope.Route.Provider, "cwd": scope.Cwd,
		"sandbox": sandbox, "approvalPolicy": "on-request", "config": overrides}, &out)
	if err != nil {
		c.failed(scope.Instance)
		return receipt, err // May have applied. Never classify an absent receipt as NotSent.
	}
	if out.Thread.ID != scope.NativeID || out.Model != scope.Route.Model || out.Provider != scope.Route.Provider {
		return receipt, fmt.Errorf("改配回执身份或路由不匹配，保留原绑定等待实际状态核对")
	}
	return receipt, nil
}

func (c *Codex) reconcileExecution(ctx context.Context, p codexConnection, in Intent, receipt Receipt) (Resolution, error) {
	if in.PreviousScope != nil {
		return c.reconcilePluginExecution(ctx, p, in, receipt)
	}
	out := Resolution{State: "unknown", Receipt: receipt, Evidence: Evidence{Source: "codex.configuration", At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "尚未核对新配置；不重复发送改配"}}
	if in.Scope.NativeID == "" || (receipt.NativeID != "" && receipt.NativeID != in.Scope.NativeID) {
		return out, fmt.Errorf("改配回执不属于原会话")
	}
	scope := in.Scope
	scope.NoAutoResume = true
	thread, err := codex.Read(ctx, p, scope.NativeID)
	if err != nil || thread.ID != scope.NativeID {
		return out, fmt.Errorf("原生身份不一致或不可读，不能确认配置版本")
	}
	view, err := c.observe(ctx, scope)
	out.View = view
	if err != nil {
		return out, err
	}
	if view.NativeStatus == "notLoaded" {
		out.Evidence.Detail = "改配后原线程未加载，实际版本未知；不会凭旧回执重新加载或重放"
		return out, nil
	}
	if err = verifyCodexThreadRoute(ctx, p, scope); err != nil {
		return out, err
	}
	if _, err = codexRouteConfig(ctx, p, scope); err != nil {
		return out, err
	}
	if view.NativeStatus != "idle" || !configurationIdle(view) || len(scope.MCPSelections) == 0 {
		out.Evidence.Detail = "原线程尚未空闲或实际 MCP 清单与目标版本未核对一致"
		return out, nil
	}
	// observe only reports ExecutionClear when the complete per-thread MCP
	// inventory matches every explicit selection in this intent.
	out.State, out.Status = "finished", "succeeded"
	out.Receipt.NativeID = scope.NativeID
	out.Evidence.Complete = true
	out.Evidence.Detail = "原 ID、路由、空闲执行范围与目标 MCP 清单已核对一致；未重放改配"
	return out, nil
}
