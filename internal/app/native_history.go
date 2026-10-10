package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type NativeHistoryRequest struct {
	ProjectID  string `json:"project_id"`
	InstanceID string `json:"instance_id"`
	NativeID   string `json:"native_id"`
	Cursor     string `json:"cursor,omitempty"`
}

type NativeHistoryResult struct {
	ReadOnly       bool                         `json:"read_only"`
	InstanceID     string                       `json:"instance_id"`
	Kind           string                       `json:"kind"`
	StateNamespace string                       `json:"state_namespace"`
	Session        runtime.NativeSession        `json:"session"`
	Entries        []runtime.NativeHistoryEntry `json:"entries"`
	Baseline       string                       `json:"baseline"`
	Offset         int                          `json:"offset"`
	Total          int                          `json:"total"`
	Next           string                       `json:"next_cursor,omitempty"`
	At             string                       `json:"at"`
	Coverage       string                       `json:"coverage"`
	Detail         string                       `json:"detail"`
}

type historyCursor struct {
	Binding  string
	Baseline string
	Offset   int
}

// Re-read the bounded native view on every page. Two matching reads detect
// observable drift without treating metadata timestamps or offset cursors as
// immutable snapshots. This is not a native atomic snapshot or control proof.
func (a *Application) ReadNativeHistory(ctx context.Context, request NativeHistoryRequest) (NativeHistoryResult, error) {
	if _, err := uuid.Parse(request.NativeID); err != nil {
		return NativeHistoryResult{}, fmt.Errorf("原生会话 ID 必须为 UUID")
	}
	discovery := NativeDiscoveryRequest{ProjectID: request.ProjectID, InstanceID: request.InstanceID}
	a.mu.Lock()
	native, binding, namespace, err := a.discoveryBinding(discovery)
	a.mu.Unlock()
	if err != nil {
		return NativeHistoryResult{}, err
	}
	bound := digest([]string{binding, request.NativeID, "native-history-v1-asc-50"})
	var cursor historyCursor
	if request.Cursor != "" {
		if len(request.Cursor) > 1024 {
			return NativeHistoryResult{}, fmt.Errorf("历史游标过长")
		}
		data, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Binding != bound || len(cursor.Baseline) != 64 || cursor.Offset < 1 || cursor.Offset >= runtime.MaxHistoryEntries || cursor.Offset%50 != 0 {
			return NativeHistoryResult{}, fmt.Errorf("历史游标不属于当前会话/项目/执行配置，请从头读取")
		}
	}
	history, ok := a.runtime.(runtime.NativeHistory)
	if !ok {
		return NativeHistoryResult{}, fmt.Errorf("运行适配器没有原生历史读取接口")
	}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var snapshot runtime.NativeHistorySnapshot
	var baseline string
	for read := 0; read < 2; read++ {
		snapshot, err = history.ReadNativeHistory(check, native, request.NativeID)
		if err != nil {
			return NativeHistoryResult{}, err
		}
		if err = validateNativeHistory(&snapshot, native.Cwd, request.NativeID); err != nil {
			return NativeHistoryResult{}, err
		}
		current := digest(snapshot)
		if read == 1 && baseline != current || cursor.Baseline != "" && cursor.Baseline != current {
			return NativeHistoryResult{}, fmt.Errorf("原生历史在读取期间或分页后发生变化，请从头重新读取")
		}
		baseline = current
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, after, _, err := a.discoveryBinding(discovery)
	if err != nil || after != binding {
		return NativeHistoryResult{}, fmt.Errorf("历史读取期间项目或执行配置已变化，请重新读取")
	}
	if err = check.Err(); err != nil {
		return NativeHistoryResult{}, err
	}
	if cursor.Offset > 0 && cursor.Offset >= len(snapshot.Entries) {
		return NativeHistoryResult{}, fmt.Errorf("历史游标超过当前记录范围，请从头读取")
	}
	end := min(cursor.Offset+50, len(snapshot.Entries))
	out := NativeHistoryResult{ReadOnly: true, InstanceID: native.Instance.ID, Kind: native.Instance.Kind, StateNamespace: namespace,
		Session: snapshot.Session, Entries: snapshot.Entries[cursor.Offset:end], Baseline: baseline, Offset: cursor.Offset, Total: len(snapshot.Entries), At: now(),
		Coverage: "codex_persisted_turn_items",
		Detail:   "原生接口可见的持久轮次及全部条目（含工具、错误和未知类型）。连续两次读取内容一致；这不是原生原子快照，也不证明实时状态或输入权。未包含原始事件日志、外部文件内容或子会话历史。"}
	if native.Instance.Kind == "claude" {
		out.Coverage = "claude_main_transcript_entries"
		out.Detail = "SDK 读取的主会话文件记录，按文件顺序显示，含系统正文、工具调用/结果、元数据和仍在文件中的压缩前记录及旧分支；不等同于当前有效对话。连续两次读取内容一致；不包含 SDK 无法解析的行、子会话文件或外部文件内容，也不证明实时状态或输入权。"
	}
	if len(snapshot.Entries) == 0 {
		out.Detail += " 接口未返回可读条目，不能据此证明没有历史或历史完整。"
	}
	if end < len(snapshot.Entries) {
		data, _ := json.Marshal(historyCursor{Binding: bound, Baseline: baseline, Offset: end})
		out.Next = base64.RawURLEncoding.EncodeToString(data)
	}
	return out, nil
}

func validateNativeHistory(snapshot *runtime.NativeHistorySnapshot, cwd, id string) error {
	resolved, err := filepath.EvalSymlinks(snapshot.Session.Cwd)
	if err != nil || resolved != cwd || snapshot.Session.NativeID != id {
		return fmt.Errorf("原生历史的身份或工作目录不属于所选项目")
	}
	snapshot.Session.Cwd = resolved
	if snapshot.Entries == nil || len(snapshot.Entries) > runtime.MaxHistoryEntries {
		return fmt.Errorf("原生历史条目缺失或超过读取上限")
	}
	seen, size := map[string]bool{}, 0
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		// Namespace turn headers separately from their items; native item IDs
		// need only be unique within their containing turn.
		key := entry.TurnID + "\x00item\x00" + entry.ID
		if entry.Kind == "turn" {
			key = entry.TurnID + "\x00turn\x00" + entry.ID
		}
		if entry.ID == "" || entry.Kind == "" || seen[key] {
			return fmt.Errorf("原生历史条目身份缺失或重复")
		}
		seen[key] = true
		size += len(entry.Data)
		if size > runtime.MaxHistoryBytes || len(entry.Data) > runtime.MaxHistoryEntryBytes {
			return fmt.Errorf("原生历史超过 8 MiB 或单条 1 MiB 读取上限")
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(entry.Data, &object) != nil || object == nil {
			return fmt.Errorf("原生历史条目不是有效对象")
		}
		// Canonicalize key order so equivalent native JSON hashes equally.
		var value any
		decoder := json.NewDecoder(bytes.NewReader(entry.Data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		entry.Data, _ = json.Marshal(value)
		entry.RawJSON = string(entry.Data)
	}
	return nil
}
