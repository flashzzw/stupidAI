package runtime

import (
	"strings"
	"testing"
)

func TestClaudeRecoveryProofAndFailureIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "missing-proof", "unverified", "missing-operation", "invalid-time", "missing-history", "wrong-original", "candidate-active", "failure", "failure-old-id", "failure-secret-reason", "failure-baseline"} {
		t.Run(mode, func(t *testing.T) {
			failed, s := configurationFailureFixture()
			in := Intent{ID: "recovery", Kind: "recover_execution", Scope: *failed.PreviousScope, Permission: failed.Permission, ConfigurationBaseline: failed.ConfigurationBaseline, ConfigurationRecoveryOf: failed.ID, ConfigurationTarget: &failed.Scope}
			if _, e := recoveryOriginalIntent(in); e != nil {
				t.Fatal(e)
			}
			s.Entries = []claudeEntry{{Kind: "initialized"}}
			s.Configuration.Status = "recovered"
			s.Configuration.Recovery = &claudeConfigurationRecovery{Operation: in.ID, Status: "verified", At: "2026-10-06T00:00:00Z", HistoryDigest: strings.Repeat("e", 64), Inventory: claudeMCPInventory{Available: true, Servers: []claudeMCPStatus{}}}
			switch mode {
			case "missing-proof":
				s.Configuration.Recovery = nil
			case "unverified":
				s.Configuration.Recovery.Status = "recovering"
			case "missing-operation":
				s.Configuration.Recovery.Operation = ""
			case "invalid-time":
				s.Configuration.Recovery.At = "invalid"
			case "missing-history":
				s.Configuration.Recovery.HistoryDigest = ""
			case "wrong-original":
				s.Configuration.SettingsPrevious = nil
			case "candidate-active":
				in.Scope = failed.Scope
			}
			if strings.HasPrefix(mode, "failure") {
				s.Configuration.Status = "applying"
				f := *s.ConfigurationFailure
				f.Operation = in.ID
				f.Reason = "stage_failed"
				s.ConfigurationRecoveryFailure = &f
				switch mode {
				case "failure-old-id":
					f.Operation = failed.ID
				case "failure-secret-reason":
					f.Reason = "private"
				case "failure-baseline":
					f.Baseline = "wrong"
				}
				got := claudeRecoveryFailure(in, s)
				if (got != nil) != (mode == "failure") {
					t.Fatalf("incorrect failure binding: %+v", got)
				}
				if got != nil && (!strings.Contains(got.Detail, "恢复失败") || strings.Contains(got.Detail, "PRIVATE")) {
					t.Fatal("invalid diagnostic detail")
				}
				return
			}
			v := projectClaude(in.Scope, s)
			if v.ExecutionClear != (mode == "valid") {
				t.Fatalf("recovery proof accepted=%v", v.ExecutionClear)
			}
		})
	}
}
