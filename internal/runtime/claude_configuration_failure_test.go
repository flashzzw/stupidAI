package runtime

import (
	"strings"
	"testing"
)

func configurationFailureFixture() (Intent, claudeSnapshot) {
	old := []ClaudeSettingsSource{{Path: "/repo/.claude/settings.json", ResolvedPath: "/repo/.claude/settings.json", Kind: "absent"}, {Path: "/repo/.claude/settings.local.json", ResolvedPath: "/repo/.claude/settings.local.json", Kind: "file", SHA256: strings.Repeat("a", 64), Mode: 0600}}
	target := append([]ClaudeSettingsSource{}, old...)
	target[1].SHA256 = strings.Repeat("b", 64)
	in := Intent{ID: "operation", Kind: "apply_execution", ConfigurationBaseline: strings.Repeat("c", 64), Permission: "read_only", Scope: Scope{NativeID: "native", Instance: Instance{Kind: "claude", Executable: "claude"}, Cwd: "/repo", Route: Route{Model: "fixture", Endpoint: "http://127.0.0.1:1"}, SettingsGuardVersion: 1, ClaudeSettingsSources: target}}
	previous := in.Scope
	previous.ClaudeSettingsSources = old
	in.PreviousScope = &previous
	var s claudeSnapshot
	s.Config.ID, s.Config.Cwd, s.Config.Model, s.Config.Endpoint, s.Config.Permission, s.Config.Executable = "native", "/repo", "fixture", "http://127.0.0.1:1", "read_only", "claude"
	s.Config.SettingsGuardVersion, s.Config.SettingsSources = 1, old
	s.Configuration = &claudeConfigurationChange{Operation: in.ID, Baseline: in.ConfigurationBaseline, Status: "applying", SettingsTarget: target, SettingsPrevious: old}
	s.ConfigurationFailure = &ConfigurationFailure{Version: 1, Operation: in.ID, Baseline: in.ConfigurationBaseline, Stage: "check_settings", Reason: "settings_parse_errors", Cleanup: "confirmed", At: "2026-10-06T00:00:00Z", Detail: "PRIVATE_NATIVE_ERROR"}
	return in, s
}

func TestClaudeConfigurationFailureBinding(t *testing.T) {
	for _, mode := range []string{"valid-old", "valid-target", "operation", "empty-operation", "baseline", "empty-baseline", "version", "reason", "cleanup", "timestamp", "target", "missing-target-with-candidate-config", "previous", "native-config", "identity", "no-previous"} {
		t.Run(mode, func(t *testing.T) {
			in, s := configurationFailureFixture()
			switch mode {
			case "valid-target":
				s.Config.SettingsSources = in.Scope.ClaudeSettingsSources
			case "operation":
				s.ConfigurationFailure.Operation = "other"
			case "empty-operation":
				in.ID, s.Configuration.Operation, s.ConfigurationFailure.Operation = "", "", ""
			case "baseline":
				s.ConfigurationFailure.Baseline = "other"
			case "empty-baseline":
				in.ConfigurationBaseline, s.Configuration.Baseline, s.ConfigurationFailure.Baseline = "", "", ""
			case "version":
				s.ConfigurationFailure.Version = 2
			case "reason":
				s.ConfigurationFailure.Reason = "PRIVATE_NATIVE_ERROR"
			case "cleanup":
				s.ConfigurationFailure.Cleanup = "maybe"
			case "timestamp":
				s.ConfigurationFailure.At = "invalid"
			case "target":
				s.Configuration.SettingsTarget = nil
			case "missing-target-with-candidate-config":
				s.Configuration.SettingsTarget = nil
				s.Config.SettingsSources = in.Scope.ClaudeSettingsSources
			case "previous":
				s.Configuration.SettingsPrevious = nil
			case "native-config":
				s.Config.SettingsSources = nil
			case "identity":
				s.Config.ID = "other"
			case "no-previous":
				in.PreviousScope = nil
			}
			got := claudeConfigurationFailure(in, s)
			if strings.HasPrefix(mode, "valid") {
				if got == nil || strings.Contains(got.Detail, "PRIVATE") || !strings.Contains(got.Detail, "解析错误") {
					t.Fatalf("invalid sanitized diagnostic: %+v", got)
				}
				view := projectClaude(in.Scope, s)
				if view.ExecutionClear || view.Evidence.Complete {
					t.Fatal("diagnostic promoted unknown configuration")
				}
			} else if got != nil {
				t.Fatalf("unbound diagnostic accepted: %+v", got)
			}
		})
	}
}
