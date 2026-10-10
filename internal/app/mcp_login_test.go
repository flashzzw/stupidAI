package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPLoginPreparationIsBoundPrivateDurableAndDoesNotExecute(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// The copied command must preserve paths literally, including shell syntax.
	c.Instances[0].Executable = filepath.Join(t.TempDir(), "native cli '$(not-a-command)")
	if err := os.WriteFile(c.Instances[0].Executable, []byte("#!/usr/bin/python3\nimport json,os,sys\nprint(json.dumps({'cwd':os.getcwd(),'profile':os.environ['CLAUDE_CONFIG_DIR'],'args':sys.argv[1:]}))\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.Instances[0].SettingsFile = filepath.Join(t.TempDir(), "settings '$(not-a-command).json")
	if err := os.WriteFile(c.Instances[0].SettingsFile, []byte(`{"env":{"FIXTURE_ACCOUNT":"A"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "mcp.json")
	definition := `{"mcpServers":{"identity":{"type":"http","url":"https://example.invalid/mcp","headers":{"X-Fixture":"${FIXTURE_ACCOUNT}"}},"other":{"type":"http","url":"https://other.invalid/mcp"}}}`
	if err := os.WriteFile(source, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	c.CapabilityAssets = []CapabilityAsset{{ID: "shared", Name: "Shared", Kind: "mcp", Version: "v1", Source: source, Backends: []string{"claude"}}}
	f := newFixture()
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "shared", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	command := Command{ID: "login", Kind: "prepare_mcp_login", SessionID: created.SessionID, MCPLogin: &MCPLoginRequest{SnapshotID: before.Snapshot.ID, Server: "identity"}}
	receipt, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	plan := *receipt.MCPLogin
	if f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("preparation executed a native request")
	}
	after, _ := a.ReadExecution(created.SessionID)
	if digest(before.Snapshot) != digest(after.Snapshot) || len(after.MCPLogins) != 1 || plan.Profile != c.Instances[0].Profile {
		t.Fatal("preparation changed binding or lost receipt")
	}
	data, err := os.ReadFile(filepath.Join(plan.Directory, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var copied struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &copied); err != nil || len(copied.Servers) != 1 || copied.Servers["identity"] == nil {
		t.Fatal("unselected server copied")
	}
	for _, path := range []string{plan.Directory, filepath.Join(plan.Directory, ".mcp.json"), filepath.Join(plan.Directory, ".claude", "settings.local.json")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("login preparation is not private")
		}
	}
	output, err := exec.Command("bash", "-c", plan.Command).Output()
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Cwd, Profile string
		Args         []string
	}
	if err := json.Unmarshal(output, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Cwd != plan.Directory || actual.Profile != plan.Profile || digest(actual.Args) != digest([]string{"--settings", c.Instances[0].SettingsFile, "mcp", "login", "identity", "--no-browser"}) {
		t.Fatalf("command changed arguments: %+v", actual)
	}
	// Caller mutation cannot corrupt the durable preparation receipt.
	receipt.MCPLogin.Command = "modified by caller"
	retried, err := a.Apply(command)
	if err != nil || retried.MCPLogin.Command != plan.Command {
		t.Fatal("receipt aliases application state")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	restored, _ := a.ReadExecution(created.SessionID)
	if len(restored.MCPLogins) != 1 || restored.MCPLogins[0].Command != plan.Command {
		t.Fatal("prepared command lost on restart")
	}
	for i, request := range []MCPLoginRequest{{SnapshotID: "old", Server: "identity"}, {SnapshotID: before.Snapshot.ID, Server: "missing"}} {
		if _, err := a.Apply(Command{ID: []string{"old", "missing"}[i], Kind: "prepare_mcp_login", SessionID: created.SessionID, MCPLogin: &request}); err == nil {
			t.Fatal("unbound authentication target accepted")
		}
	}
	if err := os.WriteFile(source, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	command.ID = "drifted"
	if _, err := a.Apply(command); err == nil {
		t.Fatal("changed source admitted for authentication")
	}
}

func TestMCPLoginCandidateBoundariesAndPersistenceFailure(t *testing.T) {
	c := fixtureConfig(t)
	c.Instances[0].Kind = "claude"
	c.Instances[0].Bridge = filepath.Join(c.Instances[0].Profile, "bridge.js")
	if err := os.WriteFile(c.Instances[0].Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for id, definition := range map[string]string{
		"local":  `{"mcpServers":{"local":{"command":"python3"}}}`,
		"remote": `{"mcpServers":{"identity":{"type":"sse","url":"https://example.invalid/sse"}}}`,
		"helper": `{"mcpServers":{"helper":{"type":"http","url":"https://example.invalid/mcp","headersHelper":"./authenticate.sh"}}}`,
	} {
		source := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(source, []byte(definition), 0600); err != nil {
			t.Fatal(err)
		}
		c.CapabilityAssets = append(c.CapabilityAssets, CapabilityAsset{ID: id, Name: id, Kind: "mcp", Version: "v1", Source: source, Backends: []string{"claude"}})
	}
	f := newFixture()
	a := openFixture(t, c, f)
	created, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilitySelections: []CapabilitySelection{{AssetID: "local", Mode: "enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, _ := a.ReadExecution(created.SessionID)
	change := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "remote", Mode: "enabled"}, {AssetID: "helper", Mode: "enabled"}}}
	preview, err := a.PreviewExecutionChange(created.SessionID, change)
	if err != nil {
		t.Fatal(err)
	}
	change.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stage", Kind: "stage_execution", SessionID: created.SessionID, ExecutionChange: &change}); err != nil {
		t.Fatal(err)
	}
	staged, _ := a.ReadExecution(created.SessionID)
	command := Command{ID: "candidate", Kind: "prepare_mcp_login", SessionID: created.SessionID, MCPLogin: &MCPLoginRequest{SnapshotID: staged.PendingChange.Snapshot.ID, Server: "identity"}}
	for _, invalid := range []Command{
		{ID: "stdio", Kind: command.Kind, SessionID: created.SessionID, MCPLogin: &MCPLoginRequest{SnapshotID: before.Snapshot.ID, Server: "local"}},
		{ID: "wrong-snapshot", Kind: command.Kind, SessionID: created.SessionID, MCPLogin: &MCPLoginRequest{SnapshotID: before.Snapshot.ID, Server: "identity"}},
		{ID: "helper-cwd", Kind: command.Kind, SessionID: created.SessionID, MCPLogin: &MCPLoginRequest{SnapshotID: staged.PendingChange.Snapshot.ID, Server: "helper"}},
		{ID: "extra-input", Kind: command.Kind, SessionID: created.SessionID, MCPLogin: command.MCPLogin, Text: "not a login field"},
		{ID: "wrong-command", Kind: "send", SessionID: created.SessionID, MCPLogin: command.MCPLogin, Text: "not an input command"},
	} {
		_, err := a.Apply(invalid)
		if err == nil {
			t.Fatalf("invalid authentication request accepted: %s", invalid.ID)
		}
		if invalid.ID == "helper-cwd" && (!strings.Contains(err.Error(), "工作目录") || !strings.Contains(err.Error(), "凭据环境")) {
			t.Fatalf("helper preparation must explain both native binding changes: %v", err)
		}
	}
	receipt, err := a.Apply(command)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := a.ReadExecution(created.SessionID)
	if receipt.MCPLogin.SnapshotID != staged.PendingChange.Snapshot.ID || digest(after.PendingChange) != digest(staged.PendingChange) || digest(after.Snapshot) != digest(before.Snapshot) || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("candidate authentication changed native execution or snapshot bindings")
	}
	store := &failingStore{Persistence: a.store}
	store.fail.Store(true)
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
	command.ID = "disk-failure"
	if _, err := a.Apply(command); err == nil {
		t.Fatal("failed persistence accepted")
	}
	paths, err := filepath.Glob(filepath.Join(c.StateDir, "mcp-login", "login-*"))
	if err != nil || len(paths) != 1 || paths[0] != receipt.MCPLogin.Directory {
		t.Fatalf("failure must remove only the new preparation: %v %v", paths, err)
	}
	after, _ = a.ReadExecution(created.SessionID)
	if len(after.MCPLogins) != 1 || after.MCPLogins[0].Directory != receipt.MCPLogin.Directory {
		t.Fatal("failed persistence published a preparation receipt")
	}
}
