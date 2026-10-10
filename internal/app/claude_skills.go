package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex-netcap/internal/runtime"
)

func claudeSkillSelection(scope runtime.Scope, ref CapabilityReference, mode, stateDir string) (runtime.SkillSelection, error) {
	var empty runtime.SkillSelection
	if scope.Instance.Kind != "claude" || (mode != "enabled" && mode != "disabled") {
		return empty, fmt.Errorf("当前 Skill 选择入口支持 Claude 的 enabled/disabled 模式")
	}
	tree, stamp, err := readCapabilityResources(ref.Asset)
	if err != nil {
		return empty, err
	}
	if stamp != ref.Source {
		return empty, fmt.Errorf("Skill 来源在绑定期间发生变化")
	}
	entry, source := "SKILL.md", stamp.Resolved
	if tree.Kind == "directory" {
		source = filepath.Join(source, entry)
	} else if tree.Name != entry {
		return empty, fmt.Errorf("Skill 文件入口必须为 SKILL.md")
	}
	if diagnoseResources(ref.Asset, tree).Status == "invalid" {
		return empty, fmt.Errorf("Skill 入口或声明依赖无效，请先核对能力库诊断")
	}
	metadata, err := skillDocumentMetadata(tree.files()[entry])
	if err != nil {
		return empty, err
	}
	name, _ := metadata["name"].(string)
	if raw, exists := metadata["name"]; exists && (name == "" || len(name) > 200 || strings.TrimSpace(name) != name || raw == nil) {
		return empty, fmt.Errorf("Skill 声明名称需要有效字符串")
	}
	roots, err := claudeSkillRoots(scope)
	if err != nil {
		return empty, err
	}
	sourceInfo, err := os.Stat(source)
	if err != nil || !sourceInfo.Mode().IsRegular() {
		return empty, fmt.Errorf("无法核对 Skill 原始入口的文件身份")
	}
	candidates := []runtime.SkillSelection{}
	count := 0
	for _, r := range roots {
		entries, err := os.ReadDir(r.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return empty, fmt.Errorf("无法核对 Skill 来源目录：%s", r.path)
		}
		for _, child := range entries {
			count++
			if count > 2048 {
				return empty, fmt.Errorf("Skill 来源条目超过核对上限")
			}
			if !child.IsDir() && child.Type()&os.ModeSymlink == 0 {
				continue
			}
			info, err := os.Stat(filepath.Join(r.path, child.Name()))
			if err != nil {
				return empty, fmt.Errorf("无法核对 Skill 来源目录条目")
			}
			if !info.IsDir() {
				continue
			}
			path := filepath.Join(r.path, child.Name(), "SKILL.md")
			resolved, err := filepath.EvalSymlinks(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return empty, fmt.Errorf("无法核对 Skill 入口：%s", path)
			}
			candidates = append(candidates, runtime.SkillSelection{AssetID: ref.Asset.ID, Name: name, NativeKey: child.Name(), Mode: mode, Source: r.source, SourcePath: resolved, DiscoveryPath: path})
		}
	}
	var matches []runtime.SkillSelection
	for _, candidate := range candidates {
		info, err := os.Stat(candidate.SourcePath)
		if err != nil {
			return empty, fmt.Errorf("无法核对 Skill 原生入口的文件身份")
		}
		// A hard link can expose the same asset through a different directory,
		// including different relative resources. Do not wrap it as external or
		// silently substitute that directory for the registered source.
		if os.SameFile(sourceInfo, info) && candidate.SourcePath != source {
			return empty, fmt.Errorf("Skill 文件存在其他原生入口别名；请按实际入口登记，不能单独选配当前来源")
		}
		if candidate.SourcePath == source {
			if !os.SameFile(sourceInfo, info) {
				return empty, fmt.Errorf("Skill 原始入口在绑定期间变化")
			}
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		if name == "" {
			name = "method"
		}
		if !validID(name) || scope.SessionID == "" || stamp.SHA256 == "" {
			return empty, fmt.Errorf("外部 Skill 需要有效名称、会话身份与来源版本")
		}
		// A per-session namespace isolates shared assets without changing the
		// native session identity. Capture only the plan; Session creates it.
		plugin := "netcap-skill-" + digest([]string{scope.SessionID, ref.Asset.ID})[:24]
		root := filepath.Join(stateDir, "runtimes", scope.Instance.ID, "skill-resources", plugin, stamp.SHA256)
		return runtime.SkillSelection{AssetID: ref.Asset.ID, Name: plugin + ":" + name,
			NativeKey: plugin + ":method", Mode: mode, Source: "plugin", SourcePath: source,
			DiscoveryPath: filepath.Join(root, "skills", "method", "SKILL.md"),
			Delivery:      "local_plugin", PluginName: plugin, ResourceRoot: stamp.Resolved,
			ResourceKind: tree.Kind, ResourceHash: stamp.SHA256}, nil
	}
	if len(matches) != 1 {
		return empty, fmt.Errorf("所选 Skill 未唯一匹配当前用户/项目的原生入口")
	}
	selected := matches[0]
	if !validID(selected.NativeKey) {
		return empty, fmt.Errorf("Skill 原生目录键尚不受支持")
	}
	if selected.Name == "" {
		selected.Name = selected.NativeKey
	}
	for _, candidate := range candidates {
		if candidate.DiscoveryPath != selected.DiscoveryPath && candidate.NativeKey == selected.NativeKey {
			return empty, fmt.Errorf("Skill 原生目录键 %s 对应多个来源，不能单独选配此资产", selected.NativeKey)
		}
	}
	return selected, nil
}
