package runtime

import (
	"encoding/json"
	"testing"
)

func TestInstalledSettingsAdoptionPreservesNonLocalStartupEvidence(t *testing.T) {
	local := []ClaudeSettingsSource{{Path: "/owned/settings.local.json", Kind: "absent"}}
	old := ClaudeStartupSettings{SDKVersion: "0.3.280", Version: 1, Cwd: "/owned", ReadComplete: true,
		Files:    []ClaudeStartupFile{{Path: "/etc/claude-code/managed-settings.json", Status: "read", SHA256: "policy", IdentitySHA256: "original-inode"}, {Path: local[0].Path, Status: "absent"}},
		Sources:  []ClaudeStartupLayer{{Source: "user", Path: "/profile/settings.json", SettingsSHA256: "user"}},
		Registry: []ClaudeStartupRegistry{{Hive: "hklm", Outcome: "absent"}}, BoundLocalFiles: local}
	for _, scenario := range []string{"local", "policy", "identity", "registry", "source-layer", "path-binding", "helper", "graph"} {
		t.Run(scenario, func(t *testing.T) {
			body, _ := json.Marshal(old)
			var next ClaudeStartupSettings
			if err := json.Unmarshal(body, &next); err != nil {
				t.Fatal(err)
			}
			next.At = "new sampling time"
			next.Files[1] = ClaudeStartupFile{Path: local[0].Path, Status: "read", SHA256: "new-local", IdentitySHA256: "new-local-inode"}
			next.Sources = append(next.Sources, ClaudeStartupLayer{Source: "local", Path: local[0].Path, SettingsSHA256: "new-local"})
			next.BoundLocalFiles[0].Kind = "file"
			switch scenario {
			case "policy":
				next.Files[0].SHA256 = "new-policy"
			case "identity":
				next.Files[0].IdentitySHA256 = "replaced-same-content"
			case "registry":
				next.Registry[0].Outcome = "present"
			case "source-layer":
				next.Sources[0].SettingsSHA256 = "new-user"
			case "path-binding":
				next.PathBindings = []ClaudeStartupPathBinding{{Path: "/profile/settings.json", ResolvedPath: "/another/settings.json"}}
			case "helper":
				next.PolicyHelpersPresent = true
			case "graph":
				next.Files = append(next.Files, ClaudeStartupFile{Path: "/new/managed.json", Status: "absent"})
			}
			if sameClaudeNonLocalStartup(old, next, local) != (scenario == "local") {
				t.Fatal("local adoption changed the acceptance of an independently bound startup source")
			}
		})
	}
}
