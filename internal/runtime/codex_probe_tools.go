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
)

func codexProbeTools(scope Scope, path string) ([]ToolResult, error) {
	if path == "" {
		return nil, nil
	}
	profile, err := filepath.EvalSymlinks(scope.Instance.Profile)
	if err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(profile, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("探测 rollout 不属于隔离 profile")
	}
	f, err := os.Open(actual)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("探测 rollout 必须是可读取的普通文件")
	}
	b, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 || len(b) > 0 && b[len(b)-1] != '\n' {
		return nil, fmt.Errorf("探测 rollout 过大或尚未完整写入")
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	owned := false
	commands := map[string]ToolResult{}
	seen := map[string]bool{}
	order := []string{}
	for scanner.Scan() {
		var entry struct {
			Type     string          `json:"type"`
			Payload  json.RawMessage `json:"payload"`
			Metadata struct {
				ClientAuthored *bool `json:"client_authored"`
			} `json:"metadata"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			return nil, fmt.Errorf("探测 rollout 格式无效")
		}
		if entry.Type == "session_meta" {
			var meta struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			}
			if owned || scope.NativeID == "" || json.Unmarshal(entry.Payload, &meta) != nil || meta.ID != scope.NativeID || filepath.Clean(meta.Cwd) != filepath.Clean(scope.Cwd) {
				return nil, fmt.Errorf("探测 rollout 原生身份不匹配")
			}
			owned = true
		}
		if entry.Type != "response_item" {
			continue
		}
		if !owned {
			return nil, fmt.Errorf("探测 rollout 工具记录缺少先行会话身份")
		}
		var item struct {
			Type      string          `json:"type"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Namespace string          `json:"namespace"`
			Input     string          `json:"input"`
			Output    json.RawMessage `json:"output"`
			Meta      struct {
				TurnID string `json:"turn_id"`
			} `json:"internal_chat_message_metadata_passthrough"`
		}
		if json.Unmarshal(entry.Payload, &item) != nil || item.Meta.TurnID == "" || item.CallID == "" {
			continue
		}
		key := item.Meta.TurnID + "/" + item.CallID
		if item.Type == "custom_tool_call" || item.Type == "custom_tool_call_output" {
			identity := item.Type + "/" + key
			if seen[identity] {
				return nil, fmt.Errorf("探测 rollout 存在重复工具调用或结果")
			}
			seen[identity] = true
		}
		if item.Type == "custom_tool_call" && item.Name == "exec" && item.Namespace == "functions" {
			if command, ok := execCommand(item.Input); ok {
				commands[key] = ToolResult{ID: item.CallID, TurnID: item.Meta.TurnID, Command: command}
				order = append(order, key)
			}
		}
		if item.Type != "custom_tool_call_output" {
			continue
		}
		tool, exists := commands[key]
		if !exists {
			continue
		}
		if entry.Metadata.ClientAuthored == nil || *entry.Metadata.ClientAuthored {
			return nil, fmt.Errorf("探测工具结果缺少原生执行来源")
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(item.Output, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "input_text" {
				continue
			}
			var result struct {
				ExitCode *int   `json:"exit_code"`
				Output   string `json:"output"`
			}
			if json.Unmarshal([]byte(block.Text), &result) == nil && result.ExitCode != nil {
				if tool.Complete {
					return nil, fmt.Errorf("探测调用包含多个终态工具结果")
				}
				tool.ExitCode, tool.Output, tool.Complete = result.ExitCode, result.Output, true
				commands[key] = tool
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !owned {
		return nil, fmt.Errorf("缺少探测 rollout 会话所有权")
	}
	results := []ToolResult{}
	for _, key := range order {
		results = append(results, commands[key])
	}
	return results, nil
}
