package app

import (
	"testing"

	"codex-netcap/internal/runtime"
)

func TestClaudeInstructionLoadBindsOnlyItsDurableOriginalInput(t *testing.T) {
	for _, mode := range []string{"original", "other-session", "other-native", "other-namespace", "other-instance", "other-cwd", "not-input", "unknown-operation", "missing-prompt", "missing-correlation", "unknown-correlation", "subagent"} {
		t.Run(mode, func(t *testing.T) {
			scope := runtime.Scope{NativeID: "native-original", Cwd: "/owned", StateNamespace: "/profile", Instance: runtime.Instance{ID: "claude", Kind: "claude"}}
			op := Operation{ID: "original-input", SessionID: "session", ExecutionSnapshotID: "original-snapshot", Intent: runtime.Intent{Kind: "send", Scope: scope}}
			session := Session{ID: "session", Scope: scope, ExecutionSnapshot: &ExecutionSnapshot{ID: "new-snapshot"},
				View: runtime.View{InstructionLoads: &runtime.InstructionLoadHistory{Available: true, Items: []runtime.InstructionLoad{{NativeID: scope.NativeID, Cwd: scope.Cwd,
					PromptID: "native-prompt", Operation: op.ID, InputCorrelation: "native_tool_message", ExecutionSnapshotID: "must-revalidate"}}}}}
			load := &session.View.InstructionLoads.Items[0]
			switch mode {
			case "other-session":
				op.SessionID = "other"
			case "other-native":
				op.Intent.Scope.NativeID = "other"
			case "other-namespace":
				op.Intent.Scope.StateNamespace = "/other-profile"
			case "other-instance":
				op.Intent.Scope.Instance.ID = "other"
			case "other-cwd":
				op.Intent.Scope.Cwd = "/other-project"
			case "not-input":
				op.Intent.Kind = "apply_execution"
			case "unknown-operation":
				load.Operation = "unknown"
			case "missing-prompt":
				load.PromptID = ""
			case "missing-correlation":
				load.InputCorrelation = ""
			case "unknown-correlation":
				load.InputCorrelation = "active-operation"
			case "subagent":
				load.AgentID = "native-worker"
			}
			bindClaudeInstructionLoads(&session, map[string]Operation{op.ID: op})
			if mode == "original" {
				if load.Operation != op.ID || load.ExecutionSnapshotID != "original-snapshot" {
					t.Fatal("notification used the current snapshot instead of its original operation")
				}
			} else if load.Operation != "" || load.ExecutionSnapshotID != "" {
				t.Fatal("unverified prompt ownership was attributed")
			}
		})
	}
}
