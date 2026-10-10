package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

// SessionAccess is a read projection, never a stored grant of input authority.
type SessionAccess struct {
	Context          SessionContext              `json:"context"`
	Source           string                      `json:"source"`
	SourceLabel      string                      `json:"source_label"`
	ConnectionMethod string                      `json:"connection_method"`
	Detail           string                      `json:"detail"`
	ObservedAt       string                      `json:"observed_at,omitempty"`
	PreviewAvailable bool                        `json:"preview_available"`
	Capabilities     map[string]AccessCapability `json:"capabilities"`
}

type AccessCapability struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type SessionAccessPreview struct {
	SessionID    string           `json:"session_id"`
	Generation   uint64           `json:"generation"`
	CheckedAt    string           `json:"checked_at"`
	ReadOnly     bool             `json:"read_only"`
	Access       SessionAccess    `json:"access"`
	Evidence     runtime.Evidence `json:"evidence"`
	NativeStatus string           `json:"native_status,omitempty"`
	Error        string           `json:"error,omitempty"`
}

func (a *Application) sessionAccess(session Session) SessionAccess {
	access := SessionAccess{Context: a.sessionContext(session), Source: "unknown", SourceLabel: "来源待核对", ConnectionMethod: "未记录可验证的接入方式",
		Detail: "旧记录未证明创建来源；原生 ID 或历史记录不能单独证明可实时接管。", ObservedAt: session.View.Evidence.At,
		Capabilities: map[string]AccessCapability{
			"history":      {Status: "supported", Detail: "可查看工作台已保存的消息和事件；不保证完整原生历史。"},
			"live_observe": {Status: "unverified", Detail: "尚无已验证的原执行实时观察入口。"},
			"resume":       {Status: "unverified", Detail: "需核对原生来源、执行器与输入归属；不会另建会话替代恢复。"},
			"live_input":   {Status: "unverified", Detail: "来源与输入权未核对，不由历史记录授予发送能力。"},
		}}
	if session.Origin == "native" {
		access.Source, access.SourceLabel, access.ConnectionMethod = "native", "外部原生只读绑定", "原状态目录与原生 ID · 按需读取历史"
		access.Detail = "已登记原生来源，没有创建执行或取得控制；供应商、权限与原执行接入仍需独立验证。"
		access.Capabilities["history"] = AccessCapability{Status: "supported", Detail: "按原绑定读取原生接口可见历史；不复制为新的执行上下文。"}
		if session.NativeBinding != nil {
			access.ObservedAt = session.NativeBinding.BoundAt
		}
		for _, name := range []string{"live_observe", "resume", "live_input"} {
			access.Capabilities[name] = AccessCapability{Status: "unverified", Detail: "尚未验证外部原执行的控制与完整配置；此只读登记不授予输入权。"}
		}
		if session.Scope.Instance.Kind == "codex" {
			access.Capabilities["live_observe"] = AccessCapability{Status: "conditional", Detail: "可按原绑定显式查询当前后台的轮次、工具与占用；不会加载未运行线程，实际结果以本次观察为准。"}
		} else if session.Scope.Instance.Kind == "claude" {
			access.Capabilities["live_observe"] = AccessCapability{Status: "conditional", Detail: "可查询固定 CLI 的原生会话清单声明；不证明原执行器连接、完整工具状态或输入权。"}
		}
		return access
	}
	if session.ControlMode == "observed" {
		access.Source, access.SourceLabel, access.ConnectionMethod = "observation", "外部事件记录", "已登记观察来源的事件投递"
		access.Detail = "事件不证明来自独立前台或原生后台；这里只读保存的事件，不具备原生连接、完整历史或输入控制。"
		access.ObservedAt = session.ActivityAt
		for _, name := range []string{"live_observe", "resume", "live_input"} {
			access.Capabilities[name] = AccessCapability{Status: "unsupported", Detail: "观察来源不能升级为实时原生连接或控制入口；请返回提供事件的原终端。"}
		}
		return access
	}
	if session.Origin != "console" && session.Origin != "probe" {
		return access
	}
	access.Source, access.SourceLabel = "platform", "工作台创建"
	access.Detail = "接续原工作台绑定；独立前台和其他原生后台的发现、接入尚需分别验证。"
	if session.Origin == "probe" {
		access.Source, access.SourceLabel = "probe", "隔离路由探测"
		access.Detail = "仅执行已登记的路由探测，不接受额外交互输入。"
	}
	switch session.Scope.Instance.Kind {
	case "codex":
		access.ConnectionMethod = "Codex 预共享后台 · 原线程协议连接"
	case "claude":
		access.ConnectionMethod = "Claude SDK bridge · 自有 query 与原生历史"
	case "opencode":
		access.ConnectionMethod = "OpenCode 已登记服务"
	}
	for name, capability := range map[string]string{"live_observe": "history", "resume": "resume", "live_input": "send"} {
		if err := a.requireCapability(session.Scope.Instance.ID, capability); err != nil {
			access.Capabilities[name] = AccessCapability{Status: "unverified", Detail: err.Error()}
			continue
		}
		access.Capabilities[name] = AccessCapability{Status: "conditional", Detail: "当前适配器支持；实际操作仍须核对原绑定、配置、在途执行及输入归属。"}
	}
	access.PreviewAvailable = session.Scope.NativeID != "" && access.Capabilities["live_observe"].Status == "conditional"
	if session.Scope.NativeID == "" {
		for _, name := range []string{"live_observe", "resume", "live_input"} {
			access.Capabilities[name] = AccessCapability{Status: "unavailable", Detail: "尚未取得原生会话身份；不能用新执行替代核对。"}
		}
	}
	if session.Origin == "probe" {
		for _, name := range []string{"resume", "live_input"} {
			access.Capabilities[name] = AccessCapability{Status: "unsupported", Detail: "探测会话不接受额外交互输入或手动恢复自动化。"}
		}
	}
	return access
}

// PreviewSessionAccess observes only a previously bound session. It never
// imports an arbitrary native ID, resumes an unloaded thread or grants input.
func (a *Application) PreviewSessionAccess(ctx context.Context, id string) (SessionAccessPreview, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed {
		a.mu.Unlock()
		return SessionAccessPreview{}, fmt.Errorf("会话不存在或工作台已关闭")
	}
	session = queryCopy(session)
	out := SessionAccessPreview{SessionID: id, Generation: session.Generation, CheckedAt: now(), ReadOnly: true, Access: a.sessionAccess(session)}
	var checkErr error
	if !out.Access.PreviewAvailable {
		checkErr = fmt.Errorf("此记录没有可验证的原生观察入口；保留已保存的历史与来源限制")
	} else {
		checkErr = a.verifyRouteBinding(session)
	}
	a.mu.Unlock()
	var view runtime.View
	if checkErr == nil {
		scope := session.Scope
		scope.NoAutoResume = true
		check, cancel := context.WithTimeout(ctx, 15*time.Second)
		view, checkErr = a.runtime.ObserveFresh(check, scope)
		cancel()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		checkErr = fmt.Errorf("预览期间会话绑定或输入归属已变化，请重新核对")
	} else if checkErr == nil {
		checkErr = a.verifyRouteBinding(current)
	}
	if checkErr == nil && (view.NativeID != session.Scope.NativeID || !view.Evidence.Complete) {
		checkErr = fmt.Errorf("原生身份或历史证据不完整，保留待核对状态")
	}
	out.CheckedAt = now()
	out.Evidence, out.NativeStatus = view.Evidence, view.NativeStatus
	if checkErr != nil {
		out.Error = checkErr.Error()
		out.Evidence.Complete = false
		for name, cap := range out.Access.Capabilities {
			if name != "history" && cap.Status != "unsupported" {
				out.Access.Capabilities[name] = AccessCapability{Status: "unavailable", Detail: out.Error}
			}
		}
		return out, nil
	}
	out.Access.ObservedAt = view.Evidence.At
	out.Access.Context.Executor = view.Executor
	out.Access.Capabilities["live_observe"] = AccessCapability{Status: "conditional", Detail: "已取得本次原生观察；这不是持续在线或输入授权的证明，后续活动仍以新证据为准。"}
	if view.NativeStatus == "notLoaded" || view.NativeStatus == "sdk_offline" {
		for _, name := range []string{"live_observe", "live_input"} {
			if out.Access.Capabilities[name].Status != "unsupported" {
				out.Access.Capabilities[name] = AccessCapability{Status: "unavailable", Detail: "原线程未加载或 SDK 查询已退出；本次只读取得历史，没有恢复原执行。"}
			}
		}
	}
	if err := verifyHandoff(session, view); err != nil || a.occupied(id) || a.busy[id] || current.ExecutionHold {
		detail := "原执行、未决操作或配置应用仍需核对，不能恢复输入。"
		if err != nil {
			detail = err.Error()
		}
		for _, name := range []string{"resume", "live_input"} {
			if out.Access.Capabilities[name].Status == "conditional" {
				out.Access.Capabilities[name] = AccessCapability{Status: "unavailable", Detail: detail}
			}
		}
	} else {
		for _, name := range []string{"resume", "live_input"} {
			if out.Access.Capabilities[name].Status == "conditional" {
				out.Access.Capabilities[name] = AccessCapability{Status: "conditional", Detail: "本次核对为空闲；请先处理原终端草稿并停止输入。预览不授予输入权，实际操作会重新准入。"}
			}
		}
	}
	return out, nil
}
