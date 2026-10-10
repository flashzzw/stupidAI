package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestCodexExecutorEvidenceUsesBoundProcessAndDeclaredDistro(t *testing.T) {
	for _, scenario := range []string{"declared", "missing", "launch_last_value", "invalid", "foreign_profile"} {
		t.Run(scenario, func(t *testing.T) {
			profile := t.TempDir()
			env := []string{"CODEX_HOME=" + profile, "USER=must-not-be-used", "UNRELATED_SECRET=must-not-leak"}
			switch scenario {
			case "declared", "foreign_profile":
				env = append(env, "WSL_DISTRO_NAME=Ubuntu-fixture")
			case "launch_last_value":
				env = append(env, "WSL_DISTRO_NAME=A", "WSL_DISTRO_NAME=B")
			case "invalid":
				env = append(env, "WSL_DISTRO_NAME=bad\nvalue")
			}
			peer, path := ownedNamespacePeer(t, env)
			instance := Instance{Profile: profile, Socket: path}
			if scenario == "foreign_profile" {
				instance.Profile = t.TempDir()
			}
			connection := &verifiedCodexConnection{peer: peer}
			result, err := connection.ExecutorEvidence(context.Background(), instance)
			if scenario == "foreign_profile" {
				if err == nil || result != nil {
					t.Fatal("foreign namespace became backend environment evidence")
				}
				return
			}
			if err != nil || result.PID != peer.pid || result.Birth != peer.birth || result.UID != strconv.Itoa(os.Geteuid()) || result.At == "" || result.Executable == "" || result.Source != "codex.unix_peer" {
				t.Fatalf("backend evidence: %+v %v", result, err)
			}
			want := ""
			if scenario == "declared" {
				want = "Ubuntu-fixture"
			}
			if scenario == "launch_last_value" {
				want = "B"
			} // exec.Cmd deduplicates its launch environment.
			if result.WSLDistro != want {
				t.Fatalf("unexpected distro declaration: %q", result.WSLDistro)
			}
			data, _ := json.Marshal(result)
			if strings.Contains(string(data), "must-not") {
				t.Fatal("untrusted USER or unrelated environment leaked")
			}
		})
	}
}
