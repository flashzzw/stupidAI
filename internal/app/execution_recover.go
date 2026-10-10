package app

import (
	"errors"
	"fmt"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

// Failed candidates are retained independently from effective execution history.
type ExecutionChangeAttempt struct {
	Change      ExecutionChange `json:"change"`
	RecoveredAt string          `json:"recovered_at"`
}

func (a *Application) startExecutionRecovery(command Command, hash string) (CommandReceipt, error) {
	if command.ExecutionRecovery == nil || command.ExecutionChange != nil || command.ExecutionProfile != nil || command.SkillInput != nil || command.Text != "" || len(command.CapabilitySelections) > 0 || len(command.CapabilityRefs) > 0 {
		return CommandReceipt{}, fmt.Errorf("恢复命令只接受原快照、失败候选和原操作引用")
	}
	next := clone(a.state)
	s, ok := next.Sessions[command.SessionID]
	if !ok {
		return CommandReceipt{}, fmt.Errorf("会话不存在")
	}
	failed, err := a.executionRecoveryOperation(s, *command.ExecutionRecovery)
	if err != nil {
		return CommandReceipt{}, err
	}
	if _, ok := a.runtime.(runtime.ConfigurationRecoveryReader); !ok {
		return CommandReceipt{}, fmt.Errorf("当前运行时不支持恢复条件核对")
	}
	if err = verifyExecutionSources(s); err != nil {
		return CommandReceipt{}, err
	}
	plan, err := planInstalledSettingsRecovery(a.ctx, s, failed)
	if err != nil {
		return CommandReceipt{}, err
	}
	if plan != nil && command.ExecutionRecovery.InstalledSourceVersion != plan.Binding.StartupSourceVersion || plan == nil && command.ExecutionRecovery.InstalledSourceVersion != "" {
		return CommandReceipt{}, fmt.Errorf("恢复文件身份需要先核对当前来源版本；文件再变时请重新核对")
	}
	f := s.View.ConfigurationFailure
	if s.Scope.Instance.Kind == "claude" && (f == nil || f.Version != 1 || f.Operation != failed.ID || f.Baseline != failed.Intent.ConfigurationBaseline || f.Cleanup != "confirmed") {
		return CommandReceipt{}, fmt.Errorf("原失败与自有查询退出依据尚未核对")
	}
	var originalQueryRecovery *runtime.ClaudeOriginalQueryRecovery
	if command.ExecutionRecovery.OriginalQueryRetry {
		if s.Scope.Instance.Kind != "claude" || f == nil || s.Scope.ClaudeInstalledSettings == nil && failed.Intent.Scope.ClaudeInstalledSettings == nil {
			return CommandReceipt{}, fmt.Errorf("此原配置失败不适用候选启动前的原查询恢复方式")
		}
		failure := *f
		failure.Detail = ""
		originalQueryRecovery = &runtime.ClaudeOriginalQueryRecovery{Version: 1, Failure: failure}
		if !runtime.ClaudeOriginalQueryRecoveryMatchesFailure(originalQueryRecovery, f) {
			return CommandReceipt{}, fmt.Errorf("原失败尚未确认发生在候选启动前，请重新核对")
		}
	}
	for _, op := range next.Operations {
		if op.SessionID == s.ID && op.ID != failed.ID && !terminal(op.Status) {
			return CommandReceipt{}, fmt.Errorf("还有其他操作待核对")
		}
	}
	id := uuid.NewString()
	if plan != nil {
		restored := queryCopy(s.ExecutionSnapshot)
		restored.ID, restored.CreatedAt = uuid.NewString(), now()
		restored.PreviousID, restored.RestoredFrom = s.ExecutionSnapshot.ID, s.ExecutionSnapshot.ID
		restored.Revision = max(1, s.ExecutionSnapshot.Revision) + 1
		restored.ClaudeInstalledPluginSettings.Binding = queryCopy(&plan.Binding)
		restored.ClaudeInstalledPluginSettings.StartupSettings = queryCopy(plan.Startup)
		if err := planInstalledSettingsArtifact(s.Scope, restored); err != nil {
			return CommandReceipt{}, err
		}
		plan.Binding = *claudeInstalledSettings(restored)
		s.PendingExecutionChange.RecoverySnapshot = restored
	}
	s.Generation++
	s.Paused, s.ExecutionHold = true, true
	s.PendingExecutionChange.RecoveryOperationID = id
	if s.Scope.Instance.Kind == "claude" {
		s.PendingExecutionChange.Failure = queryCopy(f)
	} else {
		s.PendingExecutionChange.FailureDetail = "原生改配未取得完整确认；旧配置与候选的实际状态尚不能推断。"
	}
	s.PendingExecutionChange.Status = "recovering"
	s.UpdatedAt = now()
	target := queryCopy(failed.Intent.Scope)
	op := Operation{ID: id, CommandID: command.ID, SessionID: s.ID, Generation: s.Generation, Status: "prepared", CreatedAt: now(), ExecutionSnapshotID: s.ExecutionSnapshot.ID,
		Intent: runtime.Intent{ID: id, Kind: "recover_execution", Scope: s.Scope, ConfigurationTarget: &target, ConfigurationRecoveryOf: failed.ID, ConfigurationBaseline: failed.Intent.ConfigurationBaseline, Permission: s.Permission}}
	op.Intent.CreatedAt = op.CreatedAt
	op.Intent.ClaudeInstalledRecovery = plan
	op.Intent.ClaudeOriginalQueryRecovery = originalQueryRecovery
	if s.PendingExecutionChange.RecoverySnapshot != nil {
		op.ExecutionSnapshotID = s.PendingExecutionChange.RecoverySnapshot.ID
	}
	receipt := CommandReceipt{ID: command.ID, Hash: hash, OperationID: id, SessionID: s.ID, Status: "accepted"}
	next.Sessions[s.ID], next.Operations[id], next.Commands[command.ID] = s, op, receipt
	if err = a.commit(next, "execution.recovering", s.ID); err != nil {
		return CommandReceipt{}, err
	}
	a.busy[s.ID] = true
	a.wg.Add(1)
	go a.dispatch(op)
	return receipt, nil
}

func (a *Application) verifyExecutionRecoveryIntent(s Session, op Operation) error {
	p := s.PendingExecutionChange
	if s.ExecutionSnapshot == nil || p == nil || p.Status != "recovering" || p.RecoveryOperationID != op.ID || op.Intent.ConfigurationRecoveryOf != p.OperationID || op.Intent.Kind != "recover_execution" || op.Intent.PreviousScope != nil || op.Intent.Permission != s.Permission || digest(op.Intent.Scope) != digest(s.Scope) || op.Intent.ConfigurationBaseline != p.Baseline {
		return fmt.Errorf("恢复操作与原执行或失败候选不一致")
	}
	expectedSnapshot := s.ExecutionSnapshot.ID
	if p.RecoverySnapshot != nil {
		expectedSnapshot = p.RecoverySnapshot.ID
	}
	if op.ExecutionSnapshotID != expectedSnapshot {
		return fmt.Errorf("恢复操作的执行版本不一致")
	}
	if err := validateInstalledSettingsRecoverySnapshot(s, op); err != nil {
		return err
	}
	failed, ok := a.state.Operations[p.OperationID]
	if !ok || failed.SessionID != s.ID || failed.Status != "uncertain" || digest(op.Intent.ConfigurationTarget) != digest(&failed.Intent.Scope) {
		return fmt.Errorf("原失败操作绑定不一致")
	}
	if proof := op.Intent.ClaudeOriginalQueryRecovery; proof != nil && (s.Scope.Instance.Kind != "claude" ||
		s.Scope.ClaudeInstalledSettings == nil && failed.Intent.Scope.ClaudeInstalledSettings == nil ||
		!runtime.ClaudeOriginalQueryRecoveryMatchesFailure(proof, p.Failure)) {
		return fmt.Errorf("持久恢复方式与原失败记录不一致")
	}
	return verifyExecutionChangeIntent(s, failed)
}

func (a *Application) verifyExecutionRecoveryOperation(s Session, op Operation) error {
	if err := a.verifyExecutionRecoveryIntent(s, op); err != nil {
		return err
	}
	if s.ControlMode != "managed" {
		return fmt.Errorf("恢复前输入归属已变化")
	}
	if err := a.verifyRouteIdentity(s); err != nil {
		return err
	}
	return verifyExecutionSources(s)
}

func releaseExecutionRecovery(s *Session, op Operation) {
	if p := s.PendingExecutionChange; p != nil && p.RecoveryOperationID == op.ID {
		p.RecoveryOperationID = ""
		p.RecoverySnapshot = nil
		p.Status = "applying"
	}
}
func (a *Application) finishExecutionRecoveryReceipt(next State, op Operation, receipt runtime.Receipt, err error) {
	s := next.Sessions[op.SessionID]
	op.Receipt, op.Status = receipt, "uncertain"
	if err == nil && receipt.NativeID != s.Scope.NativeID {
		err = fmt.Errorf("恢复回执的原生身份不匹配")
	}
	if err == nil && digest(receipt.ClaudeOriginalQueryRecovery) != digest(op.Intent.ClaudeOriginalQueryRecovery) {
		err = fmt.Errorf("恢复回执缺少匹配的原查询恢复方式，需要核对原操作")
	}
	if err == nil && op.Intent.ClaudeInstalledRecovery != nil && digest(receipt.ClaudeInstalledSettings) != digest(&op.Intent.ClaudeInstalledRecovery.Binding) {
		err = fmt.Errorf("恢复回执缺少匹配的安装设置版本，需要核对原操作")
	}
	if err == nil {
		op.Status = "acknowledged"
	} else {
		var unsent *runtime.NotSent
		if errors.As(err, &unsent) {
			op.Status = "not_sent"
			releaseExecutionRecovery(&s, op)
		}
		op.Error = err.Error()
	}
	op.Evidence = append(op.Evidence, runtime.Evidence{Source: "console.configuration_recovery", At: now(), Complete: op.Status == "not_sent", Detail: op.Error})
	s.Status, s.Paused, s.UpdatedAt = "unknown", true, now()
	next.Sessions[s.ID], next.Operations[op.ID] = s, op
	_ = a.commit(next, "execution.recovery_receipt", s.ID)
}
func (a *Application) finishVerifiedExecutionRecovery(next State, s *Session, op Operation, receipt runtime.Receipt) error {
	if err := a.verifyExecutionRecoveryIntent(*s, op); err != nil {
		return err
	}
	if digest(receipt.ClaudeOriginalQueryRecovery) != digest(op.Intent.ClaudeOriginalQueryRecovery) {
		return fmt.Errorf("持久恢复回执与明确选择的原查询恢复方式不一致")
	}
	if err := a.verifyRouteIdentity(*s); err != nil {
		return err
	}
	if err := verifyExecutionSources(*s); err != nil {
		return err
	}
	failed := next.Operations[op.Intent.ConfigurationRecoveryOf]
	p := queryCopy(*s.PendingExecutionChange)
	p.Status = "recovered"
	if p.RecoverySnapshot != nil {
		s.ExecutionHistory = append(s.ExecutionHistory, queryCopy(s.ExecutionSnapshot))
		s.ExecutionSnapshot = queryCopy(p.RecoverySnapshot)
		s.Scope.ClaudeInstalledSettings = claudeInstalledSettings(s.ExecutionSnapshot)
	}
	s.ExecutionChangeAttempts = append(s.ExecutionChangeAttempts, ExecutionChangeAttempt{Change: p, RecoveredAt: now()})
	s.PendingExecutionChange = nil
	s.ExecutionHold = false
	s.Paused = true
	failed.Status = "failed"
	failed.Error = "候选配置未通过核对；已明确按原 ID 恢复原版本。已发生的副作用保留。"
	failed.Evidence = append(failed.Evidence, runtime.Evidence{Source: "console.configuration_recovery", At: now(), Complete: true, Detail: failed.Error})
	next.Operations[failed.ID] = failed
	return nil
}

// A completed recovery is a historical fact. While automation remains paused,
// passive polling must not load the thread to recreate lost connection evidence.
func (a *Application) preservePausedRecoveryObservation(s Session, scope *runtime.Scope) {
	if !s.Paused || s.Scope.Instance.Kind != "codex" || len(s.ExecutionChangeAttempts) == 0 {
		return
	}
	id := s.ExecutionChangeAttempts[len(s.ExecutionChangeAttempts)-1].Change.RecoveryOperationID
	a.mu.Lock()
	op, ok := a.state.Operations[id]
	a.mu.Unlock()
	if ok && op.Status == "succeeded" && op.Intent.Kind == "recover_execution" && op.Receipt.ConfigurationRecovery != nil && digest(op.Intent.Scope) == digest(s.Scope) {
		scope.NoAutoResume = true
		scope.ConfigurationBaseline = op.Intent.ConfigurationBaseline
	}
}
