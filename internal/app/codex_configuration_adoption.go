package app

import (
	"fmt"
	"path/filepath"
	"reflect"

	"codex-netcap/internal/runtime"
)

type CodexConfigurationChange struct {
	Settings runtime.ConfigurationSettingsDifference `json:"settings"`
	Files    []CodexConfigurationFileChange          `json:"files"`
}
type CodexConfigurationFileChange struct {
	Kind   string              `json:"kind"`
	Before *runtime.FileSource `json:"before"`
	After  *runtime.FileSource `json:"after"`
}

func (r ExecutionChangeRequest) adoptsCodexSources() bool {
	return r.AdoptCodexHookSources || r.AdoptCodexConfigurationSources
}
func (c *ExecutionChange) adoptsCodexSources() bool {
	return c != nil && (c.AdoptCodexHookSources || c.AdoptCodexConfigurationSources)
}

func (a *Application) verifyConfigurationAdoptionRoute(session Session) error {
	if err := a.verifyRouteRegistry(session); err != nil {
		return err
	}
	snapshot := session.ExecutionSnapshot
	if snapshot == nil || session.Scope.Instance.Kind != "codex" || session.Scope.Instance.SettingsFile != "" || snapshot.ProfileSource.Kind != "file" || snapshot.ProfileSource.Path != filepath.Join(session.Scope.Instance.Profile, "config.toml") || configurationProfileFingerprint(snapshot) != session.Fingerprint {
		return fmt.Errorf("配置采纳缺少原主配置与路由指纹依据")
	}
	fingerprint, err := routeFingerprintWithProfile(session.Scope.Instance, session.Scope.Route, session.Fingerprint)
	if err != nil || fingerprint != session.RouteFingerprint {
		return fmt.Errorf("配置采纳的原路由指纹不一致")
	}
	return nil
}

func configurationProfileFingerprint(snapshot *ExecutionSnapshot) string {
	if snapshot == nil || snapshot.CodexConfigurationSources == nil {
		return ""
	}
	for _, layer := range snapshot.CodexConfigurationSources.Layers {
		if layer.Kind == "user" && layer.File != nil && layer.File.Kind == "file" && layer.File.Path == snapshot.ProfileSource.Path && layer.File.ResolvedPath == snapshot.ProfileSource.Resolved {
			return layer.File.SHA256
		}
	}
	return ""
}

func (a *Application) captureCodexConfigurationAdoption(session Session, candidate *ExecutionSnapshot) (*CodexConfigurationChange, error) {
	previous := session.Scope.CodexConfigurationSources
	if previous == nil || previous.Settings == nil {
		return nil, fmt.Errorf("原快照未保存设置比较依据，不能补造原值后采纳")
	}
	sources, err := a.captureCodexConfigurationSources(session.Scope)
	if err != nil {
		return nil, err
	}
	project, err := a.captureCodexProjectHooks(session.Scope)
	if err != nil {
		return nil, err
	}
	candidate.CodexConfigurationSources, candidate.CodexProjectHooks = sources, project
	if !runtime.CodexConfigurationSourceAdoption(session.Scope, executionChangeScope(session.Scope, candidate)) {
		return nil, fmt.Errorf("配置采纳目前要求原值与目标 effort 均为已记录的非空值；其余设置、配置层结构、hook 文件和来源身份必须不变，且存在来源版本差异")
	}
	profile, err := stampSource(candidate.ProfileSource.Path, candidate.ProfileSource.Optional)
	if err != nil {
		return nil, err
	}
	candidate.ProfileSource = profile
	change := &CodexConfigurationChange{Settings: runtime.CompareConfigurationSettings(previous.Settings, sources.Settings), Files: []CodexConfigurationFileChange{}}
	change.Settings.BaseSnapshotID = session.ExecutionSnapshot.ID
	for i, layer := range previous.Layers {
		if !reflect.DeepEqual(layer.File, sources.Layers[i].File) {
			change.Files = append(change.Files, CodexConfigurationFileChange{Kind: layer.Kind, Before: queryCopy(layer.File), After: queryCopy(sources.Layers[i].File)})
		}
	}
	return change, nil
}
