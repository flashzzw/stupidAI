package app

import (
	"context"
	"fmt"

	"codex-netcap/internal/runtime"
)

type InstalledSettingsRecoveryPreview struct {
	SourceVersion string   `json:"source_version"`
	NextRevision  int      `json:"next_revision"`
	RestoredFiles []string `json:"restored_files"`
}

func planInstalledSettingsRecovery(ctx context.Context, session Session, failed Operation) (*runtime.ClaudeInstalledSettingsRecovery, error) {
	if session.PendingExecutionChange == nil || !session.PendingExecutionChange.AdoptLocalSettings || session.Scope.ClaudeInstalledSettings == nil {
		return nil, nil
	}
	return runtime.PlanClaudeInstalledSettingsRecovery(ctx, session.Scope, failed.Intent.Scope, session.ExecutionSnapshot.ClaudeInstalledPluginSettings.StartupSettings)
}

func installedSettingsRecoveryPreview(session Session, plan *runtime.ClaudeInstalledSettingsRecovery) *InstalledSettingsRecoveryPreview {
	if plan == nil {
		return nil
	}
	files := []string{}
	for i, file := range plan.Startup.Files {
		if file.IdentitySHA256 != plan.OriginalStartup.Files[i].IdentitySHA256 {
			files = append(files, file.Path)
		}
	}
	return &InstalledSettingsRecoveryPreview{SourceVersion: plan.Binding.StartupSourceVersion, NextRevision: max(1, session.ExecutionSnapshot.Revision) + 1, RestoredFiles: files}
}

func validateInstalledSettingsRecoverySnapshot(session Session, op Operation) error {
	plan, snapshot := op.Intent.ClaudeInstalledRecovery, session.PendingExecutionChange.RecoverySnapshot
	if plan == nil && snapshot == nil {
		return nil
	}
	if plan == nil || snapshot == nil || op.Intent.ConfigurationTarget == nil || !installedExecutionSnapshot(snapshot) ||
		!runtime.ValidClaudeInstalledSettingsRecovery(session.Scope, *op.Intent.ConfigurationTarget, plan) ||
		digest(snapshot.ClaudeInstalledPluginSettings.Binding) != digest(&plan.Binding) || digest(snapshot.ClaudeInstalledPluginSettings.StartupSettings) != digest(plan.Startup) ||
		snapshot.ID == session.ExecutionSnapshot.ID || snapshot.ID == session.PendingExecutionChange.Snapshot.ID || snapshot.ID != plan.Binding.Artifact.ID ||
		snapshot.PreviousID != session.ExecutionSnapshot.ID || snapshot.RestoredFrom != session.ExecutionSnapshot.ID || snapshot.Revision != max(1, session.ExecutionSnapshot.Revision)+1 {
		return fmt.Errorf("恢复版本与原快照、原来源或持久恢复计划不一致")
	}
	expected := queryCopy(session.ExecutionSnapshot)
	expected.ID, expected.CreatedAt, expected.Revision, expected.PreviousID, expected.RestoredFrom = snapshot.ID, snapshot.CreatedAt, snapshot.Revision, snapshot.PreviousID, snapshot.RestoredFrom
	expected.ClaudeInstalledPluginSettings.Binding = queryCopy(&plan.Binding)
	expected.ClaudeInstalledPluginSettings.StartupSettings = queryCopy(plan.Startup)
	if digest(expected) != digest(snapshot) {
		return fmt.Errorf("恢复版本改变了原设置以外的执行绑定")
	}
	for _, source := range checkExecutionSources(snapshot) {
		if source.Status != "unchanged" {
			return fmt.Errorf("恢复版本来源已变化：%s", source.Name)
		}
	}
	return nil
}
