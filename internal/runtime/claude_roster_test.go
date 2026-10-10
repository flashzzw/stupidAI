package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const rosterNativeID = "12345678-1234-4234-8234-123456789abc"

func TestClaudeRosterQueryBindsIdentityWithoutStartingSDK(t *testing.T) {
	for _, scenario := range []string{"working_idle", "done_idle", "no_process", "interactive", "waiting", "unknown_values", "wrong_uuid", "short_only", "duplicate", "wrong_cwd", "null", "malformed", "bad_pid", "bad_kind", "oversized", "future_version", "namespace_changed", "alias_stable", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, "profile")
			if err := os.Mkdir(profile, 0700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(root, "cli")
			log := filepath.Join(root, "calls.jsonl")
			script := `#!/usr/bin/python3
import json,os,sys
from pathlib import Path
root=Path(__file__).parent
with (root/'calls.jsonl').open('a') as f:f.write(json.dumps({'args':sys.argv[1:],'profile':os.environ.get('CLAUDE_CONFIG_DIR')})+'\n')
if sys.argv[1:]==['--version']:
 print((root/'version').read_text());sys.exit(0)
if sys.argv[1:4]!=['agents','--json','--all'] or sys.argv[4:5]!=['--cwd'] or len(sys.argv)!=6:sys.exit(9)
if (root/'replace-alias').exists():
 (root/'alias').unlink();(root/'alias').symlink_to(root/'other',target_is_directory=True)
sys.stdout.write((root/'roster.json').read_text())
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			version := "2.1.280 (Claude Code)"
			if scenario == "future_version" {
				version = "future"
			}
			if err := os.WriteFile(filepath.Join(root, "version"), []byte(version), 0600); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": "12345678", "sessionId": rosterNativeID, "cwd": root, "kind": "background", "state": "working", "status": "idle", "pid": 4242, "startedAt": 1790880000000}
			rows := []any{row}
			valid := true
			switch scenario {
			case "done_idle":
				row["state"] = "done"
			case "no_process":
				delete(row, "pid")
				delete(row, "status")
			case "interactive":
				row["kind"] = "interactive"
				delete(row, "id")
				delete(row, "state")
			case "waiting":
				row["state"] = "blocked"
				row["status"] = "waiting"
				row["waitingFor"] = "permission prompt"
			case "unknown_values":
				row["state"] = "private-future-detail"
				row["status"] = "private-future-detail"
				row["waitingFor"] = "private-future-detail"
			case "wrong_uuid":
				row["sessionId"] = "12345678-1234-4234-8234-999999999999"
				valid = false
			case "short_only":
				delete(row, "sessionId")
				valid = false
			case "duplicate":
				rows = append(rows, row)
				valid = false
			case "wrong_cwd":
				row["cwd"] = "/foreign"
				valid = false
			case "bad_pid":
				row["pid"] = -1
				valid = false
			case "bad_kind":
				row["kind"] = "unknown"
				valid = false
			case "future_version", "namespace_changed", "cancelled", "null", "malformed", "oversized":
				valid = false
			}
			data, _ := json.Marshal(rows)
			if scenario == "null" {
				data = []byte("null")
			}
			if scenario == "malformed" {
				data = []byte("private-error-not-json")
			}
			if scenario == "oversized" {
				data = []byte(strings.Repeat("x", claudeRosterLimit+1))
			}
			if err := os.WriteFile(filepath.Join(root, "roster.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			scope := Scope{NativeID: rosterNativeID, Cwd: root, StateNamespace: profile, Instance: Instance{ID: "roster", Kind: "claude", Executable: binary, Profile: profile, Bridge: "must-not-start"}}
			if scenario == "namespace_changed" || scenario == "alias_stable" {
				alias := filepath.Join(root, "alias")
				if err := os.Symlink(profile, alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "other"), 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "namespace_changed" {
					if err := os.WriteFile(filepath.Join(root, "replace-alias"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				scope.Instance.Profile = alias
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			c := NewClaude(filepath.Join(root, "state"))
			defer c.Close()
			view, err := c.ObserveNativeExecution(ctx, scope)
			if valid {
				if err != nil || view.NativeID != rosterNativeID || view.AgentRoster == nil || view.ExecutionClear || view.Evidence.Complete || view.Status != "unknown" || view.TerminalCommand != "" {
					t.Fatalf("bad declaration scope: %+v %v", view, err)
				}
				encoded, _ := json.Marshal(view)
				if strings.Contains(string(encoded), "private-future-detail") {
					t.Fatal("unknown native value leaked")
				}
			} else if err == nil {
				t.Fatalf("%s admitted: %+v", scenario, view)
			}
			c.mu.Lock()
			clients := len(c.clients)
			c.mu.Unlock()
			if clients != 0 {
				t.Fatal("roster query started SDK")
			}
			calls, _ := os.ReadFile(log)
			if scenario == "alias_stable" && strings.Contains(string(calls), scope.Instance.Profile) {
				t.Fatal("query used mutable alias instead of saved namespace")
			}
			if scenario == "future_version" && strings.Contains(string(calls), "agents") {
				t.Fatal("unknown version queried native registry")
			}
			if scenario == "cancelled" && len(calls) > 0 {
				t.Fatal("cancelled query launched CLI")
			}
			if strings.Contains(string(calls), "resume") || strings.Contains(string(calls), "attach") {
				t.Fatal("query requested execution control")
			}
		})
	}
}

// Real fixed CLI reads synthetic job records in isolated A/B namespaces. This
// proves the JSON query/projection, not a live worker's control or tool state.
func TestClaudeNativeRosterReadsIsolatedRecords(t *testing.T) {
	binary := os.Getenv("NETCAP_NATIVE_CLAUDE_ROSTER")
	if binary == "" {
		t.Skip("set fixed native CLI path for isolated roster query")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	results := map[string]any{}
	for _, state := range []string{"working", "stopped"} {
		base := filepath.Join(root, state)
		profile := filepath.Join(base, "claude")
		dir := filepath.Join(profile, "jobs", rosterNativeID[:8])
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		record := map[string]any{"state": state, "detail": "fixture", "intent": "fixture-no-model-input", "sessionId": rosterNativeID, "daemonShort": rosterNativeID[:8], "cliVersion": "2.1.280", "cwd": cwd, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "updatedAt": time.Now().UTC().Format(time.RFC3339Nano), "template": "bg", "backend": "daemon"}
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		c := NewClaude(filepath.Join(base, "state"))
		defer c.Close()
		scope := Scope{NativeID: rosterNativeID, Cwd: cwd, StateNamespace: profile, Instance: Instance{ID: state, Kind: "claude", Executable: binary, Profile: profile, isolatedHome: base}}
		view, err := c.ObserveNativeExecution(context.Background(), scope)
		if err != nil || view.AgentRoster == nil || view.AgentRoster.State != state || view.NativeID != rosterNativeID || view.ExecutionClear || view.Evidence.Complete || view.AgentRoster.PID != nil {
			t.Fatalf("native roster %s: %+v %v declaration=%+v", state, view, err, view.AgentRoster)
		}
		if _, err := os.Stat(filepath.Join(profile, "daemon.log")); !os.IsNotExist(err) {
			t.Fatal("JSON query unexpectedly created daemon log")
		}
		if _, err := os.Stat(filepath.Join(profile, "daemon", "roster.json")); !os.IsNotExist(err) {
			t.Fatal("JSON query unexpectedly created daemon roster")
		}
		results[state] = view
	}
	if path := os.Getenv("NETCAP_NATIVE_CLAUDE_ROSTER_OUTPUT"); path != "" {
		data, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeNativeLiveRosterScope(t *testing.T) {
	raw := os.Getenv("NETCAP_NATIVE_CLAUDE_ROSTER_LIVE_SCOPE")
	if raw == "" {
		t.Skip("requires owned live native CLI from roster probe")
	}
	var input struct {
		Binary, Profile, Home, Cwd, NativeID, Output string
		PID                                          int
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	c := NewClaude(t.TempDir())
	defer c.Close()
	scope := Scope{NativeID: input.NativeID, Cwd: input.Cwd, StateNamespace: input.Profile, Instance: Instance{ID: "owned-live", Kind: "claude", Executable: input.Binary, Profile: input.Profile, isolatedHome: input.Home}}
	view, err := c.ObserveNativeExecution(context.Background(), scope)
	if err != nil || view.AgentRoster == nil || view.AgentRoster.Kind != "interactive" || view.AgentRoster.PID == nil || *view.AgentRoster.PID != input.PID || view.NativeID != input.NativeID || view.ExecutionClear || view.Evidence.Complete {
		t.Fatalf("live roster: %+v declaration=%+v err=%v", view, view.AgentRoster, err)
	}
	data, _ := json.MarshalIndent(view, "", "  ")
	if input.Output != "" {
		if err := os.WriteFile(input.Output, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.clients) != 0 {
		t.Fatal("live roster query started SDK")
	}
}
