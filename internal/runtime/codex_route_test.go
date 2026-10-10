package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/codex"
)

type routeNativeFixture struct {
	*nativeFixture
	environment []byte
}

func TestCodexCredentialProcessHelper(t *testing.T) {
	if os.Getenv("NETCAP_CREDENTIAL_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestCodexCredentialReadTargetsOnlyOwnedPeerProcess(t *testing.T) {
	profile := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCredentialProcessHelper$")
	cmd.Env = []string{"NETCAP_CREDENTIAL_HELPER=1", "CODEX_HOME=" + profile, "NETCAP_OWNED_PEER_KEY=owned-fixture-secret", "NETCAP_OWNED_EMPTY=", "NETCAP_UNREQUESTED=unrelated-private-value"}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("owned credential process did not start: %q %v", line, err)
	}
	// The real socket peer handshake remains a separate integration check.
	// This file stands in only for its already-captured inode identity.
	path := filepath.Join(t.TempDir(), "peer-identity")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	peer := &codexPeer{file: info, pid: cmd.Process.Pid, birth: processBirth(cmd.Process.Pid)}
	p := &verifiedCodexConnection{peer: peer}
	instance := Instance{Profile: profile, Socket: path}
	if err := p.VerifyCredential(ctx, instance, "NETCAP_OWNED_PEER_KEY", "owned-fixture-secret"); err != nil {
		t.Fatal(err)
	}
	if err := p.VerifyCredential(ctx, instance, "NETCAP_OWNED_PEER_KEY", "rotated-secret"); err == nil {
		t.Fatal("stale daemon accepted rotated credential")
	}
	values, err := p.MCPEnvironment(ctx, instance, []string{"NETCAP_OWNED_PEER_KEY", "NETCAP_OWNED_EMPTY", "NETCAP_ABSENT"})
	if err != nil || len(values) != 2 || values["NETCAP_OWNED_PEER_KEY"] != "owned-fixture-secret" {
		t.Fatal("MCP environment read did not return exactly the requested present values")
	}
	if value, present := values["NETCAP_OWNED_EMPTY"]; !present || value != "" {
		t.Fatal("an explicitly empty variable was conflated with an absent one")
	}
	other := instance
	other.Profile = t.TempDir()
	if values, err := p.MCPEnvironment(ctx, other, []string{"NETCAP_OWNED_PEER_KEY"}); err == nil || values != nil || strings.Contains(err.Error(), "owned-fixture-secret") {
		t.Fatal("a different profile read or disclosed the peer credential")
	}
	cancelled, cancelRead := context.WithCancel(ctx)
	cancelRead()
	if values, err := p.MCPEnvironment(cancelled, instance, []string{"NETCAP_OWNED_PEER_KEY"}); err == nil || values != nil {
		t.Fatal("cancelled environment read returned credentials")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.VerifyCredential(ctx, instance, "NETCAP_OWNED_PEER_KEY", "owned-fixture-secret"); err == nil {
		t.Fatal("replaced peer identity accepted")
	}
	if values, err := p.MCPEnvironment(ctx, instance, []string{"NETCAP_OWNED_PEER_KEY"}); err == nil || values != nil {
		t.Fatal("MCP environment read accepted a replaced peer identity")
	}
}

func (f *routeNativeFixture) VerifyCredential(_ context.Context, instance Instance, key, value string) error {
	return verifyCodexEnvironment(f.environment, instance.Profile, key, value)
}

func codexRouteHarness(t *testing.T) (*Codex, *routeNativeFixture, Scope) {
	t.Helper()
	c, native, scope := nativeHarness(t)
	scope.Instance.Profile = t.TempDir()
	scope.Route = Route{Provider: "fixture", Model: "model", Endpoint: "http://127.0.0.1:9011/v1", CredentialRef: "env:NETCAP_ROUTE_KEY_A"}
	t.Setenv("NETCAP_ROUTE_KEY_A", "fixture-secret-A")
	t.Setenv("NETCAP_ROUTE_KEY_B", "fixture-secret-B")
	scope.CredentialStamp, _ = CredentialStamp(scope.Route)
	f := &routeNativeFixture{nativeFixture: native, environment: []byte("CODEX_HOME=" + scope.Instance.Profile + "\x00NETCAP_ROUTE_KEY_A=fixture-secret-A\x00NETCAP_ROUTE_KEY_B=fixture-secret-B\x00")}
	c.clients[scope.Instance.ID] = f
	return c, f, scope
}

func TestCodexCreateAndResumePinAllRouteOverrides(t *testing.T) {
	c, f, original := codexRouteHarness(t)
	nativeConfig := map[string]any{"name": "fixture", "base_url": "http://global.example/v1", "env_key": "NETCAP_ROUTE_KEY_A", "requires_openai_auth": true}
	for _, key := range []string{"NETCAP_ROUTE_KEY_A", "NETCAP_ROUTE_KEY_B"} {
		t.Run(key, func(t *testing.T) {
			scope := original
			scope.RecoveryPermission = "read_only"
			scope.Route.CredentialRef = "env:" + key
			scope.Route.Endpoint = "http://127.0.0.1:9012/" + key
			scope.CredentialStamp, _ = CredentialStamp(scope.Route)
			created, resumed, loaded := false, false, false
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "config/read":
					return map[string]any{"config": map[string]any{"model_providers": map[string]any{"fixture": nativeConfig}}}, nil
				case "thread/start", "thread/resume":
					cfg := params["config"].(map[string]any)
					if cfg["model_providers.fixture.base_url"] != scope.Route.Endpoint || cfg["model_providers.fixture.env_key"] != key || cfg["model_providers.fixture.requires_openai_auth"] != false || params["model"] != "model" || params["modelProvider"] != "fixture" {
						t.Fatalf("route override omitted: %+v", params)
					}
					encoded, _ := json.Marshal(params)
					if strings.Contains(string(encoded), "fixture-secret") {
						t.Fatal("raw credential entered RPC config")
					}
					if method == "thread/start" {
						created = true
					} else {
						resumed, loaded = true, true
					}
					return codex.Started{Thread: codex.Thread{ID: "native", Status: codex.Status{Type: "idle"}}, Model: "model", Provider: "fixture", ApprovalPolicy: json.RawMessage(`"on-request"`), Sandbox: json.RawMessage(`{"type":"readOnly","networkAccess":false}`)}, nil
				case "thread/read":
					status := "notLoaded"
					if loaded {
						status = "idle"
					}
					return map[string]any{"thread": map[string]any{"id": "native", "status": map[string]string{"type": status}, "turns": []any{}}}, nil
				}
				return emptyControl(method)
			}
			if _, err := c.Execute(context.Background(), Intent{ID: key, Kind: "create", Scope: scope, Permission: "read_only"}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Observe(context.Background(), scope); err != nil {
				t.Fatal(err)
			}
			if !created || !resumed {
				t.Fatal("did not exercise creation and unloaded recovery")
			}
			if nativeConfig["env_key"] != "NETCAP_ROUTE_KEY_A" {
				t.Fatal("thread override mutated shared config")
			}
		})
	}
}

func TestCodexConflictingAuthenticationNeverReachesNativeWrite(t *testing.T) {
	for _, raw := range []string{
		`{"experimental_bearer_token":"secret-extra"}`, `{"auth":{"command":"helper"}}`, `{"aws":{}}`, `{"gateway_oauth":{}}`,
		`{"http_headers":{"AUTHORIZATION":"secret-extra"}}`, `{"env_http_headers":{"x-api-key":"KEY_OTHER"}}`, `{"query_params":{"api_key":"secret-extra"}}`, `null`,
	} {
		t.Run(raw, func(t *testing.T) {
			c, f, scope := codexRouteHarness(t)
			f.call = func(method string, _ map[string]any) (any, error) {
				if method != "config/read" {
					t.Fatalf("native mutation occurred before auth validation: %s", method)
				}
				return map[string]any{"config": map[string]any{"model_providers": map[string]any{"fixture": json.RawMessage(raw)}}}, nil
			}
			_, err := c.Execute(context.Background(), Intent{Kind: "create", Scope: scope})
			var notSent *NotSent
			if !errors.As(err, &notSent) || strings.Contains(err.Error(), "secret-extra") {
				t.Fatalf("conflicting auth was accepted or exposed: %v", err)
			}
		})
	}
}

func TestCodexDaemonAndSessionCredentialRotationAreSeparateChecks(t *testing.T) {
	c, f, scope := codexRouteHarness(t)
	f.call = func(method string, _ map[string]any) (any, error) {
		if method != "config/read" {
			t.Fatalf("unexpected native call on changed credential: %s", method)
		}
		return map[string]any{"config": map[string]any{"model_providers": map[string]any{"fixture": map[string]any{}}}}, nil
	}
	t.Setenv("NETCAP_ROUTE_KEY_A", "rotated-secret")
	for _, freshScope := range []bool{false, true} {
		candidate := scope
		if freshScope {
			candidate.CredentialStamp, _ = CredentialStamp(candidate.Route)
		}
		_, err := c.Execute(context.Background(), Intent{Kind: "send", Scope: candidate, Text: "work"})
		var notSent *NotSent
		if !errors.As(err, &notSent) || strings.Contains(err.Error(), "rotated-secret") {
			t.Fatalf("rotation did not block native input: %v", err)
		}
		if freshScope && !strings.Contains(err.Error(), "后台未采用") {
			t.Fatalf("fresh session did not verify daemon environment: %v", err)
		}
	}
}

func TestCodexEnvironmentRequiresMatchingProfileAndUniqueCredential(t *testing.T) {
	profile := t.TempDir()
	for _, data := range []string{
		"CODEX_HOME=" + profile + "\x00KEY=value\x00KEY=value\x00",
		"CODEX_HOME=" + profile + "\x00KEY=other\x00",
		"CODEX_HOME=" + t.TempDir() + "\x00KEY=value\x00",
		"HOME=" + t.TempDir() + "\x00KEY=value\x00",
		"CODEX_HOME=" + profile + "\x00KEY=value",
	} {
		if err := verifyCodexEnvironment([]byte(data), profile, "KEY", "value"); err == nil {
			t.Fatal("ambiguous/wrong daemon environment accepted")
		}
	}
	alias := filepath.Join(t.TempDir(), "profile-link")
	if err := os.Symlink(profile, alias); err != nil {
		t.Fatal(err)
	}
	if err := verifyCodexEnvironment([]byte("CODEX_HOME="+alias+"\x00KEY=value\x00"), profile, "KEY", "value"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(profile))
	if err := verifyCodexEnvironment([]byte("CODEX_HOME="+filepath.Base(profile)+"\x00KEY=value\x00"), profile, "KEY", "value"); err == nil {
		t.Fatal("resolved daemon's relative profile against controller cwd")
	}
}

func TestCodexChangedNativeRoutePreventsNewTurn(t *testing.T) {
	for _, field := range []string{"model", "modelProvider", "cwd"} {
		t.Run(field, func(t *testing.T) {
			c, f, scope := codexRouteHarness(t)
			f.call = func(method string, _ map[string]any) (any, error) {
				if method == "config/read" {
					return map[string]any{"config": map[string]any{"model_providers": map[string]any{"fixture": map[string]any{}}}}, nil
				}
				if method == "thread/read" {
					thread := map[string]any{"id": scope.NativeID, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "cwd": scope.Cwd, "status": map[string]string{"type": "idle"}, "turns": []any{}}
					thread[field] = "changed"
					return map[string]any{"thread": thread}, nil
				}
				if method == "turn/start" {
					t.Fatal("changed native route received model input")
				}
				if method == "thread/resume" {
					return codex.Started{Thread: codex.Thread{ID: scope.NativeID, Status: codex.Status{Type: "idle"}}, Model: scope.Route.Model, Provider: scope.Route.Provider, ApprovalPolicy: json.RawMessage(`"on-request"`), Sandbox: json.RawMessage(`{"type":"readOnly"}`)}, nil
				}
				return emptyControl(method)
			}
			_, err := c.Execute(context.Background(), Intent{Kind: "send", Scope: scope, Permission: "read_only", Text: "work"})
			var notSent *NotSent
			if !errors.As(err, &notSent) || !strings.Contains(err.Error(), "已变化") {
				t.Fatalf("native drift not caught: %v", err)
			}
		})
	}
}
