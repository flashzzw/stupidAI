package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type CapabilityHookInspectionRequest struct {
	AssetID      string `json:"asset_id"`
	Revision     string `json:"revision"`
	SourceSHA256 string `json:"source_sha256"`
	InstanceID   string `json:"instance_id"`
}

type CapabilityHookInspection struct {
	InspectionID string                          `json:"inspection_id"`
	Request      CapabilityHookInspectionRequest `json:"request"`
	AssetVersion string                          `json:"asset_version"`
	Source       SourceStamp                     `json:"source"`
	Inspection   runtime.PluginHookInspection    `json:"inspection"`
}

type capabilityHookReceipt struct {
	Result       CapabilityHookInspection
	InstanceHash string
	PackageHash  string
	Expires      time.Time
}

func (a *Application) InspectCapabilityHooks(ctx context.Context, request CapabilityHookInspectionRequest) (CapabilityHookInspection, error) {
	var empty CapabilityHookInspection
	a.mu.Lock()
	item, exists := a.capabilityReadLocked(request.AssetID)
	var instance runtime.Instance
	for _, candidate := range a.config.Instances {
		if candidate.ID == request.InstanceID {
			instance = queryCopy(candidate)
		}
	}
	reader, supported := a.runtime.(runtime.PluginHookInspector)
	if a.closed || !exists || item.Removed || item.Revision != request.Revision || item.Asset.Kind != "cli_plugin" || !slices.Contains(item.Asset.Backends, "codex") || instance.Kind != "codex" || request.SourceSHA256 == "" {
		a.mu.Unlock()
		return empty, fmt.Errorf("插件登记、来源版本或 Codex 实例不可核对，请刷新能力库")
	}
	if !supported {
		a.mu.Unlock()
		return empty, fmt.Errorf("当前运行时尚未接通独立插件 hook 定义读取")
	}
	key := "hook-inspection/" + request.AssetID + "/" + request.InstanceID
	active := 0
	for name, busy := range a.observing {
		if busy && strings.HasPrefix(name, "hook-inspection/") {
			active++
		}
	}
	if a.observing[key] || active >= 2 {
		a.mu.Unlock()
		return empty, fmt.Errorf("已有插件定义读取正在进行，请等待当前读取完成")
	}
	a.observing[key] = true
	a.wg.Add(1)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.observing, key)
		a.mu.Unlock()
		a.wg.Done()
	}()
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	stop := context.AfterFunc(a.ctx, cancel)
	defer func() { stop(); cancel() }()
	tree, stamp, err := readCapabilityResources(item.Asset)
	if err != nil {
		return empty, err
	}
	if tree.Kind != "directory" || stamp.SHA256 != request.SourceSHA256 {
		return empty, fmt.Errorf("插件资源版本已变化，请刷新来源后重新读取")
	}
	if len(item.Versions) > 0 {
		registered := item.Versions[len(item.Versions)-1].Source
		if registered != nil && *registered != stamp {
			return empty, fmt.Errorf("当前文件与登记版本不同，请先核对并更新登记版本")
		}
	}
	pkg, err := capabilityHookPackage(item.Asset, tree, stamp)
	if err != nil {
		return empty, err
	}
	out, err := reader.InspectPluginHooks(check, queryCopy(instance), queryCopy(pkg))
	if err != nil {
		return empty, err
	}
	if err := runtime.ValidatePluginHookInspection(instance, pkg, out); err != nil {
		return empty, err
	}
	afterTree, after, err := readCapabilityResources(item.Asset)
	if err != nil || after != stamp || afterTree.Mode != tree.Mode {
		return empty, fmt.Errorf("读取期间插件内容或权限已变化，未返回旧定义")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.capabilityReadLocked(request.AssetID)
	instanceMatches := false
	for _, candidate := range a.config.Instances {
		instanceMatches = instanceMatches || candidate.ID == instance.ID && digest(candidate) == digest(instance)
	}
	if a.closed || check.Err() != nil || !exists || current.Removed || current.Revision != request.Revision || !instanceMatches {
		return empty, fmt.Errorf("读取期间能力登记或实例配置已变化，请重新读取")
	}
	result := CapabilityHookInspection{InspectionID: uuid.NewString(), Request: request, AssetVersion: item.Asset.Version, Source: stamp, Inspection: queryCopy(out)}
	if a.hookInspections == nil {
		a.hookInspections = map[string]capabilityHookReceipt{}
	}
	for id, receipt := range a.hookInspections {
		if time.Now().After(receipt.Expires) {
			delete(a.hookInspections, id)
		}
	}
	if len(a.hookInspections) >= 64 {
		oldest := ""
		for id, receipt := range a.hookInspections {
			if oldest == "" || receipt.Expires.Before(a.hookInspections[oldest].Expires) {
				oldest = id
			}
		}
		delete(a.hookInspections, oldest)
	}
	a.hookInspections[result.InspectionID] = capabilityHookReceipt{Result: queryCopy(result), InstanceHash: digest(instance), PackageHash: digest(pkg), Expires: time.Now().Add(15 * time.Minute)}
	return result, nil
}

// Called under the Application lock. Only a server-owned inspection can grant
// a version-specific review; arbitrary browser hashes never become native trust.
func (a *Application) bindCodexHookReview(scope runtime.Scope, ref CapabilityReference, pick CapabilitySelection, pack *runtime.PluginSelection) error {
	if pick.hookBinding != nil {
		stored, current := queryCopy(*pick.hookBinding), queryCopy(*pack)
		stored.Mode, stored.Hooks, current.Hooks = current.Mode, nil, nil
		if digest(stored) != digest(current) || pick.hookBinding.Hooks == nil {
			return fmt.Errorf("已保存 hook 审核的完整资源绑定不一致")
		}
		pack.Hooks = queryCopy(pick.hookBinding.Hooks)
		return runtime.ValidatePluginHookSelection(scope.Instance, *pack)
	}
	if pack.Hooks == nil {
		if pick.HookReview != nil {
			return fmt.Errorf("此插件没有可审核的 hook 定义")
		}
		return nil
	}
	if pick.HookReview == nil || (pick.HookReview.Trust != "trusted" && pick.HookReview.Trust != "untrusted") {
		return fmt.Errorf("含 hook 的插件需要先读取当前包定义，并明确选择该版本的信任状态")
	}
	receipt, ok := a.hookInspections[pick.HookReview.InspectionID]
	item, exists := a.capabilityReadLocked(ref.Asset.ID)
	r := receipt.Result
	packageHash := digest(runtime.PluginHookPackage{AssetID: pack.AssetID, Name: pack.Name, Version: pack.Version, SourcePath: pack.SourcePath, SourceHash: pack.SourceHash, RootMode: pack.RootMode, Files: pack.Files, Expected: pack.Hooks.Expected})
	if !ok || time.Now().After(receipt.Expires) || !exists || item.Removed || item.Revision != r.Request.Revision ||
		r.Request.AssetID != ref.Asset.ID || r.AssetVersion != ref.Asset.Version || r.Source != ref.Source ||
		r.Request.InstanceID != scope.Instance.ID || receipt.InstanceHash != digest(scope.Instance) || receipt.PackageHash != packageHash {
		return fmt.Errorf("hook 审核依据已过期，或资产版本、来源、实例已变化；请重新读取")
	}
	for _, definition := range r.Inspection.Definitions {
		if definition.Background {
			return fmt.Errorf("异步 hook 的执行与恢复尚未核对，不能启用含该组件的插件")
		}
	}
	pack.Hooks.Trust = pick.HookReview.Trust
	pack.Hooks.Inspection = queryCopy(r.Inspection)
	return runtime.ValidatePluginHookSelection(scope.Instance, *pack)
}

var codexHookEvents = map[string]string{
	"SessionStart": "sessionStart", "SessionEnd": "sessionEnd", "PreToolUse": "preToolUse", "PostToolUse": "postToolUse",
	"PermissionRequest": "permissionRequest", "UserPromptSubmit": "userPromptSubmit", "Stop": "stop", "Interrupt": "interrupt",
	"PreCompact": "preCompact", "PostCompact": "postCompact", "SubagentStart": "subagentStart", "SubagentStop": "subagentStop",
}

func capabilityHookPackage(asset CapabilityAsset, tree capabilityResources, stamp SourceStamp) (runtime.PluginHookPackage, error) {
	var out runtime.PluginHookPackage
	manifest, issue := resolvePluginManifest(tree, "codex")
	if issue != nil {
		return out, fmt.Errorf("插件 manifest 无效，未启动原生读取")
	}
	if manifest.Source.Layout != "legacy" {
		return out, fmt.Errorf("固定 Codex 0.158.0 尚未返回已验证的通用插件 hook 定义；不能把空清单当作已核对")
	}
	diagnosis := diagnoseResources(asset, tree)
	if diagnosis.Status == "invalid" {
		return out, fmt.Errorf("插件结构或依赖无效，请先核对能力库诊断")
	}
	out.AssetID, out.SourcePath, out.SourceHash, out.RootMode = asset.ID, stamp.Resolved, stamp.SHA256, tree.Mode
	out.Name, _ = manifest.Identity["name"].(string)
	out.Version, _ = manifest.Identity["version"].(string)
	for _, file := range tree.Entries {
		out.Files = append(out.Files, runtime.PluginFile{Path: file.Path, Kind: file.Kind, Mode: file.Mode, SHA256: file.SHA256})
	}
	files := tree.files()
	total := 0
	for _, component := range diagnosis.Components {
		if component.Backend != "codex" || component.Kind != "hook" {
			continue
		}
		if component.Path == manifest.Source.Identity || !resourcePath(component.Path) {
			return out, fmt.Errorf("此 hook 内联声明尚未核对；当前读取需要包内 hook JSON 文件")
		}
		event, known := codexHookEvents[component.Name]
		if !known {
			return out, fmt.Errorf("此 hook 事件尚未核对，未返回部分定义")
		}
		object, err := jsonObject(files[component.Path])
		if err != nil {
			return out, fmt.Errorf("hook JSON 文件不可解析")
		}
		events, _ := object["hooks"].(map[string]any)
		rows, ok := events[component.Name].([]any)
		if !ok {
			return out, fmt.Errorf("hook 事件需要匹配项数组")
		}
		count := 0
		for _, row := range rows {
			group, ok := row.(map[string]any)
			if !ok {
				return out, fmt.Errorf("hook 匹配项无效")
			}
			handlers, ok := group["hooks"].([]any)
			if !ok || len(handlers) == 0 {
				return out, fmt.Errorf("hook 匹配项缺少执行定义")
			}
			for _, raw := range handlers {
				handler, ok := raw.(map[string]any)
				if !ok || handler["type"] != "command" {
					return out, fmt.Errorf("当前仅核对 command 类型的 hook 定义")
				}
				count++
			}
		}
		if count == 0 {
			return out, fmt.Errorf("hook 声明没有可核对的执行定义")
		}
		total += count
		out.Expected = append(out.Expected, runtime.PluginHookExpectation{Path: component.Path, Event: event, Count: count})
	}
	if total < 1 || total > 512 {
		return out, fmt.Errorf("此插件需要 1 至 512 个可核对的 command hook 定义")
	}
	return out, nil
}
