package runtime

import (
	"encoding/json"
	"strings"
	"time"
)

type claudeInstructionPrompt struct {
	Version         int    `json:"version"`
	NativeID        string `json:"native_id"`
	Cwd             string `json:"cwd"`
	QueryID         string `json:"query_id"`
	ConfigurationID string `json:"configuration_id"`
	At              string `json:"at"`
	PromptID        string `json:"prompt_id"`
	ToolUseID       string `json:"tool_use_id"`
	Tool            string `json:"tool"`
	AgentID         string `json:"agent_id,omitempty"`
}

func instructionPromptKey(query, configuration, prompt string) string {
	return query + "\x00" + configuration + "\x00" + prompt
}

// Join two independent native facts inside the same owned query. Never infer
// prompt identity from the active input, journal position or matching text.
func claudeInstructionInputOperations(scope Scope, snapshot claudeSnapshot) map[string]string {
	inputs := map[string]bool{}
	for _, entry := range snapshot.Entries {
		if entry.Kind == "input" && instructionIdentifier.MatchString(entry.QueryID) && instructionIdentifier.MatchString(entry.Operation) {
			inputs[entry.QueryID+"\x00"+entry.Operation] = true
		}
	}
	toolOperations, promptOperations := map[string]map[string]bool{}, map[string]map[string]bool{}
	add := func(target map[string]map[string]bool, key, operation string) {
		if target[key] == nil {
			target[key] = map[string]bool{}
		}
		target[key][operation] = true
	}
	valid := func(value string, max int) bool {
		return value != "" && len(value) <= max && !strings.ContainsRune(value, '\x00')
	}
	for _, entry := range snapshot.Entries {
		event := entry.Event
		if entry.Kind != "event" || event.Type != "assistant" || event.SessionID != scope.NativeID || event.ParentToolUseID != nil || event.IsReplay ||
			!inputs[entry.QueryID+"\x00"+event.UserMessageUUID] {
			continue
		}
		var blocks []struct{ Type, ID string }
		if json.Unmarshal(event.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" && valid(block.ID, 300) {
				add(toolOperations, entry.QueryID+"\x00"+block.ID, event.UserMessageUUID)
			}
		}
	}
	for _, entry := range snapshot.Entries {
		link := entry.InstructionPrompt
		if entry.Kind != "instruction_prompt" || link == nil || link.Version != 1 || link.NativeID != scope.NativeID || link.Cwd != scope.Cwd || link.AgentID != "" ||
			!instructionIdentifier.MatchString(link.QueryID) || !instructionIdentifier.MatchString(link.PromptID) || !claudeSettingsFileSHA.MatchString(link.ConfigurationID) ||
			!valid(link.ToolUseID, 300) || !valid(link.Tool, 200) {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, link.At); err != nil {
			continue
		}
		for operation := range toolOperations[link.QueryID+"\x00"+link.ToolUseID] {
			add(promptOperations, instructionPromptKey(link.QueryID, link.ConfigurationID, link.PromptID), operation)
		}
	}
	result := map[string]string{}
	for key, operations := range promptOperations {
		if len(operations) == 1 {
			for operation := range operations {
				result[key] = operation
			}
		}
	}
	return result
}
