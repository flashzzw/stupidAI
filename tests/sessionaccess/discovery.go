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

const controlledNativeID = "018f0000-0000-7000-8000-000000000001"
const externalNativeID = "018f0000-0000-7000-8000-000000000002"

func (f *controlledRuntime) Discover(_ context.Context, r runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
	if r.Cursor != "" {
		return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{}}, nil
	}
	return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{
		{NativeID: controlledNativeID, Title: "已有绑定的原生记录", Cwd: r.Cwd, Source: "appServer", NativeStatus: "idle", Provider: "fixture"},
		{NativeID: externalNativeID, Title: "尚未接入的 CLI 记录", Cwd: r.Cwd, Source: "cli", NativeStatus: "notLoaded", Provider: "other-provider"},
	}, Next: "page-2"}, nil
}
func (f *controlledRuntime) InspectNative(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
	if id != externalNativeID {
		return runtime.NativeSession{}, fmt.Errorf("fixture record not found")
	}
	return runtime.NativeSession{NativeID: id, Title: "尚未接入的 CLI 记录", Cwd: r.Cwd, Source: "cli", NativeStatus: "notLoaded", Provider: "other-provider"}, nil
}

func dumpNativeResponses(handler http.Handler, responses map[string]json.RawMessage) (map[string]bool, error) {
	call := func(path string, body any) (app.NativeDiscoveryResult, error) {
		data, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", "http://127.0.0.1:9451"+path, strings.NewReader(string(data)))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			return app.NativeDiscoveryResult{}, fmt.Errorf("%s: %d %s", path, response.Code, response.Body.String())
		}
		responses[path] = append(json.RawMessage{}, response.Body.Bytes()...)
		var result app.NativeDiscoveryResult
		err := json.Unmarshal(response.Body.Bytes(), &result)
		return result, err
	}
	query := app.NativeDiscoveryRequest{ProjectID: "fixture-project", InstanceID: "codex-fixture"}
	first, err := call("/api/native-sessions/discover", query)
	if err != nil {
		return nil, err
	}
	query.Cursor = first.Next
	second, err := call("/api/native-sessions/discover?fixture_page=2", query)
	if err != nil {
		return nil, err
	}
	preview, err := call("/api/native-sessions/preview", app.NativePreviewRequest{ProjectID: query.ProjectID, InstanceID: query.InstanceID, NativeID: externalNativeID})
	if err != nil {
		return nil, err
	}
	return map[string]bool{
		"native_metadata_read_only": first.ReadOnly && preview.ReadOnly,
		"native_metadata_scoped":    len(first.Sessions) == 2 && first.StateNamespace != "" && first.Cwd != "",
		"native_existing_binding":   len(first.Sessions) == 2 && len(first.Sessions[0].KnownSessions) == 1 && len(first.Sessions[1].KnownSessions) == 0,
		"native_pagination":         first.Next != "" && len(second.Sessions) == 0 && second.Next == "",
		"native_preview_identity":   len(preview.Sessions) == 1 && preview.Sessions[0].NativeID == externalNativeID && preview.Sessions[0].NativeStatus == "notLoaded",
	}, nil
}
