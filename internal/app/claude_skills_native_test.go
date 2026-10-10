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

func TestClaudeNativeApplicationSkillSelection(t *testing.T) {
	claudeNativeApplicationSkills(t, "natural")
}

func TestClaudeNativeApplicationSkillFileAlias(t *testing.T) {
	claudeNativeApplicationSkills(t, "alias")
}
func TestClaudeNativeApplicationSkillDirectory(t *testing.T) {
	claudeNativeApplicationSkills(t, "directory")
}
func TestClaudeNativeApplicationSkillFile(t *testing.T) { claudeNativeApplicationSkills(t, "file") }

func TestClaudeNativeApplicationSkillChanges(t *testing.T) {
	claudeNativeApplicationSkills(t, "changes")
}

func TestClaudeNativeApplicationSkillCatalog(t *testing.T) {
	claudeNativeApplicationSkills(t, "catalog")
}

func TestClaudeNativeApplicationHookCatalog(t *testing.T) {
	claudeNativeApplicationSkills(t, "hooks")
}

func claudeNativeApplicationSkills(t *testing.T, delivery string) {
	cli := os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if cli == "" {
		t.Skip("opt-in real native CLI")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "netcap-w0207-claude-app-")
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
	source := filepath.Join(root, "shared")
	write(filepath.Join(source, "SKILL.md"), "---\nname: display-method\ndescription: fixture method\n---\nPRIVATE_METHOD_BODY\n")
	cwd := filepath.Join(root, "work")
	write(filepath.Join(cwd, ".claude/skills/inherited/SKILL.md"), "---\nname: inherited-method\ndescription: keep inherited\n---\n")
	c := Config{Version: 1, StateDir: filepath.Join(root, "application"), Projects: []Project{{ID: "project", Name: "Skill native test", Cwd: cwd}}, CapabilityAssets: []CapabilityAsset{{ID: "shared", Name: "Shared Skill", Kind: "skill", Version: "v1", Source: source, Backends: []string{"claude"}}}}
	if delivery == "file" {
		c.CapabilityAssets[0].Source = filepath.Join(source, "SKILL.md")
	}
	for _, id := range []string{"a", "b"} {
		profile := filepath.Join(root, "profile-"+id)
		if err := os.MkdirAll(filepath.Join(profile, "skills"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := func() error {
			if delivery == "natural" || delivery == "alias" {
				return os.Symlink(source, filepath.Join(profile, "skills/native-key"))
			}
			return nil
		}(); err != nil {
			t.Fatal(err)
		}
		if delivery == "catalog" {
			write(filepath.Join(profile, "skills/user-method/SKILL.md"), "---\nname: user-method\ndescription: unselected user method\n---\nPRIVATE_USER_METHOD\n")
		}
		if delivery == "hooks" {
			installNativeHookCatalogFixture(t, root, profile, cwd, cli, id == "b")
		}
		key := "NETCAP_SKILL_NATIVE_" + strings.ToUpper(id)
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
		receipt, err := a.Apply(Command{ID: "create-" + id, Kind: "create", Title: "Skill " + strings.ToUpper(id), ProjectID: "project", RouteID: id, Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: mode}}})
		if err != nil {
			t.Fatal(err)
		}
		sessions[id] = receipt.SessionID
		deadline := time.Now().Add(20 * time.Second)
		for {
			s := a.Snapshot()
			found := false
			for _, session := range s.Sessions {
				if session.ID == receipt.SessionID && len(session.View.SkillEvidence) == 1 && session.View.SkillEvidence[0].Verified {
					found = true
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
		check(id+"_native_inventory_matches", len(read.SkillEvidence) == 1 && read.SkillEvidence[0].Verified && read.SkillEvidence[0].DescriptionListed == (mode == "enabled"))
		check(id+"_original_shared_source", len(read.Snapshot.CapabilityRefs) == 1 && read.Snapshot.SkillSelections[0].SourcePath == filepath.Join(source, "SKILL.md"))
	}
	if delivery == "alias" {
		nativeSkillFileAlias(t, a, c, sessions, check, results)
		return
	}
	if delivery == "catalog" {
		nativeSkillCatalog(t, a, c, sessions, check, results)
		return
	}
	if delivery == "hooks" {
		nativeHookCatalog(t, a, c, sessions, check, results)
		return
	}
	if delivery == "changes" {
		nativeSkillChanges(t, a, c, sessions, check, results)
		return
	}
	before := a.Snapshot()
	if delivery != "natural" {
		picks := []runtime.SkillSelection{before.Sessions[0].Scope.SkillSelections[0], before.Sessions[1].Scope.SkillSelections[0]}
		check("independent_resource_packages", picks[0].PluginName != picks[1].PluginName && picks[0].ResourceRoot == picks[1].ResourceRoot)
		for _, pick := range picks {
			_, err := os.Stat(pick.DiscoveryPath)
			check(pick.Mode+"_package_presence", (err == nil) == (pick.Mode == "enabled"))
		}
	}
	results["snapshot"] = before
	check("independent_namespaces", before.Sessions[0].Scope.StateNamespace != before.Sessions[1].Scope.StateNamespace)
	check("zero_model_messages", len(before.Sessions[0].View.Messages) == 0 && len(before.Sessions[1].View.Messages) == 0)
	for _, id := range sessions {
		if _, err := a.Apply(Command{ID: "pause-" + id, Kind: "takeover", SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Export caller-facing data for browser replay without adding a listener.
	responses := map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot()}
	for _, id := range sessions {
		read, err := a.ReadSession(id, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		responses["/api/sessions/"+id] = read
	}
	data, _ := json.MarshalIndent(map[string]any{"responses": responses, "sessions": sessions}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "browser-responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if delivery == "natural" {
		write(filepath.Join(cwd, ".claude/skills/native-key/SKILL.md"), "---\nname: conflict\ndescription: unrelated same-key source\n---\n")
	} else {
		if err := os.Symlink(source, filepath.Join(cwd, ".claude/skills/added")); err != nil {
			t.Fatal(err)
		}
	}
	sid := sessions["b"]
	receipt, err := a.Apply(Command{ID: "must-not-send", Kind: "send", SessionID: sid, Text: "MUST NOT REACH MODEL"})
	if err == nil {
		deadline := time.Now().Add(10 * time.Second)
		for {
			done := false
			for _, op := range a.Snapshot().Operations {
				if op.ID == receipt.OperationID && op.Status == "not_sent" {
					done = true
				}
			}
			if done {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("conflict did not reject native input")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	check("new_source_conflict_blocks_input", err != nil || receipt.OperationID != "")
	results["after_conflict"] = a.Snapshot()
	if delivery != "natural" {
		write(filepath.Join(source, "SKILL.md"), "---\nname: display-method\ndescription: changed after binding\n---\n")
		_, err := a.Apply(Command{ID: "drift-must-not-send", Kind: "send", SessionID: sessions["a"], Text: "MUST NOT REACH MODEL"})
		check("resource_version_drift_blocks_input", err != nil && strings.Contains(err.Error(), "执行快照来源待核对"))
	}
	for _, profile := range []string{"a", "b"} {
		paths, _ := filepath.Glob(filepath.Join(c.StateDir, "runtimes", profile, "*.jsonl"))
		for _, path := range paths {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			check("journal_"+profile+"_no_input", !strings.Contains(string(body), `"kind":"input"`))
		}
	}
}
