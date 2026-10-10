package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func (f *controlledRuntime) ReadNativeHistory(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeHistorySnapshot, error) {
	f.mu.Lock()
	revision := f.historyRevision
	f.mu.Unlock()
	out := runtime.NativeHistorySnapshot{Session: runtime.NativeSession{NativeID: id, Cwd: r.Cwd, Title: "工具与系统历史样本", NativeStatus: "notLoaded"}, Entries: []runtime.NativeHistoryEntry{}}
	if id == controlledNativeID {
		return out, nil
	}
	for n := 0; n < 53; n++ {
		kind, raw := "agentMessage", fmt.Sprintf(`{"id":"message-%d","text":"历史消息 %d"}`, n, n)
		switch n {
		case 0:
			kind, raw = "turn", `{"id":"turn-1","status":"failed","error":{"message":"工具退出错误"}}`
		case 1:
			kind, raw = "userMessage", `{"content":[{"type":"text","text":"<script>window.historyInjected=true</script> 原始文本"}]}`
		case 2:
			kind, raw = "mcpToolCall", fmt.Sprintf(`{"tool":"fixture.read","arguments":{"path":"sample.txt"},"result":{"content":[{"type":"text","text":"工具输出 revision %d"}]}}`, revision)
		case 3:
			kind, raw = "system", `{"type":"system","content":"系统记录正文","subtype":"local_command"}`
		case 4:
			kind, raw = "futureNativeType", `{"opaque":{"counter":9007199254740993},"note":"未知字段原样保留"}`
		}
		out.Entries = append(out.Entries, runtime.NativeHistoryEntry{ID: fmt.Sprint(n), Kind: kind, TurnID: "turn-1", Data: json.RawMessage(raw)})
	}
	return out, nil
}

func dumpNativeHistoryResponses(handler http.Handler, responses map[string]json.RawMessage, f *controlledRuntime) (map[string]bool, error) {
	call := func(key string, body app.NativeHistoryRequest, status int) (app.NativeHistoryResult, error) {
		data, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", "http://127.0.0.1:9451"+key, strings.NewReader(string(data)))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			return app.NativeHistoryResult{}, fmt.Errorf("%s: %d %s", key, response.Code, response.Body.String())
		}
		responses[key] = append(json.RawMessage{}, response.Body.Bytes()...)
		var result app.NativeHistoryResult
		if status == 200 {
			err := json.Unmarshal(response.Body.Bytes(), &result)
			return result, err
		}
		return result, nil
	}
	request := app.NativeHistoryRequest{ProjectID: "fixture-project", InstanceID: "codex-fixture", NativeID: externalNativeID}
	first, err := call("/api/native-sessions/history", request, 200)
	if err != nil {
		return nil, err
	}
	request.Cursor = first.Next
	last, err := call("/api/native-sessions/history?fixture_page=2", request, 200)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.historyRevision++
	f.mu.Unlock()
	_, err = call("/api/native-sessions/history?fixture_error=drift", request, 409)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.historyRevision = 0
	f.mu.Unlock()
	request.NativeID, request.Cursor = controlledNativeID, ""
	empty, err := call("/api/native-sessions/history?fixture_empty=true", request, 200)
	if err != nil {
		return nil, err
	}
	return map[string]bool{
		"history_read_only":                    first.ReadOnly && last.ReadOnly,
		"history_pages_complete_within_scope":  len(first.Entries) == 50 && len(last.Entries) == 3 && first.Total == 53 && last.Offset == 50 && last.Next == "",
		"history_baseline_reused":              first.Baseline == last.Baseline && first.Baseline != "",
		"history_retains_tools_system_unknown": len(first.Entries) > 4 && strings.Contains(first.Entries[2].RawJSON, "工具输出") && strings.Contains(first.Entries[3].RawJSON, "系统记录正文") && strings.Contains(first.Entries[4].RawJSON, "9007199254740993"),
		"history_drift_rejected":               strings.Contains(string(responses["/api/native-sessions/history?fixture_error=drift"]), "发生变化"),
		"empty_history_not_proof":              empty.Total == 0 && empty.Next == "" && strings.Contains(empty.Detail, "不能据此证明"),
	}, nil
}
