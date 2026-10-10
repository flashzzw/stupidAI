package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func configurationNoticeFixture() (Scope, claudeSnapshot) {
	scope := Scope{NativeID: uuid.NewString(), Cwd: "/owned", Instance: Instance{ID: "A", Kind: "claude"}}
	query, configuration, operation := uuid.NewString(), strings.Repeat("c", 64), uuid.NewString()
	registered, failed := true, false
	baseline := &PermissionApprovalEvidence{Version: 1, QueryID: query, ConfigurationID: configuration, State: "observed",
		BaselineAt: "2026-10-11T01:00:00Z", CheckedAt: "2026-10-11T01:00:00Z", BaselineHash: strings.Repeat("a", 64), CheckedHash: strings.Repeat("a", 64)}
	s := claudeSnapshot{Live: true, Active: operation, Connection: uuid.NewString(),
		InstructionObserver:   &claudeInstructionObserver{Version: 1, CurrentQueryID: query, CurrentConfigurationID: configuration},
		ConfigurationObserver: &claudeConfigurationObserver{Version: 1, Registered: &registered, RecordingFailed: &failed, CurrentQueryID: query, CurrentConfigurationID: configuration},
		Entries: []claudeEntry{{Kind: "initialized"}, {Kind: "input", QueryID: query, Operation: operation, PermissionBaseline: baseline},
			{Kind: "configuration_notice", ConfigurationNotice: &claudeConfigurationNotice{Version: 1, ConfigurationNotice: ConfigurationNotice{
				ID: uuid.NewString(), NativeID: scope.NativeID, Cwd: scope.Cwd, NativeCwd: "/owned/nested", QueryID: query, ConfigurationID: configuration,
				At: "2026-10-11T01:00:01Z", Path: "/etc/claude-code/managed-settings.json", Source: "policy_settings", PromptID: uuid.NewString(),
				ReceiptOperation: "untrusted", ReceiptSnapshotID: "untrusted", Current: true}}}}}
	s.Config.ID, s.Config.Cwd = scope.NativeID, scope.Cwd
	g := *baseline
	s.Pending = []claudeRequest{{ID: "request", Operation: operation, Connection: s.Connection, Tool: "Read", Status: "pending", PermissionGuard: &g}}
	return scope, s
}

func TestClaudeConfigurationNoticesKeepReceiptPositionSeparateFromNativePrompt(t *testing.T) {
	scope, s := configurationNoticeFixture()
	v := projectClaude(scope, s)
	h := v.ConfigurationNotices
	if h == nil || !h.Available || !h.Registered || h.Complete || h.RecordingFailed || h.CurrentCount != 1 || len(h.Items) != 1 {
		t.Fatalf("invalid notification history: %+v", h)
	}
	n := h.Items[0]
	if n.ReceiptOperation != s.Entries[1].Operation || n.ReceiptCorrelation != "journal_in_flight" || n.ReceiptSnapshotID != "" ||
		n.PromptID == n.ReceiptOperation || n.PromptID != s.Entries[2].ConfigurationNotice.PromptID || !n.Current {
		t.Fatal("receipt position was lost or promoted to causal/snapshot ownership", n)
	}
	g := v.Pending[0].PermissionGuard
	if g.State != "unavailable" || g.ConfigurationNoticeID != n.ID || !strings.Contains(g.Detail, "不证明权限规则已变更") {
		t.Fatal("journal notification did not independently invalidate the unchanged approval", g)
	}
}

func TestClaudeConfigurationNoticeReceiptUsesOnlyItsOwnQueryJournal(t *testing.T) {
	for _, mode := range []string{"later-input", "old-query-open", "old-query-terminal", "before-input", "two-open-inputs", "duplicate-input", "wrong-configuration", "missing-baseline", "native-agent"} {
		t.Run(mode, func(t *testing.T) {
			scope, s := configurationNoticeFixture()
			input, notice := s.Entries[1], s.Entries[2]
			second := input
			second.Operation = uuid.NewString()
			s.Active = second.Operation
			terminal := claudeEntry{Kind: "event", QueryID: input.QueryID, Operation: input.Operation, Event: claudeEvent{Type: "result", SessionID: scope.NativeID}}
			want := ""
			switch mode {
			case "later-input":
				s.Entries = append(s.Entries, terminal, second)
				want = input.Operation
			case "old-query-open", "old-query-terminal":
				second.QueryID = uuid.NewString()
				b := *input.PermissionBaseline
				b.QueryID = second.QueryID
				second.PermissionBaseline = &b
				s.ConfigurationObserver.CurrentQueryID = second.QueryID
				s.Entries = []claudeEntry{input}
				if mode == "old-query-terminal" {
					s.Entries = append(s.Entries, terminal)
				} else {
					want = input.Operation
				}
				s.Entries = append(s.Entries, second, notice)
			case "before-input":
				s.Entries = []claudeEntry{notice, input}
			case "two-open-inputs":
				s.Entries = []claudeEntry{input, second, notice}
			case "duplicate-input":
				s.Entries = []claudeEntry{input, input, notice}
			case "wrong-configuration":
				notice.ConfigurationNotice.ConfigurationID = strings.Repeat("b", 64)
			case "missing-baseline":
				s.Entries[1].PermissionBaseline = nil
			case "native-agent":
				notice.ConfigurationNotice.AgentID = "worker"
				want = input.Operation // Receipt position still says nothing about affected input ownership.
			}
			h := projectClaude(scope, s).ConfigurationNotices
			if h == nil || len(h.Items) != 1 || h.Items[0].ReceiptOperation != want || h.Items[0].ReceiptOperation == s.Active || h.Items[0].ReceiptSnapshotID != "" {
				t.Fatalf("notice followed a different active input: %+v", h)
			}
			if strings.HasPrefix(mode, "old-query") && h.Items[0].Current {
				t.Fatal("late old-query callback became current")
			}
		})
	}
}

func TestClaudeConfigurationNoticeRejectsInvalidMetadataAndDuplicateIdentities(t *testing.T) {
	for _, mode := range []string{"foreign-session", "foreign-cwd", "version", "time", "query", "config", "relative-path", "invalid-prompt", "NUL-source", "nil", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			scope, s := configurationNoticeFixture()
			n := s.Entries[2].ConfigurationNotice
			switch mode {
			case "foreign-session":
				n.NativeID = uuid.NewString()
			case "foreign-cwd":
				n.Cwd = "/other"
			case "version":
				n.Version = 2
			case "time":
				n.At = "yesterday"
			case "query":
				n.QueryID = "unknown"
			case "config":
				n.ConfigurationID = "unverified"
			case "relative-path":
				n.Path = "settings.json"
			case "invalid-prompt":
				n.PromptID = "unverified"
			case "NUL-source":
				n.Source = "policy\x00"
			case "nil":
				s.Entries[2].ConfigurationNotice = nil
			case "duplicate":
				s.Entries = append(s.Entries, s.Entries[2])
			}
			h := projectClaude(scope, s).ConfigurationNotices
			if h == nil || !h.RecordingFailed || h.Complete || len(h.Items) != 0 || h.CurrentCount != 0 {
				t.Fatal("invalid notice became usable evidence", h)
			}
			if g := projectClaude(scope, s).Pending[0].PermissionGuard; g.State != "unavailable" {
				t.Fatal("invalid notification evidence was treated as an unchanged approval", g)
			}
		})
	}
}

func TestClaudeConfigurationNoticeStatesHistoryAndTruncation(t *testing.T) {
	scope, s := configurationNoticeFixture()
	for n := 0; n < 130; n++ {
		notice := *s.Entries[2].ConfigurationNotice
		notice.ID, notice.Source, notice.Path, notice.PromptID = uuid.NewString(), "future_settings", "", ""
		s.Entries = append(s.Entries, claudeEntry{Kind: "configuration_notice", ConfigurationNotice: &notice})
	}
	h := projectClaude(scope, s).ConfigurationNotices
	if len(h.Items) != 128 || h.CurrentCount != 131 || !h.Truncated || h.Complete || h.Items[0].Path != "" || h.Items[0].Source != "future_settings" {
		t.Fatal("history truncation or unknown metadata lost", h)
	}
	s.Live = false
	h = projectClaude(scope, s).ConfigurationNotices
	if !h.Available || h.Registered || h.CurrentQueryID != "" || h.CurrentCount != 0 || h.Items[0].Current {
		t.Fatal("saved history promoted to live observation", h)
	}
	s.ConfigurationObserver = nil
	if h = projectClaude(scope, s).ConfigurationNotices; !h.Available || h.Registered || h.Complete {
		t.Fatal("observer absence lost valid history", h)
	}
	s.Entries = nil
	if projectClaude(scope, s).ConfigurationNotices != nil {
		t.Fatal("legacy absence became an empty verified history")
	}
}

func TestClaudeApprovalNoticeReferencesAndRegistrationMustBeVerifiable(t *testing.T) {
	for _, mode := range []string{"valid-reference", "changed", "unknown-reference", "before-input", "foreign-query", "foreign-config", "foreign-input", "registration-missing", "registration-failed", "recording-failed"} {
		t.Run(mode, func(t *testing.T) {
			scope, s := configurationNoticeFixture()
			g := s.Pending[0].PermissionGuard
			g.State, g.ConfigurationNoticeID = "unavailable", s.Entries[2].ConfigurationNotice.ID
			valid := mode == "valid-reference" || mode == "changed"
			switch mode {
			case "changed":
				g.State = "changed"
			case "unknown-reference":
				g.ConfigurationNoticeID = uuid.NewString()
			case "before-input":
				s.Entries[1], s.Entries[2] = s.Entries[2], s.Entries[1]
			case "foreign-query":
				s.Entries[2].ConfigurationNotice.QueryID = uuid.NewString()
			case "foreign-config":
				s.Entries[2].ConfigurationNotice.ConfigurationID = strings.Repeat("f", 64)
			case "foreign-input":
				s.Pending[0].Operation = uuid.NewString()
			case "registration-missing", "registration-failed", "recording-failed":
				s.Entries = s.Entries[:2]
				g.State, g.ConfigurationNoticeID = "observed", ""
				if mode == "registration-missing" {
					s.ConfigurationObserver = nil
				}
				if mode == "registration-failed" {
					*s.ConfigurationObserver.Registered = false
				}
				if mode == "recording-failed" {
					*s.ConfigurationObserver.RecordingFailed = true
				}
			}
			out := projectClaude(scope, s).Pending[0].PermissionGuard
			want := "unavailable"
			if mode == "changed" {
				want = "changed"
			}
			if out.State != want || out.Complete || (out.ConfigurationNoticeID != "") != valid {
				t.Fatal("unverified observer/reference enabled an approval or claimed attribution", out)
			}
			body, _ := json.Marshal(projectClaude(scope, s))
			if strings.Contains(string(body), "untrusted") {
				t.Fatal("raw receipt attribution escaped validation")
			}
		})
	}
}
