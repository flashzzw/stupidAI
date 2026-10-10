package runtime

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use a real owned process environment; only the prior socket handshake and
// subsequent RPC replies are controlled fixtures. No native listener is used.
func ownedNamespacePeer(t *testing.T, environment []string) (*codexPeer, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCredentialProcessHelper$")
	cmd.Env = append([]string{"NETCAP_CREDENTIAL_HELPER=1"}, environment...)
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
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Wait() })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("owned process not ready: %q %v", line, err)
	}
	path := filepath.Join(t.TempDir(), "peer-identity")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return &codexPeer{file: info, pid: cmd.Process.Pid, birth: processBirth(cmd.Process.Pid)}, path
}

func TestCodexReadonlyRejectsForeignNamespaceBeforeRPC(t *testing.T) {
	for _, operation := range []string{"discover", "inspect", "history"} {
		t.Run(operation, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.Cwd, scope.Instance.Profile = t.TempDir(), t.TempDir()
			peer, socket := ownedNamespacePeer(t, []string{"CODEX_HOME=" + scope.Instance.Profile})
			scope.Instance.Socket = socket
			c.clients[scope.Instance.ID] = &verifiedCodexConnection{codexConnection: f, peer: peer}
			f.call = func(method string, _ map[string]any) (any, error) {
				thread := map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "status": map[string]string{"type": "notLoaded"}}
				switch method {
				case "thread/list":
					return map[string]any{"data": []any{thread}}, nil
				case "thread/read":
					return map[string]any{"thread": thread}, nil
				case "thread/turns/list":
					return map[string]any{"data": []any{}}, nil
				}
				return nil, fmt.Errorf("unexpected RPC %s", method)
			}
			read := func() error {
				request := DiscoveryRequest{Instance: scope.Instance, Cwd: scope.Cwd, Limit: 50}
				switch operation {
				case "discover":
					_, err := c.Discover(context.Background(), request)
					return err
				case "inspect":
					_, err := c.InspectNative(context.Background(), request, scope.NativeID)
					return err
				default:
					_, err := c.ReadNativeHistory(context.Background(), request, scope.NativeID)
					return err
				}
			}
			if err := read(); err != nil {
				t.Fatalf("matching namespace: %v", err)
			}
			// Same instance ID/socket/project/native ID, but a different saved state
			// directory. Matching native metadata cannot establish its namespace.
			scope.Instance.Profile = t.TempDir()
			c.pending[scope.Instance.ID+"/approval"] = Pending{Status: "pending"}
			f.methods = nil
			if err := read(); err == nil {
				t.Error("foreign daemon history accepted under configured namespace")
			}
			if len(f.methods) != 0 {
				t.Errorf("foreign namespace reached native RPC: %v", f.methods)
			}
			if !f.closed || c.clients[scope.Instance.ID] != nil {
				t.Error("unverified connection remained reusable")
			}
			if c.pending[scope.Instance.ID+"/approval"].Status != "unverified" {
				t.Error("stale approval retained authority")
			}
		})
	}
}

func TestCodexNamespaceEnvironmentBoundaries(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".codex")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(profile, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, data, profile string
		allowed             bool
	}{
		{"explicit_without_credentials", "CODEX_HOME=" + profile + "\x00", profile, true},
		{"home_fallback", "HOME=" + home + "\x00", profile, true},
		{"empty_override_fallback", "HOME=" + home + "\x00CODEX_HOME=\x00", profile, true},
		{"daemon_alias", "CODEX_HOME=" + alias + "\x00", profile, true},
		{"configured_alias", "CODEX_HOME=" + profile + "\x00", alias, true},
		{"explicit_over_home", "CODEX_HOME=" + profile + "\x00HOME=/unrelated\x00", profile, true},
		{"unselected_credentials", "CODEX_HOME=" + profile + "\x00KEY=secret-fixture\x00KEY=secret-fixture\x00", profile, true},
		{"duplicate_codex_home", "CODEX_HOME=" + profile + "\x00CODEX_HOME=" + profile + "\x00", profile, false},
		{"duplicate_home", "HOME=" + home + "\x00HOME=" + home + "\x00", profile, false},
		{"relative_codex_home", "CODEX_HOME=relative\x00HOME=" + home + "\x00", profile, false},
		{"relative_home", "HOME=relative\x00", profile, false},
		{"relative_requested", "CODEX_HOME=" + profile + "\x00", ".codex", false},
		{"foreign", "CODEX_HOME=" + t.TempDir() + "\x00", profile, false},
		{"missing", "KEY=secret-fixture\x00", profile, false},
		{"truncated", "CODEX_HOME=" + profile, profile, false},
		{"empty", "", profile, false},
		{"file", "CODEX_HOME=" + file + "\x00", file, false},
		{"nonexistent", "CODEX_HOME=" + home + "/missing\x00", home + "/missing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := parseCodexEnvironment([]byte(tc.data), "")
			if err == nil {
				_, err = verifyCodexProfile(selected, tc.profile)
			}
			if (err == nil) != tc.allowed {
				t.Fatalf("namespace acceptance=%t, want %t: %v", err == nil, tc.allowed, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-fixture") {
				t.Fatal("environment exposed")
			}
		})
	}
}

func TestCodexNamespacePeerPreflightAndRecheck(t *testing.T) {
	for _, change := range []string{"matching", "alias", "foreign", "retarget_alias", "replace_directory", "replace_socket", "process_birth", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(profile, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(profile, alias); err != nil {
				t.Fatal(err)
			}
			peer, socket := ownedNamespacePeer(t, []string{"CODEX_HOME=" + alias})
			instance := Instance{Profile: profile, Socket: socket}
			ctx := context.Background()
			if change == "foreign" {
				instance.Profile = t.TempDir()
				if err := peer.verifyNamespace(ctx, instance); err == nil {
					t.Fatal("foreign initial namespace accepted")
				}
				if peer.namespace != nil {
					t.Fatal("failed preflight recorded namespace")
				}
				return
			}
			if err := peer.verifyNamespace(ctx, instance); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "alias":
				instance.Profile = alias
			case "retarget_alias":
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), alias); err != nil {
					t.Fatal(err)
				}
				instance.Profile = alias
			case "replace_directory":
				if err := os.Rename(profile, profile+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(profile, 0700); err != nil {
					t.Fatal(err)
				}
			case "replace_socket":
				if err := os.Rename(socket, socket+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(socket, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "process_birth":
				peer.birth = "different-process-start"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := peer.verifyNamespace(ctx, instance)
			allowed := change == "matching" || change == "alias"
			if (err == nil) != allowed {
				t.Fatalf("recheck acceptance=%t, want %t: %v", err == nil, allowed, err)
			}
		})
	}
}
