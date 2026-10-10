package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func hookInspectionPackage(t *testing.T) PluginHookPackage {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{".codex-plugin/plugin.json": `{"name":"fixture","version":"1.0.0"}`, "hooks/hooks.json": `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","timeout":1}]}]}}`} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	info, _ := os.Stat(root)
	pkg := PluginHookPackage{AssetID: "plugin", Name: "fixture", Version: "1.0.0", SourcePath: root,
		SourceHash: strings.Repeat("b", 64), RootMode: uint32(info.Mode().Perm()),
		Expected: []PluginHookExpectation{{Path: "hooks/hooks.json", Event: "sessionStart", Count: 1}}}
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		file := PluginFile{Path: filepath.ToSlash(rel), Kind: "file", Mode: uint32(info.Mode().Perm())}
		if entry.IsDir() {
			file.Kind = "directory"
		} else {
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256(body)
			file.SHA256 = hex.EncodeToString(h[:])
		}
		pkg.Files = append(pkg.Files, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

const hookInspectionServer = `#!/usr/bin/python3
import json,os,sys,time,subprocess
from pathlib import Path
mode=__MODE__
trace=Path(__TRACE__)
def record(value):
 with trace.open('a') as f:f.write(json.dumps(value)+'\n')
record({'argv':sys.argv[1:],'profile':os.environ.get('CODEX_HOME'),'home':os.environ.get('HOME'),'cwd':os.getcwd(),
 'private_env_present':'NETCAP_HOOK_PRIVATE' in os.environ,'pid':os.getpid()})
if sys.argv[1:]==['--version']:
 print('X'*5000 if mode=='huge-version' else 'codex-cli 0.159.0' if mode=='version' else 'codex-cli 0.158.0')
 raise SystemExit(0)
if mode=='timeout':time.sleep(30)
profile=Path(os.environ['CODEX_HOME'])
cache=next((profile/'plugins/cache').glob('*/*/*'))
native=cache.parent.name+'@'+cache.parent.parent.name
for line in sys.stdin:
 request=json.loads(line);record({'method':request.get('method')})
 if request['method']=='initialized':continue
 if request['method']=='initialize':
  result={'codexHome':str(profile) if mode!='profile' else '/wrong-profile',
          'userAgent':'netcap-hook-inspection/'+('0.159.0' if mode=='server-version' else '0.158.0')+' fixture'}
 else:
  h={'key':native+':hooks/hooks.json:session_start:0:0','eventName':'sessionStart','source':'plugin',
   'sourcePath':str(cache/'hooks/hooks.json'),'pluginId':native,'handlerType':'command','command':'PRIVATE_COMMAND',
   'currentHash':'sha256:'+'a'*64,'trustStatus':'untrusted','timeoutSec':1,'displayOrder':0,'async':False,'enabled':True,'isManaged':False}
  result={'data':[{'cwd':os.getcwd(),'hooks':[h],'warnings':[],'errors':[]}]}
  row=result['data'][0]
  if mode=='empty':row['hooks']=[]
  if mode=='extra':row['hooks']=[h,{**h,'key':native+':extra'}]
  if mode=='duplicate':row['hooks']=[h,h]
  if mode=='cwd':row['cwd']='/wrong-cwd'
  if mode=='warning':row['warnings']=['PRIVATE_NATIVE_WARNING']
  if mode=='missing-warnings':del row['warnings']
  if mode=='outside':h['sourcePath']='/outside/hooks.json'
  if mode=='trust':h['trustStatus']='trusted'
  if mode=='missing-async':del h['async']
  if mode=='hash':h['currentHash']='PRIVATE_HASH'
  if mode=='event':h['eventName']='sessionEnd'
  if mode=='cache-drift':(cache/'hooks/hooks.json').write_text('{"changed":true}')
  if mode=='request':
   print(json.dumps({'id':99,'method':'item/commandExecution/requestApproval','params':{'command':'PRIVATE_COMMAND'}}),flush=True)
   continue
  if mode=='hook-event':print(json.dumps({'method':'hook/started','params':{}}),flush=True)
  if mode=='oversize':
   print('X'*(5<<20),flush=True)
   continue
  if mode=='descendant':
   child=subprocess.Popen(['/usr/bin/python3','-c','import time; time.sleep(30)'])
   record({'descendant_pid':child.pid})
  if mode=='detached-output':
   child=subprocess.Popen(['/usr/bin/python3','-c','import time; time.sleep(30)'],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   record({'descendant_pid':child.pid})
 print(json.dumps({'id':3 if mode=='reply' and request['id']==2 else request['id'],'result':result}),flush=True)
 if request['method']=='hooks/list' and mode=='trailing-hook':print(json.dumps({'method':'hook/completed','params':{}}),flush=True)
`

func hookInspectionBinary(t *testing.T, mode string) (string, string) {
	t.Helper()
	root := t.TempDir()
	binary, trace := filepath.Join(root, "server"), filepath.Join(root, "trace.jsonl")
	script := strings.NewReplacer("__MODE__", strconv.Quote(mode), "__TRACE__", strconv.Quote(trace)).Replace(hookInspectionServer)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return binary, trace
}

func TestCodexPluginHookInspectionOwnsOnlyTemporaryMetadataProcess(t *testing.T) {
	pkg := hookInspectionPackage(t)
	binary, trace := hookInspectionBinary(t, "valid")
	t.Setenv("NETCAP_HOOK_PRIVATE", "fixture-secret")
	t.Setenv("TMPDIR", t.TempDir())
	i := Instance{ID: "codex", Kind: "codex", ServerExecutable: binary, Profile: "/must-not-read-profile", Socket: "/must-not-connect.sock", EnvAllow: []string{"NETCAP_HOOK_PRIVATE"}}
	out, err := NewCodex().InspectPluginHooks(context.Background(), i, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidatePluginHookInspection(i, pkg, out); err != nil || len(out.Definitions) != 1 || out.Definitions[0].Path != "hooks/hooks.json" {
		t.Fatalf("wrong package result: %+v %v", out, err)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "must-not-") || strings.Contains(string(b), "trust_status") {
		t.Fatal("package metadata exposed command, user state or trust policy")
	}
	lines, _ := os.ReadFile(trace)
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if method, ok := row["method"].(string); ok {
			methods = append(methods, method)
		} else if profile, ok := row["profile"].(string); ok {
			if row["private_env_present"] != false || profile == i.Profile || row["home"] == os.Getenv("HOME") {
				t.Fatal("inspector inherited user state or credential environment")
			}
			if _, err := os.Stat(filepath.Dir(profile)); !os.IsNotExist(err) {
				t.Fatal("owned temporary tree retained after inspection")
			}
		}
	}
	if strings.Join(methods, ",") != "initialize,initialized,hooks/list" {
		t.Fatalf("unexpected calls: %v", methods)
	}
}

func TestCodexPluginHookInspectionRejectsIncompleteAndUnexpectedProtocols(t *testing.T) {
	for _, mode := range []string{"version", "huge-version", "profile", "server-version", "empty", "extra", "duplicate", "cwd", "warning", "missing-warnings", "outside", "trust", "missing-async", "hash", "event", "cache-drift", "request", "hook-event", "oversize", "reply", "trailing-hook", "timeout", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			pkg := hookInspectionPackage(t)
			binary, trace := hookInspectionBinary(t, mode)
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if mode == "timeout" || mode == "descendant" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
			}
			defer cancel()
			out, err := NewCodex().InspectPluginHooks(ctx, Instance{ID: "codex", Kind: "codex", ServerExecutable: binary}, pkg)
			if err == nil || len(out.Definitions) != 0 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe protocol accepted or leaked: %+v %v", out, err)
			}
			entries, _ := os.ReadDir(tmp)
			if len(entries) != 0 {
				t.Fatal("failed inspection retained temporary files")
			}
			if mode == "descendant" {
				body, _ := os.ReadFile(trace)
				var child int
				for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
					var row struct {
						PID int `json:"descendant_pid"`
					}
					_ = json.Unmarshal([]byte(line), &row)
					if row.PID != 0 {
						child = row.PID
					}
				}
				if child == 0 {
					t.Fatal("descendant cleanup scenario did not start")
				}
				status, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat"))
				if readErr == nil && !strings.Contains(string(status), ") Z ") {
					t.Fatal("owned descendant still running after timeout")
				}
			}
		})
	}
}

func TestCodexPluginHookInspectionRejectsSourceDriftBeforeLaunching(t *testing.T) {
	pkg := hookInspectionPackage(t)
	binary, trace := hookInspectionBinary(t, "valid")
	if err := os.WriteFile(filepath.Join(pkg.SourcePath, "hooks/hooks.json"), []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodex().InspectPluginHooks(context.Background(), Instance{ID: "codex", Kind: "codex", ServerExecutable: binary}, pkg); err == nil {
		t.Fatal("source drift accepted")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("native process launched before source version validation")
	}
}

func TestCodexPluginHookInspectionCleansDescendantAfterNormalExit(t *testing.T) {
	pkg := hookInspectionPackage(t)
	binary, trace := hookInspectionBinary(t, "detached-output")
	_, err := NewCodex().InspectPluginHooks(context.Background(), Instance{ID: "codex", Kind: "codex", ServerExecutable: binary}, pkg)
	body, readErr := os.ReadFile(trace)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var child int
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var row struct {
			PID int `json:"descendant_pid"`
		}
		_ = json.Unmarshal([]byte(line), &row)
		if row.PID != 0 {
			child = row.PID
		}
	}
	if child == 0 {
		t.Fatal("normal-exit descendant scenario did not start")
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat"))
		if os.IsNotExist(readErr) || strings.Contains(string(status), ") Z ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("owned descendant still running after successful inspection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCodexPluginHookInspectionCleansReadOnlyCopiesWithoutChangingSource(t *testing.T) {
	pkg := hookInspectionPackage(t)
	for n := range pkg.Files {
		if pkg.Files[n].Kind == "directory" {
			pkg.Files[n].Mode = 0500
			if err := os.Chmod(filepath.Join(pkg.SourcePath, pkg.Files[n].Path), 0500); err != nil {
				t.Fatal(err)
			}
		}
	}
	pkg.RootMode = 0500
	if err := os.Chmod(pkg.SourcePath, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(pkg.SourcePath, 0700)
		for _, f := range pkg.Files {
			if f.Kind == "directory" {
				_ = os.Chmod(filepath.Join(pkg.SourcePath, f.Path), 0700)
			}
		}
	})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	binary, _ := hookInspectionBinary(t, "valid")
	if _, err := NewCodex().InspectPluginHooks(context.Background(), Instance{ID: "codex", Kind: "codex", ServerExecutable: binary}, pkg); err != nil {
		t.Fatal(err)
	}
	entries, _ := filepath.Glob(filepath.Join(tmp, "netcap-codex-hook-inspection-*"))
	info, _ := os.Stat(pkg.SourcePath)
	if len(entries) != 0 || info.Mode().Perm() != 0500 {
		t.Fatal("read-only copy leaked or source directory permissions changed")
	}
}
