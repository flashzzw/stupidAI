package runtime

import (
	"context"
	"slices"
	"strings"
	"time"
)

// A preview compares original and derived settings in separate bare processes.
// It cannot authorize an installed plugin or replace an execution snapshot.
type ClaudePluginSettingsPreview struct {
	Source               string                       `json:"source"`
	At                   string                       `json:"at"`
	NativeID             string                       `json:"native_id"`
	SnapshotID           string                       `json:"snapshot_id,omitempty"`
	Cwd                  string                       `json:"cwd"`
	Profile              string                       `json:"profile"`
	Setting              ClaudePluginSetting          `json:"setting"`
	Artifact             *ClaudeDerivedSettingsFile   `json:"artifact,omitempty"`
	Validation           *claudeSettingsValidation    `json:"validation,omitempty"`
	SettingsFiles        []ClaudeStartupFile          `json:"settings_files"`
	StartupSourceVersion string                       `json:"startup_source_version,omitempty"`
	SourcesStable        bool                         `json:"sources_stable"`
	Complete             bool                         `json:"complete"`
	Problems             []string                     `json:"problems"`
	Cleanup              ClaudeSettingsPreviewCleanup `json:"cleanup"`
}

type ClaudeSettingsPreviewCleanup struct {
	State     string   `json:"state"`
	Recovered int      `json:"recovered"`
	Issues    []string `json:"issues"`
}

func PreviewClaudePluginSettings(ctx context.Context, stateDir string, scope Scope, setting ClaudePluginSetting) (out ClaudePluginSettingsPreview) {
	out = ClaudePluginSettingsPreview{Source: "claude.bare.plugin_settings_preview", At: time.Now().UTC().Format(time.RFC3339Nano),
		NativeID: scope.NativeID, Cwd: scope.Cwd, Profile: scope.Instance.Profile, Setting: setting,
		SettingsFiles: []ClaudeStartupFile{}, Problems: []string{}, Cleanup: ClaudeSettingsPreviewCleanup{State: "not_created", Issues: []string{}}}
	problem := func(err error) { out.Problems = append(out.Problems, err.Error()) }
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	m, err := newClaudeSettingsFiles(stateDir)
	if err != nil {
		problem(err)
		return
	}
	recoverFiles := func() {
		// Request cancellation cannot skip owned-file cleanup. A surviving
		// consumer keeps its flock, so recovery leaves that file in place.
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		count, err := m.recover(cleanup)
		out.Cleanup.Recovered += count
		if err != nil {
			out.Cleanup.Issues = append(out.Cleanup.Issues, err.Error())
		}
	}
	recoverFiles()
	// Bare mode can still honor an apiKeyHelper supplied through --settings.
	// Metadata inspection must not launch with a different account or home.
	if err := ValidateClaudeExecutionSettings(ctx, scope); err != nil {
		problem(err)
		return
	}
	if err := verifyClaudeInstalledStartup(ctx, scope); err != nil {
		problem(err)
		return
	}
	before, err := ReadClaudeStartupSettings(ctx, scope)
	if err != nil {
		problem(err)
		return
	}
	if err = claudeSettingsMetadataReadable(before); err != nil {
		problem(err)
		return
	}
	out.SettingsFiles = before.Files
	out.StartupSourceVersion = before.Fingerprint()
	settings := ClaudeInstalledPluginSettings(scope.ClaudeInstalledSettings)
	found := false
	for i := range settings {
		if settings[i].SourceKey == setting.SourceKey {
			settings[i], found = setting, true
		}
	}
	if !found {
		settings = append(settings, setting)
	}
	slices.SortFunc(settings, func(a, b ClaudePluginSetting) int { return strings.Compare(a.SourceKey, b.SourceKey) })
	baseline := scope.Instance.SettingsFile
	if scope.ClaudeInstalledSettings != nil {
		previous, err := m.acquire(ctx, scope.ClaudeInstalledSettings.Artifact.ID, scope)
		if err != nil {
			problem(err)
			return
		}
		defer previous.file.Close()
		baseline = previous.record.Artifact.File.Path
	}
	use, err := m.create(ctx, scope, settings)
	if err != nil {
		out.Cleanup.State = "not_acquired"
		problem(err)
		// Failed publication may already have an indexed inode/name.
		recoverFiles()
		return
	}
	out.Artifact = &use.record.Artifact
	out.Cleanup.State = "retained"
	defer func() {
		use.file.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if err := m.remove(cleanup, use.record.Artifact.ID); err != nil {
			out.Cleanup.Issues = append(out.Cleanup.Issues, err.Error())
		} else {
			out.Cleanup.State = "released"
		}
	}()
	candidate := scope
	candidate.Instance.SettingsFile = use.record.Artifact.File.Path
	validation, err := runClaudeSettingsValidation(ctx, candidate, settings, use.file, &baseline)
	if err != nil {
		problem(err)
		return
	}
	out.Validation = &validation
	if !validation.Preservation.Matches {
		out.Problems = append(out.Problems, "原文件与派生文件的其他有效设置、来源或权限规则未通过保留核对")
	}
	// Independently reopen the candidate and recheck its source before
	// returning any successful observation. The original scope is unchanged.
	checked, err := m.acquire(ctx, use.record.Artifact.ID, scope)
	if err != nil {
		problem(err)
		return
	}
	checked.file.Close()
	after, err := ReadClaudeStartupSettings(ctx, scope)
	if err != nil {
		problem(err)
		return
	}
	out.SourcesStable = sameClaudeStartupSettings(before, after)
	if !out.SourcesStable {
		out.Problems = append(out.Problems, "预览期间设置来源变化，请重新读取；本次观察不能用作来源核对结果")
	}
	return
}
