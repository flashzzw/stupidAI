//go:build linux && nativeclaude

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The companion Python probe binds an owned policy directory into a private
// mount namespace. This test never writes a host /etc policy file.
func TestClaudeNativePolicyHelperAdmission(t *testing.T) {
	base, cli := os.Getenv("NETCAP_CLAUDE_POLICY_TEST_ROOT"), os.Getenv("NETCAP_CLAUDE_NATIVE_TEST")
	if base == "" || cli == "" {
		t.Skip("use tests/claude_policy_helper_admission_probe.py for the owned policy namespace")
	}
	policy := filepath.Join(base, "policy")
	owned, ownErr := os.Lstat(policy)
	mounted, mountErr := os.Stat("/etc/claude-code")
	namespace, nsErr := os.Readlink("/proc/self/ns/mnt")
	parentNamespace := os.Getenv("NETCAP_POLICY_PARENT_NAMESPACE")
	if ownErr != nil || mountErr != nil || !owned.IsDir() || !os.SameFile(owned, mounted) || nsErr != nil || parentNamespace == "" || parentNamespace == namespace {
		t.Fatal("owned policy directory must be mounted in a separate namespace")
	}
	repository, _ := filepath.Abs("../..")
	bridge := os.Getenv("NETCAP_CLAUDE_POLICY_BRIDGE")
	if bridge == "" {
		bridge = filepath.Join(repository, "bridges/claude/dist/main.js")
	}
	root, err := os.MkdirTemp(base, "netcap-policy-admission-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	checks, reports := map[string]bool{}, map[string]any{}
	write := func(path string, value any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(value, "", "  ")
		if s, ok := value.(string); ok {
			body, err = []byte(s), nil
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		write(filepath.Join(root, "result.json"), map[string]any{"checks": checks, "reports": reports, "passed": !t.Failed(),
			"namespace": namespace, "parent_namespace": parentNamespace, "policy_directory": policy,
			"scope": "production SDK reader and Runtime creation/preview admission; fixed native CLI behind an exec-only trace; owned Linux policy namespace, no host policy or model input"})
	})
	for _, mode := range []string{"ordinary", "singular", "plural-linux", "plural-script", "plural-payload", "plural-default", "plural-other-platform", "both", "bindings-api-helper", "bindings-route", "bindings-key", "bindings-backend", "bindings-state", "bindings-home", "bindings-project-name", "bindings-xdg", "bindings-invalid-env", "bindings-benign"} {
		t.Run(mode, func(t *testing.T) {
			check := func(name string, ok bool) {
				t.Helper()
				checks[name] = ok
				if !ok {
					t.Errorf("failed: %s", name)
				}
			}
			caseDir := filepath.Join(root, mode)
			cwd, profile, custom := filepath.Join(caseDir, "work"), filepath.Join(caseDir, "profile"), filepath.Join(caseDir, "custom/original.json")
			stateDir := filepath.Join(caseDir, "state")
			for _, dir := range []string{cwd, profile, stateDir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := exec.Command("git", "init", "--quiet", cwd).Run(); err != nil {
				t.Fatal(err)
			}
			write(custom, map[string]any{"env": map[string]string{"OWNED_POLICY_SECRET": "PRIVATE_POLICY_VALUE"}})
			original, _ := os.ReadFile(custom)
			marker, calls := filepath.Join(caseDir, "helper-marker"), filepath.Join(caseDir, "native-calls.jsonl")
			helper := filepath.Join(caseDir, "helper.py")
			write(helper, fmt.Sprintf("#!/usr/bin/python3\nfrom pathlib import Path\nimport json\nwith Path(%q).open('a') as f:f.write('owned-helper\\n')\nprint(json.dumps({'managedSettings':{'permissions':{'deny':['Read(/owned-policy-output/**)']}}}))\n", marker))
			if err := os.Chmod(helper, 0700); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"permissions": map[string]any{"deny": []string{"Read(/owned-policy-payload/**)"}}}
			entry := map[string]any{"path": helper, "timeoutMs": 3000}
			source := map[string]any{}
			switch mode {
			case "ordinary":
				source = payload
			case "singular":
				source["policyHelper"] = entry
			case "plural-linux", "both":
				source["policyHelpers"] = map[string]any{"linux": entry}
				if mode == "both" {
					source["policyHelper"] = entry
				}
			case "plural-script":
				source["policyHelpers"] = map[string]any{"linux": map[string]any{"script": fmt.Sprintf("/usr/bin/python3 %q", helper), "interpreter": "sh", "timeoutMs": 3000}}
			case "plural-payload":
				source["policyHelpers"] = map[string]any{"linux": map[string]any{"defaultSettings": payload}}
			case "plural-default":
				source["policyHelpers"] = map[string]any{"default": payload}
			case "plural-other-platform":
				source["policyHelpers"] = map[string]any{"windows": entry}
			case "bindings-api-helper":
				source["apiKeyHelper"] = "/usr/bin/python3 " + helper
			case "bindings-invalid-env":
				source["env"] = map[string]any{"OWNED_INVALID": 42}
			case "bindings-route", "bindings-key", "bindings-backend", "bindings-state", "bindings-home", "bindings-project-name", "bindings-xdg", "bindings-benign":
				key, value := "OWNED_BENIGN", "PRIVATE_MANAGED_BINDING_VALUE"
				switch mode {
				case "bindings-route":
					key, value = "ANTHROPIC_BASE_URL", "http://127.0.0.1:1"
				case "bindings-key":
					key = "ANTHROPIC_API_KEY"
				case "bindings-backend":
					key, value = "CLAUDE_CODE_USE_BEDROCK", "0"
				case "bindings-state":
					key, value = "CLAUDE_CONFIG_DIR", filepath.Join(caseDir, "other-profile")
				case "bindings-home":
					key, value = "HOME", filepath.Join(caseDir, "other-home")
				case "bindings-project-name":
					key, value = "CLAUDE_CODE_PROJECT_DIR_NAME", "owned-other-project"
				case "bindings-xdg":
					key, value = "XDG_CONFIG_HOME", filepath.Join(caseDir, "other-xdg")
				}
				if filepath.IsAbs(value) {
					if err := os.MkdirAll(value, 0700); err != nil {
						t.Fatal(err)
					}
				}
				source["env"] = map[string]string{key: value}
			}
			write(filepath.Join(policy, "managed-settings.json"), source)
			write(filepath.Join(caseDir, "policy-source.json"), source)
			wrapper := filepath.Join(caseDir, "native-cli")
			write(wrapper, fmt.Sprintf("#!/usr/bin/python3\nimport os,sys,json\nwith open(%q,'a') as f:f.write(json.dumps({'argv':sys.argv[1:]})+'\\n')\nos.execve(%q,[%q,*sys.argv[1:]],os.environ)\n", calls, cli, cli))
			if err := os.Chmod(wrapper, 0700); err != nil {
				t.Fatal(err)
			}
			scope := Scope{NativeID: "00000000-0000-4000-8000-000000000001", Cwd: cwd, Route: Route{Model: "claude-sonnet-4-6"},
				Instance: Instance{Kind: "claude", Profile: profile, SettingsFile: custom, Bridge: bridge, Executable: wrapper}}
			read, readErr := ReadClaudeStartupSettings(context.Background(), scope)
			encoded, _ := json.Marshal(read)
			var exposed map[string]any
			_ = json.Unmarshal(encoded, &exposed)
			prefix := mode + "_"
			check(prefix+"source_read_complete", readErr == nil && read.ReadComplete && !read.Complete)
			check(prefix+"singular_flag", exposed["policy_helper_present"] == (mode == "singular" || mode == "both"))
			check(prefix+"platform_flag", exposed["policy_helpers_present"] == (strings.HasPrefix(mode, "plural-") || mode == "both"))
			if strings.HasPrefix(mode, "bindings-") {
				conflicts, ok := exposed["startup_conflicts"].([]any)
				check(prefix+"binding_conflicts_reported", ok && (len(conflicts) > 0) == (mode != "bindings-benign"))
			}
			_, invoked := os.Stat(calls)
			check(prefix+"read_did_not_launch_native", os.IsNotExist(invoked))
			preview := PreviewClaudePluginSettings(context.Background(), stateDir, scope, ClaudePluginSetting{SourceKey: "owned@policy", Enabled: false})
			createErr := validateClaudeCreateSettings(context.Background(), scope)
			if mode == "ordinary" || mode == "bindings-benign" {
				check(prefix+"preview_matches", preview.Validation != nil && preview.Validation.SchemaValid && len(preview.Validation.PluginSettings) == 1 && preview.Validation.PluginSettings[0].Matches && preview.Validation.Preservation != nil && preview.Validation.Preservation.Matches && preview.SourcesStable)
				check(prefix+"preview_released", len(preview.Problems) == 0 && preview.Cleanup.State == "released")
				check(prefix+"creation_allowed", createErr == nil)
				body, _ := os.ReadFile(calls)
				var consumed []string
				metadataOnly := true
				for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
					var call struct {
						Args []string `json:"argv"`
					}
					if json.Unmarshal([]byte(line), &call) != nil {
						metadataOnly = false
						continue
					}
					if slices.Equal(call.Args, []string{"--version"}) {
						continue
					}
					flag := slices.Index(call.Args, "--settings")
					if flag < 0 || flag+1 >= len(call.Args) || !slices.Contains(call.Args, "--bare") || !slices.Contains(call.Args, "--no-session-persistence") {
						metadataOnly = false
						continue
					}
					consumed = append(consumed, call.Args[flag+1])
				}
				check(prefix+"real_native_validation_ran", metadataOnly && preview.Artifact != nil && slices.Equal(consumed, []string{custom, preview.Artifact.File.Path, custom}))
			} else {
				check(prefix+"preview_rejected_before_candidate", len(preview.Problems) > 0 && preview.Artifact == nil && preview.Validation == nil && preview.Cleanup.State == "not_created")
				check(prefix+"creation_rejected", createErr != nil)
				_, invoked = os.Stat(calls)
				check(prefix+"no_native_launch", os.IsNotExist(invoked))
			}
			_, helperErr := os.Stat(marker)
			check(prefix+"no_helper_execution", os.IsNotExist(helperErr))
			after, _ := os.ReadFile(custom)
			check(prefix+"original_settings_preserved", string(after) == string(original))
			check(prefix+"never_full_admission", !preview.Complete)
			check(prefix+"source_values_not_exported", !strings.Contains(string(encoded), "PRIVATE_POLICY_VALUE") && !strings.Contains(string(encoded), "owned-policy-payload") && !strings.Contains(string(encoded), "PRIVATE_MANAGED_BINDING_VALUE"))
			message := ""
			if createErr != nil {
				message = createErr.Error()
			}
			reports[mode] = map[string]any{"read": read, "preview": preview, "create_error": message, "case_directory": caseDir, "read_error": fmt.Sprint(readErr)}
		})
	}
}
