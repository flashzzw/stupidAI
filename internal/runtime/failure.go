package runtime

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codex-netcap/internal/codex"
)

// Failure contains only structured native error evidence. Model text, tool
// output and control-transport HTTP errors must not become provider failures.
type Failure struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Code    string `json:"code,omitempty"`
	RetryAt string `json:"retry_at,omitempty"`
}

func codexFailure(err *codex.TurnError) *Failure {
	if err == nil {
		return nil
	}
	f := &Failure{Kind: "unknown", Source: "codex.turn.error"}
	if json.Unmarshal(err.Info, &f.Code) == nil {
		switch f.Code {
		case "unauthorized":
			f.Kind = "authentication"
		case "usageLimitExceeded":
			f.Kind = "quota"
		case "rateLimitExceeded":
			f.Kind = "rate_limit"
		case "serverOverloaded", "internalServerError":
			f.Kind = "service"
		case "contextWindowExceeded", "sessionBudgetExceeded", "badRequest", "threadRollbackFailed", "cyberPolicy", "misalignmentPolicyViolation":
			f.Kind = "session"
		case "sandboxError":
			f.Kind = "tool"
		default:
			f.Code = "other"
		}
		return f
	}
	var variants map[string]struct {
		Status *int `json:"httpStatusCode"`
	}
	if json.Unmarshal(err.Info, &variants) != nil || len(variants) != 1 {
		return f
	}
	for code, value := range variants {
		switch code {
		case "httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts":
			f.Code = code
			if value.Status == nil {
				f.Kind = "network"
			} else {
				f.Kind = providerHTTPFailure(*value.Status)
			}
		case "activeTurnNotSteerable":
			f.Kind, f.Code = "session", code
		}
	}
	return f
}

func providerHTTPFailure(status int) string {
	switch {
	case status == 401:
		return "authentication"
	case status == 403:
		return "permission"
	case status == 402:
		return "quota"
	case status == 429:
		return "rate_limit"
	case status == 408:
		return "network"
	case status >= 500 && status <= 599:
		return "service"
	case status >= 400 && status <= 499:
		return "session"
	default:
		return "unknown"
	}
}

func claudeFailure(code string) *Failure {
	if code == "" {
		return nil
	}
	f := &Failure{Source: "claude.sdk.assistant.error", Code: code, Kind: "unknown"}
	switch code {
	case "authentication_failed", "cloud_credential_error":
		f.Kind = "authentication"
	case "oauth_org_not_allowed", "account_on_hold", "verification_required":
		f.Kind = "permission"
	case "billing_error":
		f.Kind = "quota"
	case "rate_limit":
		f.Kind = "rate_limit"
	case "overloaded", "server_error":
		f.Kind = "service"
	case "invalid_request", "model_not_found", "max_output_tokens":
		f.Kind = "session"
	default:
		f.Code = "unknown"
	}
	return f
}

func nativeTimestamp(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339Nano)
}

func opencodeFailure(name string, data json.RawMessage, completed int64) *Failure {
	f := &Failure{Kind: "unknown", Source: "opencode.message.error"}
	switch name {
	case "ProviderAuthError":
		f.Kind, f.Code = "authentication", name
	case "ContextOverflowError", "MessageOutputLengthError":
		f.Kind, f.Code = "session", name
	case "MessageAbortedError":
		f.Kind, f.Code = "cancelled", name
	case "APIError":
		var native struct {
			Status    *int              `json:"statusCode"`
			Retryable bool              `json:"isRetryable"`
			Headers   map[string]string `json:"responseHeaders"`
		}
		if json.Unmarshal(data, &native) != nil {
			return f
		}
		f.Code = name
		if native.Status != nil {
			f.Kind = providerHTTPFailure(*native.Status)
		} else if native.Retryable {
			f.Kind = "network"
		}
		for key, value := range native.Headers {
			if !strings.EqualFold(key, "retry-after") {
				continue
			}
			if seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32); err == nil && completed > 0 {
				f.RetryAt = time.UnixMilli(completed).Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339Nano)
			} else if at, err := http.ParseTime(value); err == nil {
				f.RetryAt = at.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return f
}
