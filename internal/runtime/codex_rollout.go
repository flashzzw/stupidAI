package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var errCodexRolloutNotMaterialized = errors.New("原生日志尚未物化")

// Both preparation context and saved policy use the same confined, stable file
// sample. Callers parse only after native metadata and file identity agree.
func readCodexRollout(ctx context.Context, p codexConnection, scope Scope) ([]byte, error) {
	if scope.Instance.Kind != "codex" || scope.Purpose != "" || scope.NativeID == "" || !filepath.IsAbs(scope.Cwd) || scope.StateNamespace == "" {
		return nil, skillContextError()
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return nil, err
	}
	type metadata struct {
		Thread struct {
			ID   string `json:"id"`
			Cwd  string `json:"cwd"`
			Path string `json:"path"`
		} `json:"thread"`
	}
	read := func() (metadata, error) {
		var out metadata
		e := p.Call(ctx, "thread/read", map[string]any{"threadId": scope.NativeID, "includeTurns": false}, &out)
		if e != nil || out.Thread.ID != scope.NativeID || out.Thread.Cwd != scope.Cwd {
			return out, skillContextError()
		}
		return out, nil
	}
	before, err := read()
	if err != nil {
		return nil, err
	}
	path := before.Thread.Path
	if !filepath.IsAbs(path) {
		return nil, skillContextError()
	}
	rel, err := filepath.Rel(scope.StateNamespace, path)
	if err != nil || !filepath.IsLocal(rel) || !(strings.HasPrefix(rel, "sessions/") || strings.HasPrefix(rel, "archived_sessions/")) {
		return nil, skillContextError()
	}
	root, err := os.OpenRoot(scope.StateNamespace)
	if err != nil {
		return nil, skillContextError()
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if os.IsNotExist(err) {
		after, readErr := read()
		_, missingErr := root.Lstat(rel)
		bound, boundErr := root.Stat(".")
		requested, requestedErr := os.Stat(scope.StateNamespace)
		if readErr != nil || before != after || !os.IsNotExist(missingErr) || VerifyStateNamespace(scope) != nil || boundErr != nil || requestedErr != nil || !os.SameFile(bound, requested) {
			return nil, skillContextError()
		}
		return nil, errCodexRolloutNotMaterialized
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, skillContextError()
	}
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, skillContextError()
	}
	defer file.Close()
	first, err := file.Stat()
	if err != nil || !first.Mode().IsRegular() || !os.SameFile(info, first) {
		return nil, skillContextError()
	}
	data, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil || len(data) > 32<<20 || len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, skillContextError()
	}
	last, err := file.Stat()
	if err != nil || first.Size() != last.Size() || first.ModTime() != last.ModTime() {
		return nil, skillContextError()
	}
	after, err := read()
	if err != nil || before != after {
		return nil, skillContextError()
	}
	if err := VerifyStateNamespace(scope); err != nil {
		return nil, err
	}
	current, statErr := root.Lstat(rel)
	bound, boundErr := root.Stat(".")
	requested, requestedErr := os.Stat(scope.StateNamespace)
	if statErr != nil || !os.SameFile(first, current) || current.Size() != first.Size() || current.ModTime() != first.ModTime() || boundErr != nil || requestedErr != nil || !os.SameFile(bound, requested) {
		return nil, skillContextError()
	}
	return data, nil
}
