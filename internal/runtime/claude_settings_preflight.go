package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// This reports source reads, not a complete effective configuration or a
// permission to launch installed plugins. In particular, policy helpers and
// fresh remote policy are outside the metadata-only resolver's contract.
type ClaudeStartupSettings struct {
	SDKVersion           string                     `json:"sdk_version"`
	Version              int                        `json:"version"`
	Source               string                     `json:"source"`
	Cwd                  string                     `json:"cwd"`
	At                   string                     `json:"at"`
	NativeID             string                     `json:"native_id,omitempty"`
	SnapshotID           string                     `json:"snapshot_id,omitempty"`
	ResolverReturned     bool                       `json:"resolver_returned"`
	ReadComplete         bool                       `json:"read_complete"`
	Complete             bool                       `json:"complete"`
	Files                []ClaudeStartupFile        `json:"files"`
	PathBindings         []ClaudeStartupPathBinding `json:"path_bindings,omitempty"`
	Sources              []ClaudeStartupLayer       `json:"sources"`
	Registry             []ClaudeStartupRegistry    `json:"registry"`
	Issues               []ClaudeStartupIssue       `json:"issues"`
	PolicyHelperPresent  bool                       `json:"policy_helper_present"`
	PolicyHelpersPresent bool                       `json:"policy_helpers_present"`
	StartupConflicts     []ClaudeStartupConflict    `json:"startup_conflicts"`
	Limitations          []string                   `json:"limitations"`
	BoundLocalFiles      []ClaudeSettingsSource     `json:"bound_local_files"`
}

// Fingerprint binds the complete observed source report, including registry
// outcomes and path identities. A sampling timestamp is not a source version.
func (s ClaudeStartupSettings) Fingerprint() string {
	s.At = ""
	b, _ := json.Marshal(s)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

type ClaudeStartupConflict struct {
	Source       string `json:"source"`
	Path         string `json:"path,omitempty"`
	PolicyOrigin string `json:"policy_origin,omitempty"`
	Kind         string `json:"kind"`
}
type ClaudeStartupFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	SHA256         string `json:"sha256,omitempty"`
	Bytes          *int64 `json:"bytes,omitempty"`
	ResolvedPath   string `json:"resolved_path,omitempty"`
	IdentitySHA256 string `json:"identity_sha256,omitempty"`
}
type ClaudeStartupPathBinding struct {
	Path           string `json:"path"`
	ResolvedPath   string `json:"resolved_path"`
	IdentitySHA256 string `json:"identity_sha256"`
}
type ClaudeStartupLayer struct {
	Source           string `json:"source"`
	Path             string `json:"path,omitempty"`
	PolicyOrigin     string `json:"policy_origin,omitempty"`
	SettingsSHA256   string `json:"settings_sha256"`
	HasPolicyHelper  bool   `json:"has_policy_helper"`
	HasPolicyHelpers bool   `json:"has_policy_helpers"`
}
type ClaudeStartupRegistry struct {
	Hive    string `json:"hive"`
	Outcome string `json:"outcome"`
	Code    string `json:"code,omitempty"`
}
type ClaudeStartupIssue struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Code      string `json:"code"`
}
type startupOutput struct{ bytes.Buffer }

func (b *startupOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("settings source result exceeded limit")
	}
	return b.Buffer.Write(p)
}

func ReadClaudeStartupSettings(ctx context.Context, scope Scope) (ClaudeStartupSettings, error) {
	var out ClaudeStartupSettings
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || !filepath.IsAbs(scope.Cwd) || !filepath.IsAbs(scope.Instance.Bridge) || !filepath.IsAbs(scope.Instance.Profile) {
		return out, fmt.Errorf("此执行尚无 Claude 设置来源读取入口")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	before, err := ReadClaudeLocalSettingsSources(ctx, scope)
	if err != nil {
		return out, err
	}
	paths := []string{filepath.Join(scope.Instance.Profile, "settings.json")}
	if scope.Instance.SettingsFile != "" {
		paths = append(paths, scope.Instance.SettingsFile)
	}
	for _, s := range before {
		paths = append(paths, s.Path)
	}
	body, _ := json.Marshal(map[string]any{"cwd": scope.Cwd, "additional_files": paths})
	node := scope.Instance.NodeExecutable
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, filepath.Join(filepath.Dir(scope.Instance.Bridge), "settings_preflight.js"), "--stdio")
	cmd.Env, cmd.Dir, cmd.Stdin = claudeEnv(scope.Instance), scope.Cwd, bytes.NewReader(body)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	var output startupOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("独立设置来源读取未完成；未启动或恢复 Claude 查询")
	}
	if json.Unmarshal(output.Bytes(), &out) != nil || out.Version != 1 || out.SDKVersion != "0.3.280" || out.Source != "claude.sdk.observed_startup_reads" || out.Cwd != scope.Cwd || out.Complete || out.Files == nil || out.Sources == nil || out.Registry == nil || out.Issues == nil || out.Limitations == nil || len(out.Files) > 512 || len(out.PathBindings) > 512 || len(out.Sources) > 32 || len(out.Registry) > 8 || len(out.Issues) > 128 || len(out.Limitations) > 16 {
		return ClaudeStartupSettings{}, fmt.Errorf("设置来源读取结果无效，不能作为启动依据")
	}
	if out.StartupConflicts == nil || len(out.StartupConflicts) > 4096 {
		return ClaudeStartupSettings{}, fmt.Errorf("设置来源读取缺少有效的启动绑定检查，请更新 Claude bridge 后重新读取")
	}
	for _, conflict := range out.StartupConflicts {
		validSource := false
		switch conflict.Source {
		case "file":
			validSource = filepath.IsAbs(conflict.Path)
		case "registry":
			validSource = conflict.PolicyOrigin == "hklm" || conflict.PolicyOrigin == "hkcu"
		case "effective", "user", "project", "local", "managed":
			validSource = true
		}
		validKind := conflict.Kind == "api_key_helper" || conflict.Kind == "route_environment" || conflict.Kind == "state_environment" || conflict.Kind == "invalid_environment"
		if !validSource || !validKind || len(conflict.Path) > 16384 || len(conflict.PolicyOrigin) > 100 || strings.ContainsRune(conflict.Path+conflict.PolicyOrigin, '\x00') || conflict.Path != "" && !filepath.IsAbs(conflict.Path) {
			return ClaudeStartupSettings{}, fmt.Errorf("设置来源的启动绑定冲突元数据无效")
		}
	}
	// Older workers omit the platform declaration. Missing or null evidence
	// must not be decoded as an observed absence before a native launch.
	var policyEvidence struct {
		Present *bool `json:"policy_helpers_present"`
		Sources []struct {
			Present *bool `json:"has_policy_helpers"`
		} `json:"sources"`
	}
	if json.Unmarshal(output.Bytes(), &policyEvidence) != nil || policyEvidence.Present == nil {
		return ClaudeStartupSettings{}, fmt.Errorf("设置来源读取缺少策略辅助配置检查，请更新 Claude bridge 后重新读取")
	}
	for _, layer := range policyEvidence.Sources {
		if layer.Present == nil {
			return ClaudeStartupSettings{}, fmt.Errorf("设置来源层缺少策略辅助配置检查，不能作为启动依据")
		}
	}
	valid := func(s string, max int) bool { return len(s) <= max && !strings.ContainsRune(s, '\x00') }
	hex := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if out.ReadComplete && (!out.ResolverReturned || len(out.Issues) != 0) {
		return ClaudeStartupSettings{}, fmt.Errorf("设置读取状态与诊断不一致")
	}
	if _, err := time.Parse(time.RFC3339Nano, out.At); err != nil {
		return ClaudeStartupSettings{}, fmt.Errorf("设置来源读取缺少有效时间")
	}
	for _, f := range out.Files {
		badResolution := f.ResolvedPath != "" && (!filepath.IsAbs(f.ResolvedPath) || !valid(f.ResolvedPath, 16384))
		badIdentity := f.IdentitySHA256 != "" && !hex.MatchString(f.IdentitySHA256)
		missingReadEvidence := f.Status == "read" && (f.SHA256 == "" || f.ResolvedPath == "" || f.IdentitySHA256 == "" || f.Bytes == nil)
		if !filepath.IsAbs(f.Path) || !valid(f.Path, 16384) || !valid(f.Status, 80) || f.SHA256 != "" && !hex.MatchString(f.SHA256) || badResolution || badIdentity || missingReadEvidence || f.Bytes != nil && (*f.Bytes < 0 || *f.Bytes > 1<<20) || out.ReadComplete && f.Status != "read" && f.Status != "absent" {
			return ClaudeStartupSettings{}, fmt.Errorf("设置文件读取元数据无效")
		}
	}
	for _, binding := range out.PathBindings {
		if !filepath.IsAbs(binding.Path) || !valid(binding.Path, 16384) || !filepath.IsAbs(binding.ResolvedPath) || !valid(binding.ResolvedPath, 16384) || !hex.MatchString(binding.IdentitySHA256) {
			return ClaudeStartupSettings{}, fmt.Errorf("设置路径解析元数据无效")
		}
	}
	for _, s := range out.Sources {
		if !valid(s.Source, 100) || !valid(s.Path, 16384) || !valid(s.PolicyOrigin, 100) || !hex.MatchString(s.SettingsSHA256) {
			return ClaudeStartupSettings{}, fmt.Errorf("设置来源层元数据无效")
		}
	}
	for _, r := range out.Registry {
		if !valid(r.Hive, 40) || !valid(r.Outcome, 80) || !valid(r.Code, 100) || out.ReadComplete && r.Outcome != "present" && r.Outcome != "absent" {
			return ClaudeStartupSettings{}, fmt.Errorf("注册表读取元数据无效")
		}
	}
	for _, i := range out.Issues {
		if !valid(i.Path, 16384) || !valid(i.Operation, 80) || !valid(i.Code, 100) {
			return ClaudeStartupSettings{}, fmt.Errorf("设置读取诊断元数据无效")
		}
	}
	for _, l := range out.Limitations {
		if !valid(l, 200) {
			return ClaudeStartupSettings{}, fmt.Errorf("设置读取覆盖元数据无效")
		}
	}
	after, err := ReadClaudeLocalSettingsSources(ctx, scope)
	if err != nil {
		return ClaudeStartupSettings{}, err
	}
	if !reflect.DeepEqual(before, after) {
		return ClaudeStartupSettings{}, fmt.Errorf("读取期间 Git/cwd 设置来源或文件版本变化，请重新读取")
	}
	out.BoundLocalFiles = after
	return out, nil
}
