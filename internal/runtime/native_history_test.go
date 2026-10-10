package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCodexNativeHistoryPreservesPagedTurnsAndToolPayloads(t *testing.T) {
	c, f, scope := nativeHarness(t)
	scope.Cwd = t.TempDir()
	id := "018f0000-0000-7000-8000-000000000001"
	f.call = func(method string, p map[string]any) (any, error) {
		switch method {
		case "thread/read":
			if p["includeTurns"] != false || p["threadId"] != id {
				t.Fatal("unexpected metadata read")
			}
			return map[string]any{"thread": map[string]any{"id": id, "cwd": scope.Cwd, "status": map[string]string{"type": "notLoaded"}}}, nil
		case "thread/turns/list":
			if p["itemsView"] != "full" || p["limit"] != 50 || p["sortDirection"] != "asc" || p["threadId"] != id {
				t.Fatalf("wrong history query: %+v", p)
			}
			start, end, next := 0, 50, "page-2"
			if p["cursor"] != nil {
				if p["cursor"] != "page-2" {
					t.Fatal("wrong cursor")
				}
				start, end, next = 50, 51, ""
			}
			turns := []any{}
			for n := start; n < end; n++ {
				items := []any{}
				if n == 50 {
					items = append(items, map[string]any{"id": "tool", "type": "mcpToolCall", "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": "tool output"}}}}, map[string]any{"id": "future", "type": "futureNativeType", "opaque": "retained"})
				}
				turns = append(turns, map[string]any{"id": fmt.Sprint(n), "itemsView": "full", "status": "failed", "error": map[string]string{"message": "native failure"}, "items": items})
			}
			return map[string]any{"data": turns, "nextCursor": next}, nil
		default:
			return nil, fmt.Errorf("forbidden method %s", method)
		}
	}
	out, err := c.ReadNativeHistory(context.Background(), DiscoveryRequest{Instance: scope.Instance, Cwd: scope.Cwd}, id)
	if err != nil || len(out.Entries) != 53 || out.Entries[50].Kind != "turn" || !strings.Contains(string(out.Entries[50].Data), "native failure") || !strings.Contains(string(out.Entries[51].Data), "tool output") || out.Entries[52].Kind != "futureNativeType" {
		t.Fatalf("history: %+v %v", out, err)
	}
	if len(f.methods) != 3 {
		t.Fatalf("unexpected calls: %v", f.methods)
	}
}

func TestCodexNativeHistoryRefusesPartialOrForeignHistory(t *testing.T) {
	for _, scenario := range []string{"foreign-cwd", "foreign-id", "summary", "nil-items", "empty-page-cursor", "cursor-cycle", "page-error", "missing-page", "oversized-entry"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Cwd = t.TempDir()
			pages := 0
			f.call = func(method string, p map[string]any) (any, error) {
				if method == "thread/read" {
					cwd, id := scope.Cwd, "native-id"
					if scenario == "foreign-cwd" {
						cwd = t.TempDir()
					}
					if scenario == "foreign-id" {
						id = "other"
					}
					return map[string]any{"thread": map[string]any{"id": id, "cwd": cwd}}, nil
				}
				if method != "thread/turns/list" {
					t.Fatalf("unexpected method %s", method)
				}
				pages++
				if scenario == "page-error" {
					return nil, fmt.Errorf("native RPC failed")
				}
				if scenario == "missing-page" {
					return map[string]any{}, nil
				}
				turn := map[string]any{"id": "turn", "status": "completed", "itemsView": "full", "items": []any{}}
				switch scenario {
				case "summary":
					turn["itemsView"] = "summary"
				case "nil-items":
					turn["items"] = nil
				case "oversized-entry":
					turn["items"] = []any{map[string]any{"id": "large", "type": "commandExecution", "aggregatedOutput": strings.Repeat("a", MaxHistoryEntryBytes)}}
				case "empty-page-cursor":
					return map[string]any{"data": []any{}, "nextCursor": "next"}, nil
				case "cursor-cycle":
					return map[string]any{"data": []any{turn}, "nextCursor": "next"}, nil
				}
				return map[string]any{"data": []any{turn}}, nil
			}
			out, err := c.ReadNativeHistory(context.Background(), DiscoveryRequest{Instance: scope.Instance, Cwd: scope.Cwd}, "native-id")
			if err == nil || len(out.Entries) != 0 {
				t.Fatalf("partial history accepted: %+v %v", out, err)
			}
			if strings.HasPrefix(scenario, "foreign-") && pages != 0 {
				t.Fatal("foreign transcript was read")
			}
		})
	}
}
