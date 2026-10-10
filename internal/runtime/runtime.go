// Package runtime is the protocol boundary used by the console. Native wire
// formats never become commands that a browser can execute directly.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"codex-netcap/internal/mcpstdio"
)

type Instance struct {
	Name               string   `json:"name,omitempty"`
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Executable         string   `json:"executable"`
	Profile            string   `json:"profile"`
	SettingsFile       string   `json:"settings_file,omitempty"`
	Socket             string   `json:"socket"`
	Version            string   `json:"version"`
	PolicyRevision     string   `json:"policy_revision"`
	EnvAllow           []string `json:"env_allow,omitempty"`
	Bridge             string   `json:"bridge,omitempty"`
	NodeExecutable     string   `json:"node_executable,omitempty"`
	NotificationSender string   `json:"notification_sender,omitempty"`
	ServerExecutable   string   `json:"server_executable,omitempty"`
	isolatedHome       string
}

type Route struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instance_id"`
	Provider           string `json:"provider"`
	Endpoint           string `json:"endpoint"`
	Model              string `json:"model"`
	CredentialRef      string `json:"credential_ref"`
	CredentialRevision string `json:"credential_revision"`
	ConfigRevision     string `json:"config_revision"`
}

type Scope struct {
	SessionID                 string                     `json:"session_id,omitempty"`
	Instance                  Instance                   `json:"instance"`
	Route                     Route                      `json:"route"`
	NativeID                  string                     `json:"native_id"`
	StateNamespace            string                     `json:"state_namespace,omitempty"`
	Cwd                       string                     `json:"cwd"`
	Purpose                   string                     `json:"purpose,omitempty"`
	CredentialStamp           string                     `json:"credential_stamp,omitempty"`
	MCPSelections             []MCPSelection             `json:"mcp_selections,omitempty"`
	SkillSelections           []SkillSelection           `json:"skill_selections,omitempty"`
	PluginSelections          []PluginSelection          `json:"plugin_selections,omitempty"`
	SettingsGuardVersion      int                        `json:"settings_guard_version,omitempty"`
	ClaudeSettingsSources     []ClaudeSettingsSource     `json:"claude_settings_sources,omitempty"`
	ClaudeInstalledSettings   *ClaudeInstalledSettings   `json:"claude_installed_settings,omitempty"`
	CodexUserHooks            *CodexUserHookSource       `json:"codex_user_hooks,omitempty"`
	CodexProjectHooks         *CodexProjectHookSources   `json:"codex_project_hooks,omitempty"`
	CodexConfigurationSources *CodexConfigurationSources `json:"codex_configuration_sources,omitempty"`
	// Observation policy, never part of persisted execution identity.
	NoAutoResume          bool   `json:"-"`
	ConfigurationBaseline string `json:"-"`
	// Derived from the saved Session/Intent permission; never a new caller override.
	RecoveryPermission string `json:"-"`
	// Only explicit prepare/apply may retire changed directory resources after
	// proving the old configuration on this connection. Never accepted from JSON.
	directorySkillChange bool
	// Set only inside a verified, same-connection managed-hook removal. Never
	// persisted, accepted from JSON, or used to admit ordinary model input.
	hookDeactivation bool
	hookSourceTarget *codexHookSourceTarget
	hookSourceCold   bool
}

// MCPSelection binds an explicit native server name to a registered source.
// Legacy Codex selections use profile overrides. Resource definitions carry
// source and environment versions, never commands or credential values.
type MCPSelection struct {
	AssetID    string         `json:"asset_id"`
	Server     string         `json:"server"`
	Mode       string         `json:"mode"`
	SourcePath string         `json:"source_path"`
	Definition *MCPDefinition `json:"definition,omitempty"`
}

type MCPDefinition struct {
	Version      int                     `json:"version"`
	SHA256       string                  `json:"sha256"`
	Environment  []MCPEnvironmentBinding `json:"environment"`
	AssetVersion string                  `json:"asset_version,omitempty"`
	SourceHash   string                  `json:"source_hash,omitempty"`
	Binding      *mcpstdio.Binding       `json:"binding,omitempty"`
}

type MCPEnvironmentBinding struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	SHA256  string `json:"sha256"`
}

type CapabilityEvidence struct {
	Binding       *mcpstdio.Stamp `json:"binding,omitempty"`
	AssetID       string          `json:"asset_id,omitempty"`
	Server        string          `json:"server"`
	Mode          string          `json:"mode"`
	RuntimeStatus string          `json:"runtime_status"`
	Source        string          `json:"source,omitempty"`
	PluginID      string          `json:"plugin_id,omitempty"`
	Verified      bool            `json:"verified"`
	Tools         []string        `json:"tools"`
	UsedTools     []string        `json:"used_tools"`
	At            string          `json:"at"`
	Detail        string          `json:"detail"`
}

type Intent struct {
	ClaudeOriginalQueryRecovery *ClaudeOriginalQueryRecovery     `json:"claude_original_query_recovery,omitempty"`
	ClaudeInstalledRecovery     *ClaudeInstalledSettingsRecovery `json:"claude_installed_recovery,omitempty"`
	HookSourceProof             *CodexHookSourceProof            `json:"hook_source_proof,omitempty"`
	ID                          string                           `json:"id"`
	CreatedAt                   string                           `json:"created_at,omitempty"`
	Kind                        string                           `json:"kind"`
	Scope                       Scope                            `json:"scope"`
	Text                        string                           `json:"text,omitempty"`
	SkillInputs                 []SkillInput                     `json:"skill_inputs,omitempty"`
	NativeTurn                  string                           `json:"native_turn,omitempty"`
	Permission                  string                           `json:"permission"`
	Pending                     *Pending                         `json:"pending,omitempty"`
	OptionID                    string                           `json:"option_id,omitempty"`
	Answers                     map[string][]string              `json:"answers,omitempty"`
	OwnedInputs                 []string                         `json:"owned_inputs,omitempty"`
	ConfigurationBaseline       string                           `json:"configuration_baseline,omitempty"`
	PreviousScope               *Scope                           `json:"previous_scope,omitempty"`
	ConfigurationTarget         *Scope                           `json:"configuration_target,omitempty"`
	ConfigurationRecoveryOf     string                           `json:"configuration_recovery_of,omitempty"`
}

type Receipt struct {
	ClaudeOriginalQueryRecovery *ClaudeOriginalQueryRecovery          `json:"original_query_retry,omitempty"`
	ClaudeInstalledSettings     *ClaudeInstalledSettings              `json:"installed_settings,omitempty"`
	ConfigurationRecovery       *ConfigurationRecoveryAcknowledgement `json:"configuration_recovery,omitempty"`
	NativeID                    string                                `json:"native_id"`
	NativeTurn                  string                                `json:"native_turn,omitempty"`
	ConfigurationBaseline       string                                `json:"configuration_baseline,omitempty"`
}

type Message struct {
	ID        string `json:"id"`
	TurnID    string `json:"turn_id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Operation string `json:"operation,omitempty"`
}

type Pending struct {
	PermissionGuard *PermissionApprovalEvidence `json:"permission_guard,omitempty"`
	ID              string                      `json:"id"`
	NativeID        string                      `json:"native_id"`
	NativeTurn      string                      `json:"native_turn"`
	Kind            string                      `json:"kind"`
	Request         json.RawMessage             `json:"request"`
	ConnectionID    string                      `json:"connection_id"`
	RequestID       json.RawMessage             `json:"request_id"`
	ResponseKind    string                      `json:"response_kind"`
	Title           string                      `json:"title"`
	Details         string                      `json:"details,omitempty"`
	Options         []Option                    `json:"options"`
	Questions       []Question                  `json:"questions"`
	Status          string                      `json:"status"`
	AnswerOperation string                      `json:"answer_operation,omitempty"`
}

type Option struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Value       json.RawMessage `json:"value"`
}

type Question struct {
	ID          string   `json:"id"`
	Header      string   `json:"header"`
	Text        string   `json:"question"`
	Secret      bool     `json:"is_secret"`
	Multiple    bool     `json:"multiple,omitempty"`
	AllowCustom *bool    `json:"allow_custom,omitempty"`
	Options     []Option `json:"options"`
}

type Turn struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	CompletedAt string   `json:"completed_at,omitempty"`
	Failure     *Failure `json:"failure,omitempty"`
}

type Execution struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type Evidence struct {
	Source   string `json:"source"`
	At       string `json:"at"`
	Complete bool   `json:"complete"`
	Detail   string `json:"detail"`
}

type View struct {
	pluginPolicy         *codexPluginPolicy
	AgentRoster          *AgentRosterStatus           `json:"agent_roster,omitempty"`
	Executor             *ExecutorEvidence            `json:"executor,omitempty"`
	NativeStatus         string                       `json:"native_status,omitempty"`
	NativeID             string                       `json:"native_id"`
	Status               string                       `json:"status"`
	Messages             []Message                    `json:"messages"`
	Pending              []Pending                    `json:"pending"`
	Evidence             Evidence                     `json:"evidence"`
	Turns                []Turn                       `json:"turns"`
	Executions           []Execution                  `json:"executions"`
	ExecutionClear       bool                         `json:"execution_clear"`
	TerminalCommand      string                       `json:"terminal_command,omitempty"`
	Tools                []ToolResult                 `json:"tools,omitempty"`
	CapabilityEvidence   []CapabilityEvidence         `json:"capability_evidence,omitempty"`
	HookExecutions       []HookExecution              `json:"hook_executions,omitempty"`
	SettingsGuard        *ClaudeSettingsGuardEvidence `json:"settings_guard,omitempty"`
	InstructionLoads     *InstructionLoadHistory      `json:"instruction_loads,omitempty"`
	ConfigurationNotices *ConfigurationNoticeHistory  `json:"configuration_notices,omitempty"`
	ConfigurationFailure *ConfigurationFailure        `json:"configuration_failure,omitempty"`
	PluginEvidence       []PluginEvidence             `json:"plugin_evidence,omitempty"`
	SkillEvidence        []SkillEvidence              `json:"skill_evidence,omitempty"`
	PermissionEvidence   *PermissionEvidence          `json:"permission_evidence,omitempty"`
}

// ExecutorEvidence describes the backend reached for this observation, not
// the environment that originally created a historical thread or an input grant.
type ExecutorEvidence struct {
	Source     string `json:"source"`
	At         string `json:"at"`
	PID        int    `json:"pid"`
	Birth      string `json:"birth"`
	OS         string `json:"os"`
	UID        string `json:"uid"`
	User       string `json:"user,omitempty"`
	Executable string `json:"executable"`
	WSLDistro  string `json:"wsl_distro,omitempty"`
	Detail     string `json:"detail"`
}

// This verifies the declared mode and approval policy, not every inherited
// rule, filesystem restriction, or external permission profile.
type PermissionEvidence struct {
	Source         string          `json:"source"`
	Coverage       string          `json:"coverage"`
	ExpectedMode   string          `json:"expected_mode"`
	ApprovalPolicy json.RawMessage `json:"approval_policy"`
	Sandbox        json.RawMessage `json:"sandbox"`
	At             string          `json:"at"`
	Verified       bool            `json:"verified"`
}

type ToolResult struct {
	ID       string `json:"id"`
	TurnID   string `json:"turn_id"`
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Complete bool   `json:"complete"`
}

// NotSent is used only for evidence gathered before the native mutation.
// Missing history and expired observation timeouts are never NotSent.
type NotSent struct{ Reason string }

func (e *NotSent) Error() string { return fmt.Sprintf("操作未发送: %s", e.Reason) }

type Resolution struct {
	State    string   `json:"state"` // not_sent, running, finished, unknown
	Status   string   `json:"status"`
	Receipt  Receipt  `json:"receipt"`
	View     View     `json:"view"`
	Evidence Evidence `json:"evidence"`
}

type Description struct {
	LocalIPC      *LocalIPCCheck    `json:"local_ipc,omitempty"`
	Kind          string            `json:"kind"`
	Version       string            `json:"version"`
	Compatibility string            `json:"compatibility"`
	Detail        string            `json:"detail"`
	Capabilities  map[string]string `json:"capabilities"`
}

// LocalIPCCheck describes this workbench process's environment, not a native
// server connection, credential check or grant of tool/input authority.
type LocalIPCCheck struct {
	Status    string `json:"status"`
	Operation string `json:"operation"`
	ErrorCode string `json:"error_code,omitempty"`
	At        string `json:"at"`
	Detail    string `json:"detail"`
}

type Adapter interface {
	Describe(context.Context, Instance) (Description, error)
	Execute(context.Context, Intent) (Receipt, error)
	Observe(context.Context, Scope) (View, error)
	// ObserveFresh bypasses display caches before an explicit control handoff.
	ObserveFresh(context.Context, Scope) (View, error)
	Reconcile(context.Context, Intent, Receipt) (Resolution, error)
	Close() error
}
