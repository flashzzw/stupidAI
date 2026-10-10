package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Reproduce the live state captured by the Windows/WSL host probe: the SDK has
// submitted the original response, while its long-running tool is still live.
func TestClaudeSubmittedResponseRemainsInFlightUntilOriginalTurnCompletes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python transport fixture required")
	}
	for _, mode := range []string{"submitted", "request-still-pending", "other-request", "other-connection", "other-input", "other-tool", "disconnected", "missing-original-input", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			native, operation, connection, request := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			scope := Scope{NativeID: native, Cwd: root, Route: Route{Model: "fixture", Endpoint: "http://127.0.0.1:1"},
				Instance: Instance{ID: "owned", Kind: "claude", Profile: root, NodeExecutable: python, Bridge: filepath.Join(root, "bridge.py")}}
			user := claudeEvent{Type: "user", UUID: operation, SessionID: native}
			user.Message.Role, user.Message.Content = "user", json.RawMessage(`"original owned tool"`)
			snapshot := claudeSnapshot{Live: true, Active: operation, Connection: connection,
				Entries: []claudeEntry{{Kind: "initialized"}, {Kind: "input", Operation: operation, Text: "original owned tool"}, {Kind: "event", Operation: operation, Event: user}},
				Pending: []claudeRequest{{ID: request, Connection: connection, Operation: operation, Tool: "Bash", Input: json.RawMessage(`{"command":"owned fixture"}`), Status: "submitted"}}}
			snapshot.Config.ID, snapshot.Config.Cwd, snapshot.Config.Model, snapshot.Config.Endpoint = native, root, scope.Route.Model, scope.Route.Endpoint
			rawRequest, _ := json.Marshal(request)
			intent := Intent{ID: uuid.NewString(), Kind: "respond", Scope: scope, OptionID: "allow",
				Pending: &Pending{ID: "owned/" + connection + "/" + request, NativeID: native, NativeTurn: operation, ConnectionID: connection, RequestID: rawRequest, Kind: "Bash", Status: "pending"}}
			want := "unknown"
			switch mode {
			case "submitted":
				want = "running"
			case "request-still-pending":
				snapshot.Pending[0].Status = "pending"
			case "other-request":
				snapshot.Pending[0].ID = uuid.NewString()
			case "other-connection":
				snapshot.Pending[0].Connection = uuid.NewString()
			case "other-input":
				snapshot.Pending[0].Operation = uuid.NewString()
			case "other-tool":
				snapshot.Pending[0].Tool = "Read"
			case "disconnected":
				snapshot.Live = false
			case "missing-original-input":
				snapshot.Entries = snapshot.Entries[:1]
			case "terminal":
				zero := 0
				snapshot.Entries = append(snapshot.Entries, claudeEntry{Kind: "event", Operation: operation, Event: claudeEvent{Type: "result", SessionID: native, Subtype: "success", Queued: &zero}})
				snapshot.Active, snapshot.Pending[0].Status = "", "expired"
				want = "finished"
			}
			body, err := json.Marshal(snapshot)
			if err != nil || os.WriteFile(filepath.Join(root, "snapshot.json"), body, 0600) != nil {
				t.Fatal("snapshot fixture could not be saved", err)
			}
			program := `import json,sys
from pathlib import Path
for line in sys.stdin:
 q=json.loads(line)
 result={'version':1,'sdk':'0.3.280'} if q['method']=='hello' else json.loads(Path(__file__).with_name('snapshot.json').read_text())
 print(json.dumps({'v':1,'id':q['id'],'result':result}),flush=True)
`
			if err := os.WriteFile(scope.Instance.Bridge, []byte(program), 0600); err != nil {
				t.Fatal(err)
			}
			engine := NewClaude(filepath.Join(root, "runtime"))
			defer engine.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := engine.Reconcile(ctx, intent, Receipt{NativeID: native, NativeTurn: operation})
			if err != nil || out.State != want {
				t.Fatalf("response lifecycle = %q, want %q; err=%v", out.State, want, err)
			}
			if want == "running" && (out.Status != "" || out.View.ExecutionClear || !out.Evidence.Complete || out.Receipt.NativeTurn != operation) {
				t.Fatal("pending response became completion or lost its original identity", out)
			}
			if want == "finished" && out.Status != "succeeded" {
				t.Fatal("original terminal result did not complete response", out)
			}
		})
	}
}
