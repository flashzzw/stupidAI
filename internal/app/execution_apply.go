package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

func (a *Application) awaitExecutionObservation(ctx context.Context, op Operation) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		observing := a.observing[op.SessionID]
		session := a.state.Sessions[op.SessionID]
		var err error
		if a.closed || session.Generation != op.Generation {
			err = fmt.Errorf("原生改配前输入归属已变化")
		} else if !observing {
			err = a.verifyExecutionChangeOperation(session, op)
		}
		a.mu.Unlock()
		if err != nil {
			return &runtime.NotSent{Reason: err.Error()}
		}
		if !observing {
			return nil
		}
		select {
		case <-ctx.Done():
			return &runtime.NotSent{Reason: "等待原观察结束时取消，尚未发送改配"}
		case <-ticker.C:
		}
	}
}

func (a *Application) startExecutionChange(next State, session Session, command Command, receipt CommandReceipt) (CommandReceipt, error) {
	change := session.PendingExecutionChange
	if change == nil || len(command.ExecutionChange.Selections) != 0 || command.ExecutionChange.PreviewHash != "" || command.ExecutionChange.AdoptLocalSettings || command.ExecutionChange.adoptsCodexSources() {
		return receipt, fmt.Errorf("应用命令只接受已保存的快照与待应用 ID")
	}
	if err := checkApplicableExecutionCandidate(change); err != nil {
		return receipt, err
	}
	directoryCheck := session.Scope.Instance.Kind == "codex" && len(session.Scope.SkillSelections) > 0 && session.Status == "unknown" && session.View.NativeStatus == "idle" && session.View.Evidence.Complete
	target := executionChangeScope(session.Scope, change.Snapshot)
	hookDeactivation := runtime.CodexHookDeactivation(session.Scope, target)
	hookCheck := hookDeactivation && session.Status == "unknown" && session.View.NativeStatus == "idle" && session.View.Evidence.Complete
	sourceCheck := change.adoptsCodexSources() && session.Status == "unknown" && (session.View.NativeStatus == "idle" || change.HookSourceProof != nil)
	if session.ControlMode != "managed" || (session.Status != "idle" && !directoryCheck && !hookCheck && !sourceCheck) || (session.Connection != "ready" && !(sourceCheck && change.HookSourceProof != nil)) || a.busy[session.ID] || a.occupied(session.ID) {
		return receipt, fmt.Errorf("原会话未空闲、已转人工或正在核对；待核对结束后重试")
	}
	if err := verifyExecutionBinding(session); err != nil {
		return receipt, err
	}
	verifyRoute := a.verifyRouteIdentity
	if change.AdoptCodexConfigurationSources {
		verifyRoute = a.verifyConfigurationAdoptionRoute
	}
	if err := verifyRoute(session); err != nil {
		return receipt, err
	}
	if command.Kind == "apply_execution" && (!session.ExecutionHold || change.Baseline == "") {
		return receipt, fmt.Errorf("请先准备应用并取得完整空闲依据")
	}
	if command.Kind == "prepare_execution" && session.Scope.Instance.Kind == "codex" && !codexPluginSnapshots(session.ExecutionSnapshot, change.Snapshot) && !change.adoptsCodexSources() && session.ExecutionHold && change.Baseline != "" {
		return receipt, fmt.Errorf("已有准备依据；等待原线程卸载后应用，或撤销待应用选择")
	}
	if err := a.verifySavedExecutionChange(session); err != nil {
		return receipt, err
	}
	if change.AdoptLocalSettings && (session.View.SettingsGuard == nil || session.View.SettingsGuard.Version != 1 || session.View.SettingsGuard.State != "registered" || session.View.NativeStatus != "sdk_live") {
		return receipt, fmt.Errorf("当前原生本地设置阻止机制尚未核对可用，不能准备或应用新设置")
	}
	session.ExecutionHold, session.Paused = true, true
	session.Generation++
	id := uuid.NewString()
	op := Operation{ID: id, CommandID: command.ID, SessionID: session.ID, Generation: session.Generation,
		Status: "prepared", CreatedAt: now(), ExecutionSnapshotID: session.ExecutionSnapshot.ID,
		Intent: runtime.Intent{ID: id, Kind: command.Kind, Scope: session.Scope, Permission: session.Permission, ConfigurationBaseline: change.Baseline}}
	op.Intent.CreatedAt = op.CreatedAt
	if change.adoptsCodexSources() {
		op.Intent.HookSourceProof = queryCopy(change.HookSourceProof)
	}
	change.OperationID = id
	change.Status = "preparing"
	if command.Kind == "prepare_execution" {
		change.Baseline = ""
		if hookDeactivation || change.AdoptLocalSettings || change.adoptsCodexSources() || installedExecutionSnapshot(change.Snapshot) {
			op.Intent.ConfigurationTarget = &target
		}
	}
	if command.Kind == "apply_execution" {
		if codexPluginSnapshots(session.ExecutionSnapshot, change.Snapshot) || change.AdoptLocalSettings || change.adoptsCodexSources() || installedExecutionSnapshot(change.Snapshot) {
			previous := queryCopy(session.Scope)
			op.Intent.PreviousScope = &previous
		}
		op.ExecutionSnapshotID = change.Snapshot.ID
		op.Intent.Scope.MCPSelections = queryCopy(change.Snapshot.MCPSelections)
		op.Intent.Scope.SkillSelections = queryCopy(change.Snapshot.SkillSelections)
		op.Intent.Scope.PluginSelections = queryCopy(change.Snapshot.PluginSelections)
		op.Intent.Scope.ClaudeSettingsSources = claudeNativeSettingsSources(change.Snapshot)
		op.Intent.Scope.ClaudeInstalledSettings = claudeInstalledSettings(change.Snapshot)
		op.Intent.Scope.CodexUserHooks = codexUserHooks(change.Snapshot)
		op.Intent.Scope.CodexProjectHooks = codexProjectHooks(change.Snapshot)
		op.Intent.Scope.CodexConfigurationSources = codexConfigurationSources(change.Snapshot)
		change.Status = "applying"
	}
	session.UpdatedAt = now()
	receipt.OperationID, receipt.Status = id, "accepted"
	next.Sessions[session.ID], next.Operations[id], next.Commands[command.ID] = session, op, receipt
	if err := a.commit(next, "execution."+change.Status, session.ID); err != nil {
		return CommandReceipt{}, err
	}
	a.busy[session.ID] = true
	a.wg.Add(1)
	go a.dispatch(op)
	return receipt, nil
}

func (a *Application) verifySavedExecutionChange(session Session) error {
	change := session.PendingExecutionChange
	if err := checkApplicableExecutionCandidate(change); err != nil {
		return err
	}
	if change == nil || change.Snapshot == nil || change.Snapshot.ID == "" || change.BaseSnapshotID != session.ExecutionSnapshot.ID {
		return fmt.Errorf("待应用快照缺失或基准不一致")
	}
	if change.InstalledPluginChange {
		return a.verifyInstalledSettingsCandidate(session)
	}
	// Rebuild only for validation. The recorded candidate ID/time/version is
	// immutable and is never silently updated from a changed registry.
	copy := queryCopy(session)
	copy.PendingExecutionChange.Status = "pending"
	r := ExecutionChangeRequest{BaseSnapshotID: change.BaseSnapshotID, PendingChangeID: change.ID, AdoptLocalSettings: change.AdoptLocalSettings, AdoptCodexHookSources: change.AdoptCodexHookSources, AdoptCodexConfigurationSources: change.AdoptCodexConfigurationSources}
	if session.Scope.Instance.Kind == "claude" || codexPluginSnapshots(session.ExecutionSnapshot, change.Snapshot) {
		r.Selections = resourceCandidateSelections(session.ExecutionSnapshot, change.Snapshot)
	} else {
		for _, s := range change.Snapshot.MCPSelections {
			pick := CapabilitySelection{AssetID: s.AssetID, Mode: s.Mode}
			if !slices.Contains(r.Selections, pick) {
				r.Selections = append(r.Selections, pick)
			}
		}
	}
	preview, want, err := a.prepareExecutionChange(copy, r)
	if err != nil {
		return err
	}
	if digest(change.CodexConfigurationChange) != digest(preview.CodexConfigurationChange) {
		return fmt.Errorf("配置来源差异与已保存候选不一致")
	}
	got := queryCopy(change.Snapshot)
	if installedExecutionSnapshot(got) {
		want.ID = got.ID
		if err := planInstalledSettingsArtifact(session.Scope, want); err != nil {
			return err
		}
		want.ID = ""
	}
	got.ID, got.CreatedAt = "", ""
	if session.Scope.Instance.Kind == "claude" || codexPluginSnapshots(session.ExecutionSnapshot, change.Snapshot) {
		// Older saved candidates used the original asset ordering. Compare
		// semantics without rewriting their IDs, order or durable contents.
		slices.SortFunc(got.MCPSelections, func(a, b runtime.MCPSelection) int {
			return strings.Compare(a.AssetID+"/"+a.Server, b.AssetID+"/"+b.Server)
		})
		slices.SortFunc(got.CapabilityRefs, func(a, b CapabilityReference) int { return strings.Compare(a.Asset.ID, b.Asset.ID) })
		slices.SortFunc(got.SkillSelections, func(a, b runtime.SkillSelection) int { return strings.Compare(a.AssetID, b.AssetID) })
	}
	if digest(got) != digest(want) {
		return fmt.Errorf("待应用版本与原绑定或已确认选择不一致")
	}
	return nil
}

func (a *Application) verifyExecutionChangeOperation(session Session, op Operation) error {
	if op.Intent.Kind == "recover_execution" {
		return a.verifyExecutionRecoveryOperation(session, op)
	}
	if err := verifyExecutionChangeIntent(session, op); err != nil {
		return err
	}
	return a.verifySavedExecutionChange(session)
}

func verifyExecutionChangeIntent(session Session, op Operation) error {
	if op.Intent.Permission != session.Permission {
		return fmt.Errorf("改配操作的权限与原会话不一致")
	}
	if !session.ExecutionHold || session.PendingExecutionChange == nil || session.PendingExecutionChange.OperationID != op.ID {
		return fmt.Errorf("改配操作已失去准备状态或候选绑定")
	}
	if err := verifyExecutionBinding(session); err != nil {
		return err
	}
	change := session.PendingExecutionChange
	var proof *runtime.CodexHookSourceProof
	if change.adoptsCodexSources() {
		proof = change.HookSourceProof
	}
	if digest(op.Intent.HookSourceProof) != digest(proof) {
		return fmt.Errorf("hook 采纳操作的原配置依据与已保存候选不一致")
	}
	if change.Snapshot == nil || change.Snapshot.ID == "" || change.BaseSnapshotID != session.ExecutionSnapshot.ID {
		return fmt.Errorf("待应用快照缺失或基准不一致")
	}
	var target *runtime.Scope
	if op.Intent.Kind == "prepare_execution" {
		candidate := executionChangeScope(session.Scope, change.Snapshot)
		if runtime.CodexHookDeactivation(session.Scope, candidate) || change.AdoptLocalSettings || change.adoptsCodexSources() || installedExecutionSnapshot(change.Snapshot) {
			target = &candidate
		}
	}
	if digest(op.Intent.ConfigurationTarget) != digest(target) {
		return fmt.Errorf("准备操作的撤去目标与已保存候选不一致")
	}
	want := session.Scope
	id := session.ExecutionSnapshot.ID
	if op.Intent.Kind == "apply_execution" {
		if codexPluginSnapshots(session.ExecutionSnapshot, change.Snapshot) || change.AdoptLocalSettings || change.adoptsCodexSources() || installedExecutionSnapshot(change.Snapshot) {
			if op.Intent.PreviousScope == nil || digest(*op.Intent.PreviousScope) != digest(session.Scope) {
				return fmt.Errorf("插件改配缺少原执行绑定")
			}
		} else if op.Intent.PreviousScope != nil {
			return fmt.Errorf("改配带入了未授权的原执行绑定")
		}
		want.MCPSelections = session.PendingExecutionChange.Snapshot.MCPSelections
		want.SkillSelections = session.PendingExecutionChange.Snapshot.SkillSelections
		want.PluginSelections = session.PendingExecutionChange.Snapshot.PluginSelections
		want.ClaudeSettingsSources = claudeNativeSettingsSources(session.PendingExecutionChange.Snapshot)
		want.ClaudeInstalledSettings = claudeInstalledSettings(session.PendingExecutionChange.Snapshot)
		want.CodexUserHooks = codexUserHooks(session.PendingExecutionChange.Snapshot)
		want.CodexProjectHooks = codexProjectHooks(session.PendingExecutionChange.Snapshot)
		want.CodexConfigurationSources = codexConfigurationSources(session.PendingExecutionChange.Snapshot)
		id = session.PendingExecutionChange.Snapshot.ID
		if op.Intent.ConfigurationBaseline == "" || op.Intent.ConfigurationBaseline != session.PendingExecutionChange.Baseline {
			return fmt.Errorf("改配空闲依据不一致")
		}
	}
	if digest(op.Intent.Scope) != digest(want) || op.ExecutionSnapshotID != id {
		return fmt.Errorf("改配操作的目标版本或身份不一致")
	}
	return nil
}

func executionChangeScope(scope runtime.Scope, snapshot *ExecutionSnapshot) runtime.Scope {
	if snapshot != nil {
		scope.MCPSelections = queryCopy(snapshot.MCPSelections)
		scope.SkillSelections = queryCopy(snapshot.SkillSelections)
		scope.PluginSelections = queryCopy(snapshot.PluginSelections)
		scope.ClaudeSettingsSources = claudeNativeSettingsSources(snapshot)
		scope.ClaudeInstalledSettings = claudeInstalledSettings(snapshot)
		scope.CodexUserHooks = codexUserHooks(snapshot)
		scope.CodexProjectHooks = codexProjectHooks(snapshot)
		scope.CodexConfigurationSources = codexConfigurationSources(snapshot)
	}
	return scope
}

func (a *Application) finishExecutionChangeReceipt(next State, op Operation, receipt runtime.Receipt, err error) {
	session := next.Sessions[op.SessionID]
	change := session.PendingExecutionChange
	op.Receipt = receipt
	op.Status = "uncertain"
	if err == nil && receipt.NativeID != session.Scope.NativeID {
		err = fmt.Errorf("原生改配回执身份不匹配")
	}
	if op.Intent.Kind == "prepare_execution" {
		// Preparation only reads native state. An interrupted preparation can
		// be repeated, but never invents an idle baseline after restart.
		op.Status = "not_sent"
		if err == nil && receipt.ConfigurationBaseline != "" && session.Generation == op.Generation && !a.closed {
			op.Status = "succeeded"
			change.Baseline = receipt.ConfigurationBaseline
		} else if err == nil {
			err = fmt.Errorf("准备核对被中断或输入归属变化")
		}
		change.Status = "pending"
	} else if err == nil {
		op.Status = "acknowledged"
	} else {
		var notSent *runtime.NotSent
		if errors.As(err, &notSent) {
			op.Status = "not_sent"
			change.Status = "pending"
		}
	}
	if err != nil {
		op.Error = err.Error()
	}
	op.Evidence = append(op.Evidence, runtime.Evidence{Source: "console.configuration", At: now(), Complete: op.Status == "not_sent" || op.Status == "succeeded", Detail: op.Error})
	if op.Status == "acknowledged" || op.Status == "uncertain" {
		session.Status = "unknown"
	}
	session.Paused, session.UpdatedAt = true, now()
	next.Sessions[session.ID], next.Operations[op.ID] = session, op
	_ = a.commit(next, "execution.receipt", session.ID)
}

func (a *Application) promoteExecutionChange(session *Session, op Operation) error {
	// Admission was checked before dispatch. A verified native outcome records
	// what happened even if the library subsequently changed new-use admission.
	// Ordinary input still validates the promoted sources and execution binding.
	if err := verifyExecutionChangeIntent(*session, op); err != nil {
		return err
	}
	change := session.PendingExecutionChange
	// The old snapshot moves to history byte-for-byte. A late receipt may
	// establish effective configuration, but never restores input authority.
	if change.AdoptCodexConfigurationSources {
		profile := configurationProfileFingerprint(change.Snapshot)
		if profile == "" {
			return fmt.Errorf("候选缺少主配置来源指纹")
		}
		fingerprint, err := routeFingerprintWithProfile(session.Scope.Instance, session.Scope.Route, profile)
		if err != nil {
			return err
		}
		session.Fingerprint, session.RouteFingerprint = profile, fingerprint
	}
	session.ExecutionHistory = append(session.ExecutionHistory, session.ExecutionSnapshot)
	session.ExecutionSnapshot = queryCopy(change.Snapshot)
	session.Scope.MCPSelections = queryCopy(change.Snapshot.MCPSelections)
	session.Scope.SkillSelections = queryCopy(change.Snapshot.SkillSelections)
	session.Scope.PluginSelections = queryCopy(change.Snapshot.PluginSelections)
	session.Scope.ClaudeSettingsSources = claudeNativeSettingsSources(change.Snapshot)
	session.Scope.ClaudeInstalledSettings = claudeInstalledSettings(change.Snapshot)
	session.Scope.CodexUserHooks = codexUserHooks(change.Snapshot)
	session.Scope.CodexProjectHooks = codexProjectHooks(change.Snapshot)
	session.Scope.CodexConfigurationSources = codexConfigurationSources(change.Snapshot)
	session.PendingExecutionChange = nil
	session.ExecutionHold = false
	session.Paused = true
	return nil
}
