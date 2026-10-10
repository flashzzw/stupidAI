package app

import (
	"context"
	"fmt"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

// A source binding records where the original history lives. It deliberately
// has no Route, execution snapshot, or control grant: those require evidence
// from the original executor, not a route selected for creating a new session.
type NativeSourceBinding struct {
	Metadata      runtime.NativeSession `json:"metadata"`
	BoundAt       string                `json:"bound_at"`
	Configuration string                `json:"configuration"`
}

type NativeBindingRequest struct {
	CommandID    string `json:"command_id"`
	ProjectID    string `json:"project_id"`
	InstanceID   string `json:"instance_id"`
	NativeID     string `json:"native_id"`
	PreviewToken string `json:"preview_token"`
}

func (a *Application) BindNative(ctx context.Context, request NativeBindingRequest) (CommandReceipt, error) {
	if !validID(request.CommandID) || len(request.PreviewToken) != 64 {
		return CommandReceipt{}, fmt.Errorf("需要有效 command_id 与只读预览凭据")
	}
	hash := digest(struct {
		Kind    string
		Request NativeBindingRequest
	}{"bind_native", request})
	a.mu.Lock()
	if receipt, ok := a.state.Commands[request.CommandID]; ok {
		a.mu.Unlock()
		if receipt.Hash != hash {
			return CommandReceipt{}, fmt.Errorf("command_id 已用于其他内容")
		}
		return queryCopy(receipt), nil
	}
	a.mu.Unlock()
	preview, err := a.PreviewNative(ctx, NativePreviewRequest{ProjectID: request.ProjectID, InstanceID: request.InstanceID, NativeID: request.NativeID})
	if err != nil {
		return CommandReceipt{}, err
	}
	if len(preview.Sessions) != 1 || preview.BindingToken != request.PreviewToken {
		return CommandReceipt{}, fmt.Errorf("预览后的原生元数据或配置已变化，请重新预览")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CommandReceipt{}, err
	}
	if a.fatal != nil {
		return CommandReceipt{}, a.fatal
	}
	if receipt, ok := a.state.Commands[request.CommandID]; ok {
		if receipt.Hash != hash {
			return CommandReceipt{}, fmt.Errorf("command_id 已用于其他内容")
		}
		return queryCopy(receipt), nil
	}
	native, binding, namespace, err := a.discoveryBinding(NativeDiscoveryRequest{ProjectID: request.ProjectID, InstanceID: request.InstanceID})
	if err != nil {
		return CommandReceipt{}, err
	}
	metadata := preview.Sessions[0].NativeSession
	if digest([]any{binding, metadata}) != request.PreviewToken {
		return CommandReceipt{}, fmt.Errorf("登记前执行配置已变化，请重新预览")
	}
	next := clone(a.state)
	receipt := CommandReceipt{ID: request.CommandID, Hash: hash, Status: "succeeded"}
	existingID := ""
	for _, existing := range next.Sessions {
		if existing.Scope.Instance.Kind != native.Instance.Kind || existing.Scope.NativeID != request.NativeID || existing.Scope.Cwd != native.Cwd {
			continue
		}
		if existing.Scope.StateNamespace == "" {
			return CommandReceipt{}, fmt.Errorf("已有同 ID 记录的状态目录尚未核对，不能登记第二份绑定")
		}
		if existing.Scope.StateNamespace == namespace {
			if existingID != "" {
				return CommandReceipt{}, fmt.Errorf("已有原生身份关联不唯一，请先核对重复工作台记录")
			}
			existingID = existing.ID
		}
	}
	if existingID != "" {
		receipt.SessionID, receipt.Status = existingID, "coalesced"
		next.Commands[request.CommandID] = receipt
		if err := a.commit(next, "session.native_binding_existing", existingID); err != nil {
			return CommandReceipt{}, err
		}
		return receipt, nil
	}
	id := uuid.NewString()
	title := metadata.Title
	if title == "" {
		title = request.NativeID
	}
	session := Session{ID: id, Title: title, ProjectID: request.ProjectID, Origin: "native", ControlMode: "native_readonly", Permission: "unverified", Paused: true, Generation: 1, Status: "unknown", Connection: "history_only", UpdatedAt: now(),
		Scope:         runtime.Scope{SessionID: id, Instance: native.Instance, Cwd: native.Cwd, NativeID: request.NativeID, StateNamespace: namespace},
		NativeBinding: &NativeSourceBinding{Metadata: metadata, BoundAt: now(), Configuration: binding},
		View:          runtime.View{NativeID: request.NativeID, Status: "unknown", Messages: []runtime.Message{}, Pending: []runtime.Pending{}, Evidence: runtime.Evidence{Source: "console.native_metadata_binding", At: now(), Detail: "只保存已核对的原生来源；运行状态、完整执行配置与输入控制未验证。"}}}
	next.Sessions[id] = session
	receipt.SessionID = id
	next.Commands[request.CommandID] = receipt
	if err := a.commit(next, "session.native_bound", id); err != nil {
		return CommandReceipt{}, err
	}
	return receipt, nil
}

func (a *Application) ReadBoundNativeHistory(ctx context.Context, id, cursor string) (NativeHistoryResult, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || session.Origin != "native" || session.NativeBinding == nil {
		a.mu.Unlock()
		return NativeHistoryResult{}, fmt.Errorf("此记录没有已登记的原生只读来源")
	}
	session = queryCopy(session)
	_, binding, _, err := a.discoveryBinding(NativeDiscoveryRequest{ProjectID: session.ProjectID, InstanceID: session.Scope.Instance.ID})
	a.mu.Unlock()
	if err != nil {
		return NativeHistoryResult{}, err
	}
	if binding != session.NativeBinding.Configuration {
		return NativeHistoryResult{}, fmt.Errorf("已登记原生来源的项目或执行配置发生变化；保留原绑定，不能读取替代来源")
	}
	result, err := a.ReadNativeHistory(ctx, NativeHistoryRequest{ProjectID: session.ProjectID, InstanceID: session.Scope.Instance.ID, NativeID: session.Scope.NativeID, Cursor: cursor})
	if err != nil {
		return NativeHistoryResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.state.Sessions[id]
	if a.closed || !ok || digest(current.Scope) != digest(session.Scope) || digest(current.NativeBinding) != digest(session.NativeBinding) {
		return NativeHistoryResult{}, fmt.Errorf("历史读取期间原生来源绑定发生变化")
	}
	return result, nil
}
