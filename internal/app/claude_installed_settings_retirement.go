package app

import (
	"context"
	"time"

	"codex-netcap/internal/runtime"
)

const installedSettingsRetirementSource = "console.installed_settings_retirement"

func installedSettingsFileReferenced(state State, id string) bool {
	bound := func(scope runtime.Scope) bool {
		return scope.ClaudeInstalledSettings != nil && scope.ClaudeInstalledSettings.Artifact.ID == id
	}
	snapshot := func(s *ExecutionSnapshot) bool {
		return s != nil && s.ClaudeInstalledPluginSettings != nil && s.ClaudeInstalledPluginSettings.Binding != nil && s.ClaudeInstalledPluginSettings.Binding.Artifact.ID == id
	}
	for _, s := range state.Sessions {
		if bound(s.Scope) || snapshot(s.ExecutionSnapshot) || s.PendingExecutionChange != nil && (snapshot(s.PendingExecutionChange.Snapshot) || snapshot(s.PendingExecutionChange.RecoverySnapshot)) {
			return true
		}
		for _, old := range s.ExecutionHistory {
			if snapshot(old) {
				return true
			}
		}
		for _, attempt := range s.ExecutionChangeAttempts {
			if snapshot(attempt.Change.Snapshot) || snapshot(attempt.Change.RecoverySnapshot) {
				return true
			}
		}
	}
	for _, op := range state.Operations {
		if !terminal(op.Status) && (bound(op.Intent.Scope) || op.Intent.PreviousScope != nil && bound(*op.Intent.PreviousScope) || op.Intent.ConfigurationTarget != nil && bound(*op.Intent.ConfigurationTarget) || op.Intent.ClaudeInstalledRecovery != nil && op.Intent.ClaudeInstalledRecovery.Binding.Artifact.ID == id) {
			return true
		}
	}
	return false
}

// A not-sent operation already retains the exact file plan across restart.
// Only abandoned candidates qualify; active, pending and historical versions
// remain references even when every native process is stopped.
func (a *Application) startInstalledSettingsRetirement() {
	retirer, ok := a.runtime.(runtime.ClaudeInstalledSettingsRetirer)
	if !ok || a.retiringInstalledSettings || a.closed || a.fatal != nil {
		return
	}
	for _, op := range a.state.Operations {
		scope := op.Intent.Scope
		if op.Intent.Kind == "recover_execution" && op.Intent.ClaudeInstalledRecovery != nil {
			scope.ClaudeInstalledSettings = &op.Intent.ClaudeInstalledRecovery.Binding
		} else if op.Intent.Kind != "apply_execution" {
			continue
		}
		binding := scope.ClaudeInstalledSettings
		if op.Status != "not_sent" || binding == nil || a.busy[op.SessionID] || installedSettingsFileReferenced(a.state, binding.Artifact.ID) {
			continue
		}
		ready := true
		for _, evidence := range op.Evidence {
			if evidence.Source != installedSettingsRetirementSource {
				continue
			}
			at, _ := time.Parse(time.RFC3339Nano, evidence.At)
			if evidence.Complete || time.Since(at) < 30*time.Second {
				ready = false
			}
		}
		if !ready {
			continue
		}
		a.retiringInstalledSettings = true
		a.wg.Add(1)
		go func(op Operation, scope runtime.Scope) {
			defer a.wg.Done()
			ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
			defer cancel()
			err := retirer.RetireClaudeInstalledSettings(ctx, scope)
			a.mu.Lock()
			defer a.mu.Unlock()
			a.retiringInstalledSettings = false
			if a.closed || a.fatal != nil {
				return
			}
			current := a.state.Operations[op.ID]
			if current.Status != "not_sent" || digest(current.Intent) != digest(op.Intent) {
				return
			}
			evidence := runtime.Evidence{Source: installedSettingsRetirementSource, At: now(), Complete: err == nil, Detail: "已回收撤销候选的未使用设置文件"}
			if err != nil {
				evidence.Detail = "未使用设置文件待回收：" + err.Error()
			}
			current.Evidence = append(current.Evidence, evidence)
			next := clone(a.state)
			next.Operations[op.ID] = current
			_ = a.commit(next, "execution.settings_retirement", op.SessionID)
		}(queryCopy(op), queryCopy(scope))
		return
	}
}
