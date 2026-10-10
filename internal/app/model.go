package app

import (
	"codex-netcap/internal/events"
	"codex-netcap/internal/runtime"
)

type Session struct {
	NativeBinding           *NativeSourceBinding     `json:"native_binding,omitempty"`
	Access                  *SessionAccess           `json:"access,omitempty"` // Query projection; never persisted as authority.
	ID                      string                   `json:"id"`
	Title                   string                   `json:"title"`
	ProjectID               string                   `json:"project_id"`
	Scope                   runtime.Scope            `json:"scope"`
	Fingerprint             string                   `json:"profile_fingerprint"`
	RouteFingerprint        string                   `json:"route_fingerprint,omitempty"`
	LegacyID                string                   `json:"legacy_id,omitempty"`
	Origin                  string                   `json:"origin"`
	ControlMode             string                   `json:"control_mode"`
	InputBaseline           []string                 `json:"input_baseline,omitempty"`
	Permission              string                   `json:"permission"`
	Status                  string                   `json:"status"`
	Connection              string                   `json:"connection"`
	Generation              uint64                   `json:"generation"`
	Paused                  bool                     `json:"paused"`
	Archived                bool                     `json:"archived"`
	UpdatedAt               string                   `json:"updated_at"`
	ActivityAt              string                   `json:"activity_at,omitempty"`
	View                    runtime.View             `json:"view"`
	ExecutionSnapshot       *ExecutionSnapshot       `json:"execution_snapshot,omitempty"`
	PendingExecutionChange  *ExecutionChange         `json:"pending_execution_change,omitempty"`
	ExecutionHold           bool                     `json:"execution_hold,omitempty"`
	ExecutionHistory        []*ExecutionSnapshot     `json:"execution_history,omitempty"`
	ExecutionChangeAttempts []ExecutionChangeAttempt `json:"execution_change_attempts,omitempty"`
}

type Operation struct {
	ID                  string             `json:"id"`
	CommandID           string             `json:"command_id"`
	SessionID           string             `json:"session_id"`
	Generation          uint64             `json:"generation"`
	Intent              runtime.Intent     `json:"intent"`
	Receipt             runtime.Receipt    `json:"receipt"`
	Status              string             `json:"status"`
	Error               string             `json:"error,omitempty"`
	Evidence            []runtime.Evidence `json:"evidence"`
	CreatedAt           string             `json:"created_at"`
	TargetOperationID   string             `json:"target_operation_id,omitempty"`
	ExecutionSnapshotID string             `json:"execution_snapshot_id,omitempty"`
}

type Command struct {
	ID                   string                          `json:"command_id"`
	Kind                 string                          `json:"kind"`
	SessionID            string                          `json:"session_id,omitempty"`
	ProjectID            string                          `json:"project_id,omitempty"`
	RouteID              string                          `json:"route_id,omitempty"`
	Title                string                          `json:"title,omitempty"`
	Text                 string                          `json:"text,omitempty"`
	SkillInput           *SkillInputRequest              `json:"skill_input,omitempty"`
	Permission           string                          `json:"permission,omitempty"`
	PendingID            string                          `json:"pending_id,omitempty"`
	OptionID             string                          `json:"option_id,omitempty"`
	Answers              map[string][]string             `json:"answers,omitempty"`
	OperationID          string                          `json:"operation_id,omitempty"`
	NativeID             string                          `json:"native_id,omitempty"`
	NativeTurn           string                          `json:"native_turn,omitempty"`
	ProbeMode            string                          `json:"probe_mode,omitempty"`
	CapabilityRefs       []string                        `json:"capability_refs,omitempty"`
	CapabilitySelections []CapabilitySelection           `json:"capability_selections,omitempty"`
	ExecutionChange      *ExecutionChangeRequest         `json:"execution_change,omitempty"`
	ExecutionRecovery    *ExecutionRecoveryRequest       `json:"execution_recovery,omitempty"`
	ExecutionProfile     *ExecutionProfile               `json:"execution_profile,omitempty"`
	CapabilityChange     *CapabilityChangeRequest        `json:"capability_change,omitempty"`
	CapabilityPackage    *CapabilityPackageImportRequest `json:"capability_package,omitempty"`
	MCPLogin             *MCPLoginRequest                `json:"mcp_login,omitempty"`
}

type CapabilitySelection struct {
	AssetID    string                `json:"asset_id"`
	Mode       string                `json:"mode"`
	Version    string                `json:"version,omitempty"`
	HookReview *CapabilityHookReview `json:"hook_review,omitempty"`
	// Validation-only reconstruction from an already saved candidate. This is
	// never accepted from JSON or used to admit a new user request.
	hookBinding *runtime.PluginSelection
}

type CapabilityHookReview struct {
	InspectionID string `json:"inspection_id"`
	Trust        string `json:"trust"`
}

type CommandReceipt struct {
	ID                 string        `json:"command_id"`
	Hash               string        `json:"command_hash"`
	OperationID        string        `json:"operation_id,omitempty"`
	SessionID          string        `json:"session_id"`
	PendingChangeID    string        `json:"pending_change_id,omitempty"`
	Status             string        `json:"status"`
	ExecutionProfileID string        `json:"execution_profile_id,omitempty"`
	CapabilityID       string        `json:"capability_id,omitempty"`
	MCPLogin           *MCPLoginPlan `json:"mcp_login,omitempty"`
}

type Event struct {
	Seq       uint64 `json:"seq"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	At        string `json:"at"`
}

type State struct {
	CapabilityLibrary   map[string]CapabilityEntry    `json:"capability_library,omitempty"`
	Sessions            map[string]Session            `json:"sessions"`
	Operations          map[string]Operation          `json:"operations"`
	Commands            map[string]CommandReceipt     `json:"commands"`
	Events              []Event                       `json:"events"`
	Sequence            uint64                        `json:"sequence"`
	DomainEvents        map[string]events.Envelope    `json:"domain_events"`
	ObservationReceipts map[string]ObservationReceipt `json:"observation_receipts"`
	Probes              map[string]ProbeRecord        `json:"probes"`
	RouteHealth         map[string]RouteHealth        `json:"route_health"`
	ExecutionProfiles   map[string]ExecutionProfile   `json:"execution_profiles,omitempty"`
}

func newState() State {
	return State{CapabilityLibrary: map[string]CapabilityEntry{}, Sessions: map[string]Session{}, Operations: map[string]Operation{}, Commands: map[string]CommandReceipt{}, Events: []Event{}, DomainEvents: map[string]events.Envelope{}, ObservationReceipts: map[string]ObservationReceipt{}, Probes: map[string]ProbeRecord{}, RouteHealth: map[string]RouteHealth{}, ExecutionProfiles: map[string]ExecutionProfile{}}
}

func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "interrupted" || status == "not_sent"
}
