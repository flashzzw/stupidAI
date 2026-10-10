package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"codex-netcap/internal/app"
)

func dumpNativeBindingResponses(handler http.Handler, responses map[string]json.RawMessage) (map[string]bool, error) {
	call := func(method, key string, body any, status int) (json.RawMessage, error) {
		data, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "http://127.0.0.1:9451"+key, strings.NewReader(string(data)))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			return nil, fmt.Errorf("%s: %d %s", key, response.Code, response.Body.String())
		}
		data = append(json.RawMessage{}, response.Body.Bytes()...)
		responses[key] = data
		return data, nil
	}
	var preview app.NativeDiscoveryResult
	if err := json.Unmarshal(responses["/api/native-sessions/preview"], &preview); err != nil {
		return nil, err
	}
	request := app.NativeBindingRequest{CommandID: "native-source-bind", ProjectID: "fixture-project", InstanceID: "codex-fixture", NativeID: externalNativeID, PreviewToken: preview.BindingToken}
	data, err := call("POST", "/api/native-sessions/bind", request, 200)
	if err != nil {
		return nil, err
	}
	var receipt app.CommandReceipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	repeated, err := call("POST", "/api/native-sessions/bind?fixture_repeat=true", request, 200)
	if err != nil {
		return nil, err
	}
	if _, err = call("GET", "/api/workspace?fixture_bound=true", nil, 200); err != nil {
		return nil, err
	}
	id := receipt.SessionID
	data, err = call("GET", "/api/sessions/"+id, nil, 200)
	if err != nil {
		return nil, err
	}
	var read app.SessionRead
	if err = json.Unmarshal(data, &read); err != nil {
		return nil, err
	}
	data, err = call("POST", "/api/sessions/"+id+"/native/history", map[string]string{}, 200)
	if err != nil {
		return nil, err
	}
	var history app.NativeHistoryResult
	if err = json.Unmarshal(data, &history); err != nil {
		return nil, err
	}
	for _, kind := range []string{"send", "resume", "respond", "reconcile"} {
		if _, err = call("POST", "/api/commands?fixture_reject="+kind, app.Command{ID: "native-reject-" + kind, Kind: kind, SessionID: id, Text: "must not execute"}, 409); err != nil {
			return nil, err
		}
	}
	for _, kind := range []string{"archive", "unarchive"} {
		if _, err = call("POST", "/api/commands?fixture="+kind, app.Command{ID: "native-" + kind, Kind: kind, SessionID: id}, 202); err != nil {
			return nil, err
		}
		if _, err = call("GET", "/api/workspace?fixture="+kind, nil, 200); err != nil {
			return nil, err
		}
		if _, err = call("GET", "/api/sessions/"+id+"?fixture="+kind, nil, 200); err != nil {
			return nil, err
		}
	}
	return map[string]bool{
		"binding_command_idempotent":        string(repeated) == string(responses["/api/native-sessions/bind"]),
		"binding_original_identity":         read.Session.Scope.NativeID == externalNativeID && read.Session.NativeBinding != nil && read.Session.Scope.StateNamespace != "",
		"binding_no_route_or_execution":     read.Session.Scope.Route.ID == "" && read.Session.ExecutionSnapshot == nil && len(read.Operations) == 0,
		"binding_remains_read_only":         read.Session.ControlMode == "native_readonly" && read.Session.Paused && read.Session.Access.Source == "native",
		"bound_history_original_source":     history.Session.NativeID == externalNativeID && len(history.Entries) == 50 && history.Total == 53,
		"binding_control_commands_rejected": true,
		"binding_local_archive_supported":   true,
	}, nil
}
