package app

import (
	"codex-netcap/internal/runtime"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexProjectHookSnapshotPersistsAndRejectsDrift(t *testing.T) {
	c, _ := executionFixture(t)
	path := filepath.Join(c.Projects[0].Cwd, ".codex/hooks.json")
	const original = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"PRIVATE_HOOK_V1"}]}]}}`
	writePluginMetadata(t, path, original)
	f := newProjectHookFixture()
	a := openFixture(t, c, f)
	id := createExecution(t, a).SessionID
	before, err := a.ReadExecution(id)
	if err != nil || before.Snapshot.CodexProjectHooks == nil || before.Snapshot.CodexProjectHooks.Files[0].File.Path != path {
		t.Fatal("missing project hook version", err)
	}
	if digest(a.Snapshot().Sessions[0].Scope.CodexProjectHooks) != digest(before.Snapshot.CodexProjectHooks) {
		t.Fatal("native binding omitted hook version")
	}
	encoded, _ := json.Marshal(before)
	if strings.Contains(string(encoded), "PRIVATE_HOOK_V1") {
		t.Fatal("hook body leaked")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, path, strings.Replace(original, "V1", "V2", 1))
	a = openFixture(t, c, f)
	after, err := a.ReadExecution(id)
	if err != nil || after.Status != "drifted" || digest(before.Snapshot) != digest(after.Snapshot) {
		t.Fatal("restart lost source version", err)
	}
	if _, err := a.Apply(Command{ID: "reject", Kind: "send", SessionID: id, Text: "MUST NOT SEND"}); err == nil {
		t.Fatal("drift accepted")
	}
	if f.sends.Load() != 0 {
		t.Fatal("source check dispatched input")
	}
	writePluginMetadata(t, path, original)
	restored, err := a.ReadExecution(id)
	if err != nil || restored.Status != "unchanged" {
		t.Fatal("original file not recognized", err)
	}
}

func TestCodexProjectHookSnapshotAbsenceAndLegacyBinding(t *testing.T) {
	for _, scenario := range []string{"new", "legacy", "scope-tamper"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := executionFixture(t)
			a := openFixture(t, c, newProjectHookFixture())
			id := createExecution(t, a).SessionID
			before, _ := a.ReadExecution(id)
			if before.Snapshot.CodexProjectHooks == nil || before.Snapshot.CodexProjectHooks.Files[0].File.Kind != "absent" {
				t.Fatal("absence not recorded")
			}
			path := filepath.Join(c.Projects[0].Cwd, ".codex/hooks.json")
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("capture created a hook file")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := OpenStore(c.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			state, err := db.Load()
			if err != nil {
				t.Fatal(err)
			}
			session := state.Sessions[id]
			if scenario == "legacy" {
				session.ExecutionSnapshot.CodexProjectHooks = nil
				session.Scope.CodexProjectHooks = nil
			}
			if scenario == "scope-tamper" {
				session.Scope.CodexProjectHooks = nil
			}
			state.Sessions[id] = session
			if err := db.Save(state); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			writePluginMetadata(t, path, `{"hooks":{}}`)
			a = openFixture(t, c, newProjectHookFixture())
			read, err := a.ReadExecution(id)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "legacy" {
				if read.Snapshot.CodexProjectHooks != nil || read.Status != "unchanged" {
					t.Fatal("legacy evidence invented")
				}
			} else if read.Status != "drifted" && read.BindingError == "" {
				t.Fatal("source or binding drift hidden")
			}
		})
	}
}

func TestCodexProjectHookVersionCannotUpgradeThroughPluginChange(t *testing.T) {
	c := codexPluginFixture(t)
	path := filepath.Join(c.Projects[0].Cwd, ".codex/hooks.json")
	const original = `{"hooks":{}}`
	writePluginMetadata(t, path, original)
	a := openFixture(t, c, newProjectHookFixture())
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	before, err := a.ReadExecution(id)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionChangeRequest{BaseSnapshotID: before.Snapshot.ID, Selections: []CapabilitySelection{{AssetID: "plugin", Mode: "enabled"}}}
	if _, err := a.PreviewExecutionChange(id, request); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, path, `{"hooks":{"Stop":[]}}`)
	if _, err := a.PreviewExecutionChange(id, request); err == nil {
		t.Fatal("plugin preview adopted changed project hook file")
	}
	writePluginMetadata(t, path, original)
	preview, err := a.PreviewExecutionChange(id, request)
	if err != nil {
		t.Fatal(err)
	}
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "save", Kind: "stage_execution", SessionID: id, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	read, err := a.ReadExecution(id)
	if err != nil || read.PendingChange == nil || digest(read.PendingChange.Snapshot.CodexProjectHooks) != digest(before.Snapshot.CodexProjectHooks) {
		t.Fatal("saved candidate changed project hook version", err)
	}
}

// This fixture controls metadata at the Adapter seam; native source discovery
// is covered by the tagged Application/Runtime tests.
type projectHookFixture struct{ *fixtureRuntime }

func newProjectHookFixture() *projectHookFixture { return &projectHookFixture{newFixture()} }
func (f *projectHookFixture) ReadCodexProjectHookSources(_ context.Context, scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	path := filepath.Join(scope.Cwd, ".codex/hooks.json")
	file := runtime.FileSource{Path: path, ResolvedPath: path, Kind: "absent"}
	body, err := os.ReadFile(path)
	if err == nil {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		file.Kind = "file"
		file.SHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
		file.Mode = uint32(st.Mode().Perm())
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &runtime.CodexProjectHookSources{Version: 1, Source: "codex.config/read+hooks/list", Cwd: scope.Cwd, Profile: scope.Instance.Profile, Files: []runtime.CodexProjectHookFile{{File: file, Declared: true, LayerVersion: "sha256:" + strings.Repeat("1", 64)}}}, nil
}

type absentProjectReader struct{ runtime.Adapter }
type brokenProjectReader struct {
	*fixtureRuntime
	mode string
}

func (f *brokenProjectReader) ReadCodexProjectHookSources(ctx context.Context, scope runtime.Scope) (*runtime.CodexProjectHookSources, error) {
	if f.mode == "error" {
		return nil, fmt.Errorf("native metadata unavailable")
	}
	if f.mode == "nil" {
		return nil, nil
	}
	out, err := f.fixtureRuntime.ReadCodexProjectHookSources(ctx, scope)
	out.Cwd = "/wrong"
	return out, err
}
func TestCodexProjectHookCaptureRequiresValidReader(t *testing.T) {
	for _, mode := range []string{"missing", "error", "nil", "wrong-binding"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := executionFixture(t)
			f := newFixture()
			var adapter runtime.Adapter = &brokenProjectReader{f, mode}
			if mode == "missing" {
				adapter = &absentProjectReader{f}
			}
			a := openFixture(t, c, adapter)
			_, err := a.Apply(Command{ID: "create", Kind: "create", ProjectID: c.Projects[0].ID, RouteID: c.Routes[0].ID, Permission: "read_only"})
			if err == nil || f.creates.Load() != 0 {
				t.Fatal("missing source reached native create", err)
			}
		})
	}
}
