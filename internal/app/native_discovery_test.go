package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

const discoveredID = "018f0000-0000-7000-8000-000000000001"

type discoveryRuntime struct {
	*fixtureRuntime
	list    func(runtime.DiscoveryRequest) (runtime.DiscoveryPage, error)
	inspect func(runtime.DiscoveryRequest, string) (runtime.NativeSession, error)
	calls   int
}

func (f *discoveryRuntime) Discover(_ context.Context, r runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
	f.calls++
	return f.list(r)
}
func (f *discoveryRuntime) InspectNative(_ context.Context, r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
	f.calls++
	return f.inspect(r, id)
}

func TestNativeDiscoveryBindsPaginationAndPreservesState(t *testing.T) {
	c := fixtureConfig(t)
	c.Projects = append(c.Projects, Project{ID: "project-alias", Cwd: c.Projects[0].Cwd})
	f := &discoveryRuntime{fixtureRuntime: newFixture()}
	row := runtime.NativeSession{NativeID: discoveredID, Title: "native", Cwd: c.Projects[0].Cwd, Source: "cli", NativeStatus: "notLoaded"}
	f.list = func(r runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
		if r.Cwd != c.Projects[0].Cwd || r.Instance.ID != "instance" || r.Limit != 50 {
			t.Fatalf("wrong scope: %+v", r)
		}
		if r.Cursor == "" {
			return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{row}, Next: "page-2"}, nil
		}
		if r.Cursor != "page-2" {
			t.Fatal("cursor not decoded")
		}
		return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{}}, nil
	}
	f.inspect = func(r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
		if id != discoveredID {
			t.Fatal("wrong id")
		}
		return row, nil
	}
	a := openFixture(t, c, f)
	before := a.Sequence()
	request := NativeDiscoveryRequest{ProjectID: "project", InstanceID: "instance"}
	first, err := a.DiscoverNative(context.Background(), request)
	if err != nil || !first.ReadOnly || len(first.Sessions) != 1 || first.Next == "" || first.StateNamespace == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	request.Cursor = first.Next
	if _, err := a.DiscoverNative(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Archived = true
	if _, err := a.DiscoverNative(context.Background(), request); err == nil {
		t.Fatal("cursor accepted for another filter")
	}
	request.Archived = false
	request.ProjectID = "project-alias"
	if _, err := a.DiscoverNative(context.Background(), request); err == nil {
		t.Fatal("cursor accepted for a different registered project")
	}
	preview, err := a.PreviewNative(context.Background(), NativePreviewRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID})
	if err != nil || len(preview.Sessions) != 1 || len(preview.Sessions[0].KnownSessions) != 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if f.calls != 3 || f.creates.Load() != 0 || f.sends.Load() != 0 || a.Sequence() != before || len(a.Workspace().Sessions) != 0 {
		t.Fatal("metadata query created execution or state")
	}
}

func TestNativeDiscoveryRejectsUnprovenScopeAndMalformedPages(t *testing.T) {
	for _, scenario := range []string{"foreign-project", "missing-cwd", "wrong-id", "duplicate", "too-large", "cursor-cycle", "configuration-drift"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &discoveryRuntime{fixtureRuntime: newFixture()}
			row := runtime.NativeSession{NativeID: discoveredID, Cwd: c.Projects[0].Cwd}
			f.list = func(r runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
				rows := []runtime.NativeSession{row}
				next := ""
				switch scenario {
				case "foreign-project":
					rows[0].Cwd = t.TempDir()
				case "missing-cwd":
					rows[0].Cwd = ""
				case "wrong-id":
					rows[0].NativeID = "not-an-id"
				case "duplicate":
					rows = append(rows, row)
				case "too-large":
					for len(rows) < 51 {
						rows = append(rows, row)
					}
				case "cursor-cycle":
					next = "unchanged"
				case "configuration-drift":
					if err := os.WriteFile(filepath.Join(c.Instances[0].Profile, "config.toml"), []byte("model='changed'\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return runtime.DiscoveryPage{Sessions: rows, Next: next}, nil
			}
			a := openFixture(t, c, f)
			request := NativeDiscoveryRequest{ProjectID: "project", InstanceID: "instance"}
			out, err := a.DiscoverNative(context.Background(), request)
			if scenario == "cursor-cycle" && err == nil {
				request.Cursor = out.Next
				out, err = a.DiscoverNative(context.Background(), request)
			}
			if scenario == "foreign-project" || scenario == "missing-cwd" || scenario == "wrong-id" {
				if err != nil || out.Skipped != 1 || len(out.Sessions) != 0 {
					t.Fatalf("unproven record escaped filter: %+v %v", out, err)
				}
			} else if err == nil {
				t.Fatalf("invalid %s accepted: %+v", scenario, out)
			}
		})
	}
}

func TestNativePreviewRechecksIDAndProject(t *testing.T) {
	for _, scenario := range []string{"different-id", "different-project", "invalid-input", "unknown-instance"} {
		t.Run(scenario, func(t *testing.T) {
			c := fixtureConfig(t)
			f := &discoveryRuntime{fixtureRuntime: newFixture()}
			f.inspect = func(r runtime.DiscoveryRequest, id string) (runtime.NativeSession, error) {
				row := runtime.NativeSession{NativeID: id, Cwd: r.Cwd}
				if scenario == "different-id" {
					row.NativeID = "018f0000-0000-7000-8000-000000000002"
				}
				if scenario == "different-project" {
					row.Cwd = t.TempDir()
				}
				return row, nil
			}
			a := openFixture(t, c, f)
			request := NativePreviewRequest{ProjectID: "project", InstanceID: "instance", NativeID: discoveredID}
			if scenario == "invalid-input" {
				request.NativeID = "../../state"
			}
			if scenario == "unknown-instance" {
				request.InstanceID = "other"
			}
			if _, err := a.PreviewNative(context.Background(), request); err == nil {
				t.Fatal("unproven preview accepted")
			}
			if (scenario == "invalid-input" || scenario == "unknown-instance") && f.calls != 0 {
				t.Fatal("invalid input reached runtime")
			}
		})
	}
}

func TestNativeDiscoveryReportsUnavailableAdapter(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	if _, err := a.DiscoverNative(context.Background(), NativeDiscoveryRequest{ProjectID: "project", InstanceID: "instance"}); err == nil || !strings.Contains(err.Error(), "发现接口") {
		t.Fatalf("unsupported adapter: %v", err)
	}
}

func TestNativeDiscoveryMatchesExistingNamespaceWithoutImporting(t *testing.T) {
	c := fixtureConfig(t)
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state := newState()
	for _, id := range []string{"same", "other-namespace", "other-backend"} {
		scope := runtime.Scope{NativeID: discoveredID, Cwd: c.Projects[0].Cwd, Instance: c.Instances[0], StateNamespace: c.Instances[0].Profile}
		if id == "other-namespace" {
			scope.StateNamespace = t.TempDir()
		}
		if id == "other-backend" {
			scope.Instance.Kind = "claude"
		}
		state.Sessions[id] = Session{ID: id, Scope: scope, ControlMode: "observed", Paused: true, Status: "unknown"}
	}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	f := &discoveryRuntime{fixtureRuntime: newFixture(), list: func(r runtime.DiscoveryRequest) (runtime.DiscoveryPage, error) {
		return runtime.DiscoveryPage{Sessions: []runtime.NativeSession{{NativeID: discoveredID, Cwd: r.Cwd}}}, nil
	}}
	a := openFixture(t, c, f)
	out, err := a.DiscoverNative(context.Background(), NativeDiscoveryRequest{ProjectID: "project", InstanceID: "instance"})
	if err != nil || len(out.Sessions) != 1 || len(out.Sessions[0].KnownSessions) != 1 || out.Sessions[0].KnownSessions[0] != "same" {
		t.Fatalf("wrong association: %+v %v", out, err)
	}
	if len(a.Workspace().Sessions) != 3 || f.creates.Load() != 0 || f.sends.Load() != 0 {
		t.Fatal("discovery imported or controlled a native session")
	}
}
