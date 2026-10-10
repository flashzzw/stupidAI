package events

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Observation struct {
	Kind               Kind
	NativeSessionID    string
	NativeTurnID       string
	NativeRequestID    string
	NativeConnectionID string
	Cwd                string
	Summary            string
}

func field(m map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := m[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }

func Normalize(agent, format string, payload json.RawMessage) ([]Observation, error) {
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil || raw == nil {
		return nil, fmt.Errorf("观察事件必须为 JSON 对象")
	}
	var observations []Observation
	switch format {
	case "hook":
		if agent != "codex" && agent != "claude" {
			return nil, fmt.Errorf("hook 格式与实例不匹配")
		}
		if field(raw, "hook_source") == "opencode-plugin" {
			return nil, nil
		}
		o := Observation{NativeSessionID: field(raw, "session_id", "thread_id"), NativeTurnID: field(raw, "turn_id"), NativeRequestID: field(raw, "request_id", "permission_request_id"), NativeConnectionID: field(raw, "connection_id"), Cwd: field(raw, "cwd"), Summary: field(raw, "message", "last_assistant_message")}
		switch field(raw, "hook_event_name") {
		case "SessionStart":
			o.Kind = SessionStarted
		case "SessionEnd":
			o.Kind = SessionStopped
		case "UserPromptSubmit", "PreToolUse":
			o.Kind = Activity
		case "PermissionRequest":
			o.Kind = PermissionRequired
			if o.Summary == "" {
				o.Summary = field(object(raw["tool_input"]), "command")
			}
		case "Stop":
			o.Kind = TurnCompleted
		case "Notification":
			if agent == "claude" && (field(raw, "notification_type") == "idle_prompt" || field(raw, "notification_type") == "elicitation_dialog") {
				o.Kind = InputRequired
			}
		case "PostToolUseFailure", "StopFailure":
			if agent == "claude" {
				o.Kind = RunFailed
				if o.Summary == "" {
					o.Summary = field(object(raw["error"]), "message")
				}
			}
		}
		if o.Kind != "" {
			observations = append(observations, o)
		}
	case "opencode":
		if agent != "opencode" {
			return nil, fmt.Errorf("plugin 格式与实例不匹配")
		}
		p := object(raw["properties"])
		info := object(p["info"])
		o := Observation{NativeSessionID: field(p, "sessionID"), NativeTurnID: field(p, "turnID", "parentID"), Cwd: field(p, "directory"), Summary: field(p, "message")}
		if o.NativeSessionID == "" {
			o.NativeSessionID = field(info, "id")
		}
		if o.Cwd == "" {
			o.Cwd = field(info, "directory")
		}
		if o.Cwd == "" {
			o.Cwd = field(raw, "directory")
		}
		switch field(raw, "type") {
		case "session.created":
			o.Kind = SessionStarted
		case "session.deleted":
			o.Kind = SessionStopped
		case "session.idle":
			o.Kind = TurnCompleted
		case "session.status":
			if field(object(p["status"]), "type") == "busy" {
				o.Kind = Activity
			}
		case "permission.asked":
			o.Kind, o.NativeRequestID, o.Summary = PermissionRequired, field(p, "id", "requestID"), field(p, "permission")
		case "question.asked":
			o.Kind, o.NativeRequestID = InputRequired, field(p, "id", "requestID")
			if questions, ok := p["questions"].([]any); ok {
				parts := []string{}
				for _, q := range questions {
					parts = append(parts, field(object(q), "question", "header"))
				}
				o.Summary = strings.Join(parts, "\n")
			}
		case "permission.replied", "question.replied", "question.rejected":
			o.Kind, o.NativeRequestID = InteractionResolved, field(p, "requestID", "id")
		case "session.error":
			err := object(p["error"])
			o.Kind = RunFailed
			if field(err, "name") == "MessageAbortedError" {
				o.Kind = SessionStopped
			}
			o.Summary = field(object(err["data"]), "message")
			if o.Summary == "" {
				o.Summary = field(err, "message", "name")
			}
		}
		if o.Kind != "" {
			observations = append(observations, o)
		}
	case "otel":
		if agent != "codex" {
			return nil, fmt.Errorf("OTEL 格式与实例不匹配")
		}
		observations = normalizeOTEL(raw)
	default:
		return nil, fmt.Errorf("不支持的观察事件格式")
	}
	for n := range observations {
		o := &observations[n]
		if o.NativeSessionID == "" || len(o.NativeSessionID) > 200 || len(o.NativeTurnID) > 200 || len(o.NativeRequestID) > 200 {
			return nil, fmt.Errorf("观察事件缺少有效原生身份")
		}
		if len(o.Summary) > 20000 {
			o.Summary = string([]rune(o.Summary)[:min(len([]rune(o.Summary)), 5000)])
		}
		if o.Summary == "" {
			o.Summary = string(o.Kind)
		}
	}
	return observations, nil
}

func otelValue(value any) any {
	m := object(value)
	for _, key := range []string{"stringValue", "boolValue", "intValue", "doubleValue"} {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func attributes(into map[string]any, value any) {
	items, _ := value.([]any)
	for _, item := range items {
		m := object(item)
		if key := field(m, "key"); key != "" {
			into[key] = otelValue(m["value"])
		}
	}
}

func normalizeOTEL(raw map[string]any) []Observation {
	out := []Observation{}
	resources, _ := raw["resourceLogs"].([]any)
	for _, resource := range resources {
		r := object(resource)
		scopes, _ := r["scopeLogs"].([]any)
		for _, scope := range scopes {
			s := object(scope)
			records, _ := s["logRecords"].([]any)
			for _, record := range records {
				m := object(record)
				a := map[string]any{}
				attributes(a, object(r["resource"])["attributes"])
				attributes(a, object(s["scope"])["attributes"])
				attributes(a, m["attributes"])
				name := field(a, "event.name", "event_name", "name", "codex.event_name", "otel.event.name")
				if name != "codex.api_request" && name != "codex.sse_event" && name != "codex.websocket_request" && name != "codex.websocket_event" {
					continue
				}
				endpoint := field(a, "endpoint", "api.path", "http.route", "http.target", "http.request.url", "http.url", "url.full", "url")
				if parsed, err := url.Parse(endpoint); err == nil && name == "codex.api_request" {
					path := strings.TrimRight(parsed.Path, "/")
					if path == "models" || path == "/models" || path == "/v1/models" || path == "/backend-api/codex/models" {
						continue
					}
				}
				kind := Kind("")
				requestType := strings.ToLower(field(a, "request.type", "request_type", "type"))
				if requestType == "requestuserinput" || requestType == "request_user_input" || requestType == "toolrequestuserinput" || requestType == "tool_request_user_input" {
					kind = InputRequired
				}
				errorText := field(a, "error.message", "error_details", "exception.message", "error", "failure")
				code, _ := strconv.Atoi(fmt.Sprint(a["http.response.status_code"]))
				if code == 0 {
					code, _ = strconv.Atoi(fmt.Sprint(a["status_code"]))
				}
				if errorText != "" || code >= 400 || a["success"] == false || a["success"] == "false" {
					kind = RunFailed
				}
				if kind == "" {
					continue
				}
				if field(a, "conversation.id", "conversation_id", "session_id", "thread.id", "thread_id") == "" {
					continue
				}
				summary := errorText
				if summary == "" {
					summary = field(a, "message", "prompt", "question")
					if summary == "" {
						summary, _ = otelValue(m["body"]).(string)
					}
				}
				out = append(out, Observation{Kind: kind, NativeSessionID: field(a, "conversation.id", "conversation_id", "session_id", "thread.id", "thread_id"), NativeTurnID: field(a, "turn.id", "turn_id"), NativeRequestID: field(a, "request.id", "request_id"), Cwd: field(a, "cwd", "working_directory", "workspace", "codex.cwd"), Summary: summary})
			}
		}
	}
	return out
}
