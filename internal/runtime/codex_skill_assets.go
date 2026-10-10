package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The capability remains a Skill asset. This metadata describes only how its
// original bytes are delivered through an independently versioned native pack.
type SkillAssetDelivery struct {
	Kind     string `json:"kind"`
	RootMode uint32 `json:"root_mode"`
}

func skillAssetManifest(name string) []byte {
	b, _ := json.Marshal(struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{name, "1.0.0"})
	return append(b, '\n')
}

// BindCodexSkillAsset converts a pinned source file list into a delivery plan.
// It performs no filesystem writes and does not change the source document.
func BindCodexSkillAsset(scope Scope, source PluginSelection, kind string) (PluginSelection, error) {
	if source.SkillAsset != nil || !codexProviderKey.MatchString(source.Name) || len(source.Name) > 100 || (kind != "file" && kind != "directory") || source.RootMode > 0777 || len(source.Files) == 0 || len(source.Files) > 2044 {
		return PluginSelection{}, fmt.Errorf("Skill 资源缺少有效入口或完整文件绑定")
	}
	if kind == "file" && (filepath.Base(source.SourcePath) != "SKILL.md" || len(source.Files) != 1 || source.Files[0].Path != "SKILL.md" || source.Files[0].Kind != "file") {
		return PluginSelection{}, fmt.Errorf("单文件 Skill 需要固定 SKILL.md 入口")
	}
	method := source.Name
	sum := sha256.Sum256([]byte(scope.SessionID + "\x00" + source.AssetID))
	p := source
	p.Name, p.Version = "netcap-skill-"+hex.EncodeToString(sum[:12]), "1.0.0"
	p.SkillAsset = &SkillAssetDelivery{Kind: kind, RootMode: source.RootMode}
	p.RootMode = 0700
	manifest := skillAssetManifest(p.Name)
	hash := sha256.Sum256(manifest)
	methodMode := uint32(0700)
	if kind == "directory" {
		methodMode = source.RootMode
	}
	p.Files = []PluginFile{{Path: ".codex-plugin", Kind: "directory", Mode: 0700}, {Path: ".codex-plugin/plugin.json", Kind: "file", Mode: 0600, SHA256: hex.EncodeToString(hash[:])}, {Path: "skills", Kind: "directory", Mode: 0700}, {Path: "skills/" + method, Kind: "directory", Mode: methodMode}}
	entry := false
	for _, file := range source.Files {
		if file.Path == "" || file.Path == "." || path.Clean(file.Path) != file.Path || strings.HasPrefix(file.Path, "../") || strings.HasPrefix(file.Path, "/") || strings.ContainsAny(file.Path, "\\:\x00") {
			return PluginSelection{}, fmt.Errorf("Skill 资源路径无效")
		}
		if path.Base(file.Path) == "SKILL.md" && file.Path != "SKILL.md" {
			return PluginSelection{}, fmt.Errorf("Skill 资源包含多个原生入口，不能作为单个方法交付")
		}
		entry = entry || file.Path == "SKILL.md" && file.Kind == "file"
		file.Path = path.Join("skills", method, file.Path)
		p.Files = append(p.Files, file)
	}
	if !entry {
		return PluginSelection{}, fmt.Errorf("Skill 资源缺少 SKILL.md")
	}
	p.Components = []PluginComponent{{Kind: "skill", Name: method, NativeName: p.Name + ":" + method, Path: path.Join("skills", method, "SKILL.md"), Count: 1}}
	var err error
	p.NativeSource, p.CachePath, err = CodexPluginLocation(scope, p)
	return p, err
}

func verifyCodexSkillAssetSources(ctx context.Context, p codexConnection, scope Scope) error {
	var selected []os.FileInfo
	for _, pack := range scope.PluginSelections {
		if pack.SkillAsset == nil {
			continue
		}
		entry := pack.SourcePath
		if pack.SkillAsset.Kind == "directory" {
			entry = filepath.Join(entry, "SKILL.md")
		}
		info, err := os.Stat(entry)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("所选 Skill 入口无法核对")
		}
		selected = append(selected, info)
	}
	if len(selected) == 0 {
		return nil
	}
	inventory, err := discoverCodexSkills(ctx, p, scope)
	if err != nil || !inventory.Complete {
		return fmt.Errorf("原生 Skill 来源清单不完整，不能核对独立资源交付")
	}
	for _, item := range inventory.Skills {
		resolved, err := filepath.EvalSymlinks(item.Path)
		if err != nil {
			return fmt.Errorf("原生 Skill 入口无法核对，不能排除来源重叠")
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("原生 Skill 入口身份无法核对")
		}
		for _, entry := range selected {
			if os.SameFile(info, entry) {
				return fmt.Errorf("所选 Skill 已由原生目录或插件发现；该入口的逐会话控制尚未接通，不能用独立副本代替排除")
			}
		}
	}
	return nil
}

func skillAssetSource(scope Scope, p PluginSelection) (PluginSelection, error) {
	if p.SkillAsset == nil || len(p.Components) != 1 || p.Components[0].Kind != "skill" {
		return PluginSelection{}, fmt.Errorf("Skill 交付绑定无效")
	}
	method := p.Components[0].Name
	prefix := path.Join("skills", method) + "/"
	source := p
	source.SkillAsset, source.RootMode, source.Name = nil, p.SkillAsset.RootMode, method
	source.Files = nil
	for _, file := range p.Files {
		if strings.HasPrefix(file.Path, prefix) {
			file.Path = strings.TrimPrefix(file.Path, prefix)
			source.Files = append(source.Files, file)
		}
	}
	expected, err := BindCodexSkillAsset(scope, source, p.SkillAsset.Kind)
	if err != nil || !EqualPluginSelections([]PluginSelection{expected}, []PluginSelection{p}) {
		return PluginSelection{}, fmt.Errorf("Skill 交付副本与原资源绑定不一致")
	}
	return source, nil
}

func readCodexSkillAsset(scope Scope, p PluginSelection) (map[string][]byte, error) {
	source, err := skillAssetSource(scope, p)
	if err != nil {
		return nil, err
	}
	data, err := readCodexSkillResource(source, p.SkillAsset.Kind)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{".codex-plugin/plugin.json": skillAssetManifest(p.Name)}
	for name, body := range data {
		out[path.Join("skills", source.Name, name)] = body
	}
	return out, nil
}

// Read the original complete resource without publishing a native plugin.
func readCodexSkillResource(source PluginSelection, kind string) (map[string][]byte, error) {
	var err error
	var data map[string][]byte
	if kind == "directory" {
		root, e := os.OpenRoot(source.SourcePath)
		if e != nil {
			return nil, e
		}
		data, err = codexPluginFiles(root, source)
		root.Close()
	} else {
		root, e := os.OpenRoot(filepath.Dir(source.SourcePath))
		if e != nil {
			return nil, e
		}
		defer root.Close()
		info, e := root.Lstat("SKILL.md")
		if e != nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || uint32(info.Mode().Perm()) != source.RootMode || uint32(info.Mode().Perm()) != source.Files[0].Mode {
			return nil, fmt.Errorf("Skill 文件身份或权限已变化")
		}
		file, e := root.Open("SKILL.md")
		if e != nil {
			return nil, e
		}
		defer file.Close()
		opened, e := file.Stat()
		if e != nil || !os.SameFile(info, opened) {
			return nil, fmt.Errorf("Skill 文件读取期间身份变化")
		}
		body, e := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		h := sha256.Sum256(body)
		if e != nil || len(body) > 32<<20 || hex.EncodeToString(h[:]) != source.Files[0].SHA256 {
			return nil, fmt.Errorf("Skill 文件内容已变化或超限")
		}
		data = map[string][]byte{"SKILL.md": body}
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
