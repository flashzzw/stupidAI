//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

const inspectionRecorder = `#!/usr/bin/python3
import json,os,subprocess,sys,threading
from pathlib import Path
binary=__BINARY__
trace=Path(__TRACE__)
lock=threading.Lock()
def record(v):
 with lock:
  with trace.open('a') as f:f.write(json.dumps(v)+'\n')
record({'kind':'start','argv':sys.argv[1:],'pid':os.getpid(),'profile':os.environ['CODEX_HOME'],'cwd':os.getcwd(),
        'private_env_present':'NETCAP_INSPECTION_PRIVATE' in os.environ,
        'auth_copied':(Path(os.environ['CODEX_HOME'])/'auth.json').exists()})
if sys.argv[1:]==['--version']:os.execv(binary,[binary,'--version'])
child=subprocess.Popen([binary]+sys.argv[1:],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True)
def clean(v):
 if isinstance(v,dict):return {k:clean(x) for k,x in v.items() if k not in ['command','statusMessage']}
 if isinstance(v,list):return [clean(x) for x in v]
 return v
def output():
 for line in child.stdout:
  message=json.loads(line);record({'kind':'output','message':clean(message)})
  sys.stdout.write(line);sys.stdout.flush()
thread=threading.Thread(target=output);thread.start()
for line in sys.stdin:
 request=json.loads(line);record({'kind':'input','message':request})
 child.stdin.write(line);child.stdin.flush()
child.stdin.close();thread.join()
code=child.wait();record({'kind':'exit','code':code,'child_pid':child.pid})
raise SystemExit(code)
`

func TestCodexNativeCapabilityHookInspection(t *testing.T) {
	root, err := os.MkdirTemp("", "netcap-w0208-hook-inspection-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	if err := f.Runtime.Close(); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	hashTree := func(path string) map[string]string {
		t.Helper()
		out := map[string]string{}
		if err := filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(body)
			rel, _ := filepath.Rel(path, p)
			out[rel] = hex.EncodeToString(sum[:])
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	checks := map[string]bool{}
	results := map[string]any{}
	check := func(name string, condition bool) {
		t.Helper()
		checks[name] = condition
		if !condition {
			t.Errorf("failed: %s", name)
		}
	}
	t.Cleanup(func() {
		body, _ := json.MarshalIndent(map[string]any{"root": root, "checks": checks, "samples": results, "passed": !t.Failed(),
			"scope": "production Application/Runtime/Handler; owned recording wrapper forwards fixed 0.158.0 stdio; fixture initialization is separate, inspection never connects the original backend or requests a thread/turn/hook/tool"}, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "result.json"), body, 0600)
	})

	// Put TMPDIR below a hostile project. The reader must establish its own
	// repository boundary rather than inheriting the parent's invalid config.
	parent := filepath.Join(root, "parent-project")
	write(filepath.Join(parent, ".codex/config.toml"), "THIS IS INVALID PARENT CONFIG\n", 0600)
	git := exec.Command("git", "-c", "init.templateDir=", "init", "--quiet", parent)
	if err := git.Run(); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(parent, "tmp")
	if err := os.Mkdir(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", filepath.Join(root, "caller-home"))
	t.Setenv("NETCAP_INSPECTION_PRIVATE", "owned-private-fixture")
	write(filepath.Join(f.Instance.Profile, "auth.json"), `{"OPENAI_API_KEY":"owned-unused-auth-fixture"}`, 0600)
	beforeProfile := hashTree(f.Instance.Profile)
	trace := filepath.Join(root, "inspection-protocol.jsonl")
	binary := filepath.Join(root, "recording-server")
	write(binary, strings.NewReplacer("__BINARY__", strconv.Quote(os.Getenv("NETCAP_NATIVE_CODEX_TEST")), "__TRACE__", strconv.Quote(trace)).Replace(inspectionRecorder), 0700)
	instance := f.Instance
	instance.ServerExecutable, instance.Name, instance.EnvAllow = binary, "Codex fixture", []string{"NETCAP_INSPECTION_PRIVATE"}
	alias := instance
	alias.ID, alias.Name = instance.ID+"-alias", "Codex alias"
	stateDir := filepath.Join(root, "application")
	store, err := app.OpenStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(app.Config{Version: 1, StateDir: stateDir, Projects: []app.Project{{ID: "project", Cwd: filepath.Join(root, "work")}},
		Instances: []runtime.Instance{instance, alias}, Routes: []runtime.Route{f.Route}}, store, runtime.NewAdapters(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	assetRoot := filepath.Join(root, "source")
	marker := filepath.Join(root, "unexpected-execution.txt")
	write(filepath.Join(assetRoot, ".codex-plugin/plugin.json"), `{"name":"inspection-fixture","version":"1.0.0","hooks":["./hooks/hooks.json","./custom.json"]}`, 0600)
	write(filepath.Join(assetRoot, "hooks/hooks.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"python3 ${PLUGIN_ROOT}/entry.py","timeout":2},{"type":"command","command":"printf %s ${PLUGIN_DATA}","timeout":3,"async":true}]}],"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"python3 ${CLAUDE_PLUGIN_ROOT}/entry.py","timeout":4}]}]}}`, 0600)
	write(filepath.Join(assetRoot, "custom.json"), `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"true","timeout":1}]}]}}`, 0600)
	write(filepath.Join(assetRoot, "entry.py"), fmt.Sprintf("from pathlib import Path\nPath(%q).write_text('UNEXPECTED')\n", marker), 0600)
	write(filepath.Join(assetRoot, "skills/method/SKILL.md"), "---\nname: method\ndescription: Owned inspection method\n---\nNot invoked.\n", 0600)
	write(filepath.Join(assetRoot, ".mcp.json"), `{"mcpServers":{"marker":{"command":"python3","args":["${PLUGIN_ROOT}/entry.py"]}}}`, 0600)
	asset := app.CapabilityAsset{ID: "plugin", Name: "Hook inspection fixture", Kind: "cli_plugin", Version: "asset-v1", Source: assetRoot, Backends: []string{"codex"}}
	manage := func(id, action, revision string, asset app.CapabilityAsset) {
		t.Helper()
		preview, err := a.PreviewCapabilityChange(context.Background(), app.CapabilityChangeRequest{Action: action, AssetID: asset.ID, BaseRevision: revision, Asset: &asset})
		if err != nil {
			t.Fatal(err)
		}
		request := preview.Request
		request.PreviewHash = preview.PreviewHash
		if _, err = a.Apply(app.Command{ID: id, Kind: "manage_capability", CapabilityChange: &request}); err != nil {
			t.Fatal(err)
		}
	}
	manage("register-plugin", "register", "", asset)
	capabilities, err := a.ReadCapabilities(context.Background())
	if err != nil || len(capabilities) != 1 || capabilities[0].Source == nil {
		t.Fatalf("capability: %v %+v", err, capabilities)
	}
	request := app.CapabilityHookInspectionRequest{AssetID: asset.ID, Revision: capabilities[0].Revision, SourceSHA256: capabilities[0].Source.SHA256, InstanceID: instance.ID}
	snapshot := a.Snapshot()
	handler := console.Handler(a, "127.0.0.1:9000", strings.Repeat("a", 64), fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	post := func(request app.CapabilityHookInspectionRequest) (int, json.RawMessage) {
		t.Helper()
		body, _ := json.Marshal(request)
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/capabilities/codex-hooks", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	status, body := post(request)
	results["first"] = body
	if status != 200 {
		t.Fatalf("native inspection HTTP %d: %s", status, body)
	}
	var first app.CapabilityHookInspection
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	check("four_native_handlers_match_declarations", len(first.Inspection.Definitions) == 4)
	check("package_scope_and_server_version", first.Inspection.Scope == "package" && first.Inspection.ServerVersion == "0.158.0")
	check("registered_version_and_source_bound", first.AssetVersion == "asset-v1" && first.Source.SHA256 == request.SourceSHA256 && first.Request == request)
	check("application_state_unchanged_by_read", reflect.DeepEqual(snapshot, a.Snapshot()))
	check("no_command_or_output_body_exposed", !strings.Contains(string(body), "python3") && !strings.Contains(string(body), "UNEXPECTED"))
	paths := map[string]int{}
	async := 0
	for _, d := range first.Inspection.Definitions {
		paths[d.Path]++
		if d.Background {
			async++
		}
	}
	check("default_and_custom_entries_preserved", paths["hooks/hooks.json"] == 3 && paths["custom.json"] == 1)
	check("async_is_declaration_only", async == 1)
	request.InstanceID = alias.ID
	status, aliasBody := post(request)
	results["alias"] = aliasBody
	var second app.CapabilityHookInspection
	_ = json.Unmarshal(aliasBody, &second)
	check("alias_uses_own_instance_binding", status == 200 && second.Inspection.InstanceID == alias.ID)
	check("cold_inspections_keep_definition_identity", reflect.DeepEqual(first.Inspection.Definitions, second.Inspection.Definitions))
	request.InstanceID = instance.ID
	responses := map[string]any{"snapshot": snapshot, "capabilities": capabilities, "request": request, "inspection": first, "alias": second}

	write(filepath.Join(assetRoot, "entry.py"), fmt.Sprintf("from pathlib import Path\nPath(%q).write_text('UNEXPECTED_V2')\n", marker), 0600)
	status, drift := post(request)
	check("old_source_hash_rejected", status == 409)
	results["old-hash-rejected"] = drift
	changed, _ := a.ReadCapabilities(context.Background())
	request.SourceSHA256 = changed[0].Source.SHA256
	status, drift = post(request)
	check("fresh_hash_cannot_relabel_old_registration", status == 409)
	results["registration-drift-rejected"] = drift
	asset.Version = "asset-v2"
	manage("update-plugin", "update", capabilities[0].Revision, asset)
	updated, _ := a.ReadCapabilities(context.Background())
	request.Revision, request.SourceSHA256 = updated[0].Revision, updated[0].Source.SHA256
	status, updatedBody := post(request)
	results["updated"] = updatedBody
	var third app.CapabilityHookInspection
	_ = json.Unmarshal(updatedBody, &third)
	check("explicit_registration_update_inspects_new_version", status == 200 && third.AssetVersion == "asset-v2" && third.Source.SHA256 != first.Source.SHA256)
	check("full_package_change_has_new_inspection_key", third.Inspection.NativeSource != first.Inspection.NativeSource)
	hashes := func(v app.CapabilityHookInspection) []string {
		out := []string{}
		for _, h := range v.Inspection.Definitions {
			out = append(out, h.DefinitionHash)
		}
		return out
	}
	check("script_change_keeps_native_handler_hashes_separate", reflect.DeepEqual(hashes(first), hashes(third)))
	responses["updated_snapshot"], responses["updated_capabilities"], responses["updated_inspection"] = a.Snapshot(), updated, third
	responses["drift_error"] = json.RawMessage(drift)
	responseBytes, _ := json.MarshalIndent(responses, "", "  ")
	write(filepath.Join(root, "browser-responses.json"), string(responseBytes), 0600)
	check("original_profile_including_auth_unchanged", reflect.DeepEqual(beforeProfile, hashTree(f.Instance.Profile)))
	check("no_application_sessions_or_operations_created", len(a.Snapshot().Sessions) == 0 && len(a.Snapshot().Operations) == 0)
	_, err = os.Stat(marker)
	check("no_mcp_or_hook_marker_executed", os.IsNotExist(err))
	leftovers, _ := filepath.Glob(filepath.Join(tmp, "netcap-codex-hook-inspection-*"))
	check("all_owned_inspection_directories_removed", len(leftovers) == 0)
	traceBytes, _ := os.ReadFile(trace)
	var methods []string
	exits, starts := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(traceBytes)), "\n") {
		var row struct {
			Kind       string `json:"kind"`
			Code       int    `json:"code"`
			PrivateEnv bool   `json:"private_env_present"`
			AuthCopied bool   `json:"auth_copied"`
			Message    struct {
				Method string `json:"method"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("malformed owned recorder output")
		}
		if row.Kind == "start" {
			starts++
			check(fmt.Sprintf("process_%d_does_not_inherit_credentials", starts), !row.PrivateEnv && !row.AuthCopied)
		}
		if row.Kind == "input" {
			methods = append(methods, row.Message.Method)
		}
		if row.Kind == "exit" && row.Code == 0 {
			exits++
		}
		if row.Kind == "output" && (strings.HasPrefix(row.Message.Method, "thread/") || strings.HasPrefix(row.Message.Method, "turn/") || strings.HasPrefix(row.Message.Method, "hook/")) {
			t.Error("inspection emitted execution event")
		}
	}
	check("three_native_inspectors_exited_zero", exits == 3 && starts == 6)
	check("only_fixed_metadata_protocol_sent", strings.Join(methods, ",") == strings.TrimSuffix(strings.Repeat("initialize,initialized,hooks/list,", 3), ","))
	results["profile_files_before"], results["profile_files_after"] = beforeProfile, hashTree(f.Instance.Profile)
}
