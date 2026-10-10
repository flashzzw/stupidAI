package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codex-netcap/internal/runtime"
)

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

type Config struct {
	Version          int                `json:"version"`
	StateDir         string             `json:"state_dir"`
	Listen           string             `json:"listen"`
	Instances        []runtime.Instance `json:"instances"`
	Routes           []runtime.Route    `json:"routes"`
	Projects         []Project          `json:"projects"`
	Observers        []ObserverConfig   `json:"observers,omitempty"`
	Automation       AutomationConfig   `json:"automation,omitempty"`
	CapabilityAssets []CapabilityAsset  `json:"capability_assets,omitempty"`
}

type AutomationConfig struct {
	GlobalLimit         int `json:"global_limit,omitempty"`
	RouteLimit          int `json:"route_limit,omitempty"`
	ProbeTTLSeconds     int `json:"probe_ttl_seconds,omitempty"`
	ProbeTimeoutSeconds int `json:"probe_timeout_seconds,omitempty"`
}

type ObserverConfig struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instance_id"`
	ProjectID          string `json:"project_id"`
	Format             string `json:"format"`
	TokenRef           string `json:"token_ref"`
	NotificationSender string `json:"notification_sender,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	if path == "" {
		return c, fmt.Errorf("serve 需要独立的 --config；不会读取旧 run 配置")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("配置必须只包含一个 JSON 对象")
	}
	return normalizeConfig(c, filepath.Dir(path))
}

func normalizeConfig(c Config, directory string) (Config, error) {
	if c.Version != 1 {
		return c, fmt.Errorf("console 配置 version 必须为 1")
	}
	if c.Automation.GlobalLimit < 0 || c.Automation.GlobalLimit > 64 || c.Automation.RouteLimit < 0 || c.Automation.RouteLimit > 64 || c.Automation.ProbeTTLSeconds < 0 || c.Automation.ProbeTTLSeconds > 86400 || c.Automation.ProbeTimeoutSeconds < 0 || c.Automation.ProbeTimeoutSeconds > 3600 {
		return c, fmt.Errorf("自动执行容量或探测 TTL 无效")
	}
	base, err := filepath.Abs(directory)
	if err != nil {
		return c, err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(base, p)
	}
	if c.StateDir == "" {
		return c, fmt.Errorf("需要独立 state_dir")
	}
	assets := map[string]bool{}
	if len(c.CapabilityAssets) > 200 {
		return c, fmt.Errorf("能力资产登记最多 200 项")
	}
	for n := range c.CapabilityAssets {
		asset := &c.CapabilityAssets[n]
		if asset.Management == "managed" {
			return c, fmt.Errorf("平台管理版本必须通过能力库导入，不能仅在启动配置中声明 managed")
		}
		if err := validateAsset(*asset); err != nil {
			return c, err
		}
		if assets[asset.ID] {
			return c, fmt.Errorf("能力资产 ID 重复")
		}
		assets[asset.ID] = true
		asset.Source = resolve(asset.Source)
	}
	c.StateDir = resolve(c.StateDir)
	if c.Listen == "" {
		c.Listen = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || host != "127.0.0.1" {
		return c, fmt.Errorf("console 仅支持绑定 127.0.0.1:PORT")
	}
	instances := map[string]bool{}
	for n := range c.Instances {
		i := &c.Instances[n]
		if !validID(i.ID) || instances[i.ID] {
			return c, fmt.Errorf("实例 ID 无效或重复")
		}
		instances[i.ID] = true
		if i.Kind != "codex" && i.Kind != "claude" && i.Kind != "opencode" {
			return c, fmt.Errorf("当前支持 codex/claude/opencode 实例")
		}
		if i.NotificationSender != "" && i.NotificationSender != "legacy" && i.NotificationSender != "console" {
			return c, fmt.Errorf("实例通知发送方无效")
		}
		if i.Executable == "" || i.Profile == "" || i.PolicyRevision == "" {
			return c, fmt.Errorf("实例 %s 缺少 executable/profile/policy_revision", i.ID)
		}
		i.Executable, i.Profile = resolve(i.Executable), resolve(i.Profile)
		if len(i.Name) > 200 {
			return c, fmt.Errorf("实例名称过长")
		}
		if i.SettingsFile != "" {
			if i.Kind != "claude" {
				return c, fmt.Errorf("settings_file 仅适用于 Claude")
			}
			i.SettingsFile = resolve(i.SettingsFile)
		}
		if i.ServerExecutable != "" {
			i.ServerExecutable = resolve(i.ServerExecutable)
		}
		if i.Kind == "codex" {
			if i.Socket == "" {
				return c, fmt.Errorf("Codex 实例缺少 socket")
			}
			i.Socket = resolve(i.Socket)
		} else if i.Kind == "claude" {
			if i.Bridge == "" {
				return c, fmt.Errorf("Claude 实例缺少 bridge")
			}
			i.Bridge = resolve(i.Bridge)
			if i.NodeExecutable != "" {
				i.NodeExecutable = resolve(i.NodeExecutable)
			}
		}
	}
	routes := map[string]bool{}
	for n := range c.Routes {
		r := &c.Routes[n]
		if !validID(r.ID) || routes[r.ID] || !instances[r.InstanceID] {
			return c, fmt.Errorf("路由 ID 或实例引用无效")
		}
		routes[r.ID] = true
		u, e := url.Parse(r.Endpoint)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("路由端点无效或包含凭据/查询参数")
		}
		if r.Provider == "" || r.Model == "" || r.ConfigRevision == "" || r.CredentialRef == "" || r.CredentialRevision == "" {
			return c, fmt.Errorf("路由 %s 缺少模型、供应商或配置/凭据引用", r.ID)
		}
	}
	projects := map[string]bool{}
	for n := range c.Projects {
		p := &c.Projects[n]
		if !validID(p.ID) || projects[p.ID] || p.Cwd == "" {
			return c, fmt.Errorf("项目 ID 或 cwd 无效")
		}
		projects[p.ID] = true
		p.Cwd, err = filepath.EvalSymlinks(resolve(p.Cwd))
		if err != nil {
			return c, err
		}
		info, e := os.Stat(p.Cwd)
		if e != nil || !info.IsDir() {
			return c, fmt.Errorf("项目目录不存在")
		}
	}
	observers := map[string]bool{}
	for _, observer := range c.Observers {
		if !validID(observer.ID) || observers[observer.ID] || !instances[observer.InstanceID] || !projects[observer.ProjectID] {
			return c, fmt.Errorf("观察来源 ID/实例/项目引用无效")
		}
		observers[observer.ID] = true
		if observer.Format != "hook" && observer.Format != "opencode" && observer.Format != "otel" {
			return c, fmt.Errorf("观察来源格式无效")
		}
		if !strings.HasPrefix(observer.TokenRef, "env:") || len(observer.TokenRef) <= 4 {
			return c, fmt.Errorf("观察来源需要独立的 env: 令牌引用")
		}
		if observer.NotificationSender != "" && observer.NotificationSender != "legacy" && observer.NotificationSender != "console" {
			return c, fmt.Errorf("观察来源通知发送方无效")
		}
		for _, instance := range c.Instances {
			if instance.ID != observer.InstanceID {
				continue
			}
			if (observer.Format == "opencode" && instance.Kind != "opencode") || (observer.Format == "otel" && instance.Kind != "codex") || (observer.Format == "hook" && instance.Kind == "opencode") {
				return c, fmt.Errorf("观察来源格式与实例不匹配")
			}
		}
	}
	return c, nil
}

func validID(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-", r)) {
			return false
		}
	}
	return true
}

func profileFingerprint(instance runtime.Instance) (string, error) {
	if err := runtime.ValidateClaudeSettings(instance); err != nil {
		return "", err
	}
	return profileFileFingerprint(instance)
}

func nativeReadFingerprint(instance runtime.Instance) (string, error) {
	if err := runtime.ValidateClaudeNativeReadSettings(instance); err != nil {
		return "", err
	}
	return profileFileFingerprint(instance)
}

// Keep byte fingerprints compatible; read and execution admission are separate.
func profileFileFingerprint(instance runtime.Instance) (string, error) {
	name := "config.toml"
	if instance.Kind == "claude" {
		name = "settings.json"
	}
	if instance.Kind == "opencode" {
		name = "opencode.json"
	}
	b, err := os.ReadFile(filepath.Join(instance.Profile, name))
	if instance.Kind == "claude" && os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("默认原生配置文件无法读取：%s", filepath.Join(instance.Profile, name))
	}
	if instance.Kind == "opencode" {
		var cfg map[string]any
		if err = json.Unmarshal(b, &cfg); err != nil {
			return "", err
		}
		delete(cfg, "$schema")
		b, err = json.Marshal(cfg)
		if err != nil {
			return "", err
		}
	}
	if instance.SettingsFile != "" {
		settings, e := os.ReadFile(instance.SettingsFile)
		if e != nil {
			return "", fmt.Errorf("自定义 settings_file 无法读取")
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(settings, &value) != nil || value == nil {
			return "", fmt.Errorf("自定义 settings_file 必须是 JSON 对象")
		}
		// Preserve the legacy fingerprint exactly when no overlay is selected.
		b, _ = json.Marshal([][]byte{b, settings})
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
