package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const capabilityPackageFormat = "netcap.capability.v1"
const capabilityArchiveLimit = 40 << 20

type PackageAsset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Version  string   `json:"version"`
	Backends []string `json:"backends"`
}
type CapabilityPackageManifest struct {
	Format   string          `json:"format"`
	Asset    PackageAsset    `json:"asset"`
	RootName string          `json:"root_name"`
	RootKind string          `json:"root_kind"`
	RootMode uint32          `json:"root_mode"`
	Entries  []ResourceEntry `json:"entries"`
}
type CapabilityPackageExportRequest struct {
	AssetID     string `json:"asset_id"`
	Revision    string `json:"revision"`
	Version     string `json:"version"`
	PreviewHash string `json:"preview_hash,omitempty"`
}
type CapabilityPackagePreview struct {
	Asset         PackageAsset        `json:"asset"`
	Revision      string              `json:"revision"`
	Version       string              `json:"version"`
	Files         []ResourceEntry     `json:"files"`
	Bytes         int64               `json:"bytes"`
	Diagnosis     CapabilityDiagnosis `json:"diagnosis"`
	PreviewHash   string              `json:"preview_hash"`
	ArchiveSHA256 string              `json:"archive_sha256,omitempty"`
	Uses          []CapabilityUse     `json:"uses"`
}
type CapabilityPackageImportRequest struct {
	Archive      []byte `json:"archive"`
	AssetID      string `json:"asset_id"`
	Action       string `json:"action"`
	BaseRevision string `json:"base_revision"`
	PreviewHash  string `json:"preview_hash,omitempty"`
}

func packageAsset(asset CapabilityAsset) PackageAsset {
	return PackageAsset{asset.ID, asset.Name, asset.Kind, asset.Version, append([]string{}, asset.Backends...)}
}
func (asset PackageAsset) capability(source string) CapabilityAsset {
	return CapabilityAsset{ID: asset.ID, Name: asset.Name, Kind: asset.Kind, Version: asset.Version, Backends: asset.Backends, Source: source, Management: "managed"}
}
func packagePreview(asset CapabilityAsset, tree capabilityResources) CapabilityPackagePreview {
	out := CapabilityPackagePreview{Asset: packageAsset(asset), Version: asset.Version, Files: append([]ResourceEntry{}, tree.Entries...), Diagnosis: diagnoseResources(asset, tree), Uses: []CapabilityUse{}}
	for n, entry := range out.Files {
		out.Bytes += int64(len(entry.Data))
		out.Files[n].Data = nil
	}
	return out
}
func requirePortable(preview CapabilityPackagePreview) error {
	if preview.Diagnosis.Portable && preview.Diagnosis.Status != "invalid" {
		return nil
	}
	for _, issue := range preview.Diagnosis.Issues {
		if issue.Severity == "error" || issue.Code == "external_dependency" || issue.Code == "native_state" {
			return fmt.Errorf("资源包不能生成或导入（%s）：%s：%s", issue.Code, issue.Path, issue.Detail)
		}
	}
	return fmt.Errorf("资源包尚不能通过可移植性核对")
}
func (a *Application) prepareCapabilityExport(ctx context.Context, r CapabilityPackageExportRequest) (CapabilityPackagePreview, capabilityResources, error) {
	a.mu.Lock()
	item, exists := a.capabilityReadLocked(r.AssetID)
	a.mu.Unlock()
	if !exists || item.Revision != r.Revision {
		return CapabilityPackagePreview{}, capabilityResources{}, fmt.Errorf("能力登记版本已变化，请重新预览导出")
	}
	var selected *CapabilityVersion
	for _, version := range item.Versions {
		if version.Asset.Version == r.Version {
			copy := version
			selected = &copy
			break
		}
	}
	if selected == nil {
		return CapabilityPackagePreview{}, capabilityResources{}, fmt.Errorf("资产版本不存在")
	}
	tree, stamp, err := readCapabilityResources(selected.Asset)
	if err != nil {
		return CapabilityPackagePreview{}, tree, err
	}
	if management(selected.Asset) == "managed" && (selected.Source == nil || *selected.Source != stamp) {
		return CapabilityPackagePreview{}, tree, fmt.Errorf("固定资源版本已变化，不能导出成原版本")
	}
	preview := packagePreview(selected.Asset, tree)
	preview.Revision = item.Revision
	preview.Uses = item.Uses
	preview.PreviewHash = digest(struct {
		Preview CapabilityPackagePreview
		Source  SourceStamp
	}{preview, stamp})
	return preview, tree, ctx.Err()
}
func (a *Application) PreviewCapabilityPackageExport(ctx context.Context, r CapabilityPackageExportRequest) (CapabilityPackagePreview, error) {
	preview, _, err := a.prepareCapabilityExport(ctx, r)
	return preview, err
}
func (a *Application) ExportCapabilityPackage(ctx context.Context, r CapabilityPackageExportRequest) ([]byte, error) {
	preview, tree, err := a.prepareCapabilityExport(ctx, r)
	if err != nil {
		return nil, err
	}
	if r.PreviewHash == "" || r.PreviewHash != preview.PreviewHash {
		return nil, fmt.Errorf("导出预览已过期，请核对当前资源")
	}
	if err = requirePortable(preview); err != nil {
		return nil, err
	}
	manifest := CapabilityPackageManifest{Format: capabilityPackageFormat, Asset: preview.Asset, RootName: tree.Name, RootKind: tree.Kind, RootMode: tree.Mode, Entries: preview.Files}
	return encodeCapabilityPackage(manifest, tree)
}
func encodeCapabilityPackage(manifest CapabilityPackageManifest, tree capabilityResources) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	write := func(name string, mode os.FileMode, body []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(mode)
		header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = entry.Write(body)
		return err
	}
	metadata, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err = write("netcap-capability.json", 0600, metadata); err != nil {
		return nil, err
	}
	for _, entry := range tree.Entries {
		if entry.Kind == "directory" {
			continue
		}
		if err = write("resources/"+entry.Path, os.FileMode(entry.Mode), entry.Data); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > capabilityArchiveLimit {
		return nil, fmt.Errorf("资源包超过大小上限")
	}
	return buffer.Bytes(), nil
}
func strictPackageJSON(data []byte, out any) error {
	if err := uniqueJSONKeys(data); err != nil {
		return fmt.Errorf("资源包清单包含重复字段或无效 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("资源包清单格式无效")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("资源包清单必须只有一个 JSON 对象")
	}
	return nil
}
func decodeCapabilityPackage(data []byte) (CapabilityPackageManifest, capabilityResources, error) {
	var manifest CapabilityPackageManifest
	var tree capabilityResources
	if len(data) == 0 || len(data) > capabilityArchiveLimit {
		return manifest, tree, fmt.Errorf("资源包大小无效或超过 40 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return manifest, tree, fmt.Errorf("资源包不是有效 ZIP")
	}
	if len(reader.File) > capabilityMaxEntries+1 {
		return manifest, tree, fmt.Errorf("资源包文件数超过上限")
	}
	files := map[string][]byte{}
	modes := map[string]uint32{}
	folded := map[string]bool{}
	var total int64
	for _, file := range reader.File {
		if !resourcePath(file.Name) || folded[strings.ToLower(file.Name)] || file.Mode().Type() != 0 || file.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return manifest, tree, fmt.Errorf("资源包有不安全、重复路径或特殊文件")
		}
		if file.Name != "netcap-capability.json" && !strings.HasPrefix(file.Name, "resources/") {
			return manifest, tree, fmt.Errorf("资源包含清单外路径")
		}
		folded[strings.ToLower(file.Name)] = true
		limit := capabilityMaxBytes - total
		if file.Name == "netcap-capability.json" {
			limit = 1 << 20
		}
		if file.UncompressedSize64 > uint64(limit) {
			return manifest, tree, fmt.Errorf("资源包解压内容超过上限")
		}
		stream, e := file.Open()
		if e != nil {
			return manifest, tree, fmt.Errorf("无法读取资源包成员")
		}
		body, e := io.ReadAll(io.LimitReader(stream, limit+1))
		stream.Close()
		if e != nil || int64(len(body)) > limit {
			return manifest, tree, fmt.Errorf("资源包成员校验失败或超过上限")
		}
		files[file.Name] = body
		modes[file.Name] = uint32(file.Mode().Perm())
		if file.Name != "netcap-capability.json" {
			total += int64(len(body))
		}
	}
	metadata, ok := files["netcap-capability.json"]
	if !ok {
		return manifest, tree, fmt.Errorf("缺少资源包清单")
	}
	if err = strictPackageJSON(metadata, &manifest); err != nil {
		return manifest, tree, err
	}
	if manifest.Format != capabilityPackageFormat || (manifest.RootKind != "file" && manifest.RootKind != "directory") || !resourcePath(manifest.RootName) || strings.Contains(manifest.RootName, "/") || manifest.RootMode > 0777 || len(manifest.Entries) > capabilityMaxEntries {
		return manifest, tree, fmt.Errorf("资源包格式或根目录声明无效")
	}
	if err = validateAsset(manifest.Asset.capability("/package")); err != nil {
		return manifest, tree, err
	}
	tree = capabilityResources{Name: manifest.RootName, Kind: manifest.RootKind, Mode: manifest.RootMode, Entries: []ResourceEntry{}}
	entries := map[string]string{}
	folded = map[string]bool{}
	for _, entry := range manifest.Entries {
		if !resourcePath(entry.Path) || folded[strings.ToLower(entry.Path)] || entry.Mode > 0777 || (entry.Kind != "file" && entry.Kind != "directory") {
			return manifest, tree, fmt.Errorf("资源包成员声明无效或重复")
		}
		folded[strings.ToLower(entry.Path)] = true
		entries[entry.Path] = entry.Kind
		if entry.Kind == "file" {
			body, ok := files["resources/"+entry.Path]
			if !ok || bytesHash(body) != entry.SHA256 || entry.Mode != modes["resources/"+entry.Path] {
				return manifest, tree, fmt.Errorf("资源包文件内容或权限与清单不一致")
			}
			entry.Data = body
			delete(files, "resources/"+entry.Path)
		} else if entry.SHA256 != "" {
			return manifest, tree, fmt.Errorf("目录不能声明文件摘要")
		}
		tree.Entries = append(tree.Entries, entry)
	}
	if len(files) != 1 {
		return manifest, tree, fmt.Errorf("资源包有未声明的文件")
	}
	for _, entry := range tree.Entries {
		parent := path.Dir(entry.Path)
		if parent != "." && entries[parent] != "directory" {
			return manifest, tree, fmt.Errorf("资源包成员缺少父目录声明")
		}
	}
	if tree.Kind == "file" && (len(tree.Entries) != 1 || tree.Entries[0].Kind != "file" || tree.Entries[0].Path != tree.Name || tree.Entries[0].Mode != tree.Mode) {
		return manifest, tree, fmt.Errorf("单文件资源包布局不一致")
	}
	sort.Slice(tree.Entries, func(i, j int) bool { return tree.Entries[i].Path < tree.Entries[j].Path })
	return manifest, tree, nil
}
func (a *Application) prepareCapabilityPackageImport(ctx context.Context, r CapabilityPackageImportRequest) (CapabilityPackagePreview, CapabilityChangePreview, capabilityResources, error) {
	manifest, tree, err := decodeCapabilityPackage(r.Archive)
	if err != nil {
		return CapabilityPackagePreview{}, CapabilityChangePreview{}, tree, err
	}
	if r.Action != "register" && r.Action != "update" {
		return CapabilityPackagePreview{}, CapabilityChangePreview{}, tree, fmt.Errorf("资源包导入仅支持 register/update")
	}
	if r.AssetID != "" {
		manifest.Asset.ID = r.AssetID
	}
	asset := manifest.Asset.capability("/package/" + bytesHash(r.Archive))
	change := CapabilityChangeRequest{Action: r.Action, AssetID: asset.ID, BaseRevision: r.BaseRevision, Asset: &asset}
	a.mu.Lock()
	current, err := a.capabilityChangeBase(change)
	uses := a.capabilityUses(asset.ID)
	a.mu.Unlock()
	if err != nil {
		return CapabilityPackagePreview{}, CapabilityChangePreview{}, tree, err
	}
	preview := packagePreview(asset, tree)
	preview.ArchiveSHA256 = bytesHash(r.Archive)
	preview.Revision = r.BaseRevision
	preview.Uses = uses
	preview.PreviewHash = digest(struct {
		Preview CapabilityPackagePreview
		Action  string
	}{preview, r.Action})
	if current != nil {
		*current = diagnoseCapability(*current)
	}
	catalog := CapabilityChangePreview{Request: change, Current: current, Uses: uses}
	return preview, catalog, tree, ctx.Err()
}
func (a *Application) PreviewCapabilityPackageImport(ctx context.Context, r CapabilityPackageImportRequest) (CapabilityPackagePreview, error) {
	preview, _, _, err := a.prepareCapabilityPackageImport(ctx, r)
	return preview, err
}
func (a *Application) importCapabilityPackage(command Command) (CommandReceipt, error) {
	allowed := Command{ID: command.ID, Kind: "import_capability_package", CapabilityPackage: command.CapabilityPackage}
	hash := digest(command)
	if !validID(command.ID) || command.CapabilityPackage == nil || digest(allowed) != hash {
		return CommandReceipt{}, fmt.Errorf("资源包命令格式无效")
	}
	a.mu.Lock()
	old, done := a.state.Commands[command.ID]
	a.mu.Unlock()
	if done {
		if old.Hash != hash {
			return CommandReceipt{}, fmt.Errorf("command_id 已用于其他内容")
		}
		return old, nil
	}
	request := *command.CapabilityPackage
	preview, catalog, tree, err := a.prepareCapabilityPackageImport(a.ctx, request)
	if err != nil {
		a.mu.Lock()
		old, done := a.state.Commands[command.ID]
		a.mu.Unlock()
		if done && old.Hash == hash {
			return old, nil
		}
		return CommandReceipt{}, err
	}
	if request.PreviewHash == "" || request.PreviewHash != preview.PreviewHash {
		return CommandReceipt{}, fmt.Errorf("导入预览已过期，请重新核对资源包和引用")
	}
	if err = requirePortable(preview); err != nil {
		return CommandReceipt{}, err
	}
	a.mu.Lock()
	stateDir := a.config.StateDir
	a.mu.Unlock()
	directory := filepath.Join(stateDir, "capabilities", "versions", uuid.NewString())
	persisted := false
	defer func() {
		if !persisted {
			_ = os.RemoveAll(directory)
		}
	}()
	source, stamp, err := writeCapabilityResources(directory, tree)
	if err != nil {
		return CommandReceipt{}, err
	}
	asset := *catalog.Request.Asset
	asset.Source = source
	version := &CapabilityVersion{Asset: asset, Source: &stamp, ImportedFrom: "package:" + preview.ArchiveSHA256, CreatedAt: now()}
	receipt, committed, err := a.commitCapabilityChange(command, catalog, version)
	persisted = committed
	return receipt, err
}
func writeCapabilityResources(directory string, tree capabilityResources) (string, SourceStamp, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", SourceStamp{}, err
	}
	source := filepath.Join(directory, tree.Name)
	rootPath := source
	if tree.Kind == "file" {
		rootPath = directory
	} else if err := os.Mkdir(source, 0700); err != nil {
		return "", SourceStamp{}, err
	}
	dirs := []ResourceEntry{}
	for _, entry := range tree.Entries {
		target := filepath.Join(rootPath, filepath.FromSlash(entry.Path))
		if entry.Kind == "directory" {
			if err := os.Mkdir(target, 0700); err != nil {
				return "", SourceStamp{}, err
			}
			dirs = append(dirs, entry)
			continue
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", SourceStamp{}, err
		}
		_, err = file.Write(entry.Data)
		if err == nil {
			err = file.Chmod(os.FileMode(entry.Mode))
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return "", SourceStamp{}, err
		}
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		p := filepath.Join(rootPath, filepath.FromSlash(dirs[n].Path))
		if err := os.Chmod(p, os.FileMode(dirs[n].Mode)); err != nil {
			return "", SourceStamp{}, err
		}
		if err := syncCapabilityDir(p); err != nil {
			return "", SourceStamp{}, err
		}
	}
	if tree.Kind == "directory" {
		if err := os.Chmod(source, os.FileMode(tree.Mode)); err != nil {
			return "", SourceStamp{}, err
		}
		if err := syncCapabilityDir(source); err != nil {
			return "", SourceStamp{}, err
		}
	}
	for _, dir := range []string{directory, filepath.Dir(directory), filepath.Dir(filepath.Dir(directory)), filepath.Dir(filepath.Dir(filepath.Dir(directory)))} {
		if err := syncCapabilityDir(dir); err != nil {
			return "", SourceStamp{}, err
		}
	}
	stamp, err := stampSource(source, false)
	return source, stamp, err
}
