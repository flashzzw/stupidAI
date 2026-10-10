//go:build linux && nativeclaude

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

func TestClaudeNativeApplicationPluginSelection(t *testing.T) {
	claudeNativeApplicationPlugins(t, false)
}
func TestClaudeNativeApplicationHookExecutions(t *testing.T) { claudeNativeApplicationPlugins(t, true) }

func claudeNativeApplicationPlugins(t *testing.T, hooks bool, changes ...bool) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0208-plugin-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for key, suffix := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state"} {
		dir := filepath.Join(root, suffix)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"} {
		t.Setenv(key, "1")
	}
	c := claudePluginFixture(t)
	c.StateDir = filepath.Join(root, "application")
	c.Instances = nil
	c.Routes = nil
	source := c.CapabilityAssets[0].Source
	if hooks {
		write(filepath.Join(source, "hooks/hooks.json"), `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"printf PRIVATE_NATIVE_HOOK; exit ${NETCAP_HOOK_CODE:-0}"}]}]}}`)
	}
	cwd := filepath.Join(root, "work")
	write(filepath.Join(cwd, ".claude/skills/inherited/SKILL.md"), "---\nname: inherited-method\ndescription: keep inherited\n---\n")
	c.Projects = []Project{{ID: "project", Name: "Plugin native test", Cwd: cwd}}
	for _, id := range []string{"a", "b"} {
		profile := filepath.Join(root, "profile-"+id)
		if err := os.MkdirAll(filepath.Join(profile, "skills"), 0700); err != nil {
			t.Fatal(err)
		}
		if hooks {
			code := "0"
			if id == "b" {
				code = "2"
			}
			write(filepath.Join(profile, "settings.json"), `{"env":{"NETCAP_HOOK_CODE":"`+code+`"}}`)
		}
		key := "NETCAP_PLUGIN_NATIVE_" + strings.ToUpper(id)
		t.Setenv(key, "fixture-"+id)
		c.Instances = append(c.Instances, runtime.Instance{ID: id, Name: "Claude " + strings.ToUpper(id), Kind: "claude", Executable: cli, Profile: profile, Bridge: filepath.Join(repo, "bridges/claude/dist/main.js"), EnvAllow: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING"}})
		c.Routes = append(c.Routes, runtime.Route{ID: id, InstanceID: id, Model: "claude-sonnet-4-6", Endpoint: "http://127.0.0.1:1", CredentialRef: "env:" + key})
	}
	r := runtime.NewClaude(c.StateDir)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(c, store, r)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	results := map[string]any{}
	t.Cleanup(func() {
		_ = a.Close()
		data, e := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "results": results, "passed": !t.Failed(), "scope": "production Application/SQLite/Claude Runtime/Session/SDK; no HTTP listener or model input"}, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(root, "result.json"), data, 0600)
		}
		if e != nil {
			t.Error(e)
		}
	})
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	sessions := map[string]string{}
	for index, id := range []string{"a", "b"} {
		mode := []string{"enabled", "disabled"}[index]
		if hooks {
			mode = "enabled"
		}
		receipt, err := a.Apply(Command{ID: "create-" + id, Kind: "create", Title: "Plugin " + strings.ToUpper(id), ProjectID: "project", RouteID: id, Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "plugin", Mode: mode}}})
		if err != nil {
			t.Fatal(err)
		}
		sessions[id] = receipt.SessionID
		deadline := time.Now().Add(20 * time.Second)
		for {
			s := a.Snapshot()
			found := false
			for _, session := range s.Sessions {
				if session.ID == receipt.SessionID && len(session.View.PluginEvidence) == 1 && session.View.PluginEvidence[0].Verified {
					found = !hooks || len(session.View.HookExecutions) == 1 && (session.View.HookExecutions[0].Status == "success" || session.View.HookExecutions[0].Status == "error")
				}
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				data, _ := json.Marshal(s)
				t.Fatalf("native selection did not become verified: %s", data)
			}
			time.Sleep(20 * time.Millisecond)
		}
		read, err := a.ReadExecution(receipt.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		results[id] = read
		if hooks {
			want, code := "success", 0
			if id == "b" {
				want, code = "error", 2
			}
			check(id+"_hook_outcome", len(read.HookExecutions) == 1 && read.HookExecutions[0].Status == want && read.HookExecutions[0].ExitCode != nil && *read.HookExecutions[0].ExitCode == code)
			check(id+"_source_not_invented", read.HookExecutions[0].Source == "unreported" && read.HookExecutions[0].Operation == "")
			data, _ := json.Marshal(read)
			check(id+"_no_hook_output_in_view", !strings.Contains(string(data), "PRIVATE_NATIVE_HOOK"))
		}
		check(id+"_native_inventory_matches", len(read.PluginEvidence) == 1 && read.PluginEvidence[0].Verified && read.PluginEvidence[0].Mode == mode)
		check(id+"_original_shared_source", len(read.Snapshot.CapabilityRefs) == 1 && read.Snapshot.PluginSelections[0].SourcePath == source)
	}
	if len(changes) > 0 && changes[0] {
		nativePluginChanges(t, a, c, sessions, check, results)
		return
	}
	before := a.Snapshot()
	results["snapshot"] = before
	check("independent_namespaces", before.Sessions[0].Scope.StateNamespace != before.Sessions[1].Scope.StateNamespace)
	check("zero_model_messages", len(before.Sessions[0].View.Messages) == 0 && len(before.Sessions[1].View.Messages) == 0)
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": before}
	for _, id := range sessions {
		read, e := a.ReadSession(id, "", 100)
		if e != nil {
			t.Fatal(e)
		}
		responses["/api/sessions/"+id] = read
	}
	data, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "browser-responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err = Open(c, store, runtime.NewClaude(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range sessions {
		if hooks {
			waitTest(t, func() bool { read, e := a.ReadExecution(id); return e == nil && len(read.HookExecutions) == 1 })
		}
		read, e := a.ReadExecution(id)
		if e != nil {
			t.Fatal(e)
		}
		if hooks {
			var previous []runtime.HookExecution
			for _, old := range before.Sessions {
				if old.ID == id {
					previous = old.View.HookExecutions
				}
			}
			check(id+"_restart_keeps_hook_history", digest(read.HookExecutions) == digest(previous))
		}
		check(id+"_restart_keeps_binding", len(read.Snapshot.PluginSelections) == 1 && runtime.EqualPluginSelections(read.Snapshot.PluginSelections, before.Sessions[0].Scope.PluginSelections) == (read.Snapshot.PluginSelections[0].Mode == before.Sessions[0].Scope.PluginSelections[0].Mode))
	}
	write(filepath.Join(source, "commands/action.md"), "changed version")
	_, err = a.Apply(Command{ID: "drift-must-not-send", Kind: "send", SessionID: sessions["a"], Text: "MUST NOT REACH MODEL"})
	check("source_drift_blocks_input", err != nil && strings.Contains(err.Error(), "执行快照来源待核对"))
	results["after_drift"] = a.Snapshot()
}
