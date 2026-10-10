package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

type historyRuntime struct {
	*fixtureRuntime
	read  func(runtime.DiscoveryRequest, string, int) (runtime.NativeHistorySnapshot, error)
	calls int
}

func (f *historyRuntime) ReadNativeHistory(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeHistorySnapshot, error) {
	f.calls++
	return f.read(r, id, f.calls)
}

func historySample(r runtime.DiscoveryRequest, id string, count int) runtime.NativeHistorySnapshot {
	s := runtime.NativeHistorySnapshot{Session: runtime.NativeSession{NativeID: id, Cwd: r.Cwd, Title: "history"}, Entries: []runtime.NativeHistoryEntry{}}
	for n := 0; n < count; n++ {
		data := json.RawMessage(fmt.Sprintf(`{"type":"tool_result","content":"result %d","unknown":{"enabled":true},"counter":9007199254740993}`, n))
		s.Entries = append(s.Entries, runtime.NativeHistoryEntry{ID: fmt.Sprint(n), Kind: "user", Data: data})
	}
	return s
}

func TestNativeHistoryPaginationIsBoundAndReadOnly(t *testing.T) {
	c := fixtureConfig(t)
	c.Projects = append(c.Projects, Project{ID: "alias", Cwd: c.Projects[0].Cwd})
	f := &historyRuntime{fixtureRuntime: newFixture(), read: func(r runtime.DiscoveryRequest, id string, call int) (runtime.NativeHistorySnapshot, error) {
		if r.Cwd != c.Projects[0].Cwd || r.Instance.ID != "instance" || id != discoveredID {
			t.Fatal("wrong history scope")
		}
		return historySample(r, id, 53), nil
	}}
	a := openFixture(t, c, f)
	before := a.Sequence()
	request := NativeHistoryRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
	first, err := a.ReadNativeHistory(context.Background(), request)
	if err != nil || len(first.Entries) != 50 || first.Total != 53 || first.Offset != 0 || first.Next == "" || !first.ReadOnly || len(first.Baseline) != 64 {
		t.Fatalf("first: %+v %v", first, err)
	}
	if !strings.Contains(first.Entries[0].RawJSON, `"counter":9007199254740993`) || !strings.Contains(string(first.Entries[0].Data), `"unknown":{"enabled":true}`) {
		t.Fatal("native fields lost or number rounded")
	}
	request.Cursor = first.Next
	last, err := a.ReadNativeHistory(context.Background(), request)
	if err != nil || len(last.Entries) != 3 || last.Offset != 50 || last.Next != "" || last.Baseline != first.Baseline || last.Entries[0].ID != "50" {
		t.Fatalf("last: %+v %v", last, err)
	}
	for _, change := range []string{"project", "session", "malformed"} {
		changed := request
		if change == "project" {
			changed.ProjectID = "alias"
		}
		if change == "session" {
			changed.NativeID = "018f0000-0000-7000-8000-000000000002"
		}
		if change == "malformed" {
			changed.Cursor = "bad-cursor"
		}
		if _, err := a.ReadNativeHistory(context.Background(), changed); err == nil {
			t.Fatal("cross binding cursor accepted:", change)
		}
	}
	if f.calls != 4 || f.creates.Load() != 0 || f.sends.Load() != 0 || a.Sequence() != before || len(a.Workspace().Sessions) != 0 {
		t.Fatal("history read executed input or changed state")
	}
}

func TestNativeHistoryRejectsChangedOrIncompleteContent(t *testing.T) {
	for _, scenario := range []string{"drift-during-read", "drift-between-pages", "configuration-drift", "different-id", "different-cwd", "nil-entries", "duplicate", "malformed-object", "entry-limit", "byte-limit", "total-byte-limit", "read-error", "reordered-json"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &historyRuntime{fixtureRuntime: newFixture()}
			f.read = func(r runtime.DiscoveryRequest, id string, call int) (runtime.NativeHistorySnapshot, error) {
				s := historySample(r, id, 53)
				switch scenario {
				case "drift-during-read":
					if call == 2 {
						s.Entries[0].Data = json.RawMessage(`{"content":"tool output changed"}`)
					}
				case "drift-between-pages":
					if call > 2 {
						s.Entries[0].Data = json.RawMessage(`{"content":"same count, changed output"}`)
					}
				case "configuration-drift":
					if call == 2 {
						if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("model='changed'\n"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				case "different-id":
					s.Session.NativeID = "other"
				case "different-cwd":
					s.Session.Cwd = t.TempDir()
				case "nil-entries":
					s.Entries = nil
				case "duplicate":
					s.Entries[1] = s.Entries[0]
				case "malformed-object":
					s.Entries[0].Data = json.RawMessage(`null`)
				case "entry-limit":
					s = historySample(r, id, runtime.MaxHistoryEntries+1)
				case "byte-limit":
					data, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", runtime.MaxHistoryEntryBytes)})
					s.Entries[0].Data = data
				case "total-byte-limit":
					data, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", 256<<10)})
					for i := range s.Entries {
						s.Entries[i].Data = data
					}
				case "read-error":
					if call == 2 {
						return runtime.NativeHistorySnapshot{}, fmt.Errorf("native read failed")
					}
				case "reordered-json":
					if call == 2 {
						s.Entries[0].Data = json.RawMessage(`{"counter":9007199254740993,"unknown":{"enabled":true},"content":"result 0","type":"tool_result"}`)
					}
				}
				return s, nil
			}
			a := openFixture(t, c, f)
			request := NativeHistoryRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
			out, err := a.ReadNativeHistory(context.Background(), request)
			if scenario == "drift-between-pages" && err == nil {
				request.Cursor = out.Next
				out, err = a.ReadNativeHistory(context.Background(), request)
			}
			if scenario == "reordered-json" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || len(out.Entries) != 0 {
				t.Fatalf("unproven history returned: %+v %v", out, err)
			}
		})
	}
}

func TestNativeHistoryClaudeCoverageAndExpiredContext(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f := &historyRuntime{fixtureRuntime: newFixture(), read: func(r runtime.DiscoveryRequest, id string, _ int) (runtime.NativeHistorySnapshot, error) {
		return historySample(r, id, 1), nil
	}}
	a := openFixture(t, c, f)
	request := NativeHistoryRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
	out, err := a.ReadNativeHistory(context.Background(), request)
	if err != nil || out.Coverage != "claude_main_transcript_entries" || !strings.Contains(out.Detail, "旧分支") {
		t.Fatalf("claude coverage: %+v %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err = a.ReadNativeHistory(ctx, request); err == nil || len(out.Entries) != 0 {
		t.Fatal("expired request returned usable history")
	}
}

func TestNativeHistoryEmptyAndUnavailableRemainExplicit(t *testing.T) {
	c := fixtureConfig(t)
	f := &historyRuntime{fixtureRuntime: newFixture(), read: func(r runtime.DiscoveryRequest, id string, _ int) (runtime.NativeHistorySnapshot, error) {
		return historySample(r, id, 0), nil
	}}
	a := openFixture(t, c, f)
	request := NativeHistoryRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
	out, err := a.ReadNativeHistory(context.Background(), request)
	if err != nil || out.Total != 0 || out.Entries == nil || out.Next != "" || !strings.Contains(out.Detail, "不能据此证明") {
		t.Fatalf("empty: %+v %v", out, err)
	}
	request.NativeID = "../../private"
	if _, err = a.ReadNativeHistory(context.Background(), request); err == nil || f.calls != 2 {
		t.Fatal("invalid id reached runtime")
	}
	b := openFixture(t, fixtureConfig(t), newFixture())
	request.NativeID = discoveredID
	if _, err = b.ReadNativeHistory(context.Background(), request); err == nil || !strings.Contains(err.Error(), "历史读取接口") {
		t.Fatalf("missing adapter: %v", err)
	}
}
