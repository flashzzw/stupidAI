package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"codex-netcap/internal/codex"
)

func createdMatches(thread codex.Thread, in Intent) (bool, error) {
	if thread.ID == "" || in.ID == "" || thread.Cwd != in.Scope.Cwd || thread.Provider != in.Scope.Route.Provider || (thread.Model != "" && thread.Model != in.Scope.Route.Model) {
		return false, nil
	}
	source := thread.Source
	if source == "" {
		// Fixed 0.158.0 read/list metadata can omit threadSource after restart,
		// while the original rollout header retains it. Never resume a candidate
		// merely to obtain its source: it might belong to another operation.
		var err error
		source, err = codexRolloutCreationSource(in.Scope, thread)
		if err != nil {
			return false, err
		}
	}
	return source == "netcap-console:"+in.ID, nil
}

func codexRolloutCreationSource(scope Scope, thread codex.Thread) (string, error) {
	if !filepath.IsAbs(scope.Instance.Profile) || !filepath.IsAbs(thread.Path) {
		return "", fmt.Errorf("创建来源缺少可核对的原生状态目录或日志路径")
	}
	profile, err := filepath.EvalSymlinks(scope.Instance.Profile)
	if err != nil {
		return "", fmt.Errorf("无法读取原生创建状态目录")
	}
	path, err := filepath.EvalSymlinks(thread.Path)
	if err != nil {
		return "", fmt.Errorf("无法读取原生创建日志路径")
	}
	rel, err := filepath.Rel(profile, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("原生创建日志不属于已绑定状态目录")
	}
	root, err := os.OpenRoot(profile)
	if err != nil {
		return "", fmt.Errorf("无法打开原生创建状态目录")
	}
	defer root.Close()
	// Root prevents symlink traversal outside the bound profile during open.
	// Nonblocking open lets us reject a FIFO/device without hanging recovery.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("无法在原状态目录打开创建日志")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("原生创建日志不是普通文件")
	}
	const maxHeader = 4 << 20
	line, err := bufio.NewReader(io.LimitReader(f, maxHeader+1)).ReadBytes('\n')
	if err != nil || len(line) > maxHeader {
		return "", fmt.Errorf("原生创建日志头不完整或超出读取范围")
	}
	current, statErr := os.Stat(thread.Path)
	bound, boundErr := root.Stat(".")
	requested, requestedErr := os.Stat(scope.Instance.Profile)
	if statErr != nil || !os.SameFile(info, current) || boundErr != nil || requestedErr != nil || !os.SameFile(bound, requested) {
		return "", fmt.Errorf("读取创建来源期间原日志或状态目录已变化")
	}
	entry, err := creationJSONObject(line)
	if err != nil {
		return "", err
	}
	var kind string
	if json.Unmarshal(entry["type"], &kind) != nil || kind != "session_meta" {
		return "", fmt.Errorf("原生创建日志缺少首行会话元数据")
	}
	payload, err := creationJSONObject(entry["payload"])
	if err != nil {
		return "", err
	}
	fields := map[string]string{}
	for _, key := range []string{"id", "cwd", "model_provider", "cli_version"} {
		var value string
		if json.Unmarshal(payload[key], &value) != nil || value == "" {
			return "", fmt.Errorf("原生创建元数据缺少有效的 %s", key)
		}
		fields[key] = value
	}
	if fields["id"] != thread.ID || fields["cwd"] != scope.Cwd || fields["model_provider"] != scope.Route.Provider || fields["cli_version"] != "0.158.0" {
		return "", fmt.Errorf("原生创建日志身份、目录、供应商或格式版本不匹配")
	}
	if raw, ok := payload["session_id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || id != thread.ID {
			return "", fmt.Errorf("原生创建日志的会话身份冲突")
		}
	}
	var source string
	if raw, ok := payload["thread_source"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &source) != nil {
			return "", fmt.Errorf("原生创建来源字段格式无效")
		}
	}
	// A valid untagged native thread is not evidence for this operation. Its
	// presence must not hide a different thread carrying the exact marker.
	return source, nil
}

// Decode only the header envelope and metadata object, rejecting ambiguous
// duplicate fields. Other payloads (including instructions) are not projected.
func creationJSONObject(data []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("原生创建元数据格式无效")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, fmt.Errorf("原生创建元数据字段无效")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("原生创建元数据存在重复字段")
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, fmt.Errorf("原生创建元数据内容无效")
		}
		fields[key] = raw
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("原生创建元数据不完整")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("原生创建元数据存在额外内容")
	}
	return fields, nil
}
