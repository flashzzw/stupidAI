package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// Catalog revisions change admission to future executions. Each execution
// still records its complete CapabilityReference before native dispatch.
type CapabilityVersion struct {
	Asset        CapabilityAsset `json:"asset"`
	Source       *SourceStamp    `json:"source,omitempty"`
	ImportedFrom string          `json:"imported_from,omitempty"`
	CreatedAt    string          `json:"created_at"`
}
type CapabilityEntry struct {
	ID                 string              `json:"id"`
	Revision           uint64              `json:"revision"`
	Disabled           bool                `json:"disabled"`
	Removed            bool                `json:"removed"`
	Versions           []CapabilityVersion `json:"versions"`
	StartupFingerprint string              `json:"startup_fingerprint,omitempty"`
}
type CapabilityUse struct {
	SessionID  string `json:"session_id"`
	Title      string `json:"title"`
	SnapshotID string `json:"snapshot_id"`
	Version    string `json:"version"`
	Phase      string `json:"phase"`
	Source     string `json:"source"`
}
type CapabilityRead struct {
	Asset         CapabilityAsset      `json:"asset"`
	Revision      string               `json:"revision"`
	Removed       bool                 `json:"removed"`
	Management    string               `json:"management"`
	Source        *SourceStamp         `json:"source,omitempty"`
	SourceError   string               `json:"source_error,omitempty"`
	SourceState   string               `json:"source_state"`
	Compatibility string               `json:"compatibility"`
	Detail        string               `json:"detail"`
	SameNameWith  []string             `json:"same_name_with"`
	Uses          []CapabilityUse      `json:"uses"`
	Versions      []CapabilityVersion  `json:"versions"`
	Diagnosis     *CapabilityDiagnosis `json:"diagnosis,omitempty"`
}
type CapabilityChangeRequest struct {
	Action       string           `json:"action"`
	AssetID      string           `json:"asset_id"`
	BaseRevision string           `json:"base_revision"`
	Asset        *CapabilityAsset `json:"asset,omitempty"`
	PreviewHash  string           `json:"preview_hash,omitempty"`
}
type CapabilityChangePreview struct {
	Request     CapabilityChangeRequest `json:"request"`
	PreviewHash string                  `json:"preview_hash"`
	Current     *CapabilityRead         `json:"current,omitempty"`
	Candidate   *CapabilityRead         `json:"candidate,omitempty"`
	Uses        []CapabilityUse         `json:"uses"`
	Boundary    string                  `json:"boundary"`
}

func management(asset CapabilityAsset) string {
	if asset.Management == "managed" {
		return "managed"
	}
	return "external"
}
func latestCapability(entry CapabilityEntry) CapabilityAsset {
	asset := entry.Versions[len(entry.Versions)-1].Asset
	asset.Disabled = entry.Disabled || entry.Removed
	return asset
}
func (a *Application) capabilityAssets() []CapabilityAsset {
	assets := map[string]CapabilityAsset{}
	for _, asset := range a.config.CapabilityAssets {
		assets[asset.ID] = asset
	}
	for id, entry := range a.state.CapabilityLibrary {
		if entry.Removed {
			delete(assets, id)
		} else {
			assets[id] = latestCapability(entry)
		}
	}
	out := make([]CapabilityAsset, 0, len(assets))
	for _, asset := range assets {
		out = append(out, asset)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func checkCapabilityStartup(c Config, library map[string]CapabilityEntry) error {
	for id, entry := range library {
		if id != entry.ID || len(entry.Versions) == 0 {
			return fmt.Errorf("能力资产版本记录不完整：%s", id)
		}
	}
	for _, asset := range c.CapabilityAssets {
		if entry, ok := library[asset.ID]; ok && digest(asset) != entry.StartupFingerprint && digest(asset) != digest(latestCapability(entry)) {
			return fmt.Errorf("能力资产 %s 的启动定义与已保存版本冲突", asset.ID)
		}
	}
	return nil
}

func (a *Application) capabilityUses(id string) []CapabilityUse {
	out := []CapabilityUse{}
	for _, session := range a.state.Sessions {
		add := func(snapshot *ExecutionSnapshot, phase string) {
			if snapshot == nil {
				return
			}
			for _, ref := range snapshot.CapabilityRefs {
				if ref.Asset.ID == id {
					out = append(out, CapabilityUse{SessionID: session.ID, Title: session.Title, SnapshotID: snapshot.ID, Version: ref.Asset.Version, Phase: phase, Source: ref.Source.Path})
				}
			}
		}
		add(session.ExecutionSnapshot, "current")
		for _, snapshot := range session.ExecutionHistory {
			add(snapshot, "history")
		}
		for _, attempt := range session.ExecutionChangeAttempts {
			add(attempt.Change.Snapshot, "failed_attempt")
		}
		if session.PendingExecutionChange != nil {
			add(session.PendingExecutionChange.Snapshot, "pending")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionID+out[i].SnapshotID+out[i].Phase < out[j].SessionID+out[j].SnapshotID+out[j].Phase
	})
	return out
}
func (a *Application) capabilityReadLocked(id string) (CapabilityRead, bool) {
	entry, saved := a.state.CapabilityLibrary[id]
	asset, exists := a.asset(id)
	if saved {
		asset = latestCapability(entry)
		exists = true
	}
	if !exists {
		return CapabilityRead{}, false
	}
	out := CapabilityRead{Asset: asset, Management: management(asset), SourceState: "unverified", Compatibility: "declared_only", Detail: "适用 CLI 为登记声明；原生发现、加载、脚本依赖与权限仍需对应适配器验证", Uses: a.capabilityUses(id), SameNameWith: []string{}, Versions: []CapabilityVersion{}}
	if asset.Kind == "cli_plugin" {
		out.Detail = "CLI 插件按整体登记；其 Skill/MCP/hooks 等隐含组件尚未逐项核对，不能分别声明已排除"
	}
	if saved {
		out.Revision = digest(entry)
		out.Removed = entry.Removed
		out.Versions = entry.Versions
	} else {
		out.Revision = digest(asset)
		out.Versions = []CapabilityVersion{{Asset: asset}}
	}
	for _, other := range a.capabilityAssets() {
		if other.ID != id && other.Name == asset.Name && slices.ContainsFunc(asset.Backends, func(b string) bool { return slices.Contains(other.Backends, b) }) {
			out.SameNameWith = append(out.SameNameWith, other.ID)
		}
	}
	return queryCopy(out), true
}
func diagnoseCapability(item CapabilityRead) CapabilityRead {
	resources, stamp, err := readCapabilityResources(item.Asset)
	if err != nil {
		item.SourceState = "unavailable"
		item.SourceError = err.Error()
		return item
	}
	diagnosis := diagnoseResources(item.Asset, resources)
	item.Diagnosis = &diagnosis
	item.Compatibility = diagnosis.Status
	item.Detail = diagnosis.Boundary
	item.Source = &stamp
	item.SourceState = "readable"
	if len(item.Versions) > 0 {
		last := item.Versions[len(item.Versions)-1]
		if last.Source != nil && *last.Source != stamp {
			item.SourceState = "changed"
			item.Detail += "；当前来源与登记时内容不同"
		}
	}
	return item
}
func (a *Application) ReadCapabilities(ctx context.Context) ([]CapabilityRead, error) {
	a.mu.Lock()
	ids := map[string]bool{}
	for _, asset := range a.config.CapabilityAssets {
		ids[asset.ID] = true
	}
	for id := range a.state.CapabilityLibrary {
		ids[id] = true
	}
	out := make([]CapabilityRead, 0, len(ids))
	for id := range ids {
		item, _ := a.capabilityReadLocked(id)
		out = append(out, item)
	}
	a.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Asset.ID < out[j].Asset.ID })
	for n := range out {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[n] = diagnoseCapability(out[n])
	}
	return out, nil
}

func (a *Application) capabilityChangeBase(r CapabilityChangeRequest) (*CapabilityRead, error) {
	if !validID(r.AssetID) {
		return nil, fmt.Errorf("能力资产 ID 无效")
	}
	current, exists := a.capabilityReadLocked(r.AssetID)
	if r.Action == "register" {
		if exists || r.BaseRevision != "" {
			return nil, fmt.Errorf("能力资产 ID 已存在；更新需要当前版本")
		}
		if len(a.capabilityAssets()) >= 200 {
			return nil, fmt.Errorf("能力资产最多 200 项")
		}
	} else {
		if !exists || current.Revision != r.BaseRevision {
			return nil, fmt.Errorf("能力库版本已变化，请重新预览")
		}
		if current.Removed {
			return nil, fmt.Errorf("此 ID 已移除；历史身份不能重用")
		}
	}
	switch r.Action {
	case "register", "update":
		if r.Asset == nil || r.Asset.ID != r.AssetID || r.Asset.Disabled {
			return nil, fmt.Errorf("登记或更新需要同一 ID 的完整资产定义；停用使用独立操作")
		}
		if err := validateAsset(*r.Asset); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(r.Asset.Source) {
			return nil, fmt.Errorf("资产来源必须是绝对路径")
		}
		if exists {
			for _, version := range current.Versions {
				if version.Asset.Version == r.Asset.Version {
					return nil, fmt.Errorf("资产版本标识已使用；更新必须保存新版本")
				}
			}
			if len(current.Versions) >= 100 {
				return nil, fmt.Errorf("单项资产最多保留 100 个版本")
			}
			if r.Asset.Kind != current.Asset.Kind {
				return nil, fmt.Errorf("改变资产类型需要新 ID")
			}
		}
	case "disable", "enable", "remove":
		if r.Asset != nil {
			return nil, fmt.Errorf("停用、恢复或移除不接受资产定义")
		}
		if r.Action == "disable" && current.Asset.Disabled || r.Action == "enable" && !current.Asset.Disabled {
			return nil, fmt.Errorf("能力登记状态未变化")
		}
	default:
		return nil, fmt.Errorf("未知能力管理操作")
	}
	if exists {
		return &current, nil
	}
	return nil, nil
}
func (a *Application) PreviewCapabilityChange(ctx context.Context, r CapabilityChangeRequest) (CapabilityChangePreview, error) {
	a.mu.Lock()
	current, err := a.capabilityChangeBase(r)
	uses := a.capabilityUses(r.AssetID)
	a.mu.Unlock()
	if err != nil {
		return CapabilityChangePreview{}, err
	}
	r.PreviewHash = ""
	preview := CapabilityChangePreview{Request: r, Current: current, Uses: uses, Boundary: "登记变更只影响新的引用。旧快照、待应用版本和原生执行保持原绑定；外部文件的编辑会独立影响引用它们的执行。"}
	if current != nil {
		*current = diagnoseCapability(*current)
	}
	if r.Asset != nil {
		asset := *r.Asset
		asset.Source = filepath.Clean(asset.Source)
		if asset.Management == "external" {
			asset.Management = ""
		}
		r.Asset = &asset
		preview.Request = r
		candidate := diagnoseCapability(CapabilityRead{Asset: asset, Management: management(asset), Compatibility: "declared_only", Detail: "仅登记引用；不证明已安装或已加载", Versions: []CapabilityVersion{}, SameNameWith: []string{}, Uses: uses})
		if management(asset) == "managed" {
			if candidate.SourceError != "" {
				return preview, fmt.Errorf("导入固定版本失败：%s", candidate.SourceError)
			}
			if asset.CodexMCP != "" {
				return preview, fmt.Errorf("当前原生 Codex MCP 映射依赖原 profile 文件，不能通过复制该文件声明原生选配已生效")
			}
			if err := validateManagedAssetSource(asset.Source); err != nil {
				return preview, err
			}
			preview.Boundary = "将所选资源复制为控制台管理的固定版本；不修改原目录、不执行脚本、不安装到 CLI。旧版本和全部执行引用保留。"
		}
		preview.Candidate = &candidate
	}
	if err := ctx.Err(); err != nil {
		return preview, err
	}
	// Hash only meaningful review state, not observation timestamps.
	preview.PreviewHash = digest(preview)
	return preview, nil
}

func (a *Application) manageCapability(command Command) (CommandReceipt, error) {
	receipt := CommandReceipt{ID: command.ID, Hash: digest(command), Status: "saved"}
	allowed := Command{ID: command.ID, Kind: "manage_capability", CapabilityChange: command.CapabilityChange}
	if !validID(command.ID) || command.CapabilityChange == nil || digest(allowed) != receipt.Hash {
		return receipt, fmt.Errorf("能力管理仅接受 command_id、kind 与 capability_change")
	}
	a.mu.Lock()
	old, done := a.state.Commands[command.ID]
	a.mu.Unlock()
	if done {
		if old.Hash != receipt.Hash {
			return receipt, fmt.Errorf("command_id 已用于其他内容")
		}
		return old, nil
	}
	request := *command.CapabilityChange
	preview, err := a.PreviewCapabilityChange(a.ctx, request)
	if err != nil {
		a.mu.Lock()
		old, done := a.state.Commands[command.ID]
		a.mu.Unlock()
		if done && old.Hash == receipt.Hash {
			return old, nil
		}
		return receipt, err
	}
	if request.PreviewHash == "" || request.PreviewHash != preview.PreviewHash {
		return receipt, fmt.Errorf("能力来源、引用或库状态已变化，请重新预览")
	}
	importedDirectory := ""
	persisted := false
	defer func() {
		if !persisted && importedDirectory != "" {
			_ = os.RemoveAll(importedDirectory)
		}
	}()
	var version *CapabilityVersion
	if preview.Candidate != nil {
		candidate := preview.Candidate
		version = &CapabilityVersion{Asset: candidate.Asset, Source: candidate.Source, CreatedAt: now()}
		if candidate.Management == "managed" {
			imported, source, err := a.copyManagedCapability(candidate.Asset, *candidate.Source)
			if err != nil {
				return receipt, err
			}
			importedDirectory = filepath.Dir(imported)
			version.ImportedFrom = candidate.Asset.Source
			version.Asset.Source = imported
			version.Source = &source
		}
	}
	result, committed, err := a.commitCapabilityChange(command, preview, version)
	persisted = committed
	return result, err
}

// Resource imports and local-directory registrations share one catalog transaction.
func (a *Application) commitCapabilityChange(command Command, preview CapabilityChangePreview, version *CapabilityVersion) (CommandReceipt, bool, error) {
	receipt := CommandReceipt{ID: command.ID, Hash: digest(command), Status: "saved"}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return receipt, false, fmt.Errorf("控制台正在关闭")
	}
	if a.fatal != nil {
		return receipt, false, a.fatal
	}
	if old, done := a.state.Commands[command.ID]; done {
		if old.Hash != receipt.Hash {
			return receipt, false, fmt.Errorf("command_id 已用于其他内容")
		}
		return old, false, nil
	}
	current, err := a.capabilityChangeBase(preview.Request)
	if err != nil {
		return receipt, false, err
	}
	if digest(a.capabilityUses(preview.Request.AssetID)) != digest(preview.Uses) {
		return receipt, false, fmt.Errorf("资产引用已变化，请重新预览影响")
	}
	next := clone(a.state)
	if next.CapabilityLibrary == nil {
		next.CapabilityLibrary = map[string]CapabilityEntry{}
	}
	entry, exists := next.CapabilityLibrary[preview.Request.AssetID]
	if !exists {
		entry = CapabilityEntry{ID: preview.Request.AssetID, Versions: []CapabilityVersion{}}
		if current != nil {
			entry.StartupFingerprint = digest(current.Asset)
			entry.Disabled = current.Asset.Disabled
			entry.Versions = []CapabilityVersion{{Asset: current.Asset, Source: preview.Current.Source}}
		}
	}
	entry.Revision++
	if version != nil {
		entry.Versions = append(entry.Versions, *version)
	}
	switch preview.Request.Action {
	case "disable":
		entry.Disabled = true
	case "enable":
		entry.Disabled = false
	case "remove":
		entry.Removed = true
	}
	next.CapabilityLibrary[preview.Request.AssetID] = entry
	receipt.CapabilityID = preview.Request.AssetID
	next.Commands[command.ID] = receipt
	if err = a.commit(next, "capability."+preview.Request.Action, ""); err != nil {
		return CommandReceipt{}, false, err
	}
	return receipt, true, nil
}

// A portable catalog intentionally omits source paths and file contents. Native
// credential/configuration files are not a safe resource export format.
type CapabilityCatalogExport struct {
	Format string                 `json:"format"`
	Assets []CapabilityExportItem `json:"assets"`
}
type CapabilityExportItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Version        string   `json:"version"`
	Backends       []string `json:"backends"`
	Management     string   `json:"management"`
	SHA256         string   `json:"sha256,omitempty"`
	SourceRequired bool     `json:"source_required"`
}

func (a *Application) ExportCapabilityCatalog(ctx context.Context) (CapabilityCatalogExport, error) {
	items, err := a.ReadCapabilities(ctx)
	if err != nil {
		return CapabilityCatalogExport{}, err
	}
	out := CapabilityCatalogExport{Format: "netcap.capability-catalog.v1", Assets: []CapabilityExportItem{}}
	for _, item := range items {
		if item.Removed {
			continue
		}
		asset := item.Asset
		entry := CapabilityExportItem{ID: asset.ID, Name: asset.Name, Kind: asset.Kind, Version: asset.Version, Backends: asset.Backends, Management: item.Management, SourceRequired: true}
		if item.Source != nil {
			entry.SHA256 = item.Source.SHA256
		}
		out.Assets = append(out.Assets, entry)
	}
	return out, nil
}
