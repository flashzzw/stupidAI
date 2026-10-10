package app

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestClaudeOriginalDenialAfterSourceDrift(t *testing.T) {
	for _, mode := range []string{"local", "user", "custom", "wrong-snapshot", "wrong-route", "foreign-request", "disguised-allow", "duplicate-option", "answers"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := claudeSkillFixture(t)
			path := filepath.Join(c.Projects[0].Cwd, ".claude", "settings.local.json")
			if mode == "user" {
				path = filepath.Join(c.Instances[0].Profile, "settings.json")
			}
			if mode == "custom" {
				path = filepath.Join(c.Instances[0].Profile, "custom-settings.json")
				c.Instances[0].SettingsFile = path
			}
			writePluginMetadata(t, path, `{"env":{"NETCAP_DENIAL_VERSION":"original"}}`)
			f := newFixture()
			a := openFixture(t, c, f)
			id := createFixture(t, a).SessionID
			before := a.Snapshot().Sessions[0]
			p := runtime.Pending{ID: before.Scope.Instance.ID + "/original-connection/original-request", NativeID: before.Scope.NativeID, NativeTurn: "original-input", ConnectionID: "original-connection", RequestID: json.RawMessage(`"original-request"`), Kind: "Read", ResponseKind: "approval", Status: "pending",
				Options: []runtime.Option{{ID: "allow", Value: json.RawMessage(`"allow"`)}, {ID: "deny", Value: json.RawMessage(`"deny"`)}}}
			if mode == "foreign-request" {
				p.RequestID = json.RawMessage(`"different"`)
			}
			if mode == "disguised-allow" {
				p.Options[1].Value = json.RawMessage(`"allow"`)
			}
			if mode == "duplicate-option" {
				p.Options = append(p.Options, p.Options[1])
			}
			a.mu.Lock()
			s := a.state.Sessions[id]
			s.View.Pending = []runtime.Pending{p}
			if mode == "wrong-snapshot" {
				s.ExecutionSnapshot = queryCopy(s.ExecutionSnapshot)
				s.ExecutionSnapshot.Cwd = "/different"
			}
			if mode == "wrong-route" {
				s.Scope.Route.Model = "changed"
			}
			a.state.Sessions[id] = s
			a.mu.Unlock()
			f.mu.Lock()
			v := f.views[before.Scope.NativeID]
			v.Pending = []runtime.Pending{p}
			f.views[before.Scope.NativeID] = v
			f.mu.Unlock()
			writePluginMetadata(t, path, `{"env":{"NETCAP_DENIAL_VERSION":"changed"}}`)
			if _, err := a.Apply(Command{ID: "must-not-allow", Kind: "respond", SessionID: id, PendingID: p.ID, OptionID: "allow"}); err == nil {
				t.Fatal("source drift admitted approval")
			}
			command := Command{ID: "deny-original", Kind: "respond", SessionID: id, PendingID: p.ID, OptionID: "deny"}
			if mode == "answers" {
				command.Answers = map[string][]string{"one": {"injected"}}
			}
			r, err := a.Apply(command)
			valid := mode == "local" || mode == "user" || mode == "custom"
			if !valid {
				if err == nil {
					t.Fatal("invalid original denial admitted")
				}
				if f.sends.Load() != 0 {
					t.Fatal("invalid refusal reached native")
				}
				return
			}
			if err != nil {
				t.Fatal("cannot refuse original request after source drift", err)
			}
			waitTest(t, func() bool {
				for _, op := range a.Snapshot().Operations {
					if op.ID == r.OperationID {
						return op.Status == "succeeded"
					}
				}
				return false
			})
			if f.sends.Load() != 1 || f.creates.Load() != 1 {
				t.Fatal("refusal restarted or repeated execution")
			}
			if !reflect.DeepEqual(before.ExecutionSnapshot, a.Snapshot().Sessions[0].ExecutionSnapshot) {
				t.Fatal("refusal changed original snapshot")
			}
			replay, err := a.Apply(command)
			if err != nil || replay.OperationID != r.OperationID || f.sends.Load() != 1 {
				t.Fatal("refusal receipt replayed the command", err)
			}
		})
	}
}
