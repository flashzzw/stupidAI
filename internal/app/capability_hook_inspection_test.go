package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-netcap/internal/runtime"
)

type capabilityHookReader struct {
	*fixtureRuntime
	calls atomic.Int32
	read  func(context.Context, runtime.Instance, runtime.PluginHookPackage) (runtime.PluginHookInspection, error)
}

func (f *capabilityHookReader) InspectPluginHooks(ctx context.Context, i runtime.Instance, p runtime.PluginHookPackage) (runtime.PluginHookInspection, error) {
	f.calls.Add(1)
	return f.read(ctx, i, p)
}

func fixtureHookInspection(i runtime.Instance, p runtime.PluginHookPackage) runtime.PluginHookInspection {
	native, _, _ := runtime.CodexPluginLocation(runtime.Scope{SessionID: "hook-inspection", Instance: runtime.Instance{Kind: "codex", Profile: "/"}}, runtime.PluginSelection{AssetID: p.AssetID, Name: p.Name, Version: p.Version, SourcePath: p.SourcePath, SourceHash: p.SourceHash})
	out := runtime.PluginHookInspection{Scope: "package", Source: "codex.isolated.hooks/list", InstanceID: i.ID,
		NativeSource: native, SourceHash: p.SourceHash, ServerVersion: "0.158.0", At: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, expected := range p.Expected {
		for n := 0; n < expected.Count; n++ {
			out.Definitions = append(out.Definitions, runtime.PluginHookDefinition{Key: fmt.Sprintf("%s:fixture:%d", native, len(out.Definitions)),
				Path: expected.Path, Event: expected.Event, Type: "command", TimeoutSeconds: 1, DefinitionHash: "sha256:" + strings.Repeat("a", 64)})
		}
	}
	return out
}

func inspectionFixture(t *testing.T) (*Application, *capabilityHookReader, CapabilityHookInspectionRequest) {
	t.Helper()
	cfg := codexPluginFixture(t)
	cfg.Instances[0].ServerExecutable = "/owned/server"
	root := cfg.CapabilityAssets[0].Source
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks/hooks.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","timeout":1}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	f := &capabilityHookReader{fixtureRuntime: newFixture()}
	f.read = func(_ context.Context, i runtime.Instance, p runtime.PluginHookPackage) (runtime.PluginHookInspection, error) {
		return fixtureHookInspection(i, p), nil
	}
	a := openFixture(t, cfg, f)
	item := libraryRead(t, a, "plugin")
	return a, f, CapabilityHookInspectionRequest{AssetID: item.Asset.ID, Revision: item.Revision,
		SourceSHA256: item.Source.SHA256, InstanceID: cfg.Instances[0].ID}
}

func TestCapabilityHookInspectionIsReadOnlyAndRejectsStaleOrForeignMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "scope", "server-version", "source-hash", "instance", "missing", "duplicate", "native-source", "path", "mutated-input", "source-drift", "root-mode", "instance-drift", "library-drift", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a, f, request := inspectionFixture(t)
			before := digest(a.Snapshot())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.read = func(_ context.Context, instance runtime.Instance, pkg runtime.PluginHookPackage) (runtime.PluginHookInspection, error) {
				out := fixtureHookInspection(instance, pkg)
				switch mode {
				case "scope":
					out.Scope = "thread"
				case "server-version":
					out.ServerVersion = "0.159.0"
				case "source-hash":
					out.SourceHash = strings.Repeat("c", 64)
				case "instance":
					out.InstanceID = "other"
				case "missing":
					out.Definitions = nil
				case "duplicate":
					out.Definitions = append(out.Definitions, out.Definitions[0])
				case "native-source":
					out.NativeSource = "another@source"
				case "path":
					out.Definitions[0].Path = "/outside/hooks.json"
				case "mutated-input":
					pkg.Expected[0].Count++
					extra := out.Definitions[0]
					extra.Key += "-extra"
					out.Definitions = append(out.Definitions, extra)
				case "source-drift":
					if err := os.WriteFile(filepath.Join(pkg.SourcePath, "hooks/hooks.json"), []byte(`{"hooks":{}}`), 0600); err != nil {
						t.Fatal(err)
					}
				case "root-mode":
					if err := os.Chmod(pkg.SourcePath, os.FileMode(pkg.RootMode)^0020); err != nil {
						t.Fatal(err)
					}
				case "instance-drift":
					a.mu.Lock()
					a.config.Instances[0].PolicyRevision = "changed"
					a.mu.Unlock()
				case "library-drift":
					libraryChange(t, a, "disable-during-read", CapabilityChangeRequest{Action: "disable", AssetID: request.AssetID, BaseRevision: request.Revision})
				case "cancelled":
					cancel()
				}
				return out, nil
			}
			out, err := a.InspectCapabilityHooks(ctx, request)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("result acceptance: %+v %v", out, err)
			}
			if mode == "valid" && (out.Request != request || out.Source.SHA256 != request.SourceSHA256 || before != digest(a.Snapshot())) {
				t.Fatal("read changed state or lost version provenance")
			}
			if f.calls.Load() != 1 || f.creates.Load() != 0 || f.sends.Load() != 0 {
				t.Fatal("inspection dispatched execution")
			}
		})
	}
}

func TestCapabilityHookInspectionRejectsUnreviewableInputBeforeRuntime(t *testing.T) {
	for _, mode := range []string{"revision", "source-hash", "instance", "portable", "inline", "empty", "malformed-group", "unsupported-handler", "unknown-event"} {
		t.Run(mode, func(t *testing.T) {
			a, f, request := inspectionFixture(t)
			item := libraryRead(t, a, request.AssetID)
			root := item.Asset.Source
			var file, body string
			switch mode {
			case "revision":
				request.Revision = "old"
			case "source-hash":
				request.SourceSHA256 = "old"
			case "instance":
				request.InstanceID = "missing"
			case "portable":
				file, body = "plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-plugin","version":"1.2.3"}`
			case "inline":
				file, body = ".codex-plugin/plugin.json", `{"name":"fixture-plugin","version":"1.2.3","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`
			case "empty":
				file, body = "hooks/hooks.json", `{"hooks":{"SessionStart":[]}}`
			case "malformed-group":
				file, body = "hooks/hooks.json", `{"hooks":{"SessionStart":["invalid"]}}`
			case "unsupported-handler":
				file, body = "hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"prompt","prompt":"fixture"}]}]}}`
			case "unknown-event":
				file, body = "hooks/hooks.json", `{"hooks":{"FutureEvent":[{"hooks":[{"type":"command","command":"true"}]}]}}`
			}
			if file != "" {
				if err := os.WriteFile(filepath.Join(root, file), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				request.SourceSHA256 = libraryRead(t, a, request.AssetID).Source.SHA256
			}
			if _, err := a.InspectCapabilityHooks(context.Background(), request); err == nil || f.calls.Load() != 0 {
				t.Fatalf("invalid input reached runtime: %v, calls=%d", err, f.calls.Load())
			}
		})
	}
}

func TestCapabilityHookInspectionRejectsDuplicateAndCancelsOnApplicationClose(t *testing.T) {
	a, f, request := inspectionFixture(t)
	entered := make(chan struct{})
	f.read = func(ctx context.Context, _ runtime.Instance, _ runtime.PluginHookPackage) (runtime.PluginHookInspection, error) {
		close(entered)
		<-ctx.Done()
		return runtime.PluginHookInspection{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, err := a.InspectCapabilityHooks(context.Background(), request); done <- err }()
	<-entered
	if _, err := a.InspectCapabilityHooks(context.Background(), request); err == nil || f.calls.Load() != 1 {
		t.Fatal("duplicate inspection dispatched another read")
	}
	closed := make(chan struct{})
	go func() { a.Close(); close(closed) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed application returned a successful inspection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("application shutdown did not cancel its read")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("application shutdown did not join inspection")
	}
}
