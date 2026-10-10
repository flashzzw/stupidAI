package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type claudeEvent struct {
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	UUID            string `json:"uuid"`
	SessionID       string `json:"session_id"`
	UserMessageUUID string `json:"user_message_uuid"`
	HookID          string `json:"hook_id"`
	HookEvent       string `json:"hook_event"`
	HookOutcome     string `json:"outcome"`
	HookExitCode    *int   `json:"exit_code"`
	Message         struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	IsError         bool    `json:"is_error"`
	IsReplay        bool    `json:"isReplay"`
	Error           string  `json:"error"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	// SDK tool results are tool-specific: Bash metadata is an object, MCP
	// content is an array. Decode only the metadata required by each tool.
	ToolUseResult  json.RawMessage `json:"tool_use_result"`
	TerminalReason string          `json:"terminal_reason"`
	Queued         *int            `json:"queued_turn_count"`
	Tasks          []struct {
		ID   string `json:"task_id"`
		Type string `json:"task_type"`
	} `json:"tasks"`
}
type claudeEntry struct {
	PermissionBaseline *PermissionApprovalEvidence `json:"permission_baseline,omitempty"`
	QueryID            string                      `json:"query_id,omitempty"`
	At                 string                      `json:"at,omitempty"`
	Kind               string                      `json:"kind"`
	Operation          string                      `json:"operation"`
	Text               string                      `json:"text"`
	Event              claudeEvent                 `json:"event"`
	InstructionLoad    *claudeInstructionLoad      `json:"load,omitempty"`
	InstructionPrompt  *claudeInstructionPrompt    `json:"link,omitempty"`
}
type claudeRequest struct {
	PermissionGuard *PermissionApprovalEvidence `json:"permission_guard,omitempty"`
	ID              string                      `json:"id"`
	Connection      string                      `json:"connection"`
	Operation       string                      `json:"operation"`
	Tool            string                      `json:"tool"`
	Input           json.RawMessage             `json:"input"`
	Title           string                      `json:"title"`
	Status          string                      `json:"status"`
}
type claudeSnapshot struct {
	Config struct {
		ID                   string                     `json:"id"`
		Source               string                     `json:"source"`
		Cwd                  string                     `json:"cwd"`
		Model                string                     `json:"model"`
		Endpoint             string                     `json:"endpoint"`
		MCPSelections        []MCPSelection             `json:"mcp_selections,omitempty"`
		PluginSelections     []PluginSelection          `json:"plugin_selections,omitempty"`
		SkillSelections      []SkillSelection           `json:"skill_selections,omitempty"`
		Permission           string                     `json:"permission"`
		Executable           string                     `json:"executable"`
		SettingsFile         string                     `json:"settings_file"`
		SettingsArtifact     *ClaudeDerivedSettingsFile `json:"settings_artifact,omitempty"`
		InstalledSettings    *ClaudeInstalledSettings   `json:"installed_settings,omitempty"`
		SettingsGuardVersion int                        `json:"settings_guard_version,omitempty"`
		SettingsSources      []ClaudeSettingsSource     `json:"settings_sources,omitempty"`
	} `json:"config"`
	Connection                   string                       `json:"connection"`
	Live                         bool                         `json:"live"`
	Active                       string                       `json:"active"`
	Fault                        string                       `json:"fault"`
	Stream                       string                       `json:"stream"`
	Entries                      []claudeEntry                `json:"entries"`
	History                      []claudeEvent                `json:"history"`
	Pending                      []claudeRequest              `json:"pending"`
	MCP                          claudeMCPInventory           `json:"mcp"`
	Plugins                      claudePluginInventory        `json:"plugins"`
	Skills                       claudeSkillInventory         `json:"skills"`
	Configuration                *claudeConfigurationChange   `json:"configuration"`
	ConfigurationBaseline        string                       `json:"configuration_baseline"`
	HistoryDigest                string                       `json:"history_digest"`
	SettingsGuard                *ClaudeSettingsGuardEvidence `json:"settings_guard,omitempty"`
	InstructionObserver          *claudeInstructionObserver   `json:"instruction_observer,omitempty"`
	ConfigurationRecoveryFailure *ConfigurationFailure        `json:"configuration_recovery_failure,omitempty"`
	ConfigurationFailure         *ConfigurationFailure        `json:"configuration_failure,omitempty"`
}

func (s claudeSnapshot) initialized() bool {
	for _, e := range s.Entries {
		if e.Kind == "initialized" {
			return true
		}
	}
	return false
}

func claudeText(raw json.RawMessage) (string, bool) {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain, false
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	parts := []string{}
	tool := false
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			value, _ := claudeText(block.Content)
			parts = append(parts, value)
			tool = true
		case "tool_use":
			parts = append(parts, block.Name+" "+string(block.Input))
			tool = true
		}
	}
	return strings.Join(parts, "\n"), tool
}

func projectClaude(scope Scope, s claudeSnapshot) View {
	v := View{NativeID: s.Config.ID, Status: "idle", Messages: []Message{}, Pending: []Pending{}, Executions: []Execution{}, Turns: []Turn{}, Evidence: Evidence{
		Source: "claude.sdk.journal+native_history", At: time.Now().UTC().Format(time.RFC3339Nano), Complete: s.Fault == "" && s.initialized(), Detail: "固定 SDK 输入 UUID、原生历史、终态与后台任务事件核对"}}
	v.NativeStatus = "sdk_offline"
	if s.Live {
		v.NativeStatus = "sdk_live"
	}
	if s.Fault != "" {
		v.Evidence.Detail = s.Fault
	}
	status := map[string]string{}
	completedAt := map[string]string{}
	failures := map[string]*Failure{}
	known := map[string]bool{}
	queueClear := true
	for _, entry := range s.Entries {
		if entry.Kind == "input" {
			known[entry.Operation] = true
			status[entry.Operation] = "inProgress"
			v.Turns = append(v.Turns, Turn{ID: entry.Operation})
			continue
		}
		if entry.Kind != "event" {
			continue
		}
		e := entry.Event
		if e.Type == "assistant" && e.ParentToolUseID == nil && known[entry.Operation] && e.Error != "" {
			failures[entry.Operation] = claudeFailure(e.Error)
		}
		if e.Type == "result" && known[entry.Operation] {
			state := "failed"
			if e.Subtype == "success" && !e.IsError {
				state = "completed"
			}
			if e.TerminalReason == "aborted_streaming" || e.TerminalReason == "aborted_tools" {
				state = "interrupted"
			}
			status[entry.Operation] = state
			completedAt[entry.Operation] = entry.At
			queueClear = e.Queued != nil && *e.Queued == 0
		}
		if e.Type == "system" && e.Subtype == "background_tasks_changed" {
			v.Executions = []Execution{}
			for _, task := range e.Tasks {
				v.Executions = append(v.Executions, Execution{ID: task.ID, Kind: task.Type, Status: "running"})
			}
		}
	}
	for n := range v.Turns {
		v.Turns[n].Status = status[v.Turns[n].ID]
		v.Turns[n].CompletedAt = completedAt[v.Turns[n].ID]
		if v.Turns[n].Status == "failed" {
			v.Turns[n].Failure = failures[v.Turns[n].ID]
		}
		if v.Turns[n].Status == "inProgress" {
			v.Status = "running"
			if !s.Live {
				v.Status = "unknown"
				v.Evidence.Complete = false
			}
		}
	}
	// Native history is authoritative for user activity, including messages
	// authored outside the controller. The journal adds live SDK messages before
	// asynchronous transcript persistence catches up.
	seen := map[string]bool{}
	nativeGenerated := map[string]string{}
	for _, entry := range s.Entries {
		if entry.Kind == "event" && entry.Event.Type == "user" && !entry.Event.IsReplay && known[entry.Operation] && entry.Event.UUID != entry.Operation {
			nativeGenerated[entry.Event.UUID] = entry.Operation
		}
	}
	current := ""
	toolCalls := map[string]ToolResult{}
	appendMessage := func(e claudeEvent, operation string) {
		if e.Type != "user" && e.Type != "assistant" {
			return
		}
		if e.UUID == "" || seen[e.UUID] {
			return
		}
		body, tool := claudeText(e.Message.Content)
		if body == "" {
			return
		}
		role := e.Type
		if tool {
			role = "tool"
		}
		if owner, ok := nativeGenerated[e.UUID]; ok {
			role, operation = "tool", owner
		}
		if role == "user" {
			current = e.UUID
			operation = e.UUID
			if !known[e.UUID] {
				v.Status = "unknown"
				v.Evidence.Complete = false
				v.Evidence.Detail = "原生历史含控制器之外的输入；独立 CLI 运行状态不可由旧 SDK query 推断"
			}
		}
		if operation == "" {
			operation = current
		}
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
			Name      string `json:"name"`
			Input     struct {
				Command string `json:"command"`
			} `json:"input"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(e.Message.Content, &blocks) == nil {
			for _, block := range blocks {
				if block.Type == "tool_use" && block.Name == "Bash" {
					toolCalls[block.ID] = ToolResult{ID: block.ID, TurnID: operation, Command: block.Input.Command}
				}
				if block.Type == "tool_result" {
					if tool, ok := toolCalls[block.ToolUseID]; ok && tool.TurnID == operation {
						tool.Output, _ = claudeText(block.Content)
						var metadata struct {
							Interrupted      bool   `json:"interrupted"`
							BackgroundTaskID string `json:"backgroundTaskId"`
						}
						err := json.Unmarshal(e.ToolUseResult, &metadata)
						tool.Complete = (len(e.ToolUseResult) == 0 || err == nil) && !metadata.Interrupted && metadata.BackgroundTaskID == ""
						v.Tools = append(v.Tools, tool)
					}
				}
			}
		}
		state := status[operation]
		if state == "" {
			state = "completed"
		}
		v.Messages = append(v.Messages, Message{ID: e.UUID, TurnID: operation, Role: role, Text: body, Status: state, Operation: operation})
		seen[e.UUID] = true
	}
	for _, e := range s.History {
		appendMessage(e, "")
	}
	for _, e := range s.Entries {
		if e.Kind == "event" {
			appendMessage(e.Event, e.Operation)
		}
	}
	if s.Stream != "" && s.Active != "" {
		v.Messages = append(v.Messages, Message{ID: "stream/" + s.Active, TurnID: s.Active, Role: "assistant", Text: s.Stream, Status: "inProgress"})
	}
	for id := range known {
		if !seen[id] {
			v.Evidence.Complete = false
		}
	}
	for _, request := range s.Pending {
		id, _ := json.Marshal(request.ID)
		p := Pending{ID: scope.Instance.ID + "/" + request.Connection + "/" + request.ID, NativeID: scope.NativeID, NativeTurn: request.Operation,
			Kind: request.Tool, Request: request.Input, ConnectionID: request.Connection, RequestID: id, Title: request.Title, Status: request.Status, ResponseKind: "approval", Questions: []Question{}, Options: []Option{}}
		if request.Tool != "AskUserQuestion" {
			p.PermissionGuard = claudePermissionApproval(s, request)
		}
		if request.Tool == "AskUserQuestion" {
			p.ResponseKind = "user_input"
			var input struct {
				Questions []struct {
					Text    string `json:"question"`
					Header  string `json:"header"`
					Options []struct {
						Label       string `json:"label"`
						Description string `json:"description"`
					} `json:"options"`
				} `json:"questions"`
			}
			if json.Unmarshal(request.Input, &input) != nil {
				p.ResponseKind = "unsupported"
			}
			for n, q := range input.Questions {
				question := Question{ID: fmt.Sprintf("question-%d", n), Text: q.Text, Header: q.Header, Options: []Option{}}
				for m, option := range q.Options {
					value, _ := json.Marshal(option.Label)
					question.Options = append(question.Options, Option{ID: fmt.Sprintf("option-%d", m), Label: option.Label, Description: option.Description, Value: value})
				}
				p.Questions = append(p.Questions, question)
			}
		} else {
			var input struct {
				Command string `json:"command"`
				Path    string `json:"file_path"`
			}
			if json.Unmarshal(request.Input, &input) == nil {
				if input.Command != "" {
					p.Title = request.Tool + "\n" + input.Command
				} else if input.Path != "" {
					p.Title = request.Tool + "\n" + input.Path
				}
			}
			details, _ := json.MarshalIndent(request.Input, "", "  ")
			p.Details = string(details)
			p.Options = []Option{{ID: "allow", Label: "允许本次", Value: json.RawMessage(`"allow"`)}, {ID: "deny", Label: "拒绝本次", Value: json.RawMessage(`"deny"`)}}
		}
		v.Pending = append(v.Pending, p)
		if p.Status == "pending" || p.Status == "submitted" {
			v.Status = "waiting_approval"
			if p.ResponseKind == "user_input" {
				v.Status = "waiting_input"
			}
		}
	}
	v.HookExecutions = claudeHookExecutions(scope, s)
	v.InstructionLoads = claudeInstructionLoads(scope, s)
	if scope.SettingsGuardVersion != 0 && s.SettingsGuard != nil && s.SettingsGuard.Version == scope.SettingsGuardVersion {
		v.SettingsGuard = s.SettingsGuard
	}
	for _, hook := range v.HookExecutions {
		if hook.Status == "running" || hook.Status == "unknown" {
			v.Executions = append(v.Executions, Execution{ID: hook.ID, Kind: "hook", Status: hook.Status})
			if hook.Status == "unknown" {
				v.Evidence.Complete = false
				v.Evidence.Detail = "原生 hook 生命周期尚未核对完整"
			}
		}
	}
	if !queueClear {
		v.Executions = append(v.Executions, Execution{ID: scope.NativeID, Kind: "queue", Status: "unknown"})
	}
	if len(v.Executions) > 0 && v.Status == "idle" {
		v.Status = "running"
	}
	if len(v.Executions) > 0 && !s.Live {
		v.Status = "unknown"
		v.Evidence.Complete = false
		v.Evidence.Detail = "原 bridge 已断开，仍有未核对的后台任务占用"
	}
	if len(scope.MCPSelections) > 0 || len(claudeExecutionPlugins(scope)) > 0 || s.Configuration != nil {
		var matched bool
		v.CapabilityEvidence, matched = claudeMCPEvidence(scope, s)
		if s.Live && !matched {
			v.Evidence.Complete = false
			v.Evidence.Detail = "Claude MCP 实际清单与所选来源尚未核对一致"
		}
	}
	if s.Configuration != nil && s.Configuration.Status == "applying" {
		v.Evidence.Complete = false
		v.Evidence.Detail = "原 Claude 改配没有持久成功结果；只读核对期间不重放改配或输入"
	}
	if s.Configuration != nil && s.Configuration.Status == "recovered" && !claudeRecoveryProjectionVerified(scope, s) {
		v.Evidence.Complete = false
		v.Evidence.Detail = "原配置恢复缺少持久绑定或实际组件依据"
	}
	if len(claudeExecutionPlugins(scope)) > 0 {
		var matched bool
		v.PluginEvidence, matched = claudePluginEvidence(scope, s.Plugins)
		if s.Live && !matched {
			v.Evidence.Complete = false
			v.Evidence.Detail = "Claude 插件原生包与组件尚未核对一致"
		}
	}
	if len(scope.SkillSelections) > 0 {
		var matched bool
		v.SkillEvidence, matched = claudeSkillEvidence(scope, s.Skills)
		if s.Live && !matched {
			v.Evidence.Complete = false
			v.Evidence.Detail = "Claude Skill 原生命令/描述与所选来源尚未核对一致"
		}
	}
	if s.Live && s.Configuration != nil && s.Configuration.Status == "verified" && !claudeSkillChangeVerified(scope.SkillSelections, s.Configuration.SkillPrevious, s.Skills) {
		v.Evidence.Complete = false
		v.Evidence.Detail = "Claude Skill 改配后的选择或已释放命名空间尚未核对"
	}
	if s.Live && s.Configuration != nil && s.Configuration.Status == "verified" && !claudePluginChangeVerified(claudeExecutionPlugins(scope), claudeExecutionPlugins(Scope{PluginSelections: s.Configuration.PluginPrevious, ClaudeInstalledSettings: s.Configuration.InstalledPrevious}), s.Plugins) {
		v.Evidence.Complete = false
		v.Evidence.Detail = "Claude 插件改配后的选择或已释放组件尚未核对"
	}
	if !v.Evidence.Complete && v.Status == "idle" {
		v.Status = "unknown"
	}
	v.ExecutionClear = v.Evidence.Complete && v.Status == "idle" && len(v.Executions) == 0
	return v
}
