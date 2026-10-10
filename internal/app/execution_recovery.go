package app

import (
	"context"
	"fmt"
	"time"

	"codex-netcap/internal/runtime"
)

type ExecutionRecoveryRequest struct {
	BaseSnapshotID         string `json:"base_snapshot_id"`
	PendingChangeID        string `json:"pending_change_id"`
	OperationID            string `json:"operation_id"`
	InstalledSourceVersion string `json:"installed_source_version,omitempty"`
	OriginalQueryRetry     bool   `json:"original_query_retry,omitempty"`
}
type ExecutionRecoveryCheck struct {
	OriginalQueryRetry bool                               `json:"original_query_retry,omitempty"`
	BaseSnapshotID     string                             `json:"base_snapshot_id"`
	PendingChangeID    string                             `json:"pending_change_id"`
	Native             runtime.ConfigurationRecoveryCheck `json:"native"`
	Sources            []SourceCheck                      `json:"sources"`
	Satisfied          bool                               `json:"satisfied"`
	Coverage           string                             `json:"coverage"`
	InstalledSettings  *InstalledSettingsRecoveryPreview  `json:"installed_settings,omitempty"`
}

func (a *Application) executionRecoveryOperation(s Session, r ExecutionRecoveryRequest) (Operation, error) {
	change := s.PendingExecutionChange
	if a.closed || s.Origin != "console" || (s.Scope.Instance.Kind != "claude" && s.Scope.Instance.Kind != "codex") || s.ControlMode != "managed" || s.ExecutionSnapshot == nil || change == nil || (s.Scope.Instance.Kind == "claude" && !change.AdoptLocalSettings && !installedExecutionSnapshot(change.Snapshot)) || (s.Scope.Instance.Kind == "codex" && !codexPluginSnapshots(s.ExecutionSnapshot, change.Snapshot) && !change.adoptsCodexSources()) || change.Status != "applying" || change.RecoveryOperationID != "" || r.BaseSnapshotID != s.ExecutionSnapshot.ID || r.PendingChangeID != change.ID || r.OperationID == "" || r.OperationID != change.OperationID || a.busy[s.ID] {
		return Operation{}, fmt.Errorf("需要原会话尚待核对的配置应用操作")
	}
	op, ok := a.state.Operations[r.OperationID]
	if !ok || op.SessionID != s.ID || op.Status != "uncertain" {
		return Operation{}, fmt.Errorf("原配置操作尚未进入失败核对状态")
	}
	if err := verifyExecutionBinding(s); err != nil {
		return Operation{}, err
	}
	verifyRoute := a.verifyRouteIdentity
	if change.AdoptCodexConfigurationSources {
		verifyRoute = a.verifyConfigurationAdoptionRoute
	}
	if err := verifyRoute(s); err != nil {
		return Operation{}, err
	}
	if err := verifyExecutionChangeIntent(s, op); err != nil {
		return Operation{}, err
	}
	return queryCopy(op), nil
}

func (a *Application) ReadExecutionRecovery(ctx context.Context, id string, r ExecutionRecoveryRequest) (ExecutionRecoveryCheck, error) {
	a.mu.Lock()
	s, ok := a.state.Sessions[id]
	if !ok {
		a.mu.Unlock()
		return ExecutionRecoveryCheck{}, fmt.Errorf("会话不存在")
	}
	s = queryCopy(s)
	op, err := a.executionRecoveryOperation(s, r)
	reader, supported := a.runtime.(runtime.ConfigurationRecoveryReader)
	a.mu.Unlock()
	if err != nil {
		return ExecutionRecoveryCheck{}, err
	}
	if !supported {
		return ExecutionRecoveryCheck{}, fmt.Errorf("当前运行时尚无配置恢复条件核对入口")
	}
	sources := checkExecutionSources(s.ExecutionSnapshot)
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	plan, err := planInstalledSettingsRecovery(check, s, op)
	if err != nil {
		return ExecutionRecoveryCheck{}, err
	}
	op.Intent.ClaudeInstalledRecovery = plan
	native, err := reader.ReadConfigurationRecovery(check, op.Intent)
	if err != nil {
		return ExecutionRecoveryCheck{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || current.Generation != s.Generation || digest(current.Scope) != digest(s.Scope) || digest(current.PendingExecutionChange) != digest(s.PendingExecutionChange) {
		return ExecutionRecoveryCheck{}, fmt.Errorf("读取期间原会话或候选已变化，请重新核对")
	}
	if _, err := a.executionRecoveryOperation(current, r); err != nil {
		return ExecutionRecoveryCheck{}, err
	}
	if err := runtime.ValidateConfigurationRecoveryCheck(op.Intent, native); err != nil {
		return ExecutionRecoveryCheck{}, err
	}
	if native.OriginalQueryRetry != nil && !runtime.ClaudeOriginalQueryRecoveryMatchesFailure(native.OriginalQueryRetry, current.View.ConfigurationFailure) {
		return ExecutionRecoveryCheck{}, fmt.Errorf("原查询恢复方式与已记录的原失败不一致，请重新核对")
	}
	if digest(sources) != digest(checkExecutionSources(current.ExecutionSnapshot)) {
		return ExecutionRecoveryCheck{}, fmt.Errorf("读取期间来源文件已变化，请重新核对")
	}
	out := ExecutionRecoveryCheck{BaseSnapshotID: r.BaseSnapshotID, PendingChangeID: r.PendingChangeID, Native: native, Sources: sources, Satisfied: true, Coverage: "owned_query_and_recorded_sources"}
	out.OriginalQueryRetry = native.OriginalQueryRetry != nil
	out.InstalledSettings = installedSettingsRecoveryPreview(s, plan)
	if s.Scope.Instance.Kind == "codex" {
		out.Coverage = "native_thread_and_recorded_sources"
	}
	for _, condition := range native.Checks {
		out.Satisfied = out.Satisfied && condition.Status == "passed"
	}
	for _, source := range sources {
		out.Satisfied = out.Satisfied && source.Status == "unchanged"
	}
	return out, nil
}
