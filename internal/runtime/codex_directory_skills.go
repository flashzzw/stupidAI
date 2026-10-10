package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// CodexDirectorySkill binds the original native resource. It is never a private
// plugin copy, and its declared version does not freeze external source files.
type CodexDirectorySkill struct {
	RootMode uint32       `json:"root_mode"`
	Files    []PluginFile `json:"files"`
}

func ValidateCodexDirectorySkill(scope Scope, s SkillSelection) error {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || s.CodexDirectory == nil || s.Delivery != "native_directory" || s.Source != "codex_directory" || s.PluginName != "" || s.AssetID == "" || !codexProviderKey.MatchString(s.Name) || len(s.Name) > 100 || (s.Mode != "enabled" && s.Mode != "disabled") || s.NativeKey != s.DiscoveryPath {
		return fmt.Errorf("Codex 原生目录 Skill 缺少有效的来源与选择绑定")
	}
	for _, value := range []string{s.SourcePath, s.DiscoveryPath, s.ResourceRoot} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("原生 Skill 路径无效")
		}
	}
	entry := s.ResourceRoot
	if s.ResourceKind == "directory" {
		entry = filepath.Join(entry, "SKILL.md")
	} else if s.ResourceKind != "file" {
		return fmt.Errorf("Skill 资源类型无效")
	}
	if filepath.Base(s.DiscoveryPath) != "SKILL.md" || filepath.Base(entry) != "SKILL.md" || entry != s.SourcePath || len(s.CodexDirectory.Files) < 1 || len(s.CodexDirectory.Files) > 2048 || s.CodexDirectory.RootMode > 0777 {
		return fmt.Errorf("Skill 完整资源清单或入口无效")
	}
	if b, err := hex.DecodeString(s.ResourceHash); err != nil || len(b) != 32 {
		return fmt.Errorf("Skill 完整资源标识无效")
	}
	seen, entryCount := map[string]bool{}, 0
	for _, file := range s.CodexDirectory.Files {
		if !fs.ValidPath(file.Path) || file.Path == "." || seen[file.Path] || strings.ContainsAny(file.Path, "\\:\x00") || file.Mode > 0777 || (file.Kind != "file" && file.Kind != "directory") {
			return fmt.Errorf("Skill 资源项无效或重复")
		}
		seen[file.Path] = true
		if file.Kind == "file" {
			if b, err := hex.DecodeString(file.SHA256); err != nil || len(b) != 32 {
				return fmt.Errorf("Skill 文件摘要无效")
			}
		}
		if path.Base(file.Path) == "SKILL.md" {
			if file.Path != "SKILL.md" || file.Kind != "file" {
				return fmt.Errorf("单项 Skill 不能包含其他原生入口")
			}
			entryCount++
		}
	}
	if entryCount != 1 || s.ResourceKind == "file" && (len(s.CodexDirectory.Files) != 1 || s.CodexDirectory.Files[0].Mode != s.CodexDirectory.RootMode) {
		return fmt.Errorf("Skill 入口不是唯一固定文件")
	}
	return nil
}

func verifyCodexDirectorySkillFiles(scope Scope, s SkillSelection) error {
	if err := ValidateCodexDirectorySkill(scope, s); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(s.DiscoveryPath)
	if err != nil || resolved != s.SourcePath {
		return fmt.Errorf("原生 Skill 入口已改指或缺失")
	}
	root, err := filepath.EvalSymlinks(s.ResourceRoot)
	if err != nil || root != s.ResourceRoot {
		return fmt.Errorf("Skill 完整资源目录身份已变化")
	}
	_, err = readCodexSkillResource(PluginSelection{SourcePath: s.ResourceRoot, RootMode: s.CodexDirectory.RootMode, Files: s.CodexDirectory.Files}, s.ResourceKind)
	if err != nil {
		return fmt.Errorf("原生 Skill 完整资源内容或权限已变化；不能继续采用旧版本")
	}
	return nil
}

func verifyCodexDirectorySkills(ctx context.Context, p codexConnection, scope Scope) (SkillInventory, error) {
	if len(scope.SkillSelections) == 0 {
		return SkillInventory{}, nil
	}
	inventory, err := discoverCodexSkills(ctx, p, scope)
	if err != nil || !inventory.Complete {
		return inventory, fmt.Errorf("原生 Skill 来源清单不完整，不能核对目录选择")
	}
	seen := map[string]bool{}
	for _, selected := range scope.SkillSelections {
		if err := verifyCodexDirectorySkillFiles(scope, selected); err != nil {
			return inventory, err
		}
		if seen[selected.DiscoveryPath] || seen["name:"+selected.Name] {
			return inventory, fmt.Errorf("所选原生 Skill 来源或名称重复")
		}
		seen[selected.DiscoveryPath], seen["name:"+selected.Name] = true, true
		info, err := os.Stat(selected.SourcePath)
		if err != nil {
			return inventory, fmt.Errorf("原生 Skill 来源身份无法读取")
		}
		matches, names := 0, 0
		for _, item := range inventory.Skills {
			if item.Name == selected.Name {
				names++
			}
			other, e := os.Stat(item.Path)
			if e != nil {
				return inventory, fmt.Errorf("原生 Skill 来源无法核对，不能排除别名重叠")
			}
			if !os.SameFile(info, other) {
				continue
			}
			matches++
			if item.Path != selected.DiscoveryPath || item.Name != selected.Name || item.PluginID != "" || item.Scope != "user" && item.Scope != "repo" {
				return inventory, fmt.Errorf("Skill 没有唯一匹配原生用户/项目入口；插件、系统及受管来源需另行核对")
			}
		}
		if matches != 1 || names != 1 {
			return inventory, fmt.Errorf("Skill 原生来源缺失、同名或存在路径别名，不能独立选配")
		}
	}
	return inventory, nil
}

func addCodexDirectorySkillOverrides(ctx context.Context, p codexConnection, scope Scope, overrides map[string]any) error {
	if _, err := verifyCodexDirectorySkills(ctx, p, scope); err != nil {
		return err
	}
	var read struct {
		Config struct {
			Skills struct {
				Config []json.RawMessage `json:"config"`
			} `json:"skills"`
		} `json:"config"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": false}, &read); err != nil {
		return fmt.Errorf("无法核对继承的 Skill 配置")
	}
	if len(read.Config.Skills.Config) > 4096 {
		return fmt.Errorf("继承 Skill 配置超过核对范围")
	}
	entries, seen := []json.RawMessage{}, map[string]bool{}
	for _, raw := range read.Config.Skills.Config {
		var item struct {
			Path    string `json:"path"`
			Enabled *bool  `json:"enabled"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Enabled == nil || !filepath.IsAbs(item.Path) || filepath.Clean(item.Path) != item.Path || strings.ContainsRune(item.Path, '\x00') || seen[item.Path] {
			return fmt.Errorf("继承 Skill 配置无效或有重复入口")
		}
		seen[item.Path] = true
		selected := false
		for _, s := range scope.SkillSelections {
			if item.Path == s.DiscoveryPath {
				selected = true
				continue
			}
			left, e := os.Stat(item.Path)
			right, f := os.Stat(s.SourcePath)
			if e == nil && f == nil && os.SameFile(left, right) {
				return fmt.Errorf("继承 Skill 配置包含所选入口的另一别名")
			}
		}
		if !selected {
			entries = append(entries, raw)
		}
	}
	for _, s := range scope.SkillSelections {
		raw, _ := json.Marshal(map[string]any{"path": s.DiscoveryPath, "enabled": s.Mode == "enabled"})
		entries = append(entries, raw)
	}
	overrides["skills.config"] = entries
	return nil
}

func (c *Codex) prepareDirectorySkills(ctx context.Context, p codexConnection, scope Scope) (string, error) {
	if len(scope.SkillSelections) == 0 {
		return "", nil
	}
	if _, err := verifyCodexDirectorySkills(ctx, p, scope); err != nil {
		return "", err
	}
	return codexConfigurationFingerprint(ctx, p, scope)
}

func (c *Codex) rememberDirectorySkills(ctx context.Context, p codexConnection, scope Scope, nativeID, before string) error {
	if len(scope.SkillSelections) == 0 && before == "" {
		return nil
	}
	key := scope.Instance.ID + "/" + nativeID
	delete(c.directorySkillWitnesses, key)
	_, err := verifyCodexDirectorySkills(ctx, p, scope)
	if err != nil {
		return err
	}
	after, err := codexConfigurationFingerprint(ctx, p, scope)
	if err != nil || before == "" || before != after {
		return fmt.Errorf("建立原生 Skill 配置期间来源或继承配置已变化")
	}
	if c.directorySkillWitnesses == nil {
		c.directorySkillWitnesses = map[string]codexConfigurationWitness{}
	}
	c.directorySkillWitnesses[key] = codexConfigurationWitness{ConnectionID: p.ConnectionID(), Fingerprint: after, SelectionHash: codexScopedSelectionHash(scope)}
	return nil
}

func (c *Codex) directorySkillEvidence(ctx context.Context, p codexConnection, scope Scope) ([]SkillEvidence, bool) {
	if len(scope.SkillSelections) == 0 {
		return nil, true
	}
	if scope.hookDeactivation {
		_, err := verifyCodexDirectorySkills(ctx, p, scope)
		if err == nil {
			err = c.verifyHookDeactivationConfiguration(ctx, p, scope)
		}
		// Only internal prepare/apply observes this scope. It cannot publish
		// current Skill evidence or authorize a model input under the old policy.
		return nil, err == nil
	}
	if scope.directorySkillChange {
		// This view stays inside the explicit change operation. It attests the
		// old applied configuration, never that its changed files are verified.
		return nil, c.verifyDirectorySkillConfiguration(ctx, p, scope) == nil
	}
	inventory, err := verifyCodexDirectorySkills(ctx, p, scope)
	if err == nil {
		err = c.verifyDirectorySkillConfiguration(ctx, p, scope)
	}
	out := []SkillEvidence{}
	for _, s := range scope.SkillSelections {
		row := SkillEvidence{AssetID: s.AssetID, Name: s.Name, NativeKey: s.NativeKey, Mode: s.Mode, Source: s.Source, Verified: err == nil, At: time.Now().UTC().Format(time.RFC3339Nano), Coverage: "directory_source_and_connection_configuration", Detail: "原生目录来源、完整资源与本连接建立的 Skill 配置已核对；逐轮描述/正文另行记录，不代表方法已使用"}
		for _, item := range inventory.Skills {
			if item.Path == s.DiscoveryPath {
				row.Source = item.Scope
			}
		}
		if err != nil {
			row.Detail = err.Error()
		}
		out = append(out, row)
	}
	return out, err == nil
}

func (c *Codex) verifyDirectorySkillConfiguration(ctx context.Context, p codexConnection, scope Scope) error {
	key := scope.Instance.ID + "/" + scope.NativeID
	w, ok := c.directorySkillWitnesses[key]
	if !ok || w.ConnectionID != p.ConnectionID() || w.SelectionHash != codexScopedSelectionHash(scope) {
		return fmt.Errorf("当前 Skill 选择没有本连接的建立依据；保持待核对")
	}
	current, err := codexConfigurationFingerprint(ctx, p, scope)
	if err != nil || current != w.Fingerprint {
		delete(c.directorySkillWitnesses, key)
		return fmt.Errorf("Skill 的继承配置已变化或无法核对；保持待核对")
	}
	return nil
}
