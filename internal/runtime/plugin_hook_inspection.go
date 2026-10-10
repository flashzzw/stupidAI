package runtime

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// PluginHookPackage describes immutable resources, not an execution selection.
// The caller admits the registered asset; the reader rechecks every file.
type PluginHookPackage struct {
	AssetID, Name, Version, SourcePath, SourceHash string
	RootMode                                       uint32
	Files                                          []PluginFile
	Expected                                       []PluginHookExpectation
}

type PluginHookExpectation struct {
	Path  string `json:"path"`
	Event string `json:"event"`
	Count int    `json:"count"`
}

type PluginHookDefinition struct {
	Key            string `json:"key"`
	Path           string `json:"path"`
	Event          string `json:"event"`
	Type           string `json:"type"`
	Matcher        string `json:"matcher,omitempty"`
	DefinitionHash string `json:"definition_hash"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	DisplayOrder   int64  `json:"display_order"`
	Background     bool   `json:"background"`
}

type PluginHookInspection struct {
	Scope         string                 `json:"scope"`
	Source        string                 `json:"source"`
	InstanceID    string                 `json:"instance_id"`
	ServerVersion string                 `json:"server_version"`
	NativeSource  string                 `json:"native_source"`
	SourceHash    string                 `json:"source_hash"`
	At            string                 `json:"at"`
	Definitions   []PluginHookDefinition `json:"definitions"`
}

type PluginHookInspector interface {
	InspectPluginHooks(context.Context, Instance, PluginHookPackage) (PluginHookInspection, error)
}

func (a *Adapters) InspectPluginHooks(ctx context.Context, instance Instance, pkg PluginHookPackage) (PluginHookInspection, error) {
	adapter, err := a.adapter(instance.Kind)
	if err != nil {
		return PluginHookInspection{}, err
	}
	reader, ok := adapter.(PluginHookInspector)
	if !ok {
		return PluginHookInspection{}, fmt.Errorf("此后端尚无独立插件 hook 定义读取入口")
	}
	return reader.InspectPluginHooks(ctx, instance, pkg)
}

func inspectionPlugin(pkg PluginHookPackage, profile string) (PluginSelection, error) {
	p := PluginSelection{AssetID: pkg.AssetID, Name: pkg.Name, Version: pkg.Version,
		SourcePath: pkg.SourcePath, SourceHash: pkg.SourceHash, RootMode: pkg.RootMode, Files: pkg.Files}
	var err error
	p.NativeSource, p.CachePath, err = CodexPluginLocation(Scope{SessionID: "hook-inspection", Instance: Instance{Kind: "codex", Profile: profile}}, p)
	return p, err
}

func hookExpectationCounts(pkg PluginHookPackage) (map[string]int, error) {
	counts := map[string]int{}
	total := 0
	files := map[string]bool{}
	for _, file := range pkg.Files {
		if file.Kind == "file" {
			files[file.Path] = true
		}
	}
	for _, entry := range pkg.Expected {
		key := entry.Path + "\x00" + entry.Event
		if !files[entry.Path] || path.Clean(entry.Path) != entry.Path || !validInspectionText(entry.Event, 100) || entry.Count < 1 || counts[key] != 0 {
			return nil, fmt.Errorf("插件 hook 声明缺失或不能唯一核对")
		}
		counts[key] = entry.Count
		total += entry.Count
	}
	if total < 1 || total > 512 || len(pkg.Files) == 0 || len(pkg.Files) > 2048 || pkg.RootMode > 0777 || !hookDefinitionHash.MatchString(pkg.SourceHash) {
		return nil, fmt.Errorf("插件 hook 定义或完整资源清单超出读取范围")
	}
	return counts, nil
}

func validInspectionText(s string, maximum int) bool {
	return s != "" && len(s) <= maximum && !strings.ContainsRune(s, '\x00')
}

// ValidatePluginHookInspection is also used at the Application interface. It
// accepts complete package definitions, never current trust or session policy.
func ValidatePluginHookInspection(instance Instance, pkg PluginHookPackage, out PluginHookInspection) error {
	p, err := inspectionPlugin(pkg, string(filepath.Separator))
	if err != nil {
		return err
	}
	counts, err := hookExpectationCounts(pkg)
	if err != nil {
		return err
	}
	if instance.Kind != "codex" || out.Scope != "package" || out.Source != "codex.isolated.hooks/list" || out.InstanceID != instance.ID || out.ServerVersion != "0.158.0" || out.NativeSource != p.NativeSource || out.SourceHash != pkg.SourceHash || len(out.Definitions) > 512 {
		return fmt.Errorf("原生 hook 定义不属于本次包版本或读取范围")
	}
	if _, err := time.Parse(time.RFC3339Nano, out.At); err != nil {
		return fmt.Errorf("原生 hook 定义缺少有效读取时间")
	}
	seen := map[string]bool{}
	for _, h := range out.Definitions {
		key := h.Path + "\x00" + h.Event
		if !validInspectionText(h.Key, 4096) || !strings.HasPrefix(h.Key, p.NativeSource+":") || seen[h.Key] || counts[key] < 1 || h.Type != "command" || len(h.Matcher) > 4000 || strings.ContainsRune(h.Matcher, '\x00') || h.TimeoutSeconds < 0 || h.TimeoutSeconds > 1<<53 || h.DisplayOrder < 0 || !strings.HasPrefix(h.DefinitionHash, "sha256:") || !hookDefinitionHash.MatchString(strings.TrimPrefix(h.DefinitionHash, "sha256:")) {
			return fmt.Errorf("原生 hook 定义缺失、重复或与包声明不一致")
		}
		seen[h.Key] = true
		counts[key]--
	}
	for _, count := range counts {
		if count != 0 {
			return fmt.Errorf("原生未返回完整的已声明 hook；不能用空清单认定准备完成")
		}
	}
	return nil
}
