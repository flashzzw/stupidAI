package app

import (
	"codex-netcap/internal/runtime"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// InstalledPluginCatalog describes files under the saved profile. It carries no
// native loading, effective permission or input-admission claim.
type InstalledPluginCatalog struct {
	NativeID      string                 `json:"native_id"`
	Cwd           string                 `json:"cwd"`
	Profile       string                 `json:"profile"`
	SnapshotID    string                 `json:"snapshot_id"`
	Source        string                 `json:"source"`
	At            string                 `json:"at"`
	RegistryPath  string                 `json:"registry_path"`
	SourceVersion string                 `json:"source_version"`
	SourceFiles   []runtime.FileSource   `json:"source_files"`
	Complete      bool                   `json:"complete"`
	Entries       []InstalledPluginEntry `json:"entries"`
	Errors        []PluginRegistryIssue  `json:"errors"`
}

type PluginRegistryIssue struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type InstalledPluginEntry struct {
	SourceKey         string `json:"source_key"`
	Name              string `json:"name"`
	Scope             string `json:"scope"`
	ProjectPath       string `json:"project_path,omitempty"`
	RegisteredVersion string `json:"registered_version"`
	CachePath         string `json:"cache_path"`
	CacheResolvedPath string `json:"cache_resolved_path,omitempty"`
	CacheStatus       string `json:"cache_status"`
	MarketKind        string `json:"market_kind"`
	MarketPath        string `json:"market_path,omitempty"`
	MarketInstallPath string `json:"market_install_path,omitempty"`
	DeclaredPath      string `json:"declared_path,omitempty"`
	ResolvedPath      string `json:"resolved_path,omitempty"`
	ManifestVersion   string `json:"manifest_version,omitempty"`
	Status            string `json:"status"`
	Detail            string `json:"detail"`
}

func (a *Application) ReadSessionInstalledPlugins(ctx context.Context, id string) (InstalledPluginCatalog, error) {
	a.mu.Lock()
	session, ok := a.state.Sessions[id]
	if !ok || a.closed || session.Origin != "console" || session.Scope.Instance.Kind != "claude" || session.Scope.NativeID == "" {
		a.mu.Unlock()
		return InstalledPluginCatalog{}, fmt.Errorf("此会话尚无 Claude 插件登记读取入口")
	}
	session = queryCopy(session)
	err := a.verifyCapabilityCatalogBinding(session)
	a.mu.Unlock()
	if err != nil {
		return InstalledPluginCatalog{}, err
	}
	out := readInstalledPluginCatalog(ctx, session)
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.state.Sessions[id]
	if !exists || a.closed || current.Generation != session.Generation || digest(current.Scope) != digest(session.Scope) {
		return InstalledPluginCatalog{}, fmt.Errorf("读取期间会话绑定或输入归属已变化，请重新读取插件登记")
	}
	if err := a.verifyCapabilityCatalogBinding(current); err != nil {
		return InstalledPluginCatalog{}, err
	}
	return out, nil
}

func readInstalledPluginCatalog(ctx context.Context, session Session) (out InstalledPluginCatalog) {
	profile := session.Scope.Instance.Profile
	registry := filepath.Join(profile, "plugins/installed_plugins.json")
	out = InstalledPluginCatalog{NativeID: session.Scope.NativeID, Cwd: session.Scope.Cwd, Profile: profile,
		Source: "claude.installed_registry", At: time.Now().UTC().Format(time.RFC3339Nano), RegistryPath: registry,
		Complete: true, Entries: []InstalledPluginEntry{}, Errors: []PluginRegistryIssue{}}
	if session.ExecutionSnapshot != nil {
		out.SnapshotID = session.ExecutionSnapshot.ID
	}
	reader := pluginMetadataReader{ctx: ctx, remaining: 4 << 20, cache: map[string]pluginMetadataResult{}, sources: []runtime.FileSource{}}
	defer func() {
		out.SourceFiles = reader.sources
		out.SourceVersion = digest(struct {
			Files   []runtime.FileSource
			Entries []InstalledPluginEntry
			Errors  []PluginRegistryIssue
		}{out.SourceFiles, out.Entries, out.Errors})
	}()
	issue := func(path, detail string) {
		out.Complete = false
		out.Errors = append(out.Errors, PluginRegistryIssue{Path: path, Detail: detail})
	}
	if ctx.Err() != nil {
		issue(registry, "插件登记读取已取消或超时")
		return out
	}
	registered, err := reader.read(profile, "plugins/installed_plugins.json", true)
	if err != nil {
		issue(registry, err.Error())
		return out
	}
	if registered == nil {
		return out
	}
	version, validVersion := registered["version"].(json.Number)
	packages, validPackages := registered["plugins"].(map[string]any)
	if !validVersion || version.String() != "2" || !validPackages || len(packages) > 512 {
		issue(registry, "安装登记格式不是受支持的 version 2，或来源超过 512 项")
		return out
	}
	knownPath := filepath.Join(profile, "plugins/known_marketplaces.json")
	known, knownErr := reader.read(profile, "plugins/known_marketplaces.json", true)
	if knownErr != nil {
		issue(knownPath, knownErr.Error())
	}
	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	outputRemaining, count := 2<<20, 0
scan:
	for _, key := range keys {
		parts := strings.Split(key, "@")
		if len(parts) != 2 || !validID(parts[0]) || !validID(parts[1]) || len(key) > 201 {
			issue(registry, "安装登记含无效来源键")
			continue
		}
		rows, ok := packages[key].([]any)
		if !ok || len(rows) == 0 {
			issue(registry, "来源 "+key+" 没有有效安装记录")
			continue
		}
		for _, row := range rows {
			count++
			if count > 512 || ctx.Err() != nil {
				issue(registry, "登记读取已取消或超过 512 条安装记录上限")
				break scan
			}
			entry, err := installedPluginEntry(key, row)
			if err != nil {
				issue(registry, "来源 "+key+" 的安装范围、路径或版本无效")
				continue
			}
			entry.Name = parts[0]
			readInstalledPluginMarket(&entry, known[parts[1]], &reader)
			encoded, _ := json.Marshal(entry)
			outputRemaining -= len(encoded)
			if outputRemaining < 0 {
				issue(registry, "登记展示结果超过 2 MiB 上限")
				break scan
			}
			if entry.Status != "local_declaration" {
				out.Complete = false
			}
			out.Entries = append(out.Entries, entry)
		}
	}
	return out
}

func installedPluginEntry(key string, raw any) (InstalledPluginEntry, error) {
	out := InstalledPluginEntry{SourceKey: key, CacheStatus: "unreadable", MarketKind: "unknown", Status: "unavailable"}
	row, ok := raw.(map[string]any)
	if !ok {
		return out, fmt.Errorf("invalid record")
	}
	out.Scope, _ = row["scope"].(string)
	out.ProjectPath, _ = row["projectPath"].(string)
	if raw, exists := row["projectPath"]; exists {
		if _, ok := raw.(string); !ok {
			return out, fmt.Errorf("invalid project path")
		}
	}
	out.CachePath, _ = row["installPath"].(string)
	out.RegisteredVersion, _ = row["version"].(string)
	if !slices.Contains([]string{"user", "project", "local"}, out.Scope) || !pluginAbsolutePath(out.CachePath) || !pluginText(out.RegisteredVersion, 100) ||
		(out.Scope != "user" && !pluginAbsolutePath(out.ProjectPath)) || (out.ProjectPath != "" && !pluginAbsolutePath(out.ProjectPath)) {
		return out, fmt.Errorf("invalid record")
	}
	info, err := os.Stat(out.CachePath)
	if os.IsNotExist(err) {
		out.CacheStatus = "missing"
	} else if err == nil {
		out.CacheStatus = "not_directory"
		if info.IsDir() {
			out.CacheStatus = "directory"
			out.CacheResolvedPath, _ = filepath.EvalSymlinks(out.CachePath)
		}
	}
	return out, nil
}

func readInstalledPluginMarket(entry *InstalledPluginEntry, raw any, reader *pluginMetadataReader) {
	entry.Detail = "没有可读取的市场登记；不能从安装缓存推断实际加载来源。"
	market, ok := raw.(map[string]any)
	if !ok {
		return
	}
	source, ok := market["source"].(map[string]any)
	if !ok {
		return
	}
	kind, _ := source["source"].(string)
	if slices.Contains([]string{"directory", "file", "github", "git", "url", "npm"}, kind) {
		entry.MarketKind = kind
	}
	if kind != "directory" {
		entry.Status, entry.Detail = "unsupported", "此市场类型尚无本地声明路径核对；不会下载、安装或推断实际加载位置。"
		return
	}
	directory, _ := source["path"].(string)
	location, _ := market["installLocation"].(string)
	if !pluginAbsolutePath(directory) || !pluginAbsolutePath(location) {
		return
	}
	entry.MarketPath, entry.MarketInstallPath = directory, location
	if filepath.Clean(directory) != filepath.Clean(location) {
		entry.Status, entry.Detail = "conflict", "市场来源目录与市场安装目录不同，未选择其中一个作为加载来源。"
		return
	}
	manifest, err := reader.read(directory, ".claude-plugin/marketplace.json", false)
	if err != nil {
		entry.Detail = err.Error()
		return
	}
	marketName, _ := manifest["name"].(string)
	if marketName != strings.Split(entry.SourceKey, "@")[1] {
		entry.Status, entry.Detail = "conflict", "市场文件声明名称与登记来源键不一致。"
		return
	}
	plugins, ok := manifest["plugins"].([]any)
	if !ok || len(plugins) > 2048 {
		entry.Detail = "市场插件声明无效或超过 2048 项上限。"
		return
	}
	matches := []map[string]any{}
	for _, raw := range plugins {
		row, ok := raw.(map[string]any)
		if ok && row["name"] == entry.Name {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		entry.Status, entry.Detail = "conflict", "市场中没有唯一同名插件声明，不能绑定目录。"
		return
	}
	relative, ok := matches[0]["source"].(string)
	if !ok || !pluginText(relative, 4096) || strings.Contains(relative, "\\") || filepath.IsAbs(relative) || filepath.Clean(relative) == ".." || strings.HasPrefix(filepath.Clean(relative), ".."+string(filepath.Separator)) {
		entry.Status, entry.Detail = "unsupported", "插件声明不是市场内的相对目录；未读取外部源或执行来源命令。"
		return
	}
	entry.DeclaredPath = filepath.Join(directory, relative)
	resolved, err := filepath.EvalSymlinks(entry.DeclaredPath)
	marketRoot, marketErr := filepath.EvalSymlinks(directory)
	if err != nil || marketErr != nil {
		entry.Detail = "声明的插件目录缺失或无法解析。"
		return
	}
	within, err := filepath.Rel(marketRoot, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		entry.Status, entry.Detail = "unsupported", "插件目录链接指向市场范围外，未读取该来源。"
		return
	}
	entry.ResolvedPath = resolved
	plugin, err := reader.read(marketRoot, filepath.Join(within, ".claude-plugin/plugin.json"), false)
	if err != nil {
		entry.Detail = err.Error()
		return
	}
	version, versionOK := plugin["version"].(string)
	if plugin["name"] != entry.Name || !versionOK || !pluginText(version, 100) {
		entry.Status, entry.Detail = "conflict", "插件文件的名称或版本声明无效，或与登记名称不同。"
		return
	}
	entry.ManifestVersion = version
	entry.Status, entry.Detail = "local_declaration", "仅核对本地市场与插件文件的声明；不是原生实际加载或启用证据。"
	if version != entry.RegisteredVersion {
		entry.Status, entry.Detail = "version_mismatch", "当前文件声明版本与安装登记版本不同；原执行不会据此自动升级。"
	}
}

func pluginText(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}

func pluginAbsolutePath(value string) bool { return pluginText(value, 4096) && filepath.IsAbs(value) }

type pluginMetadataResult struct {
	object map[string]any
	err    error
}
type pluginMetadataReader struct {
	ctx       context.Context
	remaining int64
	cache     map[string]pluginMetadataResult
	sources   []runtime.FileSource
}

func (r *pluginMetadataReader) read(root, relative string, optional bool) (map[string]any, error) {
	path := filepath.Join(root, relative)
	if r.ctx.Err() != nil {
		return nil, fmt.Errorf("登记元数据读取已取消或超时")
	}
	if cached, ok := r.cache[path]; ok {
		return cached.object, cached.err
	}
	object, err := r.readFile(root, relative, optional)
	r.cache[path] = pluginMetadataResult{object, err}
	return object, err
}

func (r *pluginMetadataReader) readFile(directory, relative string, optional bool) (map[string]any, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("登记元数据所在目录无法读取")
	}
	defer root.Close()
	f, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if optional && os.IsNotExist(err) {
		if _, statErr := root.Lstat(relative); os.IsNotExist(statErr) {
			r.sources = append(r.sources, runtime.FileSource{Path: filepath.Join(directory, relative), Kind: "absent"})
			return nil, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("登记元数据文件无法读取")
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 || before.Size() > r.remaining {
		return nil, fmt.Errorf("元数据不是普通文件，或超过单文件 1 MiB / 总计 4 MiB 上限")
	}
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	r.remaining -= int64(len(body))
	after, statErr := f.Stat()
	current, pathErr := os.Stat(filepath.Join(directory, relative))
	resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(directory, relative))
	resolvedInfo, resolvedErr := os.Stat(resolved)
	if err != nil || statErr != nil || pathErr != nil || !os.SameFile(before, current) || int64(len(body)) != before.Size() ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() ||
		current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) || current.Mode() != before.Mode() || r.remaining < 0 ||
		resolveErr != nil || resolvedErr != nil || !os.SameFile(before, resolvedInfo) {
		return nil, fmt.Errorf("登记元数据在读取时变化或无法完整读取")
	}
	hash := sha256.Sum256(body)
	r.sources = append(r.sources, runtime.FileSource{Path: filepath.Join(directory, relative), ResolvedPath: resolved, Kind: "file", Mode: uint32(before.Mode()), SHA256: hex.EncodeToString(hash[:])})
	object, err := jsonObject(body)
	if err != nil {
		return nil, fmt.Errorf("登记元数据 JSON 无效，未返回正文或解析输入")
	}
	return object, nil
}
