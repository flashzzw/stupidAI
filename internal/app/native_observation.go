package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

type NativeObservation struct {
	SessionID      string       `json:"session_id"`
	InstanceID     string       `json:"instance_id"`
	StateNamespace string       `json:"state_namespace"`
	Cwd            string       `json:"cwd"`
	CheckedAt      string       `json:"checked_at"`
	ReadOnly       bool         `json:"read_only"`
	Status         string       `json:"status"`
	Detail         string       `json:"detail"`
	Error          string       `json:"error,omitempty"`
	View           runtime.View `json:"view"`
}

// This is an on-demand observation, not a Session transition. In particular,
// background polling, restart and hooks cannot convert it into input authority.
func (a *Application) ObserveBoundNative(ctx context.Context, id string) (NativeObservation, error) {
	if err := ctx.Err(); err != nil {
		return NativeObservation{}, err
	}
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if a.closed || !ok || session.Origin != "native" || session.NativeBinding == nil {
		a.mu.Unlock()
		return NativeObservation{}, fmt.Errorf("此记录没有已登记的原生只读来源")
	}
	session = queryCopy(session)
	_, binding, _, err := a.discoveryBinding(NativeDiscoveryRequest{ProjectID: session.ProjectID, InstanceID: session.Scope.Instance.ID})
	a.mu.Unlock()
	if err != nil {
		return NativeObservation{}, err
	}
	if binding != session.NativeBinding.Configuration {
		return NativeObservation{}, fmt.Errorf("原生来源的项目或执行配置已变化，不能观察替代来源")
	}
	observer, ok := a.runtime.(runtime.NativeExecutionObserver)
	if !ok {
		return NativeObservation{}, fmt.Errorf("当前适配器没有原生只读执行观察接口")
	}
	scope := session.Scope
	scope.NoAutoResume = true
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	view, observeErr := observer.ObserveNativeExecution(check, scope)
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if a.closed || !exists || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) || digest(current.NativeBinding) != digest(session.NativeBinding) {
		return NativeObservation{}, fmt.Errorf("观察期间原生来源绑定发生变化，请重新核对")
	}
	_, currentBinding, _, err := a.discoveryBinding(NativeDiscoveryRequest{ProjectID: current.ProjectID, InstanceID: current.Scope.Instance.ID})
	if err != nil {
		return NativeObservation{}, err
	}
	if currentBinding != binding {
		return NativeObservation{}, fmt.Errorf("观察期间原项目或执行配置已变化，结果不可采用")
	}
	if err := ctx.Err(); err != nil {
		return NativeObservation{}, err
	}
	out := NativeObservation{SessionID: id, InstanceID: scope.Instance.ID, StateNamespace: scope.StateNamespace, Cwd: scope.Cwd, CheckedAt: now(), ReadOnly: true, Status: "unavailable",
		Detail: "按原后台查询轮次、工具与执行占用；没有订阅输入、恢复线程或取得控制。未收到的原生审批及终端草稿仍可能存在。"}
	// Never expose a terminal control command through a read-only source, even
	// when an adapter reuses its ordinary observation projection.
	view.TerminalCommand = ""
	if view.NativeID != scope.NativeID {
		out.Error = "原生观察没有返回匹配的会话身份"
		if observeErr != nil && view.NativeID == "" {
			out.Error = observeErr.Error()
		}
		return out, nil
	}
	out.View = view
	if scope.Instance.Kind == "claude" && view.NativeStatus == "roster_only" && view.AgentRoster != nil && observeErr == nil {
		out.Status = "sampled"
		out.Detail = "仅查询固定 Claude CLI 的原生会话清单；CLI 可能维护自身元数据，没有发送输入、调用 attach 或恢复会话。清单不能证明工具结果连续或可接管。"
		out.View.Status, out.View.ExecutionClear, out.View.Evidence.Complete = "unknown", false, false
		return out, nil
	}
	if view.NativeStatus == "notLoaded" || view.NativeStatus == "sdk_offline" {
		out.Error = "原执行未加载；本次只读取得历史，没有恢复执行，请在原 CLI 核对"
		out.View.Evidence.Complete = false
	} else if observeErr != nil {
		out.Error, out.View.Evidence.Complete = observeErr.Error(), false
	} else if !view.Evidence.Complete {
		out.Error = "原生执行查询不完整；不能确认当前占用"
	} else if (view.NativeStatus != "active" && view.NativeStatus != "idle") || view.Status == "unknown" {
		out.Error = "原执行未加载或状态未知；本次未恢复执行，请在原 CLI 核对"
	} else {
		out.Status = "observed"
	}
	return out, nil
}
