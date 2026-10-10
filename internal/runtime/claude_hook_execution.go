package runtime

// HookExecution describes a native invocation. Optional source metadata is
// reported by Codex; Claude events currently leave their source unreported.
type HookExecution struct {
	ID                  string `json:"id"`
	NativeRunID         string `json:"native_run_id,omitempty"`
	TurnID              string `json:"turn_id,omitempty"`
	SourcePath          string `json:"source_path,omitempty"`
	AssetID             string `json:"asset_id,omitempty"`
	AssetVersion        string `json:"asset_version,omitempty"`
	SourceHash          string `json:"source_hash,omitempty"`
	ExecutionSnapshotID string `json:"execution_snapshot_id,omitempty"`
	Event               string `json:"event"`
	Operation           string `json:"operation,omitempty"`
	Status              string `json:"status"`
	NativeStatus        string `json:"native_status,omitempty"`
	Inconsistent        bool   `json:"inconsistent,omitempty"`
	FailureKind         string `json:"failure_kind,omitempty"`
	DurationMS          *int64 `json:"duration_ms,omitempty"`
	Source              string `json:"source"`
	StartedAt           string `json:"started_at,omitempty"`
	FinishedAt          string `json:"finished_at,omitempty"`
	ExitCode            *int   `json:"exit_code,omitempty"`
	Detail              string `json:"detail"`
}

func claudeHookExecutions(scope Scope, s claudeSnapshot) []HookExecution {
	out := []HookExecution{}
	indices := map[string]int{}
	seen := map[string]bool{}
	for _, entry := range s.Entries {
		e := entry.Event
		if entry.Kind != "event" || e.Type != "system" || (e.Subtype != "hook_started" && e.Subtype != "hook_progress" && e.Subtype != "hook_response") || e.SessionID != scope.NativeID || e.HookID == "" {
			continue
		}
		if e.UUID != "" && seen[e.UUID] {
			continue
		}
		seen[e.UUID] = true
		index, exists := indices[e.HookID]
		if !exists {
			index = len(out)
			indices[e.HookID] = index
			out = append(out, HookExecution{ID: e.HookID, Event: e.HookEvent, Operation: entry.Operation, Status: "unknown", Source: "unreported", Detail: "原生事件未报告插件来源；命令与输出正文不在此展示"})
		}
		h := &out[index]
		if h.Event != e.HookEvent || h.Operation != entry.Operation {
			h.Status = "unknown"
			continue
		}
		switch e.Subtype {
		case "hook_started":
			if h.FinishedAt != "" {
				h.Status = "unknown"
				continue
			}
			h.StartedAt = entry.At
			h.Status = "running"
		case "hook_progress":
			if h.FinishedAt != "" {
				h.Status = "unknown"
			}
		case "hook_response":
			h.FinishedAt = entry.At
			h.ExitCode = e.HookExitCode
			if h.StartedAt != "" && (e.HookOutcome == "success" || e.HookOutcome == "error" || e.HookOutcome == "cancelled") {
				h.Status = e.HookOutcome
			} else {
				h.Status = "unknown"
			}
		}
	}
	for i := range out {
		if out[i].Status == "running" && !s.Live {
			out[i].Status = "unknown"
		}
	}
	return out
}
