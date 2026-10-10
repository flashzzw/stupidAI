package notify

import (
	"encoding/json"
	"reflect"
	"testing"

	"codex-netcap/internal/events"
)

func TestLegacySettingsKeepEffectivePreferencesAndNewSubscriptionsOff(t *testing.T) {
	c, err := Parse([]byte(`{"version":1,"agents":{"claude":{"enabled":false,"events":["input_required","run_completed"],"channels":{"system":false,"wecom":true}},"codex":{"enabled":true,"events":[],"channels":{"system":true,"wecom":false}}},"behavior":{"dedup_seconds":17.0,"wecom_delay_seconds":0,"error_dedup_seconds":91}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 || c.Agents["claude"].Enabled || c.Agents["claude"].Channels.System || !c.Agents["claude"].Channels.Wecom || !reflect.DeepEqual(c.Agents["claude"].Events, []string{"input_required", "run_completed"}) || len(c.Agents["codex"].Events) != 0 || !c.Agents["opencode"].Enabled || c.Behavior["dedup_seconds"] != 17 {
		t.Fatalf("legacy preference drift: %+v", c)
	}
	for _, subscription := range c.Subscriptions {
		if subscription.Enabled {
			t.Fatal("migration enabled new notifications")
		}
	}
	b, _ := json.Marshal(c)
	roundtrip, err := Parse(b)
	if err != nil || !reflect.DeepEqual(c, roundtrip) {
		t.Fatalf("v2 roundtrip changed preferences: %v", err)
	}
	if err := CheckCompatibility(events.TaskSucceeded, 1); err == nil {
		t.Fatal("new event silently accepted by old receiver")
	}
	if err := CheckCompatibility(events.TurnCompleted, 1); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationOwnershipAndIdentitySelectExactlyOneSender(t *testing.T) {
	c := Defaults()
	runtime := events.Envelope{EventID: "turn-one", Kind: events.TurnCompleted, Source: "runtime/instance", AgentKind: "codex", Managed: true, Active: true, NotificationSender: "console"}
	hook := runtime
	hook.Source = "hook/observer"
	hook.EventID = "uncorrelated-hook"
	if len(Candidates(c, []events.Envelope{runtime, hook, runtime}, "console", "system")) != 1 || len(Candidates(c, []events.Envelope{runtime, hook}, "legacy", "system")) != 0 {
		t.Fatal("managed native overlap creates duplicate senders")
	}
	runtime.NotificationSender = "legacy"
	hook.NotificationSender = "legacy"
	if len(Candidates(c, []events.Envelope{runtime, hook}, "console", "wecom")) != 0 || len(Candidates(c, []events.Envelope{runtime, hook}, "legacy", "wecom")) != 1 {
		t.Fatal("legacy scope was silently migrated")
	}
	runtime.Sources = []string{"runtime/instance", "hook/observer"}
	if len(Candidates(c, []events.Envelope{runtime}, "legacy", "wecom")) != 1 {
		t.Fatal("correlation erased the legacy source's delivery ownership")
	}
	task := events.Envelope{EventID: "task-one", Kind: events.TaskSucceeded, Source: "console/tasks", Active: true, NotificationSender: "console"}
	if len(Candidates(c, []events.Envelope{task}, "console", "system")) != 0 {
		t.Fatal("turn completion enabled task completion")
	}
	c.Subscriptions[events.TaskSucceeded] = Subscription{Enabled: true, Channels: Channels{System: true}}
	if len(Candidates(c, []events.Envelope{task}, "console", "system")) != 1 || len(Candidates(c, []events.Envelope{task}, "console", "wecom")) != 0 {
		t.Fatal("independent subscription/channel policy ignored")
	}
	task.Active = false
	if len(Candidates(c, []events.Envelope{task}, "console", "system")) != 0 {
		t.Fatal("superseded event was resent")
	}
}
