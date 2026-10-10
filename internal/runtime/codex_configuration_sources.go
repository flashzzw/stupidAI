package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type CodexConfigurationSources struct {
	Version int    `json:"version"`
	Profile string `json:"profile"`
	Cwd     string `json:"cwd"`
	ConfigurationData
}

type CodexConfigurationSourceReader interface {
	ReadCodexConfigurationSources(context.Context, Scope) (*CodexConfigurationSources, error)
}

func (a *Adapters) ReadCodexConfigurationSources(ctx context.Context, scope Scope) (*CodexConfigurationSources, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return nil, err
	}
	reader, ok := r.(CodexConfigurationSourceReader)
	if !ok {
		return nil, fmt.Errorf("运行时尚不能固定 Codex 配置来源")
	}
	return reader.ReadCodexConfigurationSources(ctx, scope)
}

func (c *Codex) ReadCodexConfigurationSources(ctx context.Context, scope Scope) (*CodexConfigurationSources, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || !filepath.IsAbs(scope.Cwd) || !filepath.IsAbs(scope.Instance.Profile) {
		return nil, fmt.Errorf("配置来源版本需要普通 Codex 执行与已绑定目录")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return nil, fmt.Errorf("无法连接原配置以固定来源版本")
	}
	return readCodexConfigurationSources(ctx, p, scope)
}

func readCodexConfigurationSources(ctx context.Context, p codexConnection, scope Scope) (*CodexConfigurationSources, error) {
	first, err := readConfigurationCatalog(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	second, err := readConfigurationCatalog(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, fmt.Errorf("固定版本期间配置来源或受管要求变化")
	}
	return &CodexConfigurationSources{Version: 1, Profile: scope.Instance.Profile, Cwd: scope.Cwd, ConfigurationData: first.ConfigurationData}, nil
}

// File checks remain available without a native connection. New native layers,
// parsed values and origins require verifyCodexConfigurationSources as well.
func VerifyCodexConfigurationFiles(scope Scope) error {
	scope = codexHookSourceScope(scope)
	saved := scope.CodexConfigurationSources
	if saved == nil {
		return nil
	}
	if saved.Version != 1 || saved.Profile != scope.Instance.Profile || saved.Cwd != scope.Cwd || scope.Instance.Kind != "codex" || scope.Purpose != "" || saved.Layers == nil || len(saved.Layers) > 128 || saved.Origins == nil || saved.OtherOriginCount < 0 {
		return fmt.Errorf("Codex 配置来源版本与原绑定不一致")
	}
	if err := validateConfigurationSettings(saved.Settings); err != nil {
		return err
	}
	for _, hash := range []string{saved.ConfigurationSHA256, saved.OriginsSHA256, saved.LayersSHA256} {
		if !hookDefinitionHash.MatchString(hash) {
			return fmt.Errorf("配置来源摘要无效")
		}
	}
	seen := map[string]ConfigurationLayer{}
	users := 0
	for _, layer := range saved.Layers {
		if _, ok := seen[layer.ID]; ok || !hookDefinitionHash.MatchString(layer.ID) || !hookDefinitionHash.MatchString(layer.SHA256) || !hookDefinitionHash.MatchString(layer.ConfigurationSHA256) || !strings.HasPrefix(layer.Version, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(layer.Version, "sha256:")) {
			return fmt.Errorf("配置层版本记录无效或重复")
		}
		seen[layer.ID] = layer
		fileBacked := slices.Contains([]string{"packagedDefaults", "system", "user", "project", "legacyManagedConfigTomlFromFile"}, layer.Kind)
		if !fileBacked && !slices.Contains([]string{"mdm", "enterpriseManaged", "sessionFlags", "legacyManagedConfigTomlFromMdm"}, layer.Kind) || fileBacked != (layer.File != nil) {
			return fmt.Errorf("配置层来源类别与文件记录不符")
		}
		if layer.File == nil {
			continue
		}
		path := layer.File.Path
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("配置来源文件路径无效")
		}
		current, _, err := readSourceFile(path, true, "Codex 配置来源")
		if err != nil {
			return err
		}
		if current != *layer.File {
			return fmt.Errorf("Codex 配置来源文件的内容、权限、路径或存在状态已变化：%s", path)
		}
		if layer.Kind == "user" {
			expected, err := filepath.EvalSymlinks(filepath.Join(scope.Instance.Profile, "config.toml"))
			if err != nil || current.ResolvedPath != expected {
				return fmt.Errorf("用户配置来源不属于原 profile")
			}
			users++
		}
	}
	if users == 0 {
		return fmt.Errorf("配置来源版本缺少用户层")
	}
	fields := map[string]bool{}
	for _, origin := range saved.Origins {
		layer, exists := seen[origin.LayerID]
		if !exists || layer.Disabled || origin.Field == "" || fields[origin.Field] {
			return fmt.Errorf("配置字段来源记录无效")
		}
		fields[origin.Field] = true
	}
	return nil
}

func verifyCodexConfigurationSources(ctx context.Context, p codexConnection, scope Scope) error {
	scope = codexHookSourceScope(scope)
	if scope.CodexConfigurationSources == nil {
		return nil
	}
	if err := VerifyCodexConfigurationFiles(scope); err != nil {
		return err
	}
	current, err := readCodexConfigurationSources(ctx, p, scope)
	if err != nil {
		return err
	}
	if scope.CodexConfigurationSources.Settings == nil {
		// Reading newer evidence must not fabricate an older snapshot's
		// settings or prevent its existing source checks from succeeding.
		current.Settings = nil
	}
	if !reflect.DeepEqual(current, scope.CodexConfigurationSources) {
		return fmt.Errorf("Codex 合并配置、来源层或字段来源已变化；保留原执行版本")
	}
	return nil
}

// Hook adoption may add/remove an empty project layer when its .codex folder
// appears/disappears. It cannot absorb a config file, value, origin or other
// layer change. Per-layer full hashes preserve metadata hidden from the UI.
func CodexHookConfigurationTransition(previous, target Scope) bool {
	a, b := previous.CodexConfigurationSources, target.CodexConfigurationSources
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if reflect.DeepEqual(a, b) {
		return true
	}
	if a.Version != 1 || b.Version != 1 || a.Profile != b.Profile || a.Cwd != b.Cwd || a.ConfigurationSHA256 != b.ConfigurationSHA256 || a.OriginsSHA256 != b.OriginsSHA256 || a.OtherOriginCount != b.OtherOriginCount || !reflect.DeepEqual(a.Origins, b.Origins) {
		return false
	}
	empty := configurationDigest(map[string]any{})
	allowed := func(layer ConfigurationLayer, hooks *CodexProjectHookSources) bool {
		if layer.Kind != "project" || layer.ConfigurationSHA256 != empty || layer.Version != "sha256:"+empty || layer.File == nil || layer.File.Kind != "absent" || hooks == nil {
			return false
		}
		for _, hook := range hooks.Files {
			if hook.Declared && filepath.Dir(hook.File.Path) == filepath.Dir(layer.File.Path) {
				return true
			}
		}
		return false
	}
	left, right := map[string]ConfigurationLayer{}, map[string]ConfigurationLayer{}
	for _, layer := range a.Layers {
		left[layer.ID] = layer
	}
	for _, layer := range b.Layers {
		right[layer.ID] = layer
	}
	commonA, commonB := []ConfigurationLayer{}, []ConfigurationLayer{}
	for _, layer := range a.Layers {
		if other, exists := right[layer.ID]; exists {
			if !reflect.DeepEqual(layer, other) {
				return false
			}
			commonA = append(commonA, layer)
		} else if !allowed(layer, previous.CodexProjectHooks) {
			return false
		}
	}
	for _, layer := range b.Layers {
		if _, exists := left[layer.ID]; exists {
			commonB = append(commonB, layer)
		} else if !allowed(layer, target.CodexProjectHooks) {
			return false
		}
	}
	return (len(a.Layers) != len(commonA) || len(b.Layers) != len(commonB)) && reflect.DeepEqual(commonA, commonB)
}
