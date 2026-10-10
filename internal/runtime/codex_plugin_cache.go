package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// The namespace is immutable for a session and resource version. Reusing a
// native cache key for new bytes can silently refresh another loaded thread.
func CodexPluginLocation(scope Scope, p PluginSelection) (string, string, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || scope.SessionID == "" || p.AssetID == "" || !codexProviderKey.MatchString(p.Name) || len(p.Name) > 100 || !filepath.IsAbs(scope.Instance.Profile) || !filepath.IsAbs(p.SourcePath) {
		return "", "", fmt.Errorf("Codex 插件缺少有效会话、来源或名称")
	}
	if _, err := hex.DecodeString(p.SourceHash); err != nil || len(p.SourceHash) != 64 {
		return "", "", fmt.Errorf("Codex 插件缺少固定资源摘要")
	}
	version := p.Version
	if version == "" {
		version = "local"
	}
	if version == "." || version == ".." || len(version) > 100 || strings.TrimSpace(version) != version || strings.ContainsAny(version, "/\\:\x00") {
		return "", "", fmt.Errorf("Codex 插件版本不能作为缓存目录")
	}
	sum := sha256.Sum256([]byte(scope.SessionID + "\x00" + p.AssetID + "\x00" + p.SourceHash))
	market := "netcap-" + hex.EncodeToString(sum[:16])
	return p.Name + "@" + market, filepath.Join(scope.Instance.Profile, "plugins/cache", market, p.Name, version), nil
}

func validateCodexPluginBinding(scope Scope, p PluginSelection) error {
	id, cache, err := CodexPluginLocation(scope, p)
	if err != nil {
		return err
	}
	if p.NativeSource != id || p.CachePath != cache || (p.Mode != "enabled" && p.Mode != "disabled") || len(p.Files) == 0 || len(p.Files) > 2048 || p.RootMode > 0777 || len(p.Components) == 0 || len(p.Components) > 128 {
		return fmt.Errorf("Codex 插件缓存绑定无效")
	}
	seen := map[string]bool{}
	for _, c := range p.Components {
		valid := c.Kind == "mcp" && c.Name == c.NativeName || c.Kind == "skill" && c.NativeName == p.Name+":"+c.Name && codexPluginSkillEntry(p, c.Path)
		key := c.Kind + "/" + c.Name
		if c.Kind == "hook" && p.Hooks != nil {
			key += "/" + c.Path
			valid = slices.ContainsFunc(p.Hooks.Expected, func(e PluginHookExpectation) bool {
				return e.Path == c.Path && e.Event == c.NativeName && e.Count == c.Count
			})
		}
		if !valid || (c.Kind != "hook" && c.Count != 1) || !codexProviderKey.MatchString(c.Name) || seen[key] {
			return fmt.Errorf("Codex 插件组件尚不能唯一核对")
		}
		seen[key] = true
	}
	if p.Hooks != nil {
		if err := ValidatePluginHookSelection(scope.Instance, p); err != nil {
			return err
		}
	}
	if p.SkillAsset != nil {
		if _, err := skillAssetSource(scope, p); err != nil {
			return err
		}
	}
	return nil
}

// A declared Skill can live outside skills/ and its directory need not match
// its name. Resolve only a unique, pinned file inside this complete package.
func codexPluginSkillEntry(p PluginSelection, entry string) bool {
	if !fs.ValidPath(entry) || path.Base(entry) != "SKILL.md" || strings.ContainsAny(entry, "\\:\x00") {
		return false
	}
	count := 0
	for _, file := range p.Files {
		if file.Path == entry {
			if file.Kind != "file" {
				return false
			}
			if _, err := hex.DecodeString(file.SHA256); err != nil || len(file.SHA256) != 64 {
				return false
			}
			count++
		}
	}
	return count == 1
}

// Read through an already confined root; validate the entire tree, including
// extra files and permissions. A matching manifest alone cannot fix a version.
func codexPluginFiles(root *os.Root, p PluginSelection) (map[string][]byte, error) {
	expected := map[string]PluginFile{}
	for _, f := range p.Files {
		if f.Path == "" || f.Path == "." || f.Path == ".." || strings.HasPrefix(f.Path, "../") || path.Clean(f.Path) != f.Path || strings.ContainsAny(f.Path, "\\:\x00") || strings.HasPrefix(f.Path, "/") || f.Mode > 0777 || (f.Kind != "file" && f.Kind != "directory") {
			return nil, fmt.Errorf("插件资源路径或类型无效")
		}
		if _, exists := expected[f.Path]; exists {
			return nil, fmt.Errorf("插件资源路径重复")
		}
		expected[f.Path] = f
	}
	data := map[string][]byte{}
	seen := 0
	var total int64
	err := fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if name == "." {
			if !info.IsDir() || uint32(info.Mode().Perm()) != p.RootMode {
				return fmt.Errorf("插件目录权限已变化")
			}
			return nil
		}
		seen++
		f, ok := expected[name]
		if !ok || uint32(info.Mode().Perm()) != f.Mode || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("插件内容或权限已变化")
		}
		if f.Kind == "directory" {
			if !d.IsDir() {
				return fmt.Errorf("插件目录类型已变化")
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("插件包含链接或特殊文件")
		}
		opened, e := root.Open(filepath.FromSlash(name))
		if e != nil {
			return e
		}
		actual, e := opened.Stat()
		if e != nil || !os.SameFile(info, actual) {
			opened.Close()
			return fmt.Errorf("插件读取期间身份变化")
		}
		body, e := io.ReadAll(io.LimitReader(opened, (32<<20)-total+1))
		opened.Close()
		total += int64(len(body))
		if e != nil || total > 32<<20 {
			return fmt.Errorf("插件读取失败或超过大小限制")
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("插件文件内容已变化")
		}
		data[name] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	if seen != len(expected) {
		return nil, fmt.Errorf("插件资源缺失")
	}
	return data, nil
}

func verifyCodexPluginCache(scope Scope, p PluginSelection) error {
	if err := validateCodexPluginBinding(scope, p); err != nil {
		return err
	}
	profile, err := os.OpenRoot(scope.Instance.Profile)
	if err != nil {
		return fmt.Errorf("插件 profile 不可读取")
	}
	defer profile.Close()
	rel, _ := filepath.Rel(scope.Instance.Profile, p.CachePath)
	cache, err := profile.OpenRoot(rel)
	if err != nil {
		if p.Mode == "disabled" {
			if _, e := profile.Lstat(rel); os.IsNotExist(e) {
				return nil
			}
		}
		return fmt.Errorf("插件固定缓存不可读取")
	}
	defer cache.Close()
	if _, err = codexPluginFiles(cache, p); err != nil {
		return fmt.Errorf("插件固定缓存内容或权限与快照不一致")
	}
	return nil
}

// Only explicit creation/application materializes a new namespace. Observation and cold
// recovery verify it; neither repairs nor overwrites an altered native cache.
func prepareCodexPluginCaches(scope Scope) error {
	for _, p := range scope.PluginSelections {
		if err := validateCodexPluginBinding(scope, p); err != nil {
			return err
		}
		if p.Mode == "disabled" {
			if err := verifyCodexPluginCache(scope, p); err != nil {
				return err
			}
			continue
		}
		profile, err := os.OpenRoot(scope.Instance.Profile)
		if err != nil {
			return err
		}
		err = prepareCodexPluginCache(profile, scope, p)
		profile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func prepareCodexPluginCache(profile *os.Root, scope Scope, p PluginSelection) error {
	rel, _ := filepath.Rel(scope.Instance.Profile, p.CachePath)
	if _, err := profile.Lstat(rel); err == nil {
		return verifyCodexPluginCache(scope, p)
	} else if !os.IsNotExist(err) {
		return err
	}
	var data map[string][]byte
	var err error
	if p.SkillAsset != nil {
		data, err = readCodexSkillAsset(scope, p)
	} else {
		var source *os.Root
		source, err = os.OpenRoot(p.SourcePath)
		if err != nil {
			return fmt.Errorf("插件来源不可读取")
		}
		data, err = codexPluginFiles(source, p)
		source.Close()
	}
	if err != nil {
		return err
	}
	if err = writeCodexPluginCache(profile, rel, p, data); err != nil {
		return err
	}
	return verifyCodexPluginCache(scope, p)
}

// The same complete, pinned tree is used for execution caches and temporary
// metadata inspection. Only execution callers validate a session binding.
func writeCodexPluginCache(profile *os.Root, rel string, p PluginSelection, data map[string][]byte) error {
	var err error
	parent := filepath.Dir(rel)
	if err = profile.MkdirAll(parent, 0700); err != nil {
		return err
	}
	// A sibling rename preserves the directory's parent entry, so an immutable
	// source root can keep its original non-writable mode through publication.
	stage := filepath.Join(parent, ".netcap-stage-"+uuid.NewString())
	if err = profile.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer profile.RemoveAll(stage)
	for _, f := range p.Files {
		dst := filepath.Join(stage, filepath.FromSlash(f.Path))
		if f.Kind == "directory" {
			if err = profile.MkdirAll(dst, 0700); err != nil {
				return err
			}
			continue
		}
		if err = profile.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		file, e := profile.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = file.Write(data[f.Path])
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if err = profile.Chmod(dst, fs.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	// Apply directory modes only after writing children (source trees may be
	// read-only). Nested paths are sorted independently of the saved file order.
	dirs := []PluginFile{}
	for _, f := range p.Files {
		if f.Kind == "directory" {
			dirs = append(dirs, f)
		}
	}
	slices.SortFunc(dirs, func(a, b PluginFile) int { return strings.Compare(b.Path, a.Path) })
	for _, f := range dirs {
		if err = profile.Chmod(filepath.Join(stage, filepath.FromSlash(f.Path)), fs.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	if err = profile.Chmod(stage, fs.FileMode(p.RootMode)); err != nil {
		return err
	}
	staged, err := profile.OpenRoot(stage)
	if err != nil {
		return err
	}
	_, err = codexPluginFiles(staged, p)
	staged.Close()
	if err != nil {
		return err
	}
	if _, e := profile.Lstat(rel); e == nil {
		cached, e := profile.OpenRoot(rel)
		if e != nil {
			return e
		}
		defer cached.Close()
		_, e = codexPluginFiles(cached, p)
		return e
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = profile.Rename(stage, rel); err != nil {
		return err
	}
	return nil
}
