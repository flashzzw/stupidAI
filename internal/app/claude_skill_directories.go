package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"codex-netcap/internal/runtime"
)

// The native catalog and filesystem sample deliberately have separate sources.
// An entry on disk does not establish native visibility or effective selection.
type SessionSkillCatalog struct {
	runtime.SkillSourceCatalog
	Directories ClaudeSkillDirectories `json:"directories"`
}

type ClaudeSkillDirectories struct {
	Source          string                   `json:"source"`
	At              string                   `json:"at"`
	Complete        bool                     `json:"complete"`
	Entries         []ClaudeSkillDirectory   `json:"entries"`
	Errors          []runtime.SkillScanError `json:"errors"`
	SnapshotID      string                   `json:"snapshot_id"`
	SavedSelections []SavedDirectorySkill    `json:"saved_selections"`
}

type ClaudeSkillDirectory struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	NativeKey    string `json:"native_key"`
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path"`
	Source       string `json:"source"`
	Control      string `json:"control"`
	Detail       string `json:"detail"`
}

type claudeSkillRoot struct{ path, source string }

func claudeSkillRoots(scope runtime.Scope) ([]claudeSkillRoot, error) {
	if !filepath.IsAbs(scope.Instance.Profile) || !filepath.IsAbs(scope.Cwd) {
		return nil, fmt.Errorf("Skill 查询缺少绝对配置目录或项目绑定")
	}
	roots := []claudeSkillRoot{{filepath.Join(scope.Instance.Profile, "skills"), "userSettings"}}
	for dir, depth := scope.Cwd, 0; ; dir, depth = filepath.Dir(dir), depth+1 {
		if depth >= 64 {
			return nil, fmt.Errorf("项目父目录超过 Skill 来源核对上限")
		}
		roots = append(roots, claudeSkillRoot{filepath.Join(dir, ".claude", "skills"), "projectSettings"})
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return roots, nil
}

func discoverClaudeSkillDirectories(ctx context.Context, session Session) ClaudeSkillDirectories {
	out := ClaudeSkillDirectories{Source: "claude.bound_directories", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Entries: []ClaudeSkillDirectory{}, Errors: []runtime.SkillScanError{}, SavedSelections: []SavedDirectorySkill{}}
	issue := func(path, detail string) {
		out.Complete = false
		out.Errors = append(out.Errors, runtime.SkillScanError{Path: path, Detail: detail})
	}
	roots, err := claudeSkillRoots(session.Scope)
	if err != nil {
		issue("", err.Error())
		return out
	}
	count, bytes := 0, int64(0)
	identities := []os.FileInfo{}
scan:
	for _, root := range roots {
		if ctx.Err() != nil {
			issue(root.path, "目录查询已取消或超时")
			break
		}
		rootInfo, rootErr := os.Stat(root.path)
		if os.IsNotExist(rootErr) {
			continue
		}
		if rootErr != nil || !rootInfo.IsDir() {
			issue(root.path, "Skill 来源不是可读取的目录")
			continue
		}
		dir, err := os.Open(root.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			issue(root.path, "无法读取 Skill 来源目录")
			continue
		}
		children, err := dir.ReadDir(2049 - count)
		_ = dir.Close()
		if err != nil && err != io.EOF {
			issue(root.path, "无法完整读取 Skill 来源目录")
			continue
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			count++
			if count > 2048 {
				issue(root.path, "Skill 来源条目超过 2048 项读取上限")
				break scan
			}
			if ctx.Err() != nil {
				issue(root.path, "目录查询已取消或超时")
				break scan
			}
			if !child.IsDir() && child.Type()&os.ModeSymlink == 0 {
				continue
			}
			container := filepath.Join(root.path, child.Name())
			info, err := os.Stat(container)
			if err != nil {
				issue(container, "来源目录链接缺失或无法读取")
				continue
			}
			if !info.IsDir() {
				continue
			}
			path := filepath.Join(container, "SKILL.md")
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				issue(path, "Skill 入口链接缺失或无法解析")
				continue
			}
			info, err = os.Stat(resolved)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
				issue(path, "Skill 入口不是可读取的普通文件或超过 256 KiB")
				continue
			}
			if bytes+info.Size() > 4<<20 {
				issue(path, "Skill 元数据采样超过 4 MiB 读取上限")
				break scan
			}
			file, err := os.Open(resolved)
			if err != nil {
				issue(path, "无法读取 Skill 入口")
				continue
			}
			opened, statErr := file.Stat()
			if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				_ = file.Close()
				issue(path, "Skill 入口在打开时变化")
				continue
			}
			body, readErr := io.ReadAll(io.LimitReader(file, (256<<10)+1))
			after, afterErr := file.Stat()
			_ = file.Close()
			current, resolveErr := filepath.EvalSymlinks(path)
			currentInfo, currentErr := os.Stat(path)
			bytes += int64(len(body))
			if readErr != nil || afterErr != nil || resolveErr != nil || currentErr != nil || !os.SameFile(info, currentInfo) || current != resolved || len(body) > 256<<10 || int64(len(body)) != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
				issue(path, "Skill 入口在读取时变化或无法完整读取")
				continue
			}
			metadata, err := skillDocumentMetadata(body)
			name, description := "", ""
			if err == nil {
				if raw, ok := metadata["name"]; ok {
					var valid bool
					name, valid = raw.(string)
					if !valid || name == "" {
						err = fmt.Errorf("invalid name")
					}
				}
				if raw, ok := metadata["description"]; ok {
					var valid bool
					description, valid = raw.(string)
					if !valid {
						err = fmt.Errorf("invalid description")
					}
				}
			}
			if name == "" {
				name = child.Name()
			}
			if err != nil || len(name) > 200 || len(description) > 4000 || strings.TrimSpace(name) != name || !utf8.ValidString(name+description) || strings.ContainsRune(name+description, '\x00') {
				issue(path, "Skill 元数据无效或超出名称/描述范围；未返回正文")
				continue
			}
			out.Entries = append(out.Entries, ClaudeSkillDirectory{Name: name, Description: description, NativeKey: child.Name(), Path: path, ResolvedPath: resolved, Source: root.source, Control: "requires_binding", Detail: "目录采样入口；可登记此路径，启停仍需原生清单与完整资源核对。"})
			identities = append(identities, info)
		}
	}
	for i := range out.Entries {
		item := &out.Entries[i]
		if !out.Complete || !validID(item.NativeKey) {
			item.Control = "unknown"
			item.Detail = "目录采样不完整或目录键尚不受支持，不能确认独立选配。"
			continue
		}
		for j, other := range out.Entries {
			if i != j && (item.Name == other.Name || item.NativeKey == other.NativeKey || os.SameFile(identities[i], identities[j])) {
				item.Control = "ambiguous"
				item.Detail = "存在同名、同目录键或指向同一文件的入口；不能据此确认单项排除。"
				break
			}
		}
	}
	if snapshot := session.ExecutionSnapshot; snapshot != nil {
		out.SnapshotID = snapshot.ID
		for _, s := range snapshot.SkillSelections {
			if s.Delivery != "" || s.Source != "userSettings" && s.Source != "projectSettings" {
				continue
			}
			version := ""
			for _, ref := range snapshot.CapabilityRefs {
				if ref.Asset.ID == s.AssetID {
					version = ref.Asset.Version
					break
				}
			}
			found := false
			for _, entry := range out.Entries {
				found = found || entry.Path == s.DiscoveryPath
			}
			out.SavedSelections = append(out.SavedSelections, SavedDirectorySkill{AssetID: s.AssetID, Version: version, Name: s.Name, Path: s.DiscoveryPath, Mode: s.Mode, Discovered: found})
		}
	}
	return out
}
