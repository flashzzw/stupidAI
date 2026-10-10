// Package notify defines notification compatibility and ownership policy.
// Delivery transports are added separately; parsing settings never sends.
package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"codex-netcap/internal/events"
)

type Channels struct {
	System bool `json:"system"`
	Wecom  bool `json:"wecom"`
}

type AgentPreferences struct {
	Enabled  bool     `json:"enabled"`
	Events   []string `json:"events"`
	Channels Channels `json:"channels"`
}

type Subscription struct {
	Enabled  bool     `json:"enabled"`
	Channels Channels `json:"channels"`
}

type Config struct {
	Version       int                          `json:"version"`
	Agents        map[string]AgentPreferences  `json:"agents"`
	Behavior      map[string]int               `json:"behavior"`
	Subscriptions map[events.Kind]Subscription `json:"subscriptions"`
}

var legacyEvents = []string{"permission_required", "input_required", "run_completed", "run_failed", "started", "stopped"}
var newEvents = []events.Kind{events.TaskSucceeded, events.GoalCompleted, events.RouteUnavailable, events.RouteRecovered}

func Defaults() Config {
	c := Config{Version: 2, Agents: map[string]AgentPreferences{}, Behavior: map[string]int{}, Subscriptions: map[events.Kind]Subscription{}}
	for _, agent := range []string{"codex", "claude", "opencode"} {
		c.Agents[agent] = AgentPreferences{Enabled: true, Events: append([]string{}, legacyEvents...), Channels: Channels{System: true, Wecom: true}}
	}
	for _, kind := range newEvents {
		c.Subscriptions[kind] = Subscription{Enabled: false, Channels: Channels{System: true, Wecom: true}}
	}
	return c
}

func readChannels(raw json.RawMessage) (Channels, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) != 2 {
		return Channels{}, fmt.Errorf("通知渠道必须包含 system/wecom")
	}
	var system, wecom bool
	if json.Unmarshal(values["system"], &system) != nil || json.Unmarshal(values["wecom"], &wecom) != nil || string(values["system"]) == "null" || string(values["wecom"]) == "null" {
		return Channels{}, fmt.Errorf("通知渠道必须为布尔值")
	}
	return Channels{System: system, Wecom: wecom}, nil
}

// Parse accepts the installed v1 tool's effective settings, preserving its
// agent switches, legacy event sets and channel preferences. New domain
// subscriptions are independent and remain off unless v2 explicitly enables them.
func Parse(raw []byte) (Config, error) {
	c := Defaults()
	if len(raw) == 0 {
		return c, nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return c, fmt.Errorf("通知设置必须为 JSON 对象")
	}
	var version float64
	if json.Unmarshal(values["version"], &version) != nil || (version != 1 && version != 2) {
		return c, fmt.Errorf("通知设置只支持版本 1 或 2")
	}
	for key := range values {
		if key != "version" && key != "agents" && key != "behavior" && !(version == 2 && key == "subscriptions") {
			return c, fmt.Errorf("未知通知设置项 %s", key)
		}
	}
	var agents map[string]json.RawMessage
	if json.Unmarshal(values["agents"], &agents) != nil || agents == nil {
		return c, fmt.Errorf("缺少 agents 通知设置")
	}
	for name, raw := range agents {
		if _, ok := c.Agents[name]; !ok {
			return c, fmt.Errorf("未知通知 Agent %s", name)
		}
		var value struct {
			Enabled  *bool           `json:"enabled"`
			Events   *[]string       `json:"events"`
			Channels json.RawMessage `json:"channels"`
		}
		if json.Unmarshal(raw, &value) != nil || value.Enabled == nil || value.Events == nil {
			return c, fmt.Errorf("Agent 通知开关/事件无效")
		}
		for _, event := range *value.Events {
			if !slices.Contains(legacyEvents, event) {
				return c, fmt.Errorf("未知旧通知事件 %s", event)
			}
		}
		channels, err := readChannels(value.Channels)
		if err != nil {
			return c, err
		}
		c.Agents[name] = AgentPreferences{Enabled: *value.Enabled, Events: append([]string{}, (*value.Events)...), Channels: channels}
	}
	var behavior map[string]float64
	if json.Unmarshal(values["behavior"], &behavior) != nil || behavior == nil {
		return c, fmt.Errorf("behavior 必须为对象")
	}
	for key, value := range behavior {
		if !slices.Contains([]string{"wecom_delay_seconds", "dedup_seconds", "error_dedup_seconds"}, key) || value < 0 || value > 604800 || value != float64(int(value)) {
			return c, fmt.Errorf("通知行为设置无效")
		}
		c.Behavior[key] = int(value)
	}
	if raw, ok := values["subscriptions"]; ok {
		var subscriptions map[string]json.RawMessage
		if json.Unmarshal(raw, &subscriptions) != nil || subscriptions == nil {
			return c, fmt.Errorf("subscriptions 必须为对象")
		}
		for name, raw := range subscriptions {
			kind := events.Kind(name)
			if !slices.Contains(newEvents, kind) {
				return c, fmt.Errorf("未知领域订阅 %s", name)
			}
			var value struct {
				Enabled  *bool           `json:"enabled"`
				Channels json.RawMessage `json:"channels"`
			}
			if json.Unmarshal(raw, &value) != nil || value.Enabled == nil {
				return c, fmt.Errorf("领域订阅开关无效")
			}
			channels, err := readChannels(value.Channels)
			if err != nil {
				return c, err
			}
			c.Subscriptions[kind] = Subscription{Enabled: *value.Enabled, Channels: channels}
		}
	}
	return c, nil
}

func LegacyEvent(kind events.Kind) (string, bool) {
	switch kind {
	case events.PermissionRequired:
		return "permission_required", true
	case events.InputRequired:
		return "input_required", true
	case events.RunFailed:
		return "run_failed", true
	case events.TurnCompleted:
		return "run_completed", true
	case events.SessionStarted:
		return "started", true
	case events.SessionStopped:
		return "stopped", true
	default:
		return "", false
	}
}

func CheckCompatibility(kind events.Kind, targetVersion int) error {
	if targetVersion != 1 && targetVersion != 2 {
		return fmt.Errorf("通知接收端版本不受支持")
	}
	if _, legacy := LegacyEvent(kind); legacy {
		return nil
	}
	if !slices.Contains(newEvents, kind) {
		return fmt.Errorf("此事件不属于通知事件")
	}
	if targetVersion < 2 {
		return fmt.Errorf("接收端不支持独立 %s 订阅，需要通知配置版本 2", kind)
	}
	return nil
}

// Candidates gives both old and new senders the same scope contract. A
// managed session is represented by runtime evidence for the console owner,
// and native hook/OTEL evidence for the legacy owner. Observation sources
// cannot opt into a different sender by adding fields to their payload.
func Candidates(c Config, input []events.Envelope, sender, channel string) []events.Envelope {
	out := []events.Envelope{}
	seen := map[string]bool{}
	for _, event := range input {
		if event.NotificationSender != sender || !event.Active || seen[event.EventID] {
			continue
		}
		if event.Managed {
			isRuntime := strings.HasPrefix(event.Source, "runtime/")
			hasLegacy := !isRuntime
			for _, source := range event.Sources {
				if strings.HasPrefix(source, "runtime/") {
					isRuntime = true
				} else {
					hasLegacy = true
				}
			}
			if (sender == "console" && !isRuntime) || (sender == "legacy" && !hasLegacy) {
				continue
			}
		}
		enabled := false
		channels := Channels{}
		if legacy, ok := LegacyEvent(event.Kind); ok {
			prefs, exists := c.Agents[event.AgentKind]
			enabled = exists && prefs.Enabled && slices.Contains(prefs.Events, legacy)
			channels = prefs.Channels
		} else if subscription, ok := c.Subscriptions[event.Kind]; ok {
			enabled = subscription.Enabled
			channels = subscription.Channels
		}
		if !enabled || !(channel == "system" && channels.System || channel == "wecom" && channels.Wecom) {
			continue
		}
		seen[event.EventID] = true
		out = append(out, event)
	}
	return out
}
