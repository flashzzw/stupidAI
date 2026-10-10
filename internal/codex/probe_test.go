package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/config"
	"codex-netcap/internal/state"
)

func probeTranscript(command, output, answer string, code int, terminal string) string {
	items := []any{
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": command, "aggregated_output": output, "exit_code": code}},
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": answer}},
		map[string]any{"type": terminal},
	}
	var b strings.Builder
	for _, item := range items {
		data, _ := json.Marshal(item)
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestProbeRequiresExecutedDateAndMatchingUTC(t *testing.T) {
	const date = "2026-09-28T08:01:02Z"
	for _, tt := range []struct {
		name, command, output, answer, terminal string
		code                                    int
		want                                    bool
	}{
		{"real date", "/bin/bash -c 'date -u +%FT%TZ'", date + "\n", date, "turn.completed", 0, true},
		{"meaningless OK", "", "", "OK", "turn.completed", 0, false},
		{"no command", "", "", date, "turn.completed", 0, false},
		{"command failed", "date -u +%FT%TZ", date, date, "turn.completed", 1, false},
		{"wrong final time", "date -u +%FT%TZ", date, "2026-09-28T08:01:03Z", "turn.completed", 0, false},
		{"not UTC", "date -u +%FT%TZ", "2026-09-28T08:01:02+08:00", "2026-09-28T08:01:02+08:00", "turn.completed", 0, false},
		{"failed turn", "date -u +%FT%TZ", date, date, "turn.failed", 0, false},
		{"missing terminal", "date -u +%FT%TZ", date, date, "turn.started", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := parseProbe(strings.NewReader(probeTranscript(tt.command, tt.output, tt.answer, tt.code, tt.terminal)))
			if r.Success != tt.want {
				t.Fatalf("got %+v", r)
			}
		})
	}
}

func TestProbeArgsPermissionsAndRouting(t *testing.T) {
	c := config.Default()
	c.Model = "configured-model"
	c.Provider = "configured-provider"
	args := ProbeArgs(c, "probe-123")
	for _, flag := range []string{"exec", "--ephemeral", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "--json", state.ProbePrompt} {
		if !slices.Contains(args, flag) {
			t.Fatalf("missing %s: %v", flag, args)
		}
	}
	for key, want := range map[string]string{"--model": c.Model, "--thread-source": "probe-123", "-c": `model_provider="configured-provider"`} {
		i := slices.Index(args, key)
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Fatalf("wrong %s: %v", key, args)
		}
	}
}

func awaitProbe(t *testing.T, p *ExecProbe) ProbeResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := p.Poll(); r != nil {
			return *r
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Cancel()
	t.Fatal("probe did not finish")
	return ProbeResult{}
}

func TestExecProbeLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "nonzero", "cancel", "timeout", "start_failure"} {
		t.Run(mode, func(t *testing.T) {
			c := config.Default()
			c.StateDir = t.TempDir()
			c.CodexBin = filepath.Join(c.StateDir, "fake-codex")
			c.ProbeTimeoutSeconds = 1
			body := "#!/bin/sh\n"
			switch mode {
			case "cancel", "timeout":
				body += "sleep 60\n"
			default:
				body += "cat <<'TRANSCRIPT'\n" + probeTranscript("date -u +%FT%TZ", "2026-09-28T08:01:02Z", "2026-09-28T08:01:02Z", 0, "turn.completed") + "TRANSCRIPT\n"
				if mode == "nonzero" {
					body += "exit 7\n"
				}
			}
			if mode != "start_failure" {
				if err := os.WriteFile(c.CodexBin, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			p := NewExecProbe(c)
			err := p.Start("test")
			if mode == "start_failure" {
				if err == nil || p.Active() {
					t.Fatal("accepted missing executable")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer p.Cancel()
				if err = p.Start("duplicate"); err == nil {
					t.Fatal("accepted concurrent probe")
				}
				if mode == "cancel" {
					p.Cancel()
				}
				r := awaitProbe(t, p)
				if r.Success != (mode == "success") || r.Cancelled != (mode == "cancel") || p.Active() {
					t.Fatalf("%s: %+v", mode, r)
				}
				if mode == "timeout" && r.Error != "context deadline exceeded" {
					t.Fatal(r)
				}
			}
			files, err := os.ReadDir(filepath.Join(c.StateDir, "probe"))
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files not cleaned: %v %v", files, err)
			}
		})
	}
}
