package runtime

import (
	"fmt"
	"regexp"
	"slices"
	"time"
)

type claudeConfigurationProgress struct {
	Version int    `json:"version"`
	Phase   string `json:"phase"`
}

// This mode retries an unchanged original query that failed before any
// candidate start. Its first complete inventory belongs to the recovery,
// never to the failed transaction's missing original inventory.
type ClaudeOriginalQueryRecovery struct {
	Version int                  `json:"version"`
	Failure ConfigurationFailure `json:"failure"`
}

var claudeRecoveryOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func validClaudeOriginalQueryRecovery(proof *ClaudeOriginalQueryRecovery, operation, baseline string) bool {
	if proof == nil || proof.Version != 1 {
		return false
	}
	f := proof.Failure
	_, err := time.Parse(time.RFC3339Nano, f.At)
	return f.Version == 1 && f.Operation == operation && f.Baseline == baseline && claudeRecoveryOperationID.MatchString(f.Operation) &&
		claudeSettingsFileSHA.MatchString(f.Baseline) && len(f.At) <= 64 && err == nil && f.Detail == "" && f.Cleanup == "confirmed" && f.Reason == "stage_failed" &&
		(f.Stage == "open_original" || f.Stage == "check_original_components")
}

// Detail is derived locally for display; all native fields must still match
// the exact original failure, including its timestamp and confirmed exit.
func ClaudeOriginalQueryRecoveryMatchesFailure(proof *ClaudeOriginalQueryRecovery, failure *ConfigurationFailure) bool {
	if failure == nil || !validClaudeOriginalQueryRecovery(proof, failure.Operation, failure.Baseline) {
		return false
	}
	f := *failure
	f.Detail = ""
	return proof.Failure == f
}

func claudeOriginalQueryRecoveryMatches(change *claudeConfigurationChange, proof *ClaudeOriginalQueryRecovery) bool {
	return change != nil && (change.Status == "applying" || change.Status == "recovered") && change.Progress != nil &&
		change.Progress.Version == 1 && change.Progress.Phase == "original" && change.OriginalPlugins == nil && !change.originalPluginsPresent &&
		(change.InstalledTarget != nil || change.InstalledPrevious != nil) && validClaudeOriginalQueryRecovery(proof, change.Operation, change.Baseline)
}

func validateClaudeOriginalQueryRecoveryCheck(in Intent, s claudeSnapshot, out ConfigurationRecoveryCheck) error {
	c, proof := s.Configuration, out.OriginalQueryRetry
	if proof == nil {
		if c != nil && (c.InstalledTarget != nil || c.InstalledPrevious != nil) && (c.OriginalPlugins == nil || !c.OriginalPlugins.Available) &&
			slices.ContainsFunc(out.Checks, func(condition RecoveryCondition) bool {
				return condition.Kind == "original_settings" && condition.Status == "passed"
			}) {
			return fmt.Errorf("恢复核对缺少原组件清单或候选启动前的失败依据")
		}
		return nil
	}
	if !claudeOriginalQueryRecoveryMatches(c, proof) || c.Status != "applying" || c.Recovery != nil ||
		!ClaudeOriginalQueryRecoveryMatchesFailure(proof, claudeConfigurationFailure(in, s)) || in.PreviousScope == nil {
		return fmt.Errorf("原查询恢复方式与持久阶段或原失败记录不一致")
	}
	previous := *in.PreviousScope
	if s.Live || s.Active != "" || !claudeInstalledConfigMatches(previous, s) || !slices.Equal(previous.ClaudeSettingsSources, s.Config.SettingsSources) ||
		!slices.Equal(previous.MCPSelections, c.Previous) || !slices.Equal(previous.MCPSelections, s.Config.MCPSelections) ||
		!slices.Equal(previous.SkillSelections, c.SkillPrevious) || !slices.Equal(previous.SkillSelections, s.Config.SkillSelections) ||
		!EqualPluginSelections(previous.PluginSelections, c.PluginPrevious) || !EqualPluginSelections(previous.PluginSelections, s.Config.PluginSelections) {
		return fmt.Errorf("候选启动前的恢复没有保持已退出的原配置绑定")
	}
	return nil
}
