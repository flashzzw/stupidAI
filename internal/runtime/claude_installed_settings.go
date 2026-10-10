package runtime

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// This is an execution binding, distinct from the preview's released file and
// from a library asset. NativeSource is the full installed name@market key.
type ClaudeInstalledSettings struct {
	Version              int                       `json:"version"`
	Artifact             ClaudeDerivedSettingsFile `json:"artifact"`
	Selections           []PluginSelection         `json:"selections"`
	SourceFiles          []FileSource              `json:"source_files"`
	StartupFiles         []ClaudeStartupFile       `json:"startup_files"`
	StartupSourceVersion string                    `json:"startup_source_version"`
}

func ClaudeInstalledPluginSettings(binding *ClaudeInstalledSettings) []ClaudePluginSetting {
	if binding == nil {
		return nil
	}
	settings := make([]ClaudePluginSetting, 0, len(binding.Selections))
	for _, p := range binding.Selections {
		settings = append(settings, ClaudePluginSetting{SourceKey: p.NativeSource, Enabled: p.Mode == "enabled"})
	}
	return settings
}

func claudeExecutionPlugins(scope Scope) []PluginSelection {
	plugins := slices.Clone(scope.PluginSelections)
	if scope.ClaudeInstalledSettings != nil {
		plugins = append(plugins, scope.ClaudeInstalledSettings.Selections...)
	}
	return plugins
}

func claudePluginSource(p PluginSelection) string {
	if p.NativeSource != "" {
		return p.NativeSource
	}
	return p.Name + "@inline"
}

func ValidateClaudeInstalledSettings(scope Scope) error {
	binding := scope.ClaudeInstalledSettings
	if binding == nil {
		return nil
	}
	fail := func() error { return fmt.Errorf("已安装插件的执行设置绑定无效或来源发生变化") }
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || binding.Version != 1 || scope.SettingsGuardVersion != 1 || len(scope.ClaudeSettingsSources) < 2 ||
		!validClaudePluginSettings(ClaudeInstalledPluginSettings(binding)) || len(binding.SourceFiles) == 0 || len(binding.SourceFiles) > 2048 ||
		len(binding.StartupFiles) == 0 || len(binding.StartupFiles) > 128 ||
		!claudeSettingsFileSHA.MatchString(binding.StartupSourceVersion) {
		return fail()
	}
	names := map[string]bool{}
	for _, p := range scope.PluginSelections {
		names[p.Name] = true
	}
	for _, p := range binding.Selections {
		if p.AssetID != "" || !strings.HasPrefix(p.NativeSource, p.Name+"@") || names[p.Name] ||
			p.Mode != "enabled" && p.Mode != "disabled" || p.SourcePath == "" || !claudeSettingsFileSHA.MatchString(p.SourceHash) ||
			p.Hooks != nil || p.ManifestSource != nil || p.SkillAsset != nil || p.CachePath != "" {
			return fail()
		}
		names[p.Name] = true
	}
	planned, err := PlanClaudeDerivedSettingsFile(scope, ClaudeInstalledPluginSettings(binding), binding.Artifact.ID)
	if err != nil || planned != binding.Artifact {
		return fail()
	}
	seen := map[string]bool{}
	for _, expected := range binding.SourceFiles {
		if expected.Path == "" || seen[expected.Path] {
			return fail()
		}
		seen[expected.Path] = true
		actual, _, err := readClaudeSettingsFile(expected.Path, expected.Kind == "absent")
		if err != nil || actual != expected {
			return fail()
		}
	}
	return nil
}

func verifyClaudeInstalledStartup(ctx context.Context, scope Scope) error {
	if scope.ClaudeInstalledSettings == nil {
		return nil
	}
	if err := ValidateClaudeInstalledSettings(scope); err != nil {
		return err
	}
	current, err := ReadClaudeStartupSettings(ctx, scope)
	if err != nil {
		return err
	}
	if err := claudeSettingsMetadataReadable(current); err != nil {
		return err
	}
	if current.Fingerprint() != scope.ClaudeInstalledSettings.StartupSourceVersion || !reflect.DeepEqual(current.Files, scope.ClaudeInstalledSettings.StartupFiles) {
		return fmt.Errorf("已安装插件保存后的启动设置来源变化，请重新核对")
	}
	return nil
}

// Local adoption gets a new observed report. The previous fingerprint still
// binds every non-local source, including registry and policy observations.
func AdoptClaudeInstalledSettings(ctx context.Context, previous Scope, saved ClaudeStartupSettings, sources []ClaudeSettingsSource) (*ClaudeInstalledSettings, ClaudeStartupSettings, error) {
	binding := previous.ClaudeInstalledSettings
	fail := func() (*ClaudeInstalledSettings, ClaudeStartupSettings, error) {
		return nil, ClaudeStartupSettings{}, fmt.Errorf("本地设置采纳不能改变已安装插件、非本地启动来源或原来源记录")
	}
	if binding == nil || saved.Fingerprint() != binding.StartupSourceVersion || !reflect.DeepEqual(saved.Files, binding.StartupFiles) || !slices.Equal(saved.BoundLocalFiles, previous.ClaudeSettingsSources) {
		return fail()
	}
	current, err := ReadClaudeStartupSettings(ctx, previous)
	if err != nil {
		return nil, current, err
	}
	if err := claudeSettingsMetadataReadable(current); err != nil {
		return nil, current, err
	}
	if !slices.Equal(current.BoundLocalFiles, sources) || !sameClaudeNonLocalStartup(saved, current, previous.ClaudeSettingsSources) {
		return fail()
	}
	next := *binding
	next.StartupFiles, next.StartupSourceVersion = current.Files, current.Fingerprint()
	// Sampling time is not part of the immutable source version or preview hash.
	current.At = ""
	return &next, current, nil
}

func sameClaudeNonLocalStartup(before, after ClaudeStartupSettings, local []ClaudeSettingsSource) bool {
	paths := map[string]bool{}
	for _, source := range local {
		paths[source.Path] = true
	}
	strip := func(report ClaudeStartupSettings) ClaudeStartupSettings {
		report.Files = slices.DeleteFunc(slices.Clone(report.Files), func(f ClaudeStartupFile) bool { return paths[f.Path] })
		report.PathBindings = slices.DeleteFunc(slices.Clone(report.PathBindings), func(f ClaudeStartupPathBinding) bool { return paths[f.Path] })
		report.Sources = slices.DeleteFunc(slices.Clone(report.Sources), func(f ClaudeStartupLayer) bool { return paths[f.Path] })
		report.BoundLocalFiles = nil
		return report
	}
	return sameClaudeStartupSettings(strip(before), strip(after))
}

func claudeInstalledLocalAdoption(previous, target Scope) bool {
	before, after := previous.ClaudeInstalledSettings, target.ClaudeInstalledSettings
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	if before.Version != after.Version || !EqualPluginSelections(before.Selections, after.Selections) || !slices.Equal(before.SourceFiles, after.SourceFiles) || before.Artifact.Original != after.Artifact.Original || before.Artifact.File.SHA256 != after.Artifact.File.SHA256 {
		return false
	}
	strip := func(files []ClaudeStartupFile) []ClaudeStartupFile {
		return slices.DeleteFunc(slices.Clone(files), func(f ClaudeStartupFile) bool {
			return slices.ContainsFunc(previous.ClaudeSettingsSources, func(s ClaudeSettingsSource) bool { return s.Path == f.Path })
		})
	}
	return reflect.DeepEqual(strip(before.StartupFiles), strip(after.StartupFiles))
}

func (c *Claude) holdInstalledSettings(ctx context.Context, p *claudeTransport, scope Scope, publish bool) (*claudeSettingsFileUse, error) {
	if scope.ClaudeInstalledSettings == nil {
		return nil, nil
	}
	if p.settingsFileConsumer != 1 || p.installedSettings != 1 {
		return nil, fmt.Errorf("当前 Claude bridge 不支持已安装插件执行设置协议，未启动候选")
	}
	if err := ValidateClaudeInstalledSettings(scope); err != nil {
		return nil, err
	}
	m, err := newClaudeSettingsFiles(c.stateDir)
	if err != nil {
		return nil, err
	}
	binding := scope.ClaudeInstalledSettings
	var use *claudeSettingsFileUse
	if publish {
		use, err = m.retain(ctx, binding.Artifact.ID, scope, ClaudeInstalledPluginSettings(binding))
	} else {
		use, err = m.acquire(ctx, binding.Artifact.ID, scope)
	}
	if err != nil {
		return nil, err
	}
	if use.record.Version != 2 || use.record.Artifact != binding.Artifact {
		use.file.Close()
		return nil, fmt.Errorf("执行设置文件与已保存版本不一致")
	}
	return use, nil
}

func equalClaudeInstalledSettings(a, b *ClaudeInstalledSettings) bool {
	return reflect.DeepEqual(a, b)
}

func claudeInstalledConfigMatches(scope Scope, s claudeSnapshot) bool {
	if !equalClaudeInstalledSettings(scope.ClaudeInstalledSettings, s.Config.InstalledSettings) {
		return false
	}
	if scope.ClaudeInstalledSettings == nil {
		return s.Config.SettingsArtifact == nil
	}
	return s.Config.SettingsArtifact != nil && *s.Config.SettingsArtifact == scope.ClaudeInstalledSettings.Artifact
}

func claudeInstalledChangeScopes(previous, target Scope) bool {
	if previous.Instance.Kind != "claude" || previous.Purpose != "" || target.ClaudeInstalledSettings == nil ||
		equalClaudeInstalledSettings(previous.ClaudeInstalledSettings, target.ClaudeInstalledSettings) {
		return false
	}
	previous.ClaudeInstalledSettings = target.ClaudeInstalledSettings
	previous.MCPSelections, previous.SkillSelections, previous.PluginSelections = target.MCPSelections, target.SkillSelections, target.PluginSelections
	return reflect.DeepEqual(previous, target)
}
