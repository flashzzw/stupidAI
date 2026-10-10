package runtime

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

type ClaudeInstalledSettingsRecovery struct {
	Version         int                     `json:"version"`
	OriginalStartup ClaudeStartupSettings   `json:"original_startup"`
	Startup         ClaudeStartupSettings   `json:"startup"`
	Binding         ClaudeInstalledSettings `json:"binding"`
}

func restoredClaudeSettingsPaths(previous, failed []ClaudeSettingsSource) map[string]bool {
	if len(previous) < 2 || len(previous) != len(failed) {
		return nil
	}
	paths := map[string]bool{}
	for i, source := range previous {
		if source.Path != failed[i].Path {
			return nil
		}
		if source != failed[i] {
			paths[source.Path] = true
		}
	}
	return paths
}

func restoredClaudeStartupFiles(files []ClaudeStartupFile, paths map[string]bool) []ClaudeStartupFile {
	copy := slices.Clone(files)
	for i := range copy {
		if paths[copy[i].Path] && copy[i].Status == "read" {
			copy[i].IdentitySHA256 = ""
		}
	}
	return copy
}

// A new recovery binding changes only identities of the local files that the
// failed adoption actually replaced. It is evidence for a new version, never
// a mutation of the original snapshot or a relaxed check for future inputs.
func ClaudeInstalledSettingsRecoveryMatches(previous, failed Scope, binding *ClaudeInstalledSettings) bool {
	before := previous.ClaudeInstalledSettings
	paths := restoredClaudeSettingsPaths(previous.ClaudeSettingsSources, failed.ClaudeSettingsSources)
	if before == nil || binding == nil || len(paths) == 0 || !claudeSettingsAdoptionScopes(previous, failed) ||
		binding.StartupSourceVersion == before.StartupSourceVersion || !claudeSettingsFileSHA.MatchString(binding.StartupSourceVersion) ||
		binding.Artifact.Original != before.Artifact.Original || binding.Artifact.File.SHA256 != before.Artifact.File.SHA256 {
		return false
	}
	for _, file := range binding.StartupFiles {
		if file.Status == "read" && !claudeSettingsFileSHA.MatchString(file.IdentitySHA256) {
			return false
		}
	}
	left, right := *before, *binding
	left.StartupFiles, right.StartupFiles = restoredClaudeStartupFiles(left.StartupFiles, paths), restoredClaudeStartupFiles(right.StartupFiles, paths)
	right.Artifact, right.StartupSourceVersion = left.Artifact, left.StartupSourceVersion
	return reflect.DeepEqual(left, right)
}

func sameClaudeRestoredStartup(before, after ClaudeStartupSettings, paths map[string]bool) bool {
	normalize := func(report ClaudeStartupSettings) ClaudeStartupSettings {
		report.Files = restoredClaudeStartupFiles(report.Files, paths)
		report.PathBindings = slices.Clone(report.PathBindings)
		for i := range report.PathBindings {
			if paths[report.PathBindings[i].Path] {
				report.PathBindings[i].IdentitySHA256 = ""
			}
		}
		return report
	}
	return sameClaudeStartupSettings(normalize(before), normalize(after))
}

func ValidClaudeInstalledSettingsRecovery(previous, failed Scope, plan *ClaudeInstalledSettingsRecovery) bool {
	if plan == nil || plan.Version != 1 || !ClaudeInstalledSettingsRecoveryMatches(previous, failed, &plan.Binding) {
		return false
	}
	before := previous.ClaudeInstalledSettings
	for _, path := range plan.Startup.PathBindings {
		if !claudeSettingsFileSHA.MatchString(path.IdentitySHA256) {
			return false
		}
	}
	return plan.OriginalStartup.Fingerprint() == before.StartupSourceVersion && reflect.DeepEqual(plan.OriginalStartup.Files, before.StartupFiles) &&
		slices.Equal(plan.OriginalStartup.BoundLocalFiles, previous.ClaudeSettingsSources) && plan.Startup.Fingerprint() == plan.Binding.StartupSourceVersion &&
		reflect.DeepEqual(plan.Startup.Files, plan.Binding.StartupFiles) && claudeSettingsMetadataReadable(plan.Startup) == nil &&
		sameClaudeRestoredStartup(plan.OriginalStartup, plan.Startup, restoredClaudeSettingsPaths(previous.ClaudeSettingsSources, failed.ClaudeSettingsSources))
}

// Planning reads current sources without opening a query or publishing a file.
// All original bytes, modes and resolved paths must already be restored; the
// full report also keeps registry, policy and unrelated file identities fixed.
func PlanClaudeInstalledSettingsRecovery(ctx context.Context, previous, failed Scope, saved ClaudeStartupSettings) (*ClaudeInstalledSettingsRecovery, error) {
	before := previous.ClaudeInstalledSettings
	fail := func() (*ClaudeInstalledSettingsRecovery, error) {
		return nil, fmt.Errorf("恢复只能重新记录已还原本地设置的文件身份；原内容、路径、权限及其他启动来源必须一致")
	}
	if before == nil || !claudeSettingsAdoptionScopes(previous, failed) || saved.Fingerprint() != before.StartupSourceVersion ||
		!reflect.DeepEqual(saved.Files, before.StartupFiles) || !slices.Equal(saved.BoundLocalFiles, previous.ClaudeSettingsSources) {
		return fail()
	}
	current, err := ReadClaudeStartupSettings(ctx, previous)
	if err != nil {
		return nil, err
	}
	if err := claudeSettingsMetadataReadable(current); err != nil {
		return nil, err
	}
	paths := restoredClaudeSettingsPaths(previous.ClaudeSettingsSources, failed.ClaudeSettingsSources)
	if len(paths) == 0 || !sameClaudeRestoredStartup(saved, current, paths) {
		return fail()
	}
	if current.Fingerprint() == saved.Fingerprint() {
		return nil, nil
	}
	next := *before
	next.StartupFiles, next.StartupSourceVersion = current.Files, current.Fingerprint()
	if !ClaudeInstalledSettingsRecoveryMatches(previous, failed, &next) {
		return fail()
	}
	current.At = ""
	plan := &ClaudeInstalledSettingsRecovery{Version: 1, OriginalStartup: saved, Startup: current, Binding: next}
	if !ValidClaudeInstalledSettingsRecovery(previous, failed, plan) {
		return fail()
	}
	return plan, nil
}

func claudeRecoveredScope(in Intent) (Scope, error) {
	scope := in.Scope
	if in.ClaudeInstalledRecovery == nil {
		return scope, nil
	}
	if in.Kind != "recover_execution" || in.ConfigurationTarget == nil || !ValidClaudeInstalledSettingsRecovery(scope, *in.ConfigurationTarget, in.ClaudeInstalledRecovery) {
		return Scope{}, fmt.Errorf("恢复的安装设置版本与原本地采纳操作不一致")
	}
	scope.ClaudeInstalledSettings = &in.ClaudeInstalledRecovery.Binding
	return scope, nil
}

func claudeRecoveredInstalledBinding(scope Scope, change *claudeConfigurationChange) bool {
	if change == nil || change.Recovery == nil {
		return false
	}
	binding := change.Recovery.InstalledSettings
	if binding == nil {
		return equalClaudeInstalledSettings(scope.ClaudeInstalledSettings, change.InstalledPrevious)
	}
	previous, failed := scope, scope
	previous.ClaudeInstalledSettings = change.InstalledPrevious
	failed.ClaudeInstalledSettings = change.InstalledTarget
	failed.ClaudeSettingsSources = change.SettingsTarget
	failed.MCPSelections, failed.SkillSelections, failed.PluginSelections = change.Target, change.SkillTarget, change.PluginTarget
	return equalClaudeInstalledSettings(scope.ClaudeInstalledSettings, binding) && ClaudeInstalledSettingsRecoveryMatches(previous, failed, binding)
}
