package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

// This is current disk provenance, not a loaded thread's configuration or an
// input grant. Config bodies, arbitrary keys and policy text stay private.
type ConfigurationCatalog struct {
	ConfigurationData
	SettingsDifference  *ConfigurationSettingsDifference `json:"settings_difference,omitempty"`
	NativeID            string                           `json:"native_id"`
	Cwd                 string                           `json:"cwd"`
	Source              string                           `json:"source"`
	Scope               string                           `json:"scope"`
	At                  string                           `json:"at"`
	Complete            bool                             `json:"complete"`
	RequirementsSHA256  string                           `json:"requirements_sha256"`
	RequirementsPresent bool                             `json:"requirements_present"`
}

// Disk source versions can be bound independently of the native requirements
// policy, whose tightening/retirement rules are handled by permission flows.
type ConfigurationData struct {
	Settings            *ConfigurationSettings `json:"settings,omitempty"`
	ConfigurationSHA256 string                 `json:"configuration_sha256"`
	OriginsSHA256       string                 `json:"origins_sha256"`
	LayersSHA256        string                 `json:"layers_sha256"`
	Layers              []ConfigurationLayer   `json:"layers"`
	Origins             []ConfigurationOrigin  `json:"origins"`
	OtherOriginCount    int                    `json:"other_origin_count"`
}

type ConfigurationLayer struct {
	SHA256              string      `json:"sha256"`
	ConfigurationSHA256 string      `json:"configuration_sha256"`
	ID                  string      `json:"id"`
	Kind                string      `json:"kind"`
	Version             string      `json:"version"`
	Disabled            bool        `json:"disabled"`
	File                *FileSource `json:"file,omitempty"`
}

type ConfigurationOrigin struct {
	Field   string `json:"field"`
	LayerID string `json:"layer_id"`
}

type ConfigurationCatalogReader interface {
	ReadConfigurationCatalog(context.Context, Scope) (ConfigurationCatalog, error)
}

func (a *Adapters) ReadConfigurationCatalog(ctx context.Context, scope Scope) (ConfigurationCatalog, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return ConfigurationCatalog{}, err
	}
	reader, ok := r.(ConfigurationCatalogReader)
	if !ok {
		return ConfigurationCatalog{}, fmt.Errorf("此运行时尚无配置来源读取入口")
	}
	return reader.ReadConfigurationCatalog(ctx, scope)
}

func configurationDigest(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func configurationObject(raw json.RawMessage) (map[string]any, error) {
	var value map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if len(raw) == 0 || len(raw) > 4<<20 || d.Decode(&value) != nil || value == nil {
		return nil, fmt.Errorf("原生配置来源响应缺失或格式无效")
	}
	return value, nil
}

type nativeConfigurationMetadata struct {
	Name    json.RawMessage `json:"name"`
	Version string          `json:"version"`
}

func configurationLayerIdentity(raw json.RawMessage) (string, string, string, error) {
	name, err := configurationObject(raw)
	if err != nil {
		return "", "", "", err
	}
	kind, _ := name["type"].(string)
	if !slices.Contains([]string{"packagedDefaults", "system", "user", "project", "mdm", "enterpriseManaged", "sessionFlags", "legacyManagedConfigTomlFromFile", "legacyManagedConfigTomlFromMdm"}, kind) {
		return "", "", "", fmt.Errorf("原生配置来源类别尚不支持")
	}
	path := ""
	switch kind {
	case "project":
		dir, _ := name["dotCodexFolder"].(string)
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Base(dir) != ".codex" {
			return "", "", "", fmt.Errorf("项目配置来源路径无效")
		}
		path = filepath.Join(dir, "config.toml")
	case "packagedDefaults", "system", "user", "legacyManagedConfigTomlFromFile":
		path, _ = name["file"].(string)
		if path == "" {
			return "", "", "", fmt.Errorf("原生配置来源缺少文件路径")
		}
	case "mdm":
		for _, key := range []string{"domain", "key"} {
			if value, _ := name[key].(string); value == "" {
				return "", "", "", fmt.Errorf("受管来源身份缺失")
			}
		}
	case "enterpriseManaged":
		for _, key := range []string{"id", "name"} {
			if value, _ := name[key].(string); value == "" {
				return "", "", "", fmt.Errorf("受管来源身份缺失")
			}
		}
	}
	if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 16384 || strings.ContainsRune(path, '\x00')) {
		return "", "", "", fmt.Errorf("原生配置来源路径无效")
	}
	return configurationDigest(name), kind, path, nil
}

func readConfigurationCatalog(ctx context.Context, p codexConnection, scope Scope) (ConfigurationCatalog, error) {
	out := ConfigurationCatalog{NativeID: scope.NativeID, Cwd: scope.Cwd, Source: "codex.config/read+configRequirements/read", Scope: "disk_configuration", ConfigurationData: ConfigurationData{Layers: []ConfigurationLayer{}, Origins: []ConfigurationOrigin{}}}
	var raw json.RawMessage
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": true}, &raw); err != nil {
		return out, fmt.Errorf("原生配置来源读取失败；未返回原始诊断正文")
	}
	if len(raw) == 0 || len(raw) > 4<<20 {
		return out, fmt.Errorf("原生配置来源响应缺失或超过上限")
	}
	var response struct {
		Config  json.RawMessage                        `json:"config"`
		Origins map[string]nativeConfigurationMetadata `json:"origins"`
		Layers  *[]struct {
			nativeConfigurationMetadata
			Config   json.RawMessage `json:"config"`
			Disabled json.RawMessage `json:"disabledReason"`
		} `json:"layers"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Origins == nil || response.Layers == nil || len(*response.Layers) > 128 || len(response.Origins) > 4096 {
		return out, fmt.Errorf("原生配置层或字段来源缺失，不能返回部分结果")
	}
	config, err := configurationObject(response.Config)
	if err != nil {
		return out, err
	}
	out.ConfigurationSHA256 = configurationDigest(config)
	origins, err := configurationObject(raw)
	if err != nil {
		return out, err
	}
	out.OriginsSHA256 = configurationDigest(origins["origins"])
	out.LayersSHA256 = configurationDigest(origins["layers"])
	byID := map[string]ConfigurationLayer{}
	userCount := 0
	for index, native := range *response.Layers {
		id, kind, path, err := configurationLayerIdentity(native.Name)
		if err != nil {
			return out, err
		}
		if _, exists := byID[id]; exists || !strings.HasPrefix(native.Version, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(native.Version, "sha256:")) {
			return out, fmt.Errorf("原生配置层身份重复或版本无效")
		}
		layerConfig, err := configurationObject(native.Config)
		if err != nil {
			return out, err
		}
		var disabled *string
		// The fixed native server omits disabledReason on enabled layers even
		// though its generated type declares a nullable field.
		if len(native.Disabled) != 0 && json.Unmarshal(native.Disabled, &disabled) != nil {
			return out, fmt.Errorf("原生配置层缺少可核对的启用状态")
		}
		layer := ConfigurationLayer{ID: id, Kind: kind, Version: native.Version, Disabled: disabled != nil && *disabled != ""}
		layer.SHA256 = configurationDigest(origins["layers"].([]any)[index])
		layer.ConfigurationSHA256 = configurationDigest(layerConfig)
		if path != "" {
			file, _, err := readSourceFile(path, true, "原生配置来源")
			if err != nil {
				return out, err
			}
			layer.File = &file
		}
		if kind == "user" {
			expected, err := absentSourceFilePath(filepath.Join(scope.Instance.Profile, "config.toml"), "用户配置")
			if actual, e := filepath.EvalSymlinks(filepath.Join(scope.Instance.Profile, "config.toml")); e == nil {
				expected, err = actual, nil
			}
			if err != nil || layer.File == nil || layer.File.ResolvedPath != expected {
				return out, fmt.Errorf("原生用户配置来源与已绑定 profile 不符")
			}
			userCount++
		}
		byID[id] = layer
		out.Layers = append(out.Layers, layer)
	}
	if userCount == 0 {
		return out, fmt.Errorf("原生配置来源缺少已绑定用户层")
	}
	// Show fixed, public configuration keys only. Arbitrary provider names,
	// plugin IDs and policy contents remain represented by complete hashes.
	fields := []string{"model", "model_provider", "model_reasoning_effort", "approval_policy", "approvals_reviewer", "sandbox_mode", "service_tier", "personality"}
	for key, origin := range response.Origins {
		id, _, _, err := configurationLayerIdentity(origin.Name)
		layer, exists := byID[id]
		if err != nil || !exists || layer.Disabled || layer.Version != origin.Version {
			return out, fmt.Errorf("原生字段来源不能关联到有效配置层")
		}
		if slices.Contains(fields, key) {
			out.Origins = append(out.Origins, ConfigurationOrigin{Field: key, LayerID: id})
		} else {
			out.OtherOriginCount++
		}
	}
	slices.SortFunc(out.Origins, func(a, b ConfigurationOrigin) int { return strings.Compare(a.Field, b.Field) })
	out.Settings = configurationSettings(config, origins)
	requirements, err := readCodexHookRequirements(ctx, p)
	if err != nil {
		return out, fmt.Errorf("原生受管要求无法完整读取；本次配置来源核对未完成")
	}
	out.RequirementsSHA256, out.RequirementsPresent = configurationDigest(requirements), requirements != nil
	return out, nil
}

func (c *Codex) ReadConfigurationCatalog(ctx context.Context, scope Scope) (ConfigurationCatalog, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || scope.NativeID == "" || !filepath.IsAbs(scope.Cwd) || strings.ContainsRune(scope.Cwd, '\x00') {
		return ConfigurationCatalog{}, fmt.Errorf("配置来源读取需要普通 Codex 会话及已绑定目录")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return ConfigurationCatalog{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return ConfigurationCatalog{}, fmt.Errorf("无法连接已绑定 Codex 后台以读取配置来源")
	}
	first, err := readConfigurationCatalog(ctx, p, scope)
	if err != nil {
		return ConfigurationCatalog{}, err
	}
	second, err := readConfigurationCatalog(ctx, p, scope)
	if err != nil {
		return ConfigurationCatalog{}, err
	}
	if !reflect.DeepEqual(first, second) {
		return ConfigurationCatalog{}, fmt.Errorf("读取期间配置来源、文件或受管要求已变化，请重新读取")
	}
	first.At = time.Now().UTC().Format(time.RFC3339Nano)
	return first, nil
}
