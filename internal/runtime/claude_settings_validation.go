package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

type claudeSettingsValidation struct {
	Version              int                              `json:"version"`
	Source               string                           `json:"source"`
	CLIVersion           string                           `json:"cli_version"`
	Cwd                  string                           `json:"cwd"`
	SettingsFile         string                           `json:"settings_file"`
	SchemaValid          bool                             `json:"schema_valid"`
	EffectiveSHA256      string                           `json:"effective_sha256"`
	BareMode             bool                             `json:"bare_mode"`
	NoSessionPersistence bool                             `json:"no_session_persistence"`
	ProcessStopped       bool                             `json:"process_stopped"`
	FileHolderObserved   bool                             `json:"file_holder_observed,omitempty"`
	PluginSettings       []ClaudePluginSettingObservation `json:"plugin_settings,omitempty"`
	Preservation         *ClaudeSettingsPreservation      `json:"preservation,omitempty"`
	Errors               []struct {
		File  string `json:"file"`
		Field string `json:"field"`
	} `json:"errors"`
	Sources []struct {
		Source string `json:"source"`
		SHA256 string `json:"sha256"`
	} `json:"sources"`
}

type ClaudePluginSettingObservation struct {
	SourceKey        string `json:"source_key"`
	RequestedEnabled bool   `json:"requested_enabled"`
	ActualEnabled    *bool  `json:"actual_enabled"`
	Matches          bool   `json:"matches"`
}

type ClaudeSettingsSignature struct {
	EffectiveSHA256   string `json:"effective_sha256"`
	SourcesSHA256     string `json:"sources_sha256"`
	AppliedSHA256     string `json:"applied_sha256"`
	PermissionsSHA256 string `json:"permissions_sha256"`
}

// Equal observations cover two bare processes, not all effective permissions
// of an executing Session or policy changes after those observations.
type ClaudeSettingsPreservation struct {
	Version                int                     `json:"version"`
	BaselineSettingsFile   string                  `json:"baseline_settings_file"`
	SourceKeys             []string                `json:"source_keys"`
	BaselineSchemaValid    bool                    `json:"baseline_schema_valid"`
	BaselineProcessStopped bool                    `json:"baseline_process_stopped"`
	Baseline               ClaudeSettingsSignature `json:"baseline"`
	Candidate              ClaudeSettingsSignature `json:"candidate"`
	Differences            []string                `json:"differences"`
	Matches                bool                    `json:"matches"`
}

// Native schema validation precedes creating the real query. It is not a
// complete permission decision or an installed-plugin exclusion receipt.
func validateClaudeCreateSettings(ctx context.Context, scope Scope) error {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	before, err := ReadClaudeStartupSettings(ctx, scope)
	if err != nil {
		return err
	}
	if err := claudeSettingsMetadataReadable(before); err != nil {
		return err
	}
	result, err := runClaudeSettingsValidation(ctx, scope, nil, nil, nil)
	if err != nil {
		return err
	}
	if !result.SchemaValid {
		issue := result.Errors[0]
		return fmt.Errorf("Claude 原生设置无效：%s（%s）；未启动执行查询", issue.File, issue.Field)
	}
	after, err := ReadClaudeStartupSettings(ctx, scope)
	if err != nil {
		return err
	}
	if !sameClaudeStartupSettings(before, after) {
		return fmt.Errorf("原生设置校验期间来源变化，请重新核对；未启动执行查询")
	}
	return nil
}

func claudeSettingsMetadataReadable(source ClaudeStartupSettings) error {
	if len(source.StartupConflicts) > 0 {
		return fmt.Errorf("Claude 设置来源存在启动绑定冲突（%s），无法按登记的路由和状态目录校验", source.StartupConflicts[0].Kind)
	}
	if !source.ReadComplete || source.PolicyHelperPresent || source.PolicyHelpersPresent {
		return fmt.Errorf("设置来源读取不完整或需要策略辅助程序，无法完成原生设置校验")
	}
	for _, layer := range source.Sources {
		if layer.HasPolicyHelper || layer.HasPolicyHelpers {
			return fmt.Errorf("设置来源声明了策略辅助程序，设置校验不会执行它")
		}
	}
	return nil
}

func sameClaudeStartupSettings(before, after ClaudeStartupSettings) bool {
	// Timestamps describe each read; only the source evidence must stay equal.
	before.At, after.At = "", ""
	return reflect.DeepEqual(before, after)
}

func runClaudeSettingsValidation(ctx context.Context, scope Scope, settings []ClaudePluginSetting, holder *os.File, baselineSettingsFile *string) (claudeSettingsValidation, error) {
	var result claudeSettingsValidation
	// Check the exact candidate consumed by the child, even when a previous
	// check covered its original source before a metadata read or composition.
	if err := ValidateClaudeExecutionSettings(ctx, scope); err != nil {
		return result, err
	}
	input := map[string]any{"executable": scope.Instance.Executable, "cwd": scope.Cwd,
		"settings_file": scope.Instance.SettingsFile, "model": scope.Route.Model}
	if len(settings) > 0 {
		if holder == nil {
			return result, fmt.Errorf("插件设置预览缺少派生文件使用锁")
		}
		input["plugin_settings"] = settings
	}
	if baselineSettingsFile != nil {
		if len(settings) == 0 {
			return result, fmt.Errorf("设置保留核对缺少目标插件键")
		}
		input["baseline_settings_file"] = *baselineSettingsFile
	}
	request, _ := json.Marshal(input)
	node := scope.Instance.NodeExecutable
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, filepath.Join(filepath.Dir(scope.Instance.Bridge), "settings_validation.js"), "--stdio")
	cmd.Env, cmd.Dir, cmd.Stdin = claudeEnv(scope.Instance), scope.Cwd, bytes.NewReader(request)
	if holder != nil {
		// fd 3 follows the holder through Node to the native metadata child.
		// A surviving consumer must keep the shared flock after a parent dies.
		cmd.ExtraFiles = []*os.File{holder}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	var output startupOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if cmd.Run() != nil {
		return result, fmt.Errorf("原生设置校验未完成；未启动执行查询")
	}
	hex := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Version != 1 || result.Source != "claude.bare.settings_validation" || result.CLIVersion != "2.1.280" ||
		result.Cwd != scope.Cwd || result.SettingsFile != scope.Instance.SettingsFile || !result.BareMode || !result.NoSessionPersistence || !result.ProcessStopped ||
		result.Errors == nil || len(result.Errors) > 64 || result.Sources == nil || len(result.Sources) > 32 || !hex.MatchString(result.EffectiveSHA256) || result.SchemaValid != (len(result.Errors) == 0) {
		return claudeSettingsValidation{}, fmt.Errorf("原生设置校验结果无效；未启动执行查询")
	}
	for _, source := range result.Sources {
		if source.Source == "" || len(source.Source) > 100 || strings.ContainsRune(source.Source, '\x00') || !hex.MatchString(source.SHA256) {
			return claudeSettingsValidation{}, fmt.Errorf("原生设置来源校验结果无效")
		}
	}
	for _, issue := range result.Errors {
		if issue.File == "" || len(issue.File) > 8192 || len(issue.Field) > 512 || strings.ContainsRune(issue.File+issue.Field, '\x00') {
			return claudeSettingsValidation{}, fmt.Errorf("原生设置诊断字段无效")
		}
	}
	if len(result.PluginSettings) != len(settings) || result.FileHolderObserved != (len(settings) > 0) {
		return claudeSettingsValidation{}, fmt.Errorf("插件设置预览的选择或文件使用依据缺失")
	}
	for i, setting := range settings {
		observed := result.PluginSettings[i]
		matches := observed.ActualEnabled != nil && *observed.ActualEnabled == setting.Enabled
		if observed.SourceKey != setting.SourceKey || observed.RequestedEnabled != setting.Enabled || observed.Matches != matches {
			return claudeSettingsValidation{}, fmt.Errorf("插件设置预览与目标来源键不一致")
		}
	}
	if (baselineSettingsFile == nil) != (result.Preservation == nil) {
		return claudeSettingsValidation{}, fmt.Errorf("原文件与派生文件的原生设置保留依据缺失或与请求不符")
	}
	if observed := result.Preservation; observed != nil {
		keys := make([]string, 0, len(settings))
		for _, setting := range settings {
			keys = append(keys, setting.SourceKey)
		}
		if observed.Version != 1 || observed.BaselineSettingsFile != *baselineSettingsFile || !slices.Equal(observed.SourceKeys, keys) || !observed.BaselineProcessStopped || observed.Differences == nil {
			return claudeSettingsValidation{}, fmt.Errorf("原生设置保留核对的基线、目标键或进程状态无效")
		}
		differences := []string{}
		for _, field := range []struct{ before, after, name string }{
			{observed.Baseline.EffectiveSHA256, observed.Candidate.EffectiveSHA256, "effective_settings"},
			{observed.Baseline.SourcesSHA256, observed.Candidate.SourcesSHA256, "source_settings"},
			{observed.Baseline.AppliedSHA256, observed.Candidate.AppliedSHA256, "applied_settings"},
			{observed.Baseline.PermissionsSHA256, observed.Candidate.PermissionsSHA256, "permission_rules"},
		} {
			if !hex.MatchString(field.before) || !hex.MatchString(field.after) {
				return claudeSettingsValidation{}, fmt.Errorf("原生设置保留核对的摘要无效")
			}
			if field.before != field.after {
				differences = append(differences, field.name)
			}
		}
		if !observed.BaselineSchemaValid || !result.SchemaValid {
			differences = append(differences, "schema")
		}
		if !slices.Equal(observed.Differences, differences) || observed.Matches != (len(differences) == 0) {
			return claudeSettingsValidation{}, fmt.Errorf("原生设置保留核对的差异与摘要不一致")
		}
	}
	return result, nil
}
