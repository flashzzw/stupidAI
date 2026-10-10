package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type CapabilityResourceInfo struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	MediaType  string `json:"media_type"`
	Inspection string `json:"inspection"`
}

func capabilityStateName(name string) bool {
	name = strings.ToLower(name)
	return name == "auth.json" || name == ".credentials.json" || name == ".claude.json" || name == "settings.json" || name == "settings.local.json" || name == "config.toml" || name == ".npmrc" || name == ".netrc" || strings.HasPrefix(name, ".env") || strings.Contains(name, ".sqlite") || name == ".git" || name == ".ssh" || name == "sessions" || name == "projects" || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key")
}
func capabilityStatePath(p string) bool {
	for _, name := range strings.Split(strings.ReplaceAll(p, "!", "/"), "/") {
		if capabilityStateName(name) {
			return true
		}
	}
	return false
}
func resourceText(data []byte) ([]byte, bool) {
	if len(data) >= 2 && (bytes.Equal(data[:2], []byte{0xff, 0xfe}) || bytes.Equal(data[:2], []byte{0xfe, 0xff})) && len(data)%2 == 0 {
		var order binary.ByteOrder = binary.BigEndian
		if data[0] == 0xff {
			order = binary.LittleEndian
		}
		units := make([]uint16, 0, (len(data)-2)/2)
		for n := 2; n < len(data); n += 2 {
			units = append(units, order.Uint16(data[n:n+2]))
		}
		data = []byte(string(utf16.Decode(units)))
	}
	if !utf8.Valid(data) {
		return data, false
	}
	for _, b := range data {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f' {
			return data, false
		}
	}
	return data, true
}
func (i *assetInspector) scanResource(p string, data []byte, depth int) {
	if capabilityStatePath(p) || bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		i.issue("warning", "native_state", p, "发现原生配置、账号或会话状态，不能纳入能力资源包")
	}
	text, isText := resourceText(data)
	if knownSecret.Match(data) || knownSecret.Match(text) {
		i.issue("error", "sensitive_content", p, "发现已知凭据或私钥形式，拒绝导出原始内容")
	}
	// Check literal assignments even in binary metadata and decoded UTF-16 text.
	for _, match := range credentialAssignment.FindAllSubmatch(text, -1) {
		if !credentialReference(string(match[1])) {
			i.issue("error", "sensitive_content", p, "发现凭据赋值字面量；请改用环境变量引用")
		}
	}
	if isText {
		trimmed := bytes.TrimSpace(text)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			var value any
			if uniqueJSONKeys(trimmed) == nil && json.Unmarshal(trimmed, &value) == nil {
				i.scanObjectValues(p, value, false)
			}
		}
		return
	}
	media := http.DetectContentType(data)
	kind := "binary"
	inspection := "known_patterns_only"
	switch {
	case bytes.HasPrefix(data, []byte{0, 97, 115, 109}):
		media = "application/wasm"
		kind = "executable"
	case bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}), bytes.HasPrefix(data, []byte{'M', 'Z'}):
		kind = "executable"
	case strings.HasPrefix(media, "image/"):
		kind = "image"
	case strings.HasPrefix(media, "audio/"), strings.HasPrefix(media, "video/"):
		kind = "media"
	case strings.HasPrefix(media, "font/"), strings.Contains(media, "font"):
		kind = "font"
	case strings.HasPrefix(media, "application/pdf"):
		kind = "document"
	}
	if bytes.HasPrefix(data, []byte{'P', 'K', 3, 4}) || bytes.HasPrefix(data, []byte{'P', 'K', 5, 6}) {
		media = "application/zip"
		kind = "archive"
		inspection = "members_scanned"
		i.scanZip(p, data, depth)
	}
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		media = "application/gzip"
		kind = "archive"
		inspection = "members_scanned"
		i.scanGzip(p, data, depth)
	}
	if len(data) > 262 && string(data[257:262]) == "ustar" {
		media = "application/x-tar"
		kind = "archive"
		inspection = "members_scanned"
		i.scanTar(p, data, depth)
	}
	if kind == "archive" {
		for _, issue := range i.result.Issues {
			if issue.Code == "invalid_resource_archive" && (issue.Path == p || strings.HasPrefix(issue.Path, p+"!")) {
				inspection = "incomplete"
				break
			}
		}
	}
	i.result.Resources = append(i.result.Resources, CapabilityResourceInfo{Path: p, Kind: kind, MediaType: media, Inspection: inspection})
	i.issue("warning", "binary_resource", p, "保留非文本资源原始字节；仅检查可识别的内容与已知凭据模式，未执行文件或验证其运行效果")
}
func (i *assetInspector) archiveError(p string) {
	i.issue("error", "invalid_resource_archive", p, "嵌套压缩资源无法检查，或含不安全路径、特殊文件、重复成员、加密内容或超出解压预算")
}
func (i *assetInspector) scanZip(p string, data []byte, depth int) {
	if depth >= 3 {
		i.archiveError(p)
		return
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		i.archiveError(p)
		return
	}
	names := map[string]bool{}
	for _, member := range reader.File {
		i.scanEntries++
		name := strings.TrimSuffix(member.Name, "/")
		fold := strings.ToLower(name)
		if i.scanEntries > capabilityMaxEntries || !resourcePath(name) || names[fold] || member.Flags&1 != 0 || (member.Mode().Type() != 0 && !member.Mode().IsDir()) || member.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			i.archiveError(p)
			return
		}
		names[fold] = true
		if capabilityStatePath(name) {
			i.issue("warning", "native_state", p+"!"+name, "压缩资源包含原生配置、账号或会话状态")
		}
		if member.Mode().IsDir() {
			continue
		}
		remaining := capabilityMaxBytes - i.scanBytes
		if member.UncompressedSize64 > uint64(remaining) {
			i.archiveError(p)
			return
		}
		stream, e := member.Open()
		if e != nil {
			i.archiveError(p)
			return
		}
		body, e := io.ReadAll(io.LimitReader(stream, remaining+1))
		stream.Close()
		if e != nil || int64(len(body)) > remaining {
			i.archiveError(p)
			return
		}
		i.scanBytes += int64(len(body))
		i.scanResource(p+"!"+name, body, depth+1)
	}
}
func (i *assetInspector) scanGzip(p string, data []byte, depth int) {
	if depth >= 3 {
		i.archiveError(p)
		return
	}
	input := bytes.NewReader(data)
	for member := 1; input.Len() > 0; member++ {
		reader, err := gzip.NewReader(input)
		if err != nil {
			i.archiveError(p)
			return
		}
		reader.Multistream(false)
		name := reader.Name
		if name == "" {
			name = "content"
		}
		if !resourcePath(name) {
			reader.Close()
			i.archiveError(p)
			return
		}
		remaining := capabilityMaxBytes - i.scanBytes
		body, err := io.ReadAll(io.LimitReader(reader, remaining+1))
		reader.Close()
		if err != nil || int64(len(body)) > remaining {
			i.archiveError(p)
			return
		}
		i.scanBytes += int64(len(body))
		i.scanEntries++
		if i.scanEntries > capabilityMaxEntries {
			i.archiveError(p)
			return
		}
		// Keep each gzip member's identity; concatenated streams have separate names.
		i.scanResource(fmt.Sprintf("%s!%d/%s", p, member, name), body, depth+1)
	}
}
func (i *assetInspector) scanTar(p string, data []byte, depth int) {
	if depth >= 3 {
		i.archiveError(p)
		return
	}
	reader := tar.NewReader(bytes.NewReader(data))
	names := map[string]bool{}
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			i.archiveError(p)
			return
		}
		name := strings.TrimSuffix(entry.Name, "/")
		fold := strings.ToLower(name)
		i.scanEntries++
		if i.scanEntries > capabilityMaxEntries || !resourcePath(name) || names[fold] || (entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeDir) || entry.Mode&07000 != 0 {
			i.archiveError(p)
			return
		}
		names[fold] = true
		if capabilityStatePath(name) {
			i.issue("warning", "native_state", p+"!"+name, "压缩资源包含原生配置、账号或会话状态")
		}
		if entry.Typeflag == tar.TypeDir {
			continue
		}
		remaining := capabilityMaxBytes - i.scanBytes
		if entry.Size < 0 || entry.Size > remaining {
			i.archiveError(p)
			return
		}
		body, err := io.ReadAll(io.LimitReader(reader, remaining+1))
		if err != nil || int64(len(body)) > remaining {
			i.archiveError(p)
			return
		}
		i.scanBytes += int64(len(body))
		i.scanResource(p+"!"+name, body, depth+1)
	}
}
