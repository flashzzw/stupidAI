package app

// A native prompt ID is distinct from an input UUID. Runtime verifies their
// tool-message link; snapshot ownership comes from that durable input.
func bindClaudeInstructionLoads(session *Session, operations map[string]Operation) {
	if session.Scope.Instance.Kind != "claude" || session.View.InstructionLoads == nil {
		return
	}
	for i := range session.View.InstructionLoads.Items {
		load := &session.View.InstructionLoads.Items[i]
		operation := load.Operation
		load.Operation, load.ExecutionSnapshotID = "", ""
		if load.NativeID != session.Scope.NativeID || load.Cwd != session.Scope.Cwd || load.AgentID != "" || operation == "" || load.PromptID == "" || load.InputCorrelation != "native_tool_message" {
			continue
		}
		op, exists := operations[operation]
		if !exists || op.SessionID != session.ID || op.Intent.Scope.NativeID != session.Scope.NativeID || op.Intent.Kind != "send" || op.ID != operation ||
			op.Intent.Scope.Cwd != load.Cwd || op.Intent.Scope.StateNamespace != session.Scope.StateNamespace || op.Intent.Scope.Instance.ID != session.Scope.Instance.ID {
			continue
		}
		load.Operation, load.ExecutionSnapshotID = op.ID, op.ExecutionSnapshotID
	}
}
