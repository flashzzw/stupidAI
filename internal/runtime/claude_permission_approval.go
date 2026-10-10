package runtime

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PermissionApprovalEvidence compares observations of the original input's
// native rule list. It is never proof of atomic policy enforcement.
type PermissionApprovalEvidence struct {
	Version               int    `json:"version"`
	Complete              bool   `json:"complete"`
	QueryID               string `json:"query_id"`
	ConfigurationID       string `json:"configuration_id"`
	BaselineAt            string `json:"baseline_at"`
	BaselineHash          string `json:"baseline_hash"`
	CheckedAt             string `json:"checked_at"`
	CheckedHash           string `json:"checked_hash"`
	State                 string `json:"state"`
	ConfigurationNoticeID string `json:"configuration_notice_id,omitempty"`
	Detail                string `json:"detail,omitempty"`
}

func claudePermissionApproval(s claudeSnapshot, request claudeRequest) *PermissionApprovalEvidence {
	unknown := &PermissionApprovalEvidence{Version: 1, State: "unavailable", Detail: "原审批缺少可核对的权限规则观察，不能批准；可拒绝本次工具操作或中止原输入。"}
	if request.PermissionGuard == nil {
		return unknown
	}
	g := *request.PermissionGuard
	validHash := func(v string) bool { return len(v) == 64 && strings.Trim(v, "0123456789abcdef") == "" }
	validTime := func(v string) bool { _, err := time.Parse(time.RFC3339Nano, v); return err == nil }
	_, queryError := uuid.Parse(g.QueryID)
	if g.Version != 1 || g.Complete || queryError != nil || !validHash(g.ConfigurationID) ||
		s.InstructionObserver == nil || g.QueryID != s.InstructionObserver.CurrentQueryID || g.ConfigurationID != s.InstructionObserver.CurrentConfigurationID {
		return unknown
	}
	if g.BaselineHash != "" && (!validHash(g.BaselineHash) || !validTime(g.BaselineAt)) ||
		g.CheckedHash != "" && (!validHash(g.CheckedHash) || !validTime(g.CheckedAt)) {
		return unknown
	}
	bound, baselineObserved := 0, false
	for _, entry := range s.Entries {
		baseline := entry.PermissionBaseline
		if entry.Kind == "input" && entry.Operation == request.Operation && entry.QueryID == g.QueryID && baseline != nil &&
			baseline.Version == 1 && !baseline.Complete && baseline.QueryID == g.QueryID && baseline.ConfigurationID == g.ConfigurationID &&
			baseline.BaselineAt == g.BaselineAt && baseline.BaselineHash == g.BaselineHash {
			bound++
			baselineObserved = baseline.State == "observed"
		}
	}
	if bound != 1 {
		return unknown
	}
	notices, noticeRecordsFailed := configurationNoticeItems(Scope{NativeID: s.Config.ID, Cwd: s.Config.Cwd}, s.Entries)
	matched := g.ConfigurationNoticeID == ""
	for _, notice := range notices {
		if notice.QueryID != g.QueryID || notice.ConfigurationID != g.ConfigurationID || notice.ReceiptOperation != request.Operation {
			continue
		}
		if g.ConfigurationNoticeID == notice.ID {
			matched = true
		}
		// Independently refuse an apparently unchanged guard when the journal
		// contains a matching notice after the original input was handed off.
		if g.ConfigurationNoticeID == "" {
			g.ConfigurationNoticeID = notice.ID
		}
		if g.State == "observed" {
			g.State = "unavailable"
		}
	}
	if !matched {
		return unknown
	}
	switch g.State {
	case "observed":
		observer := s.ConfigurationObserver
		if !baselineObserved || noticeRecordsFailed || g.BaselineHash == "" || g.BaselineHash != g.CheckedHash || !s.Live || !validConfigurationObserver(observer) ||
			!*observer.Registered || *observer.RecordingFailed || observer.CurrentQueryID != g.QueryID || observer.CurrentConfigurationID != g.ConfigurationID {
			return unknown
		}
		g.Detail = "尚未观察到权限规则变化，批准前会再次读取；这不保证外部策略已同步或冻结。"
	case "changed":
		if g.BaselineHash == "" {
			return unknown
		}
		g.Detail = "原输入等待期间已观察到权限规则变化，这条旧请求不能批准；可拒绝本次工具操作或中止原输入。"
	case "unavailable":
		g.Detail = "原输入的权限规则观察不完整，这条请求不能批准；可拒绝本次工具操作或中止原输入。"
		if g.ConfigurationNoticeID != "" {
			g.Detail = "原输入等待期间已收到配置变化通知，旧审批依据失效；通知不证明权限规则已变更。可拒绝本次工具操作或中止原输入。"
		}
	default:
		return unknown
	}
	return &g
}

// IsClaudeToolDenial admits only the saved request's exact native deny option.
// Source-version admission checks can be skipped for this refusal, while the
// original scope, connection, request, snapshot and transport must still match.
func IsClaudeToolDenial(in Intent) bool {
	p := in.Pending
	if in.Kind != "respond" || in.Scope.Instance.Kind != "claude" || in.Scope.Purpose != "" || p == nil ||
		p.Status != "pending" || p.ResponseKind != "approval" || p.Kind == "AskUserQuestion" || p.Kind == "" ||
		p.NativeID == "" || p.NativeID != in.Scope.NativeID || p.NativeTurn == "" || p.ConnectionID == "" ||
		in.OptionID != "deny" || in.Text != "" || len(in.Answers) != 0 || len(in.SkillInputs) != 0 || in.ConfigurationTarget != nil {
		return false
	}
	var requestID string
	if json.Unmarshal(p.RequestID, &requestID) != nil || requestID == "" || p.ID != in.Scope.Instance.ID+"/"+p.ConnectionID+"/"+requestID {
		return false
	}
	matches := 0
	for _, option := range p.Options {
		if option.ID != in.OptionID {
			continue
		}
		var value string
		if json.Unmarshal(option.Value, &value) != nil || value != "deny" {
			return false
		}
		matches++
	}
	return matches == 1
}
