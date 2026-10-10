package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const capabilityMaxBytes int64 = 32 << 20
const capabilityMaxEntries = 2048

type ResourceEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256,omitempty"`
	Data   []byte `json:"-"`
}
type capabilityResources struct {
	Name    string
	Kind    string
	Mode    uint32
	Entries []ResourceEntry
}

func bytesHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func resourcePath(p string) bool {
	if p == "" || p == "." || strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}
func readCapabilityResources(asset CapabilityAsset) (capabilityResources, SourceStamp, error) {
	before, err := stampSource(asset.Source, false)
	if err != nil {
		return capabilityResources{}, before, err
	}
	info, err := os.Stat(before.Resolved)
	if err != nil {
		return capabilityResources{}, before, err
	}
	tree := capabilityResources{Name: filepath.Base(before.Resolved), Kind: before.Kind, Mode: uint32(info.Mode().Perm()), Entries: []ResourceEntry{}}
	if !resourcePath(tree.Name) || strings.Contains(tree.Name, "/") {
		return tree, before, fmt.Errorf("资源根名称不能用于可移植路径")
	}
	rootPath := before.Resolved
	if before.Kind == "file" {
		rootPath = filepath.Dir(rootPath)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return tree, before, err
	}
	defer root.Close()
	var total int64
	folded := map[string]bool{}
	err = filepath.WalkDir(before.Resolved, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		metadata, e := d.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(rootPath, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if !resourcePath(rel) || folded[strings.ToLower(rel)] {
			return fmt.Errorf("资源内路径无效：%s", rel)
		}
		folded[strings.ToLower(rel)] = true
		if len(tree.Entries) >= capabilityMaxEntries {
			return fmt.Errorf("资源条目超过上限")
		}
		entry := ResourceEntry{Path: rel, Kind: "file", Mode: uint32(metadata.Mode().Perm())}
		if metadata.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("资源具有特殊权限：%s", rel)
		}
		if d.IsDir() {
			entry.Kind = "directory"
		} else {
			if !metadata.Mode().IsRegular() {
				return fmt.Errorf("资源含链接或特殊文件：%s", rel)
			}
			f, e := root.Open(filepath.FromSlash(rel))
			if e != nil {
				return e
			}
			opened, e := f.Stat()
			if e != nil || !os.SameFile(metadata, opened) {
				f.Close()
				return fmt.Errorf("读取期间资源身份变化")
			}
			entry.Data, e = io.ReadAll(io.LimitReader(f, capabilityMaxBytes-total+1))
			f.Close()
			if e != nil {
				return e
			}
			total += int64(len(entry.Data))
			if total > capabilityMaxBytes {
				return fmt.Errorf("资源超过 32 MiB")
			}
			entry.SHA256 = bytesHash(entry.Data)
		}
		tree.Entries = append(tree.Entries, entry)
		return nil
	})
	if err != nil {
		return tree, before, err
	}
	sort.Slice(tree.Entries, func(i, j int) bool { return tree.Entries[i].Path < tree.Entries[j].Path })
	after, err := stampSource(asset.Source, false)
	if err != nil || after != before {
		return tree, before, fmt.Errorf("读取期间来源发生变化")
	}
	return tree, before, nil
}
func (r capabilityResources) files() map[string][]byte {
	out := map[string][]byte{}
	for _, entry := range r.Entries {
		if entry.Kind == "file" {
			out[entry.Path] = entry.Data
		}
	}
	return out
}
func (r capabilityResources) exists(p string) bool {
	if p == "." {
		return r.Kind == "directory"
	}
	for _, entry := range r.Entries {
		if entry.Path == p {
			return true
		}
	}
	return false
}
