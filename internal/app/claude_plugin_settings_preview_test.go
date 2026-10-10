package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func pluginSettingsPreviewFixture(t *testing.T, mode string) (*Application, *fixtureRuntime, string, string, string) {
	t.Helper()
	c, market, _, registry := installedCatalogFixture(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	c.Instances[0].NodeExecutable = python
	custom := filepath.Join(c.Instances[0].Profile, "custom/original.json")
	writePluginMetadata(t, custom, `{"env":{"PRIVATE":"PRIVATE_PREVIEW_SETTINGS"}}`)
	c.Instances[0].SettingsFile = custom
	reader := `import json,sys,hashlib,os
from pathlib import Path
r=json.load(sys.stdin)
files=[]
def key(s):return ':'.join(str(x) for x in [s.st_dev,s.st_ino,s.st_mode,s.st_uid,s.st_gid,s.st_nlink,s.st_size,s.st_mtime_ns,s.st_ctime_ns])
for path in dict.fromkeys(r['additional_files']):
 p=Path(path)
 if not p.exists():files.append({'path':path,'status':'absent'});continue
 data=p.read_bytes();identity=json.dumps([str(p.resolve()),key(p.stat()),key(p.lstat())],separators=(',',':'))
 files.append({'path':path,'status':'read','sha256':hashlib.sha256(data).hexdigest(),'bytes':len(data),'resolved_path':str(p.resolve()),'identity_sha256':hashlib.sha256(identity.encode()).hexdigest()})
print(json.dumps({'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':'2026-10-08T00:00:00Z','resolver_returned':True,'read_complete':True,'complete':False,'files':files,'sources':[],'registry':[],'issues':[],'limitations':['read_observation_not_admission'],'policy_helpers_present':False,'startup_conflicts':[]}))
`
	worker := fmt.Sprintf(`import json,sys,os,time
from pathlib import Path
r=json.load(sys.stdin)
assert os.fstat(3).st_ino==os.stat(r['settings_file']).st_ino
root=Path(__file__).parent
(root/'preview-started').write_text('started')
mode=%q
if mode=='slow':
 while not (root/'preview-release').exists():time.sleep(.01)
if mode=='drift':
 p=Path(%q);p.write_bytes(p.read_bytes()+b'\n')
s=r['plugin_settings'][0]
signature={'effective_sha256':'a'*64,'sources_sha256':'b'*64,'applied_sha256':'c'*64,'permissions_sha256':'d'*64}
preservation={'version':1,'baseline_settings_file':r['baseline_settings_file'],'source_keys':[s['source_key']],'baseline_schema_valid':True,'baseline_process_stopped':True,'baseline':signature,'candidate':signature,'differences':[],'matches':True}
print(json.dumps({'version':1,'source':'claude.bare.settings_validation','cli_version':'2.1.280','cwd':r['cwd'],'settings_file':r['settings_file'],'schema_valid':True,'errors':[],'effective_sha256':'a'*64,'sources':[],'bare_mode':True,'no_session_persistence':True,'process_stopped':True,'file_holder_observed':True,'preservation':preservation,'plugin_settings':[{'source_key':s['source_key'],'requested_enabled':s['enabled'],'actual_enabled':s['enabled'],'matches':True}]}))
`, mode, filepath.Join(market, "selected/.claude-plugin/plugin.json"))
	for name, body := range map[string]string{"settings_preflight.js": reader, "settings_validation.js": worker} {
		writePluginMetadata(t, filepath.Join(filepath.Dir(c.Instances[0].Bridge), name), body)
	}
	f := newFixture()
	if mode == "recovery-local" {
		writePluginMetadata(t, filepath.Join(c.Projects[0].Cwd, ".claude/settings.local.json"), `{}`)
		writePluginMetadata(t, filepath.Join(c.Projects[0].Cwd, ".claude/settings.json"), `{}`)
	}
	a := openFixture(t, c, f)
	id := createFixture(t, a).SessionID
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	return a, f, id, market, registry
}

func TestInstalledPluginSettingsPreviewPreservesSession(t *testing.T) {
	for _, mode := range []string{"valid", "drift"} {
		t.Run(mode, func(t *testing.T) {
			a, f, id, _, _ := pluginSettingsPreviewFixture(t, mode)
			catalog, err := a.ReadSessionInstalledPlugins(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			before := a.Snapshot().Sessions[0]
			out, err := a.PreviewSessionPluginSettings(context.Background(), id, PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "disabled", SourceVersion: catalog.SourceVersion})
			if err != nil {
				t.Fatal(err)
			}
			if out.Validation == nil || !out.Validation.PluginSettings[0].Matches || out.Complete || out.Cleanup.State != "released" || out.SourcesStable != (mode == "valid") || (len(out.Problems) == 0) != (mode == "valid") {
				t.Fatalf("preview: %+v", out)
			}
			after := a.Snapshot().Sessions[0]
			if !reflect.DeepEqual(before.Scope, after.Scope) || !reflect.DeepEqual(before.ExecutionSnapshot, after.ExecutionSnapshot) || before.Generation != after.Generation || f.creates.Load() != 1 || f.sends.Load() != 0 {
				t.Fatal("metadata preview changed execution")
			}
			data, _ := json.Marshal(out)
			if strings.Contains(string(data), "PRIVATE_") {
				t.Fatal("private content escaped preview")
			}
		})
	}
}

func TestInstalledPluginSettingsPreviewRejectsStaleSources(t *testing.T) {
	a, _, id, market, registry := pluginSettingsPreviewFixture(t, "valid")
	for _, path := range []string{registry, filepath.Join(filepath.Dir(registry), "known_marketplaces.json"), filepath.Join(market, ".claude-plugin/marketplace.json"), filepath.Join(market, "selected/.claude-plugin/plugin.json")} {
		before, err := a.ReadSessionInstalledPlugins(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writePluginMetadata(t, path, string(body)+"\n")
		after, err := a.ReadSessionInstalledPlugins(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if before.SourceVersion == after.SourceVersion || !reflect.DeepEqual(before.Entries, after.Entries) {
			t.Fatal("raw source change was not independently versioned")
		}
		if _, err := a.PreviewSessionPluginSettings(context.Background(), id, PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "enabled", SourceVersion: before.SourceVersion}); err == nil {
			t.Fatal("stale source accepted")
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(registry), "..", "preview-started")); !os.IsNotExist(err) {
		t.Fatal("stale source ran metadata worker")
	}
}

func TestInstalledPluginSettingsPreviewRejectsChangedOwnership(t *testing.T) {
	a, _, id, _, _ := pluginSettingsPreviewFixture(t, "slow")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	catalog, err := a.ReadSessionInstalledPlugins(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
	done := make(chan error, 1)
	go func() {
		_, err := a.PreviewSessionPluginSettings(ctx, id, PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "enabled", SourceVersion: catalog.SourceVersion})
		done <- err
	}()
	waitTest(t, func() bool { _, err := os.Stat(filepath.Join(profile, "preview-started")); return err == nil })
	if _, err := a.Apply(Command{ID: "change-owner", Kind: "takeover", SessionID: id}); err != nil {
		t.Fatal(err)
	}
	writePluginMetadata(t, filepath.Join(profile, "preview-release"), "release")
	if err := <-done; err == nil {
		t.Fatal("changed ownership returned a usable preview")
	}
	paths, _ := filepath.Glob(filepath.Join(profile, "custom/.netcap-settings-*.json"))
	if len(paths) != 0 {
		t.Fatal("changed ownership left owned settings files")
	}
}

func TestInstalledPluginSettingsPreviewRejectsUnprovenSelection(t *testing.T) {
	for _, mode := range []string{"missing-key", "duplicate", "remote", "wrong-project", "bad-mode", "missing-version", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			a, _, id, _, registry := pluginSettingsPreviewFixture(t, "valid")
			profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
			if mode == "duplicate" || mode == "wrong-project" {
				body, _ := os.ReadFile(registry)
				var value map[string]any
				if json.Unmarshal(body, &value) != nil {
					t.Fatal("bad fixture")
				}
				plugins := value["plugins"].(map[string]any)
				rows := plugins["selected@local"].([]any)
				if mode == "duplicate" {
					plugins["selected@local"] = append(rows, rows[0])
				} else {
					row := rows[0].(map[string]any)
					row["scope"] = "project"
					row["projectPath"] = "/different/project"
				}
				writePluginMetadata(t, registry, value)
			}
			if mode == "remote" {
				writePluginMetadata(t, filepath.Join(filepath.Dir(registry), "known_marketplaces.json"), `{"local":{"source":{"source":"github","repo":"owned-fixture"}}}`)
			}
			if mode == "unreadable" {
				writePluginMetadata(t, registry, `{"broken":`)
			}
			catalog, err := a.ReadSessionInstalledPlugins(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			request := PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "disabled", SourceVersion: catalog.SourceVersion}
			switch mode {
			case "missing-key":
				request.SourceKey = "not-registered@local"
			case "bad-mode":
				request.Mode = "inherit"
			case "missing-version":
				request.SourceVersion = ""
			}
			if _, err := a.PreviewSessionPluginSettings(context.Background(), id, request); err == nil {
				t.Fatal("unproven selection accepted")
			}
			if _, err := os.Stat(filepath.Join(profile, "preview-started")); !os.IsNotExist(err) {
				t.Fatal("unproven selection ran the native metadata worker")
			}
		})
	}
}

func TestInstalledPluginSettingsPreviewRejectsCurrentStartupConflict(t *testing.T) {
	for _, layer := range []string{"custom", "user", "project", "local"} {
		for _, kind := range []string{"helper", "route"} {
			t.Run(layer+"-"+kind, func(t *testing.T) {
				a, f, id, _, _ := pluginSettingsPreviewFixture(t, "valid")
				before := a.Snapshot().Sessions[0]
				path := before.Scope.Instance.SettingsFile
				switch layer {
				case "user":
					path = filepath.Join(before.Scope.Instance.Profile, "settings.json")
				case "project":
					path = filepath.Join(before.Scope.Cwd, ".claude/settings.json")
				case "local":
					path = filepath.Join(before.Scope.Cwd, ".claude/settings.local.json")
				}
				body := `{"apiKeyHelper":"PRIVATE_API_HELPER"}`
				if kind == "route" {
					body = `{"env":{"ANTHROPIC_API_KEY":"PRIVATE_ROUTE_VALUE"}}`
				}
				writePluginMetadata(t, path, body)
				catalog, err := a.ReadSessionInstalledPlugins(context.Background(), id)
				if err != nil || !catalog.Complete {
					t.Fatal("startup conflict blocked independent metadata reading", err)
				}
				out, err := a.PreviewSessionPluginSettings(context.Background(), id, PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "disabled", SourceVersion: catalog.SourceVersion})
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Problems) == 0 || out.Validation != nil || out.Artifact != nil || out.Cleanup.State != "not_created" || out.Complete {
					t.Fatalf("startup conflict was not reported before native validation: %+v", out)
				}
				if _, err := os.Stat(filepath.Join(before.Scope.Instance.Profile, "preview-started")); !os.IsNotExist(err) {
					t.Fatal("startup conflict launched the native worker")
				}
				after := a.Snapshot().Sessions[0]
				if !reflect.DeepEqual(before.Scope, after.Scope) || !reflect.DeepEqual(before.ExecutionSnapshot, after.ExecutionSnapshot) || before.Generation != after.Generation || f.creates.Load() != 1 || f.sends.Load() != 0 {
					t.Fatal("rejection changed original execution")
				}
				encoded, _ := json.Marshal(out)
				if strings.Contains(string(encoded), "PRIVATE_API_HELPER") || strings.Contains(string(encoded), "PRIVATE_ROUTE_VALUE") {
					t.Fatal("helper/credential value escaped preview")
				}
				current, _ := os.ReadFile(path)
				if string(current) != body {
					t.Fatal("preview rewrote the conflicting source")
				}
				if directory := os.Getenv("NETCAP_ADMISSION_EVIDENCE_ROOT"); directory != "" && layer == "custom" && kind == "helper" {
					view, err := a.ReadSession(id, "", 100)
					if err != nil {
						t.Fatal(err)
					}
					writePluginMetadata(t, filepath.Join(directory, "application-conflict.json"), map[string]any{"catalog": catalog, "preview": out, "responses": map[string]any{"/api/workspace": a.Workspace(), "/api/snapshot": a.Snapshot(), "/api/sessions/" + id: view}, "session_id": id, "scope": "production Application and owned transport fixture; native worker not launched"})
				}
			})
		}
	}
}
