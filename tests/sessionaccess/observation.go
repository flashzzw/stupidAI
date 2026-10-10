package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
)

func (f *controlledRuntime) ObserveNativeExecution(_ context.Context, scope runtime.Scope) (runtime.View, error) {
	f.mu.Lock()
	mode := f.observationMode
	f.mu.Unlock()
	if !scope.NoAutoResume || scope.NativeID != externalNativeID || scope.Route.ID != "" || scope.StateNamespace == "" {
		return runtime.View{}, fmt.Errorf("unbound or controlling observation")
	}
	view := runtime.View{Executor: controlledExecutor(), NativeID: scope.NativeID, NativeStatus: "active", Status: "running", TerminalCommand: "must be removed",
		Evidence:   runtime.Evidence{Source: "controlled.native.observation", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Detail: "受控原轮次、后台终端与队列证据；没有调用真实 CLI。"},
		Turns:      []runtime.Turn{{ID: "original-live-turn", Status: "inProgress"}},
		Messages:   []runtime.Message{{ID: "original-message", TurnID: "original-live-turn", Role: "assistant", Text: "原执行的进行中消息", Status: "inProgress"}},
		Executions: []runtime.Execution{{ID: "original-terminal", Kind: "terminal", Status: "running"}, {ID: "original-queue", Kind: "queue", Status: "queued"}},
		Pending:    []runtime.Pending{{ID: "original-request", Title: "原终端中的待审批请求", Status: "unverified"}},
		Tools:      []runtime.ToolResult{{ID: "original-tool", TurnID: "original-live-turn", Command: "owned-fixture", Output: "原工具输出\n<script>window.__nativeExecutionInjected = true</script>", Complete: false}},
	}
	switch mode {
	case "idle":
		view.NativeStatus, view.Status, view.ExecutionClear, view.Executions = "idle", "idle", true, nil
	case "not_loaded":
		view.NativeStatus, view.Status = "notLoaded", "unknown"
		view.Evidence.Complete = false
		view.Evidence.Detail = "未加载线程的终端范围不可读"
	case "incomplete":
		view.Evidence.Complete = false
	case "disconnected":
		return view, fmt.Errorf("原后台连接已断开；未恢复执行")
	}
	return view, nil
}

func dumpNativeObservationResponses(handler http.Handler, responses map[string]json.RawMessage, f *controlledRuntime) (map[string]bool, error) {
	var bound app.CommandReceipt
	if err := json.Unmarshal(responses["/api/native-sessions/bind"], &bound); err != nil {
		return nil, err
	}
	checks := map[string]bool{}
	for _, mode := range []string{"running", "idle", "not_loaded", "incomplete", "disconnected"} {
		f.mu.Lock()
		f.observationMode = mode
		f.mu.Unlock()
		key := "/api/sessions/" + bound.SessionID + "/native/observe?fixture=" + mode
		request := httptest.NewRequest("POST", "http://127.0.0.1:9451"+key, strings.NewReader("{}"))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			return nil, fmt.Errorf("native observation %s: %d %s", mode, response.Code, response.Body.String())
		}
		responses[key] = append(json.RawMessage{}, response.Body.Bytes()...)
		var out app.NativeObservation
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			return nil, err
		}
		checks["native_observation_"+mode] = out.ReadOnly && out.View.NativeID == externalNativeID && out.View.TerminalCommand == "" && (out.Status == "observed") == (mode == "running" || mode == "idle")
		if mode == "running" {
			checks["native_original_tools_and_occupancy"] = len(out.View.Tools) == 1 && len(out.View.Executions) == 2 && len(out.View.Pending) == 1
		}
	}
	f.mu.Lock()
	f.observationMode = "running"
	f.mu.Unlock()
	return checks, nil
}
