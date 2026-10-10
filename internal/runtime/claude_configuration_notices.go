package runtime

import (
	"path/filepath"
	"strings"
	"time"
)

// ConfigurationNotice records receipt of a native ConfigChange. Neither its
// query configuration nor its receipt-time input identifies applied file bytes.
type ConfigurationNotice struct {
	ID                 string `json:"id"`
	NativeID           string `json:"native_id"`
	Cwd                string `json:"cwd"`
	NativeCwd          string `json:"native_cwd"`
	QueryID            string `json:"query_id"`
	ConfigurationID    string `json:"configuration_id"`
	At                 string `json:"at"`
	Source             string `json:"source"`
	Path               string `json:"path"`
	PromptID           string `json:"prompt_id,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	AgentType          string `json:"agent_type,omitempty"`
	Current            bool   `json:"current"`
	ReceiptOperation   string `json:"receipt_operation,omitempty"`
	ReceiptCorrelation string `json:"receipt_correlation,omitempty"`
	ReceiptSnapshotID  string `json:"receipt_snapshot_id,omitempty"`
}

type ConfigurationNoticeHistory struct {
	Source          string                `json:"source"`
	Available       bool                  `json:"available"`
	Complete        bool                  `json:"complete"`
	Registered      bool                  `json:"registered"`
	RecordingFailed bool                  `json:"recording_failed"`
	Truncated       bool                  `json:"truncated"`
	CurrentQueryID  string                `json:"current_query_id,omitempty"`
	CurrentCount    int                   `json:"current_count"`
	Items           []ConfigurationNotice `json:"items"`
}

type claudeConfigurationNotice struct {
	Version int `json:"version"`
	ConfigurationNotice
}

type claudeConfigurationObserver struct {
	Version                int    `json:"version"`
	Registered             *bool  `json:"registered"`
	RecordingFailed        *bool  `json:"recording_failed"`
	CurrentQueryID         string `json:"current_query_id"`
	CurrentConfigurationID string `json:"current_configuration_id"`
}

func validConfigurationObserver(observer *claudeConfigurationObserver) bool {
	return observer != nil && observer.Version == 1 && observer.Registered != nil && observer.RecordingFailed != nil &&
		(observer.CurrentQueryID == "" && observer.CurrentConfigurationID == "" && !*observer.Registered ||
			instructionIdentifier.MatchString(observer.CurrentQueryID) && claudeSettingsFileSHA.MatchString(observer.CurrentConfigurationID))
}

func validConfigurationNotice(scope Scope, notice *claudeConfigurationNotice) bool {
	if notice == nil || notice.Version != 1 || notice.NativeID != scope.NativeID || notice.Cwd != scope.Cwd ||
		!instructionIdentifier.MatchString(notice.ID) || !instructionIdentifier.MatchString(notice.QueryID) || !claudeSettingsFileSHA.MatchString(notice.ConfigurationID) {
		return false
	}
	valid := func(s string, max int) bool { return s != "" && len(s) <= max && !strings.ContainsRune(s, '\x00') }
	abs := func(s string) bool { return valid(s, 4096) && filepath.IsAbs(s) }
	if !abs(notice.NativeCwd) || !valid(notice.Source, 100) || notice.Path != "" && !abs(notice.Path) ||
		notice.PromptID != "" && !instructionIdentifier.MatchString(notice.PromptID) ||
		notice.AgentID != "" && !valid(notice.AgentID, 200) || notice.AgentType != "" && !valid(notice.AgentType, 200) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, notice.At)
	return err == nil
}

// Follow journal order independently for each query. A late callback from an
// older query must not inherit the global active input. Receipt position is
// deliberately weaker than a native causal link to an affected prompt.
func configurationNoticeItems(scope Scope, entries []claudeEntry) ([]ConfigurationNotice, bool) {
	items, failed := []ConfigurationNotice{}, false
	counts := map[string]int{}
	for _, entry := range entries {
		if entry.Kind == "configuration_notice" && entry.ConfigurationNotice != nil {
			counts[entry.ConfigurationNotice.ID]++
		}
	}
	inputs := map[string]map[string]string{}
	ambiguous := map[string]bool{}
	seenInputs := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == "input" && instructionIdentifier.MatchString(entry.QueryID) {
			if inputs[entry.QueryID] == nil {
				inputs[entry.QueryID] = map[string]string{}
			}
			key := entry.QueryID + "/" + entry.Operation
			if seenInputs[key] || !instructionIdentifier.MatchString(entry.Operation) {
				ambiguous[entry.QueryID] = true
			}
			seenInputs[key] = true
			configuration := ""
			if b := entry.PermissionBaseline; b != nil && b.Version == 1 && !b.Complete && b.QueryID == entry.QueryID && claudeSettingsFileSHA.MatchString(b.ConfigurationID) {
				configuration = b.ConfigurationID
			}
			inputs[entry.QueryID][entry.Operation] = configuration
		}
		if entry.Kind == "event" && entry.Event.Type == "result" && entry.Event.SessionID == scope.NativeID &&
			(entry.Event.UserMessageUUID == "" || entry.Event.UserMessageUUID == entry.Operation) {
			delete(inputs[entry.QueryID], entry.Operation)
		}
		if entry.Kind != "configuration_notice" {
			continue
		}
		notice := entry.ConfigurationNotice
		if !validConfigurationNotice(scope, notice) || counts[notice.ID] != 1 {
			failed = true
			continue
		}
		item := notice.ConfigurationNotice
		item.Current, item.ReceiptOperation, item.ReceiptCorrelation, item.ReceiptSnapshotID = false, "", "", ""
		if active := inputs[item.QueryID]; len(active) == 1 && !ambiguous[item.QueryID] {
			for operation, configuration := range active {
				if configuration == item.ConfigurationID {
					item.ReceiptOperation, item.ReceiptCorrelation = operation, "journal_in_flight"
				}
			}
		}
		items = append(items, item)
	}
	return items, failed
}

func claudeConfigurationNotices(scope Scope, snapshot claudeSnapshot) *ConfigurationNoticeHistory {
	items, failed := configurationNoticeItems(scope, snapshot.Entries)
	observer := snapshot.ConfigurationObserver
	if observer == nil && len(items) == 0 && !failed {
		return nil
	}
	result := &ConfigurationNoticeHistory{Source: "claude.sdk.config_change", Items: items, RecordingFailed: failed}
	if validConfigurationObserver(observer) {
		result.Available = true
		result.RecordingFailed = result.RecordingFailed || *observer.RecordingFailed
		if snapshot.Live {
			result.CurrentQueryID = observer.CurrentQueryID
			result.Registered = *observer.Registered
		}
	} else if observer != nil {
		result.RecordingFailed = true
	}
	for i := range result.Items {
		item := &result.Items[i]
		item.Current = result.CurrentQueryID != "" && item.QueryID == result.CurrentQueryID && item.ConfigurationID == observer.CurrentConfigurationID
		if item.Current {
			result.CurrentCount++
		}
		result.Available = true
	}
	if len(result.Items) > 128 {
		result.Truncated = true
		result.Items = result.Items[len(result.Items)-128:]
	}
	return result
}
