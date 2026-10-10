package app

// The runtime supplies only a position in the query's journal. Bind its saved
// input to a snapshot without turning receipt position into causal ownership.
func bindClaudeConfigurationNotices(session *Session, operations map[string]Operation) {
	if session.Scope.Instance.Kind != "claude" || session.View.ConfigurationNotices == nil {
		return
	}
	for i := range session.View.ConfigurationNotices.Items {
		notice := &session.View.ConfigurationNotices.Items[i]
		operation := notice.ReceiptOperation
		notice.ReceiptOperation, notice.ReceiptSnapshotID = "", ""
		if notice.NativeID != session.Scope.NativeID || notice.Cwd != session.Scope.Cwd || operation == "" || notice.ReceiptCorrelation != "journal_in_flight" {
			continue
		}
		op, exists := operations[operation]
		if !exists || op.SessionID != session.ID || op.Intent.Scope.NativeID != session.Scope.NativeID || op.Intent.Kind != "send" || op.ID != operation ||
			op.Intent.Scope.Cwd != notice.Cwd || op.Intent.Scope.StateNamespace != session.Scope.StateNamespace || op.Intent.Scope.Instance.ID != session.Scope.Instance.ID {
			continue
		}
		notice.ReceiptOperation, notice.ReceiptSnapshotID = op.ID, op.ExecutionSnapshotID
	}
}
