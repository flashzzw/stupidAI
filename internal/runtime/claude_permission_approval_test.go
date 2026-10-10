package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ownedClaudeDenial() Intent {
	id, connection, request := uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw, _ := json.Marshal(request)
	return Intent{ID: uuid.NewString(), Kind: "respond", Scope: Scope{NativeID: id, Instance: Instance{ID: "owned", Kind: "claude"}}, OptionID: "deny",
		Pending: &Pending{ID: "owned/" + connection + "/" + request, NativeID: id, NativeTurn: uuid.NewString(), ConnectionID: connection, RequestID: raw,
			Kind: "Read", Status: "pending", ResponseKind: "approval", Options: []Option{{ID: "allow", Value: json.RawMessage(`"allow"`)}, {ID: "deny", Value: json.RawMessage(`"deny"`)}}}}
}

func TestClaudeToolDenialRequiresOriginalNativeOption(t *testing.T) {
	for _, mode := range []string{"original", "allow", "disguised-allow", "duplicate-option", "foreign-session", "foreign-connection", "foreign-request", "expired", "question", "other-backend", "text", "answers", "skill-input", "missing-turn"} {
		t.Run(mode, func(t *testing.T) {
			in := ownedClaudeDenial()
			switch mode {
			case "allow":
				in.OptionID = "allow"
			case "disguised-allow":
				in.Pending.Options[1].Value = json.RawMessage(`"allow"`)
			case "duplicate-option":
				in.Pending.Options = append(in.Pending.Options, in.Pending.Options[1])
			case "foreign-session":
				in.Pending.NativeID = uuid.NewString()
			case "foreign-connection":
				in.Pending.ConnectionID = uuid.NewString()
			case "foreign-request":
				in.Pending.RequestID = json.RawMessage(`"different"`)
			case "expired":
				in.Pending.Status = "expired"
			case "question":
				in.Pending.ResponseKind = "user_input"
			case "other-backend":
				in.Scope.Instance.Kind = "codex"
			case "text":
				in.Text = "new input"
			case "answers":
				in.Answers = map[string][]string{"one": {"new input"}}
			case "skill-input":
				in.SkillInputs = []SkillInput{{}}
			case "missing-turn":
				in.Pending.NativeTurn = ""
			}
			if IsClaudeToolDenial(in) != (mode == "original") {
				t.Fatal("incorrect denial scope", mode)
			}
		})
	}
}

func TestClaudeDenialUsesExistingTransportWithoutSourceReadOrRestart(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdio fixture required")
	}
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "live"}[live], func(t *testing.T) {
			root := t.TempDir()
			in := ownedClaudeDenial()
			in.Scope.Cwd = root
			in.Scope.SettingsGuardVersion = 1
			in.Scope.ClaudeSettingsSources = []ClaudeSettingsSource{{Path: filepath.Join(root, "missing-settings.json"), Kind: "file", SHA256: strings.Repeat("a", 64)}}
			in.Scope.Instance.NodeExecutable = python
			in.Scope.Instance.Bridge = filepath.Join(root, "bridge.py")
			in.Scope.Instance.Profile = root
			in.Scope.Instance.Executable = "/must-not-start-claude"
			worker := `import json,sys
from pathlib import Path
log=Path(sys.argv[1])/'calls.jsonl'
for line in sys.stdin:
 q=json.loads(line)
 with log.open('a') as out:out.write(json.dumps(q)+'\n')
 result={'version':1,'sdk':'0.3.280'} if q['method']=='hello' else {'native_id':q['params']['native_id']}
 print(json.dumps({'v':1,'id':q['id'],'result':result}),flush=True)
`
			if err := os.WriteFile(in.Scope.Instance.Bridge, []byte(worker), 0600); err != nil {
				t.Fatal(err)
			}
			engine := NewClaude(filepath.Join(root, "application"))
			defer engine.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			transportDir := filepath.Join(root, "transport")
			if live {
				p, e := connectClaude(ctx, in.Scope.Instance, transportDir)
				if e != nil {
					t.Fatal(e)
				}
				engine.clients[in.Scope.Instance.ID] = p
			}
			receipt, e := engine.Execute(ctx, in)
			if !live {
				var unsent *NotSent
				if !errors.As(e, &unsent) {
					t.Fatal("closed original request was not rejected", e)
				}
				if _, err := os.Stat(filepath.Join(root, "application", "runtimes")); !os.IsNotExist(err) {
					t.Fatal("denial created a new bridge")
				}
				return
			}
			if e != nil || receipt.NativeID != in.Scope.NativeID {
				t.Fatal("owned refusal failed after source drift", receipt, e)
			}
			data, err := os.ReadFile(filepath.Join(transportDir, "calls.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 2 {
				t.Fatal("denial performed more than hello and original response", string(data))
			}
			var call struct {
				Method string            `json:"method"`
				Params map[string]string `json:"params"`
			}
			if json.Unmarshal([]byte(lines[1]), &call) != nil || call.Method != "respond" || call.Params["decision"] != "deny" || call.Params["connection"] != in.Pending.ConnectionID {
				t.Fatal("original refusal identity changed", string(data))
			}
		})
	}
}

func TestClaudeApprovalProjectionRequiresOriginalInputObservation(t *testing.T) {
	for _, mode := range []string{"observed", "changed", "unavailable", "missing", "wrong-query", "wrong-config", "no-input", "wrong-input", "bad-hash", "bad-time", "false-complete", "unknown-version"} {
		t.Run(mode, func(t *testing.T) {
			in := ownedClaudeDenial()
			query, config := uuid.NewString(), strings.Repeat("b", 64)
			g := PermissionApprovalEvidence{Version: 1, QueryID: query, ConfigurationID: config, State: "observed", BaselineAt: "2026-10-10T00:00:00Z", BaselineHash: strings.Repeat("a", 64), CheckedAt: "2026-10-10T00:00:01Z", CheckedHash: strings.Repeat("a", 64)}
			baseline := g
			s := claudeSnapshot{Live: true, Connection: in.Pending.ConnectionID, InstructionObserver: &claudeInstructionObserver{Version: 1, CurrentQueryID: query, CurrentConfigurationID: config},
				Entries: []claudeEntry{{Kind: "input", Operation: in.Pending.NativeTurn, QueryID: query, PermissionBaseline: &baseline}}}
			r := claudeRequest{ID: "original-request", Operation: in.Pending.NativeTurn, Connection: in.Pending.ConnectionID, Tool: "Read", Status: "pending", PermissionGuard: &g}
			switch mode {
			case "changed":
				g.State = "changed"
			case "unavailable":
				g.State = "unavailable"
				g.CheckedHash = ""
			case "missing":
				r.PermissionGuard = nil
			case "wrong-query":
				g.QueryID = uuid.NewString()
			case "wrong-config":
				g.ConfigurationID = strings.Repeat("c", 64)
			case "no-input":
				s.Entries = nil
			case "wrong-input":
				r.Operation = uuid.NewString()
			case "bad-hash":
				g.BaselineHash = "bad"
			case "bad-time":
				g.CheckedAt = "bad"
			case "false-complete":
				g.Complete = true
			case "unknown-version":
				g.Version = 2
			}
			s.Pending = []claudeRequest{r}
			out := projectClaude(in.Scope, s).Pending[0].PermissionGuard
			want := "unavailable"
			if mode == "observed" || mode == "changed" {
				want = mode
			}
			if out == nil || out.State != want || out.Complete || out.Detail == "" {
				t.Fatalf("invalid approval projection: %+v", out)
			}
		})
	}
}

func TestClaudeApprovalRequiresBridgeObservationCapability(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python stdio fixture required")
	}
	for _, version := range []int{0, 1, 2} {
		t.Run(map[int]string{0: "legacy", 1: "supported", 2: "unknown"}[version], func(t *testing.T) {
			root := t.TempDir()
			in := ownedClaudeDenial()
			in.OptionID = "allow"
			in.Scope.Cwd = root
			in.Scope.Instance.Profile = root
			in.Scope.Instance.NodeExecutable = python
			in.Scope.Instance.Bridge = filepath.Join(root, "bridge.py")
			in.Scope.Instance.Executable = "/must-not-start-claude"
			worker := `import json,sys
from pathlib import Path
log=Path(sys.argv[1])/'calls.jsonl'
for line in sys.stdin:
 q=json.loads(line)
 with log.open('a') as out:out.write(json.dumps(q)+'\n')
 result={'version':1,'sdk':'0.3.280','pending_permissions':CAPABILITY} if q['method']=='hello' else {'native_id':q['params']['native_id']}
 print(json.dumps({'v':1,'id':q['id'],'result':result}),flush=True)
`
			encoded, _ := json.Marshal(version)
			worker = strings.ReplaceAll(worker, "CAPABILITY", string(encoded))
			if err := os.WriteFile(in.Scope.Instance.Bridge, []byte(worker), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			dir := filepath.Join(root, "transport")
			peer, err := connectClaude(ctx, in.Scope.Instance, dir)
			if err != nil {
				t.Fatal(err)
			}
			engine := NewClaude(filepath.Join(root, "application"))
			defer engine.Close()
			engine.clients[in.Scope.Instance.ID] = peer
			_, err = engine.Execute(ctx, in)
			if version == 1 && err != nil {
				t.Fatal("supported peer rejected", err)
			}
			if version != 1 {
				var unsent *NotSent
				if !errors.As(err, &unsent) || !strings.Contains(err.Error(), "未声明原审批权限观察") {
					t.Fatal("unsupported bridge was not refused", err)
				}
			}
			body, err := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(body)), "\n")
			want := 1
			if version == 1 {
				want = 2
			}
			if len(calls) != want {
				t.Fatal("unsupported peer received an approval", string(body))
			}
		})
	}
}
