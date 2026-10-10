package app

import (
	"testing"

	"codex-netcap/internal/runtime"
)

func TestClaudeConfigurationNoticeBindsReceiptToOriginalSnapshotOnly(t *testing.T) {
	for _, mode := range []string{"original", "no-native-prompt", "native-agent", "other-session", "other-native", "other-namespace", "other-instance", "other-cwd", "not-input", "unknown-operation", "unknown-correlation"} {
		t.Run(mode, func(t *testing.T) {
			scope := runtime.Scope{NativeID: "native-original", Cwd: "/owned", StateNamespace: "/profile", Instance: runtime.Instance{ID: "claude", Kind: "claude"}}
			op := Operation{ID: "original-input", SessionID: "session", ExecutionSnapshotID: "original-snapshot", Intent: runtime.Intent{Kind: "send", Scope: scope}}
			session := Session{ID: "session", Scope: scope, ExecutionSnapshot: &ExecutionSnapshot{ID: "new-snapshot"},
				View: runtime.View{ConfigurationNotices: &runtime.ConfigurationNoticeHistory{Available: true, Items: []runtime.ConfigurationNotice{{NativeID: scope.NativeID, Cwd: scope.Cwd,
					PromptID: "independent-native-prompt", ReceiptOperation: op.ID, ReceiptCorrelation: "journal_in_flight", ReceiptSnapshotID: "untrusted"}}}}}
			n := &session.View.ConfigurationNotices.Items[0]
			switch mode {
			case "no-native-prompt":
				n.PromptID = ""
			case "native-agent":
				n.AgentID = "worker"
			case "other-session":
				op.SessionID = "other"
			case "other-native":
				op.Intent.Scope.NativeID = "other"
			case "other-namespace":
				op.Intent.Scope.StateNamespace = "/other"
			case "other-instance":
				op.Intent.Scope.Instance.ID = "other"
			case "other-cwd":
				op.Intent.Scope.Cwd = "/other"
			case "not-input":
				op.Intent.Kind = "apply_execution"
			case "unknown-operation":
				n.ReceiptOperation = "unknown"
			case "unknown-correlation":
				n.ReceiptCorrelation = "active-input"
			}
			bindClaudeConfigurationNotices(&session, map[string]Operation{op.ID: op})
			if mode == "original" || mode == "no-native-prompt" || mode == "native-agent" {
				if n.ReceiptOperation != op.ID || n.ReceiptSnapshotID != "original-snapshot" {
					t.Fatal("receipt followed the current snapshot", n)
				}
			} else if n.ReceiptOperation != "" || n.ReceiptSnapshotID != "" {
				t.Fatal("unverified receipt acquired snapshot ownership", n)
			}
		})
	}
}
