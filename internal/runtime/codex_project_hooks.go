package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// Configuration layers and hook discovery can name different files in a Git
// worktree. Keep both proven sources without treating discovery as execution.
type CodexProjectHookFile struct {
	File          FileSource `json:"file"`
	Declared      bool       `json:"configuration_layer"`
	LayerVersion  string     `json:"layer_version,omitempty"`
	LayerDisabled bool       `json:"layer_disabled"`
	Discovered    bool       `json:"native_discovered"`
}

type CodexProjectHookSources struct {
	Version int                    `json:"version"`
	Source  string                 `json:"source"`
	Cwd     string                 `json:"cwd"`
	Profile string                 `json:"profile"`
	Files   []CodexProjectHookFile `json:"files"`
}

type CodexProjectHookReader interface {
	ReadCodexProjectHookSources(context.Context, Scope) (*CodexProjectHookSources, error)
}

func (a *Adapters) ReadCodexProjectHookSources(ctx context.Context, scope Scope) (*CodexProjectHookSources, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return nil, err
	}
	reader, ok := r.(CodexProjectHookReader)
	if !ok {
		return nil, fmt.Errorf("此运行时尚不能核对 Codex 项目 hook 文件来源")
	}
	return reader.ReadCodexProjectHookSources(ctx, scope)
}

func (c *Codex) ReadCodexProjectHookSources(ctx context.Context, scope Scope) (*CodexProjectHookSources, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" {
		return nil, fmt.Errorf("项目 hook 来源只用于普通 Codex 执行")
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, scope.Instance)
	if err != nil {
		return nil, fmt.Errorf("无法连接原配置以核对项目 hook 来源")
	}
	return readCodexProjectHooks(ctx, p, scope)
}

const codexProjectHookSource = "codex.config/read+hooks/list"

func projectHookPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsRune(path, '\x00') && filepath.Base(path) == "hooks.json" && filepath.Base(filepath.Dir(path)) == ".codex"
}

func projectHookMetadata(ctx context.Context, p codexConnection, scope Scope) ([]CodexProjectHookFile, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || !filepath.IsAbs(scope.Cwd) || !filepath.IsAbs(scope.Instance.Profile) {
		return nil, fmt.Errorf("项目 hook 来源需要普通 Codex 执行及已绑定目录")
	}
	var config struct {
		Layers *[]struct {
			Name struct {
				Type, File string
				Directory  string `json:"dotCodexFolder"`
			} `json:"name"`
			Version  string  `json:"version"`
			Disabled *string `json:"disabledReason"`
		} `json:"layers"`
	}
	if err := p.Call(ctx, "config/read", map[string]any{"cwd": scope.Cwd, "includeLayers": true}, &config); err != nil || config.Layers == nil || len(*config.Layers) > 128 {
		return nil, fmt.Errorf("原生项目配置层缺失、不可读取或超过核对范围")
	}
	files := map[string]CodexProjectHookFile{}
	userCount := 0
	for _, layer := range *config.Layers {
		if layer.Name.Type == "user" {
			actual, err := filepath.EvalSymlinks(layer.Name.File)
			expected, expectedErr := filepath.EvalSymlinks(filepath.Join(scope.Instance.Profile, "config.toml"))
			if err != nil || expectedErr != nil || actual != expected {
				return nil, fmt.Errorf("配置层不属于已保存的 Codex profile")
			}
			userCount++
		}
		if layer.Name.Type != "project" {
			continue
		}
		path := filepath.Join(layer.Name.Directory, "hooks.json")
		if !projectHookPath(path) || filepath.Dir(path) != layer.Name.Directory || !strings.HasPrefix(layer.Version, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(layer.Version, "sha256:")) {
			return nil, fmt.Errorf("原生项目配置层路径或版本无法核对")
		}
		if _, exists := files[path]; exists {
			return nil, fmt.Errorf("原生项目配置层重复")
		}
		files[path] = CodexProjectHookFile{File: FileSource{Path: path}, Declared: true, LayerVersion: layer.Version, LayerDisabled: layer.Disabled != nil && *layer.Disabled != ""}
	}
	if userCount != 1 {
		return nil, fmt.Errorf("原生未返回唯一的已绑定用户配置层")
	}
	var catalog struct {
		Data []struct {
			Cwd   string `json:"cwd"`
			Hooks []struct {
				Source, SourcePath string
				Managed            *bool `json:"isManaged"`
			} `json:"hooks"`
			Warnings []json.RawMessage `json:"warnings"`
			Errors   []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if err := p.Call(ctx, "hooks/list", map[string]any{"cwds": []string{scope.Cwd}}, &catalog); err != nil || len(catalog.Data) != 1 || catalog.Data[0].Cwd != scope.Cwd {
		return nil, fmt.Errorf("原生项目 hook 清单不可读取或目录不匹配")
	}
	entry := catalog.Data[0]
	if entry.Hooks == nil || entry.Warnings == nil || entry.Errors == nil || len(entry.Hooks) > 2048 || len(entry.Warnings) > 0 || len(entry.Errors) > 0 {
		return nil, fmt.Errorf("原生 hook 来源清单不完整或报告了配置问题，不能固定部分来源")
	}
	for _, hook := range entry.Hooks {
		if !slices.Contains([]string{"user", "project", "plugin", "managed"}, hook.Source) {
			return nil, fmt.Errorf("原生 hook 来源类别尚不能核对")
		}
		if hook.Source != "project" {
			continue
		}
		if !projectHookPath(hook.SourcePath) || hook.Managed == nil || *hook.Managed {
			return nil, fmt.Errorf("原生项目 hook 路径或受管状态尚不能核对")
		}
		file := files[hook.SourcePath]
		file.File.Path = hook.SourcePath
		file.Discovered = true
		files[hook.SourcePath] = file
	}
	if len(files) > 64 {
		return nil, fmt.Errorf("项目 hook 文件超过 64 项核对上限")
	}
	out := make([]CodexProjectHookFile, 0, len(files))
	for _, file := range files {
		out = append(out, file)
	}
	slices.SortFunc(out, func(a, b CodexProjectHookFile) int { return strings.Compare(a.File.Path, b.File.Path) })
	return out, nil
}

func readCodexProjectHooks(ctx context.Context, p codexConnection, scope Scope) (*CodexProjectHookSources, error) {
	metadata, err := projectHookMetadata(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	out := &CodexProjectHookSources{Version: 1, Source: codexProjectHookSource, Cwd: scope.Cwd, Profile: scope.Instance.Profile, Files: append([]CodexProjectHookFile{}, metadata...)}
	for i := range out.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, _, err := readSourceFile(out.Files[i].File.Path, true, "Codex 项目 hook")
		if err != nil {
			return nil, err
		}
		if out.Files[i].Discovered && file.Kind != "file" {
			return nil, fmt.Errorf("原生发现的项目 hook 文件缺失，不能采用不完整来源")
		}
		out.Files[i].File = file
	}
	after, err := projectHookMetadata(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(metadata, after) {
		return nil, fmt.Errorf("读取期间项目 hook 来源集合或配置层已变化")
	}
	if err := VerifyCodexProjectHookFiles(Scope{Instance: scope.Instance, Cwd: scope.Cwd, CodexProjectHooks: out}); err != nil {
		return nil, err
	}
	return out, nil
}

func VerifyCodexProjectHookFiles(scope Scope) error {
	scope = codexHookSourceScope(scope)
	saved := scope.CodexProjectHooks
	if saved == nil {
		return nil
	}
	if saved.Version != 1 || saved.Source != codexProjectHookSource || saved.Cwd != scope.Cwd || saved.Profile != scope.Instance.Profile || scope.Instance.Kind != "codex" || scope.Purpose != "" || saved.Files == nil || len(saved.Files) > 64 {
		return fmt.Errorf("Codex 项目 hook 来源版本与原绑定不一致")
	}
	last := ""
	for _, file := range saved.Files {
		if !projectHookPath(file.File.Path) || file.File.Path <= last || !file.Declared && (!file.Discovered || file.LayerVersion != "" || file.LayerDisabled) || file.Discovered && file.File.Kind != "file" || file.Declared && (!strings.HasPrefix(file.LayerVersion, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(file.LayerVersion, "sha256:"))) {
			return fmt.Errorf("Codex 项目 hook 文件来源记录无效")
		}
		last = file.File.Path
		current, _, err := readSourceFile(file.File.Path, true, "Codex 项目 hook")
		if err != nil {
			return err
		}
		if current != file.File {
			return fmt.Errorf("Codex 项目 hook 文件内容、权限、路径或存在状态已变化：%s", file.File.Path)
		}
	}
	return nil
}

func verifyCodexProjectHooks(ctx context.Context, p codexConnection, scope Scope) error {
	scope = codexHookSourceScope(scope)
	if scope.CodexProjectHooks == nil {
		return nil
	}
	if err := VerifyCodexProjectHookFiles(scope); err != nil {
		return err
	}
	current, err := readCodexProjectHooks(ctx, p, scope)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, scope.CodexProjectHooks) {
		return fmt.Errorf("Codex 项目 hook 来源集合或配置层已变化；原文件版本保持不变")
	}
	return nil
}
