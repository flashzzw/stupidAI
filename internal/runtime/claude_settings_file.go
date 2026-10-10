package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type ClaudePluginSetting struct {
	SourceKey string `json:"source_key"`
	Enabled   bool   `json:"enabled"`
}

// ClaudeDerivedSettingsFile describes a private artifact, not input permission.
// The original path remains the source; only the chosen plugin keys are merged.
type ClaudeDerivedSettingsFile struct {
	ID       string     `json:"id"`
	Original FileSource `json:"original"`
	File     FileSource `json:"file"`
}

type claudeFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
}

type claudeSettingsFileRecord struct {
	Version  int                       `json:"version"`
	State    string                    `json:"state"`
	Scope    string                    `json:"scope_sha256"`
	Parent   string                    `json:"parent"`
	Resolved string                    `json:"resolved_parent"`
	ParentID claudeFileIdentity        `json:"parent_identity"`
	FileID   claudeFileIdentity        `json:"file_identity"`
	Settings []ClaudePluginSetting     `json:"settings"`
	Artifact ClaudeDerivedSettingsFile `json:"artifact"`
}

type claudeSettingsFileUse struct {
	record claudeSettingsFileRecord
	file   *os.File
}

type claudeSettingsFiles struct {
	root string
	// An internal seam for pausing a real child at durable filesystem boundaries.
	checkpoint func(string)
}

var errClaudeSettingsFileBusy = errors.New("派生设置文件仍在使用，尚未回收")
var claudePluginSourceKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}@[A-Za-z0-9_-]{1,100}$`)
var claudeSettingsFileSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validClaudePluginSettings(settings []ClaudePluginSetting) bool {
	if len(settings) == 0 || len(settings) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range settings {
		if !claudePluginSourceKey.MatchString(s.SourceKey) || seen[s.SourceKey] {
			return false
		}
		seen[s.SourceKey] = true
	}
	return true
}

func claudeFileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func claudeSettingsScopeHash(scope Scope) string {
	data, _ := json.Marshal(scope)
	return claudeFileHash(data)
}

func fileIdentity(info os.FileInfo) claudeFileIdentity {
	stat := info.Sys().(*syscall.Stat_t)
	return claudeFileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, UID: stat.Uid}
}

func newClaudeSettingsFiles(stateDir string) (*claudeSettingsFiles, error) {
	base, err := filepath.EvalSymlinks(stateDir)
	if err != nil || !filepath.IsAbs(base) {
		return nil, fmt.Errorf("派生设置记录目录无法核对")
	}
	root := filepath.Join(base, "claude-settings-files")
	for _, directory := range []string{root, filepath.Join(root, "released")} {
		if err := os.Mkdir(directory, 0700); err == nil {
			if err := os.Chmod(directory, 0700); err != nil {
				return nil, fmt.Errorf("派生设置记录目录权限未设置")
			}
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("无法创建派生设置记录目录")
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || fileIdentity(info).UID != uint32(os.Geteuid()) {
			return nil, fmt.Errorf("派生设置记录目录不是当前用户的私有目录")
		}
	}
	return &claudeSettingsFiles{root: root}, nil
}

func (m *claudeSettingsFiles) lock(ctx context.Context) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(m.root, ".lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("无法锁定派生设置记录")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || fileIdentity(info).UID != uint32(os.Geteuid()) {
		f.Close()
		return nil, fmt.Errorf("派生设置记录锁无效")
	}
	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("派生设置记录锁不可用")
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (m *claudeSettingsFiles) save(record claudeSettingsFileRecord) error {
	data, _ := json.Marshal(record)
	f, err := os.CreateTemp(m.root, ".record-")
	if err != nil {
		return fmt.Errorf("无法保存派生设置记录")
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("派生设置记录未完整保存")
	}
	target := filepath.Join(m.root, record.Artifact.ID+".json")
	if record.State == "publishing" {
		// Publication never replaces an existing receipt, even on an ID clash.
		err = os.Link(name, target)
	} else {
		err = os.Rename(name, target)
	}
	if err != nil {
		return fmt.Errorf("派生设置记录未提交")
	}
	if record.State == "released" {
		if m.checkpoint != nil {
			m.checkpoint("recorded_release_before_archive")
		}
		if err := os.Rename(target, filepath.Join(m.root, "released", record.Artifact.ID+".json")); err != nil {
			return fmt.Errorf("已回收记录尚未归档")
		}
		archive, err := os.Open(filepath.Join(m.root, "released"))
		if err != nil {
			return err
		}
		err = archive.Sync()
		archive.Close()
		if err != nil {
			return err
		}
	}
	dir, err := os.Open(m.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (m *claudeSettingsFiles) load(id string) (claudeSettingsFileRecord, error) {
	var record claudeSettingsFileRecord
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return record, fmt.Errorf("派生设置记录标识无效")
	}
	f, err := os.OpenFile(filepath.Join(m.root, id+".json"), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		f, err = os.OpenFile(filepath.Join(m.root, "released", id+".json"), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return record, fmt.Errorf("派生设置记录无法读取")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 || fileIdentity(info).UID != uint32(os.Geteuid()) {
		return record, fmt.Errorf("派生设置记录文件无效")
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &record) != nil || record.Version != 1 && record.Version != 2 || record.Artifact.ID != id ||
		!filepath.IsAbs(record.Parent) || filepath.Clean(record.Parent) != record.Parent || !filepath.IsAbs(record.Resolved) || filepath.Clean(record.Resolved) != record.Resolved ||
		record.Artifact.File.Path != filepath.Join(record.Parent, ".netcap-settings-"+id+".json") ||
		record.Artifact.File.ResolvedPath != filepath.Join(record.Resolved, filepath.Base(record.Artifact.File.Path)) ||
		record.Artifact.File.Kind != "file" || record.Artifact.File.Mode != 0600 || !claudeSettingsFileSHA.MatchString(record.Artifact.File.SHA256) ||
		!claudeSettingsFileSHA.MatchString(record.Scope) || !validClaudePluginSettings(record.Settings) || record.FileID.Inode == 0 || record.ParentID.Inode == 0 ||
		record.FileID.UID != uint32(os.Geteuid()) || record.ParentID.UID != uint32(os.Geteuid()) ||
		(record.State != "publishing" && record.State != "ready" && record.State != "released") {
		return claudeSettingsFileRecord{}, fmt.Errorf("派生设置记录内容无效")
	}
	original := record.Artifact.Original
	if original.Kind == "absent" {
		if original != (FileSource{Kind: "absent"}) {
			return claudeSettingsFileRecord{}, fmt.Errorf("派生设置原来源记录无效")
		}
	} else if original.Kind != "file" || !filepath.IsAbs(original.Path) || filepath.Clean(original.Path) != original.Path || filepath.Dir(original.Path) != record.Parent ||
		!filepath.IsAbs(original.ResolvedPath) || filepath.Clean(original.ResolvedPath) != original.ResolvedPath || !claudeSettingsFileSHA.MatchString(original.SHA256) || !os.FileMode(original.Mode).IsRegular() {
		return claudeSettingsFileRecord{}, fmt.Errorf("派生设置原来源记录无效")
	}
	return record, nil
}

func claudeSettingsParent(path string) (*os.File, string, claudeFileIdentity, error) {
	var zero claudeFileIdentity
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", zero, fmt.Errorf("原设置目录无法解析")
	}
	f, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", zero, fmt.Errorf("原设置目录无法打开")
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() || fileIdentity(info).UID != uint32(os.Geteuid()) || info.Mode().Perm()&0200 == 0 || info.Mode().Perm()&0022 != 0 {
		f.Close()
		return nil, "", zero, fmt.Errorf("原设置目录须由当前用户持有、可写且不允许组或其他用户写入")
	}
	return f, resolved, fileIdentity(info), nil
}

// create returns an already-held file. The caller must pass file to any child
// that can outlive it, close all holders, then remove. Receipts never contain
// settings values. An anonymous inode is recorded before its name is linked.
func (m *claudeSettingsFiles) create(ctx context.Context, scope Scope, settings []ClaudePluginSetting) (*claudeSettingsFileUse, error) {
	return m.createRecord(ctx, scope, settings, uuid.NewString(), 1)
}

// Version 2 keeps a settings artifact for an execution version, including
// periods without a live query. Older readers reject it instead of reclaiming
// a file still referenced by a saved execution. Only releaseRetained retires it.
func (m *claudeSettingsFiles) retain(ctx context.Context, id string, scope Scope, settings []ClaudePluginSetting) (*claudeSettingsFileUse, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return nil, fmt.Errorf("执行设置需要已保存的版本标识")
	}
	return m.createRecord(ctx, scope, settings, id, 2)
}

func (m *claudeSettingsFiles) createRecord(ctx context.Context, scope Scope, settings []ClaudePluginSetting, id string, version int) (*claudeSettingsFileUse, error) {
	original, body, parent, err := claudeDerivedSettingsContent(scope, settings)
	if err != nil {
		return nil, err
	}
	return m.publishRecord(ctx, scope, settings, id, version, original, body, parent)
}

// Planning has no filesystem mutations. The saved execution binds the exact
// future file, while publication remains inside its durable apply operation.
func PlanClaudeDerivedSettingsFile(scope Scope, settings []ClaudePluginSetting, id string) (ClaudeDerivedSettingsFile, error) {
	var out ClaudeDerivedSettingsFile
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return out, fmt.Errorf("派生设置计划需要执行版本标识")
	}
	original, body, parent, err := claudeDerivedSettingsContent(scope, settings)
	if err != nil {
		return out, err
	}
	dir, resolved, _, err := claudeSettingsParent(parent)
	if err != nil {
		return out, err
	}
	dir.Close()
	name := ".netcap-settings-" + id + ".json"
	return ClaudeDerivedSettingsFile{ID: id, Original: original, File: FileSource{Path: filepath.Join(parent, name),
		ResolvedPath: filepath.Join(resolved, name), Kind: "file", Mode: 0600, SHA256: claudeFileHash(body)}}, nil
}

func claudeDerivedSettingsContent(scope Scope, settings []ClaudePluginSetting) (FileSource, []byte, string, error) {
	fail := func(err error) (FileSource, []byte, string, error) { return FileSource{}, nil, "", err }
	if scope.Instance.Kind != "claude" || scope.Purpose != "" || !filepath.IsAbs(scope.Cwd) || filepath.Clean(scope.Cwd) != scope.Cwd || !validClaudePluginSettings(settings) {
		return fail(fmt.Errorf("派生设置需要普通 Claude 执行和明确的插件来源选择"))
	}
	original := FileSource{Kind: "absent"}
	body := []byte("{}")
	parent := scope.Cwd
	var err error
	if scope.Instance.SettingsFile != "" {
		if !filepath.IsAbs(scope.Instance.SettingsFile) || filepath.Clean(scope.Instance.SettingsFile) != scope.Instance.SettingsFile {
			return fail(fmt.Errorf("原设置文件需要规范的绝对路径"))
		}
		original, body, err = readClaudeSettingsFile(scope.Instance.SettingsFile, false)
		if err != nil {
			return fail(err)
		}
		parent = filepath.Dir(scope.Instance.SettingsFile)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}), &object) != nil || object == nil {
		return fail(fmt.Errorf("原设置不是有效 JSON 对象"))
	}
	plugins := map[string]*bool{}
	if raw, ok := object["enabledPlugins"]; ok && (json.Unmarshal(raw, &plugins) != nil || plugins == nil) {
		return fail(fmt.Errorf("原设置 enabledPlugins 不是布尔映射"))
	}
	for _, value := range plugins {
		if value == nil {
			return fail(fmt.Errorf("原设置 enabledPlugins 不是布尔映射"))
		}
	}
	for _, setting := range settings {
		value := setting.Enabled
		plugins[setting.SourceKey] = &value
	}
	object["enabledPlugins"], _ = json.Marshal(plugins)
	body, _ = json.Marshal(object)
	if len(body) > 2<<20 {
		return fail(fmt.Errorf("派生设置超过 2 MiB"))
	}
	return original, body, parent, nil
}

func (m *claudeSettingsFiles) publishRecord(ctx context.Context, scope Scope, settings []ClaudePluginSetting, id string, version int, original FileSource, body []byte, parent string) (*claudeSettingsFileUse, error) {
	lock, err := m.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if version == 2 {
		for _, name := range []string{filepath.Join(m.root, id+".json"), filepath.Join(m.root, "released", id+".json")} {
			if _, err := os.Lstat(name); err == nil {
				r, err := m.load(id)
				if err != nil || r.Version != 2 || r.State != "ready" || !equalClaudePluginSettings(r.Settings, settings) {
					return nil, fmt.Errorf("执行设置版本已用于其他内容、未就绪或已释放")
				}
				return m.acquireRecord(scope, r)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("执行设置版本无法核对")
			}
		}
	}
	dir, resolved, identity, err := claudeSettingsParent(parent)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("原设置目录的文件系统不支持可恢复的私有文件创建")
	}
	f := os.NewFile(uintptr(fd), "anonymous-derived-settings")
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()
	if err := f.Chmod(0600); err != nil {
		return nil, fmt.Errorf("派生设置私有权限未设置")
	}
	if _, err := f.Write(body); err != nil {
		return nil, fmt.Errorf("派生设置写入失败")
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("派生设置未完整保存")
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("派生设置使用锁不可用")
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	name := ".netcap-settings-" + id + ".json"
	r := claudeSettingsFileRecord{Version: version, State: "publishing", Scope: claudeSettingsScopeHash(scope), Parent: parent,
		Resolved: resolved, ParentID: identity, FileID: fileIdentity(info), Settings: settings,
		Artifact: ClaudeDerivedSettingsFile{ID: id, Original: original, File: FileSource{Path: filepath.Join(parent, name),
			ResolvedPath: filepath.Join(resolved, name), Kind: "file", Mode: 0600, SHA256: claudeFileHash(body)}}}
	if err := m.save(r); err != nil {
		return nil, err
	}
	if m.checkpoint != nil {
		m.checkpoint("recorded_before_link")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := unix.Linkat(unix.AT_FDCWD, fmt.Sprintf("/proc/self/fd/%d", fd), int(dir.Fd()), name, unix.AT_SYMLINK_FOLLOW); err != nil {
		return nil, fmt.Errorf("派生设置名称未发布；已有文件不会被覆盖")
	}
	if err := dir.Sync(); err != nil {
		return nil, fmt.Errorf("派生设置发布未确认，需核对记录后回收")
	}
	if m.checkpoint != nil {
		m.checkpoint("linked_before_ready")
	}
	if err := verifyClaudeSettingsFileSource(scope, r); err != nil {
		return nil, err
	}
	r.State = "ready"
	if err := m.save(r); err != nil {
		return nil, err
	}
	keep = true
	return &claudeSettingsFileUse{record: r, file: f}, nil
}

func verifyClaudeSettingsFileSource(scope Scope, r claudeSettingsFileRecord) error {
	if r.Scope != claudeSettingsScopeHash(scope) {
		return fmt.Errorf("派生设置与原执行绑定不一致")
	}
	if scope.Instance.SettingsFile != "" {
		current, _, err := readClaudeSettingsFile(scope.Instance.SettingsFile, false)
		if err != nil || current != r.Artifact.Original {
			return fmt.Errorf("派生设置的原文件版本或路径已变化")
		}
	}
	dir, resolved, id, err := claudeSettingsParent(r.Parent)
	if err != nil {
		return err
	}
	dir.Close()
	if resolved != r.Resolved || id != r.ParentID {
		return fmt.Errorf("派生设置的原目录身份已变化")
	}
	return nil
}

func claudeSettingsRecordParent(r claudeSettingsFileRecord) (*os.File, error) {
	dir, resolved, id, err := claudeSettingsParent(r.Parent)
	if err != nil {
		return nil, err
	}
	if resolved != r.Resolved || id != r.ParentID {
		dir.Close()
		return nil, fmt.Errorf("派生设置的原目录身份已变化")
	}
	return dir, nil
}

func sameClaudeDerivedName(dir *os.File, r claudeSettingsFileRecord) bool {
	var current unix.Stat_t
	err := unix.Fstatat(int(dir.Fd()), filepath.Base(r.Artifact.File.Path), &current, unix.AT_SYMLINK_NOFOLLOW)
	return err == nil && current.Mode == unix.S_IFREG|0600 &&
		(claudeFileIdentity{Device: uint64(current.Dev), Inode: current.Ino, UID: current.Uid}) == r.FileID
}

func openClaudeDerivedFile(dir *os.File, r claudeSettingsFileRecord, exclusive bool) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(r.Artifact.File.Path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), r.Artifact.File.Path)
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errClaudeSettingsFileBusy
		}
		return nil, fmt.Errorf("派生设置使用锁不可用")
	}
	info, err := f.Stat()
	if err != nil || info.Mode() != 0600 || fileIdentity(info) != r.FileID || info.Size() > 2<<20 {
		f.Close()
		return nil, fmt.Errorf("派生设置文件被替换或权限已变化，不会使用或删除")
	}
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || !sameClaudeDerivedName(dir, r) || int64(len(data)) != info.Size() || claudeFileHash(data) != r.Artifact.File.SHA256 {
		f.Close()
		return nil, fmt.Errorf("派生设置内容或文件身份已变化，不会使用或删除")
	}
	return f, nil
}

func (m *claudeSettingsFiles) acquire(ctx context.Context, id string, scope Scope) (*claudeSettingsFileUse, error) {
	lock, err := m.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	r, err := m.load(id)
	if err != nil || r.State != "ready" {
		return nil, fmt.Errorf("派生设置尚未就绪或已回收")
	}
	return m.acquireRecord(scope, r)
}

func (m *claudeSettingsFiles) acquireRecord(scope Scope, r claudeSettingsFileRecord) (*claudeSettingsFileUse, error) {
	if err := verifyClaudeSettingsFileSource(scope, r); err != nil {
		return nil, err
	}
	dir, err := claudeSettingsRecordParent(r)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := openClaudeDerivedFile(dir, r, false)
	if err != nil {
		return nil, err
	}
	return &claudeSettingsFileUse{record: r, file: f}, nil
}

func (m *claudeSettingsFiles) remove(ctx context.Context, id string) error {
	lock, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	r, err := m.load(id)
	if err != nil {
		return err
	}
	if r.Version == 2 && r.State != "released" {
		return fmt.Errorf("执行设置仍由保存的版本保留，需要明确释放")
	}
	return m.removeRecord(ctx, r)
}

func (m *claudeSettingsFiles) releaseRetained(ctx context.Context, id string, scope Scope) error {
	lock, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	r, err := m.load(id)
	if err != nil || r.Version != 2 || r.Scope != claudeSettingsScopeHash(scope) {
		return fmt.Errorf("执行设置与待释放的原执行绑定不一致")
	}
	return m.removeRecord(ctx, r)
}

func equalClaudePluginSettings(a, b []ClaudePluginSetting) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func (m *claudeSettingsFiles) removeRecord(ctx context.Context, r claudeSettingsFileRecord) error {
	id := r.Artifact.ID
	if r.State == "released" {
		if _, err := os.Lstat(filepath.Join(m.root, id+".json")); os.IsNotExist(err) {
			return nil
		}
		// A crash after the durable release receipt but before its rename must
		// not leave a completed record occupying the active recovery index.
		return m.save(r)
	}
	dir, err := claudeSettingsRecordParent(r)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := openClaudeDerivedFile(dir, r, true)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if f != nil {
		defer f.Close()
		// The private directory and file lock serialize managed users; an
		// unexpected replacement is rejected rather than removed by name.
		if !sameClaudeDerivedName(dir, r) {
			return fmt.Errorf("派生设置删除前身份变化，保留当前文件")
		}
		if err := unix.Unlinkat(int(dir.Fd()), filepath.Base(r.Artifact.File.Path), 0); err != nil {
			return fmt.Errorf("派生设置尚未删除")
		}
		if err := dir.Sync(); err != nil {
			return fmt.Errorf("派生设置删除尚未确认")
		}
		if m.checkpoint != nil {
			m.checkpoint("unlinked_before_release")
		}
	}
	r.State = "released"
	return m.save(r)
}

// recover only reclaims recorded files; it never scans an original directory
// for lookalike names or adopts a file whose inode was not recorded first.
func (m *claudeSettingsFiles) recover(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return 0, fmt.Errorf("派生设置记录无法读取或超过回收上限")
	}
	released, scanned := 0, 0
	var failures []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		scanned++
		if scanned > 512 || ctx.Err() != nil {
			return released, errors.Join(append(failures, fmt.Errorf("派生设置回收已取消或超过上限"))...)
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		r, err := m.load(id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if r.Version == 2 && r.State != "released" {
			continue
		}
		if err = m.remove(ctx, id); err == nil {
			if r.State != "released" {
				released++
			}
		} else if !errors.Is(err, errClaudeSettingsFileBusy) {
			failures = append(failures, fmt.Errorf("派生设置 %s：%w", id, err))
		}
	}
	return released, errors.Join(failures...)
}
