package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// FileSource records file bytes, permissions and path resolution.
// It does not assert that the CLI loaded them or that all policy is covered.
type FileSource struct {
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path"`
	Kind         string `json:"kind"`
	SHA256       string `json:"sha256,omitempty"`
	Mode         uint32 `json:"mode,omitempty"`
}

func absentSourceFilePath(path, label string) (string, error) {
	// An absent settings directory is common. Record its prospective resolved
	// path without creating directories, but do not hide a dangling parent link.
	parent, suffix := filepath.Dir(path), filepath.Base(path)
	for depth := 0; depth < 64; depth++ {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			info, statErr := os.Stat(resolved)
			if statErr != nil || !info.IsDir() {
				break
			}
			return filepath.Join(resolved, suffix), nil
		}
		if _, statErr := os.Lstat(parent); !os.IsNotExist(statErr) || parent == filepath.Dir(parent) {
			break
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		parent = filepath.Dir(parent)
	}
	return "", fmt.Errorf("%s 的缺失路径或目录链接无法核对：%s", label, path)
}

func readSourceFile(path string, optional bool, label string) (FileSource, []byte, error) {
	source := FileSource{Path: path, Kind: "file"}
	resolved, resolveErr := filepath.EvalSymlinks(path)
	if resolveErr != nil && optional {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			future, err := absentSourceFilePath(path, label)
			if err != nil {
				return source, nil, err
			}
			source.Kind, source.ResolvedPath = "absent", future
			return source, nil, nil
		}
	}
	if resolveErr != nil {
		return source, nil, fmt.Errorf("%s 文件无法读取：%s", label, path)
	}
	source.ResolvedPath = resolved
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return source, nil, fmt.Errorf("%s 文件无法读取：%s", label, path)
	}
	defer f.Close()
	before, err := f.Stat()
	const limit = 2 << 20
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return source, nil, fmt.Errorf("%s 需要不超过 2 MiB 的普通文件：%s", label, path)
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	after, afterErr := f.Stat()
	current, pathErr := os.Stat(path)
	currentResolved, resolvedErr := filepath.EvalSymlinks(path)
	if err != nil || afterErr != nil || pathErr != nil || resolvedErr != nil || currentResolved != resolved || len(body) > limit || int64(len(body)) != before.Size() || !os.SameFile(before, current) || after.Size() != before.Size() || current.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() || current.Mode() != before.Mode() {
		return source, nil, fmt.Errorf("%s 在读取时变化或无法完整读取：%s", label, path)
	}
	hash := sha256.Sum256(body)
	source.SHA256, source.Mode = hex.EncodeToString(hash[:]), uint32(before.Mode())
	return source, body, nil
}
