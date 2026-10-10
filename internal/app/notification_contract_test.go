package app

import (
	"encoding/json"
	"fmt"
	"testing"

	"codex-netcap/internal/events"
	"codex-netcap/internal/notify"
)

// This receiver exercises the ordinary event-selection contract without
// invoking a desktop helper, webhook or any installed notification tool.
type fakeNotificationReceiver struct{ deliveries []events.Envelope }

func (f *fakeNotificationReceiver) receive(config notify.Config, ledger []events.Envelope, sender, channel string) {
	f.deliveries = append(f.deliveries, notify.Candidates(config, ledger, sender, channel)...)
}

func TestNotificationOwnershipFromApplicationToFakeSender(t *testing.T) {
	for _, owner := range []string{"legacy", "console"} {
		t.Run(owner, func(t *testing.T) {
			c := observationConfig(t)
			c.Instances[0].NotificationSender = owner
			a := openFixture(t, c, newFixture())
			config := notify.Defaults()
			config.Agents["codex"] = notify.AgentPreferences{Enabled: true, Events: []string{"run_completed"}, Channels: notify.Channels{System: true}}
			var sessions []string
			var original events.Envelope
			for n := range 2 {
				created, err := a.Apply(Command{ID: fmt.Sprintf("create-%d", n), Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"})
				if err != nil {
					t.Fatal(err)
				}
				sid := created.SessionID
				sessions = append(sessions, sid)
				waitTest(t, func() bool { read, _ := a.ReadSession(sid, "", 50); return read.Session.Status == "idle" })
				if _, err := a.Apply(Command{ID: fmt.Sprintf("send-%d", n), Kind: "send", SessionID: sid, Text: "same text"}); err != nil {
					t.Fatal(err)
				}
				waitTest(t, func() bool { read, _ := a.ReadSession(sid, "", 50); return read.Session.Status == "idle" })
				var completion events.Envelope
				for _, event := range a.DomainEvents(sid) {
					if event.Kind == events.TurnCompleted {
						completion = event
					}
				}
				if completion.EventID == "" {
					t.Fatal("missing runtime completion")
				}
				if n == 0 {
					original = completion
				}
				payload := json.RawMessage(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"turn_id":%q,"message":"same text"}`, completion.NativeSessionID, completion.NativeTurnID))
				if _, err := a.IngestObservation("local-hooks", observationToken(), ObservationDelivery{EventID: fmt.Sprintf("hook-%d", n), OccurredAt: completion.OccurredAt, Payload: payload}); err != nil {
					t.Fatal(err)
				}
			}
			ledger := a.DomainEvents("")
			for _, sender := range []string{"legacy", "console"} {
				fake := &fakeNotificationReceiver{}
				fake.receive(config, append(ledger, ledger...), sender, "system")
				want := 0
				if sender == owner {
					want = 2
				}
				if len(fake.deliveries) != want {
					t.Fatalf("%s received %d, want %d", sender, len(fake.deliveries), want)
				}
				for _, event := range fake.deliveries {
					if len(event.Sources) != 2 || event.OperationID == "" {
						t.Fatalf("lost native association: %+v", event)
					}
				}
				if got := notify.Candidates(config, ledger, sender, "wecom"); len(got) != 0 {
					t.Fatal("disabled channel received notifications")
				}
			}
			if _, err := a.Apply(Command{ID: "new-activity", Kind: "send", SessionID: sessions[0], Text: "new turn"}); err != nil {
				t.Fatal(err)
			}
			waitTest(t, func() bool { read, _ := a.ReadSession(sessions[0], "", 50); return read.Session.Status == "idle" })
			// Native reference preserves cancellation even if the delivery arrives
			// again; an older event lacking a reference must also stay inactive.
			for n, turn := range []string{original.NativeTurnID, ""} {
				payload := json.RawMessage(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"turn_id":%q,"message":"same text"}`, original.NativeSessionID, turn))
				if _, err := a.IngestObservation("local-hooks", observationToken(), ObservationDelivery{EventID: fmt.Sprintf("late-%d", n), OccurredAt: original.OccurredAt, Payload: payload}); err != nil {
					t.Fatal(err)
				}
			}
			for _, event := range notify.Candidates(config, a.DomainEvents(""), owner, "system") {
				if event.EventID == original.EventID || (event.SessionID == sessions[0] && event.NativeTurnID == "") {
					t.Fatalf("late completion resurrected notification: %+v", event)
				}
			}
		})
	}
}

func observationToken() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
