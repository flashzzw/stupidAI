package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"codex-netcap/internal/codex"
)

const (
	MaxHistoryEntries    = 5000
	MaxHistoryBytes      = 8 << 20
	MaxHistoryEntryBytes = 1 << 20
)

// Entries retain the native payload, including tools, errors and unknown types.
// These are persisted-history observations, never runtime control evidence.
type NativeHistoryEntry struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	TurnID  string          `json:"turn_id,omitempty"`
	Data    json.RawMessage `json:"data"`
	RawJSON string          `json:"raw_json,omitempty"`
}

type NativeHistorySnapshot struct {
	Session NativeSession        `json:"session"`
	Entries []NativeHistoryEntry `json:"entries"`
}

type NativeHistory interface {
	ReadNativeHistory(context.Context, DiscoveryRequest, string) (NativeHistorySnapshot, error)
}

func (a *Adapters) ReadNativeHistory(ctx context.Context, request DiscoveryRequest, id string) (NativeHistorySnapshot, error) {
	r, err := a.adapter(request.Instance.Kind)
	if err != nil {
		return NativeHistorySnapshot{}, err
	}
	h, ok := r.(NativeHistory)
	if !ok {
		return NativeHistorySnapshot{}, fmt.Errorf("该适配器尚未接入原生历史读取")
	}
	return h.ReadNativeHistory(ctx, request, id)
}

func (c *Codex) ReadNativeHistory(ctx context.Context, request DiscoveryRequest, id string) (NativeHistorySnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return NativeHistorySnapshot{}, err
	}
	return readCodexHistory(ctx, p, id, request.Cwd)
}

func readCodexHistory(ctx context.Context, p codex.Client, id, cwd string) (NativeHistorySnapshot, error) {
	var metadata struct {
		Thread discoveryThread `json:"thread"`
	}
	if err := p.Call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &metadata); err != nil {
		return NativeHistorySnapshot{}, err
	}
	resolved, err := filepath.EvalSymlinks(metadata.Thread.Cwd)
	if err != nil || resolved != cwd || metadata.Thread.ID != id {
		return NativeHistorySnapshot{}, fmt.Errorf("原生历史元数据的身份或项目不匹配")
	}
	out := NativeHistorySnapshot{Session: metadata.Thread.metadata(), Entries: []NativeHistoryEntry{}}
	cursor, size := "", 0
	seenCursors := map[string]bool{}
	appendEntry := func(entry NativeHistoryEntry) error {
		size += len(entry.Data)
		if len(out.Entries) >= MaxHistoryEntries || size > MaxHistoryBytes || len(entry.Data) > MaxHistoryEntryBytes {
			return fmt.Errorf("原生历史超过读取上限（5000 条、8 MiB、单条 1 MiB），未返回部分历史")
		}
		out.Entries = append(out.Entries, entry)
		return nil
	}
	for page := 0; page <= MaxHistoryEntries/50; page++ {
		var result struct {
			Data []json.RawMessage `json:"data"`
			Next string            `json:"nextCursor"`
		}
		params := map[string]any{"threadId": id, "limit": 50, "sortDirection": "asc", "itemsView": "full"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := p.Call(ctx, "thread/turns/list", params, &result); err != nil {
			return NativeHistorySnapshot{}, err
		}
		if result.Data == nil || len(result.Data) > 50 {
			return NativeHistorySnapshot{}, fmt.Errorf("原生历史轮次页缺失或超限")
		}
		for _, raw := range result.Data {
			var turn struct {
				ID        string            `json:"id"`
				Status    string            `json:"status"`
				ItemsView string            `json:"itemsView"`
				Items     []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(raw, &turn) != nil || turn.ID == "" || turn.Status == "" || turn.Items == nil || turn.ItemsView != "" && turn.ItemsView != "full" {
				return NativeHistorySnapshot{}, fmt.Errorf("原生轮次没有返回完整条目或身份")
			}
			var header map[string]json.RawMessage
			if err := json.Unmarshal(raw, &header); err != nil {
				return NativeHistorySnapshot{}, err
			}
			delete(header, "items")
			data, _ := json.Marshal(header)
			if err := appendEntry(NativeHistoryEntry{ID: turn.ID, Kind: "turn", TurnID: turn.ID, Data: data}); err != nil {
				return NativeHistorySnapshot{}, err
			}
			for _, rawItem := range turn.Items {
				var item struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				}
				if json.Unmarshal(rawItem, &item) != nil || item.ID == "" || item.Type == "" {
					return NativeHistorySnapshot{}, fmt.Errorf("原生历史条目缺少身份或类型")
				}
				if err := appendEntry(NativeHistoryEntry{ID: item.ID, Kind: item.Type, TurnID: turn.ID, Data: rawItem}); err != nil {
					return NativeHistorySnapshot{}, err
				}
			}
		}
		if result.Next == "" {
			return out, nil
		}
		if len(result.Data) == 0 || len(result.Next) > 4096 || seenCursors[result.Next] {
			return NativeHistorySnapshot{}, fmt.Errorf("原生历史分页未推进")
		}
		seenCursors[result.Next], cursor = true, result.Next
	}
	return NativeHistorySnapshot{}, fmt.Errorf("原生历史分页超过读取上限")
}

func (c *Claude) ReadNativeHistory(ctx context.Context, request DiscoveryRequest, id string) (NativeHistorySnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastUse[request.Instance.ID] = time.Now()
	p, err := c.client(ctx, request.Instance)
	if err != nil {
		return NativeHistorySnapshot{}, err
	}
	var out NativeHistorySnapshot
	err = p.call(ctx, "read_native_history", map[string]any{"cwd": request.Cwd, "native_id": id}, &out)
	return out, err
}
