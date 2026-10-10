package runtime

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// InstructionLoad is a received native notification, not a file version or
// evidence of instruction adherence. QueryID is assigned by the owned bridge.
type InstructionLoad struct {
	ID                  string   `json:"id"`
	NativeID            string   `json:"native_id"`
	Cwd                 string   `json:"cwd"`
	NativeCwd           string   `json:"native_cwd"`
	QueryID             string   `json:"query_id"`
	ConfigurationID     string   `json:"configuration_id"`
	At                  string   `json:"at"`
	Path                string   `json:"path"`
	Source              string   `json:"source"`
	Reason              string   `json:"reason"`
	PromptID            string   `json:"prompt_id,omitempty"`
	AgentID             string   `json:"agent_id,omitempty"`
	AgentType           string   `json:"agent_type,omitempty"`
	ParentPath          string   `json:"parent_path,omitempty"`
	TriggerPath         string   `json:"trigger_path,omitempty"`
	Globs               []string `json:"globs,omitempty"`
	Current             bool     `json:"current"`
	Operation           string   `json:"operation,omitempty"`
	InputCorrelation    string   `json:"input_correlation,omitempty"`
	ExecutionSnapshotID string   `json:"execution_snapshot_id,omitempty"`
}

type InstructionLoadHistory struct {
	Source          string            `json:"source"`
	Available       bool              `json:"available"`
	Complete        bool              `json:"complete"`
	RecordingFailed bool              `json:"recording_failed"`
	Truncated       bool              `json:"truncated"`
	CurrentQueryID  string            `json:"current_query_id,omitempty"`
	Items           []InstructionLoad `json:"items"`
}

type claudeInstructionLoad struct {
	Version int `json:"version"`
	InstructionLoad
}

type claudeInstructionObserver struct {
	Version                int    `json:"version"`
	CurrentQueryID         string `json:"current_query_id"`
	CurrentConfigurationID string `json:"current_configuration_id"`
	RecordingFailed        *bool  `json:"recording_failed"`
}

var instructionIdentifier = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func validInstructionLoad(scope Scope, load *claudeInstructionLoad) bool {
	if load == nil || load.Version != 1 || load.NativeID != scope.NativeID || load.Cwd != scope.Cwd ||
		!instructionIdentifier.MatchString(load.ID) || !instructionIdentifier.MatchString(load.QueryID) || !claudeSettingsFileSHA.MatchString(load.ConfigurationID) {
		return false
	}
	valid := func(s string, max int) bool { return s != "" && len(s) <= max && !strings.ContainsRune(s, '\x00') }
	abs := func(s string) bool { return valid(s, 4096) && filepath.IsAbs(s) }
	if !abs(load.Path) || !abs(load.NativeCwd) || !valid(load.Source, 100) || !valid(load.Reason, 100) ||
		load.ParentPath != "" && !abs(load.ParentPath) || load.TriggerPath != "" && !abs(load.TriggerPath) ||
		load.PromptID != "" && !instructionIdentifier.MatchString(load.PromptID) ||
		load.AgentID != "" && !valid(load.AgentID, 200) || load.AgentType != "" && !valid(load.AgentType, 200) || len(load.Globs) > 32 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, load.At); err != nil {
		return false
	}
	for _, glob := range load.Globs {
		if !valid(glob, 512) {
			return false
		}
	}
	return true
}

func claudeInstructionLoads(scope Scope, snapshot claudeSnapshot) *InstructionLoadHistory {
	result := &InstructionLoadHistory{Source: "claude.sdk.instructions_loaded", Items: []InstructionLoad{}}
	observer := snapshot.InstructionObserver
	if observer != nil {
		result.Available = observer.Version == 1 && observer.RecordingFailed != nil &&
			(observer.CurrentQueryID == "" && observer.CurrentConfigurationID == "" ||
				instructionIdentifier.MatchString(observer.CurrentQueryID) && claudeSettingsFileSHA.MatchString(observer.CurrentConfigurationID))
		result.RecordingFailed = !result.Available || observer.RecordingFailed != nil && *observer.RecordingFailed
		if result.Available && snapshot.Live {
			result.CurrentQueryID = observer.CurrentQueryID
		}
	}
	operations := claudeInstructionInputOperations(scope, snapshot)
	ids := map[string]bool{}
	found := false
	for _, entry := range snapshot.Entries {
		if entry.Kind != "instruction_load" {
			continue
		}
		found = true
		load := entry.InstructionLoad
		if !validInstructionLoad(scope, load) || ids[load.ID] {
			result.RecordingFailed = true
			continue
		}
		ids[load.ID] = true
		item := load.InstructionLoad
		item.Current = result.CurrentQueryID != "" && item.QueryID == result.CurrentQueryID && item.ConfigurationID == observer.CurrentConfigurationID
		item.Operation, item.ExecutionSnapshotID, item.InputCorrelation = "", "", ""
		// An active operation is not proof of ownership for a delayed callback.
		// Keep native subagent identities without assigning their prompt to main.
		if item.AgentID == "" {
			item.Operation = operations[instructionPromptKey(item.QueryID, item.ConfigurationID, item.PromptID)]
			if item.Operation != "" {
				item.InputCorrelation = "native_tool_message"
			}
		}
		result.Items = append(result.Items, item)
		result.Available = true
	}
	if observer == nil && !found {
		return nil
	}
	if len(result.Items) > 128 {
		result.Truncated = true
		result.Items = result.Items[len(result.Items)-128:]
	}
	return result
}
