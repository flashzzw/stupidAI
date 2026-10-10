package app

import (
	"context"
	"fmt"
	"slices"

	"codex-netcap/internal/runtime"
)

func claudeInstalledSettings(snapshot *ExecutionSnapshot) *runtime.ClaudeInstalledSettings {
	if snapshot == nil || snapshot.ClaudeInstalledPluginSettings == nil {
		return nil
	}
	return queryCopy(snapshot.ClaudeInstalledPluginSettings.Binding)
}

func installedExecutionSnapshot(snapshot *ExecutionSnapshot) bool {
	return snapshot != nil && snapshot.Instance.Kind == "claude" && snapshot.SchemaVersion == 2 &&
		snapshot.ClaudeInstalledPluginSettings != nil && snapshot.ClaudeInstalledPluginSettings.Binding != nil &&
		snapshot.ClaudeInstalledPluginSettings.Binding.Version == 1 && snapshot.ClaudeLocalSettings != nil && snapshot.ClaudeLocalSettings.Version == 2 && snapshot.ClaudeLocalSettings.GuardVersion == 1
}

func planInstalledSettingsArtifact(scope runtime.Scope, snapshot *ExecutionSnapshot) error {
	binding := claudeInstalledSettings(snapshot)
	if binding == nil {
		return nil
	}
	file, err := runtime.PlanClaudeDerivedSettingsFile(scope, runtime.ClaudeInstalledPluginSettings(binding), snapshot.ID)
	if err != nil {
		return err
	}
	snapshot.ClaudeInstalledPluginSettings.Binding.Artifact = file
	return nil
}

func checkClaudeInstalledSettings(snapshot *ExecutionSnapshot) []SourceCheck {
	if snapshot.ClaudeInstalledPluginSettings == nil {
		return nil
	}
	check := SourceCheck{Group: "installed_plugins", Name: "Claude 已安装插件执行设置", Path: snapshot.Instance.Profile, Status: "unchanged"}
	binding := claudeInstalledSettings(snapshot)
	if !installedExecutionSnapshot(snapshot) {
		check.Status, check.Detail = "unavailable", "此候选尚无可执行的来源和文件绑定"
		return []SourceCheck{check}
	}
	scope := runtime.Scope{Instance: snapshot.Instance, Cwd: snapshot.Cwd, ClaudeInstalledSettings: binding, PluginSelections: snapshot.PluginSelections,
		SettingsGuardVersion: claudeSettingsGuardVersion(snapshot), ClaudeSettingsSources: claudeNativeSettingsSources(snapshot)}
	if err := runtime.ValidateClaudeInstalledSettings(scope); err != nil {
		check.Status, check.Detail = "changed", err.Error()
	}
	for _, p := range binding.Selections {
		ref := CapabilityReference{Asset: CapabilityAsset{Name: p.Name, Kind: "cli_plugin", Version: p.Version, Source: p.SourcePath, Backends: []string{"claude"}}, Source: SourceStamp{Path: p.SourcePath, Resolved: p.SourcePath, Kind: "directory", SHA256: p.SourceHash}}
		actual, err := claudePluginResource(ref, p.Mode)
		actual.NativeSource = p.NativeSource
		if err != nil || !runtime.EqualPluginSelections([]runtime.PluginSelection{p}, []runtime.PluginSelection{actual}) {
			check.Status, check.Detail = "changed", "插件声明资源树、权限或组件已变化："+p.NativeSource
		}
	}
	return []SourceCheck{check}
}

func (a *Application) verifyInstalledSettingsCandidate(session Session) error {
	change := session.PendingExecutionChange
	if change == nil || !change.InstalledPluginChange || !installedExecutionSnapshot(change.Snapshot) || len(change.Changes) != 1 || change.AdoptLocalSettings || change.adoptsCodexSources() {
		return fmt.Errorf("已安装插件候选缺少原选择或带入其他配置采纳")
	}
	proof := change.Snapshot.ClaudeInstalledPluginSettings
	binding := proof.Binding
	row, setting := change.Changes[0], proof.Preview.Setting
	if row.Kind != "installed_cli_plugin" || row.SourceKey != setting.SourceKey || row.After != map[bool]string{true: "enabled", false: "disabled"}[setting.Enabled] ||
		proof.Preview.SnapshotID != session.ExecutionSnapshot.ID || proof.Preview.Entry.SourceKey != setting.SourceKey ||
		binding.StartupSourceVersion != proof.StartupSettings.Fingerprint() || binding.StartupSourceVersion != proof.Preview.StartupSourceVersion ||
		digest(binding.SourceFiles) != digest(proof.Preview.SourceFiles) || digest(binding.StartupFiles) != digest(proof.StartupSettings.Files) {
		return fmt.Errorf("已安装插件候选的预览、选择与启动来源不一致")
	}
	catalog := readInstalledPluginCatalog(context.Background(), session)
	entry, err := selectedInstalledPlugin(catalog, PluginSettingsPreviewRequest{SourceKey: setting.SourceKey, Mode: row.After, SourceVersion: proof.Preview.SourceVersion}, session.Scope.Cwd)
	if err != nil || entry != proof.Preview.Entry {
		return fmt.Errorf("已保存候选的安装登记或声明来源发生变化")
	}
	expected, previous, err := installedSettingsCandidate(session, entry, proof.Resource, proof.Preview, proof.StartupSettings, row.After)
	if err != nil || previous != row.Before {
		return fmt.Errorf("已保存候选与原安装选择不一致")
	}
	expected.Artifact = binding.Artifact
	if digest(expected) != digest(binding) {
		return fmt.Errorf("已保存候选改变了未选择的安装来源")
	}
	// Only this installed selection and its evidence may differ. Native ID,
	// ordinary resources, permissions and local source adoption stay immutable.
	before, after := queryCopy(session.ExecutionSnapshot), queryCopy(change.Snapshot)
	before.RestoredFrom = ""
	for _, s := range []*ExecutionSnapshot{before, after} {
		s.ID, s.CreatedAt, s.PreviousID, s.Revision, s.SchemaVersion, s.ClaudeInstalledPluginSettings = "", "", "", 0, 0, nil
	}
	if digest(before) != digest(after) {
		return fmt.Errorf("安装插件候选带入了其他执行绑定变化")
	}
	if binding.Artifact.ID != change.Snapshot.ID || !slices.ContainsFunc(binding.Selections, func(p runtime.PluginSelection) bool { return p.NativeSource == setting.SourceKey }) {
		return fmt.Errorf("安装插件候选的版本文件或目标选择缺失")
	}
	return verifyExecutionChangeSources(session, change.Snapshot)
}
