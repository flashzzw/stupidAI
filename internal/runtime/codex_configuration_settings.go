package runtime

import (
	"fmt"
	"reflect"
	"slices"
)

// A disk comparison, never an attestation of the loaded thread's settings.
// Only the fixed public effort enum is retained as a value. The remaining
// parsed values and provenance (including hidden layers) stay behind hashes.
type ConfigurationSettings struct {
	Version                  int                 `json:"version"`
	ReasoningEffort          ConfigurationEffort `json:"reasoning_effort"`
	EffortValueSHA256        string              `json:"effort_value_sha256"`
	EffortSourcesSHA256      string              `json:"effort_sources_sha256"`
	OtherConfigurationSHA256 string              `json:"other_configuration_sha256"`
	OtherOriginsSHA256       string              `json:"other_origins_sha256"`
	OtherLayersSHA256        string              `json:"other_layers_sha256"`
}

type ConfigurationEffort struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
}

type ConfigurationSettingsDifference struct {
	BaseSnapshotID       string               `json:"base_snapshot_id"`
	Status               string               `json:"status"`
	Before               *ConfigurationEffort `json:"before,omitempty"`
	After                *ConfigurationEffort `json:"after,omitempty"`
	EffortChanged        bool                 `json:"effort_changed"`
	EffortSourcesChanged bool                 `json:"effort_sources_changed"`
	OtherValuesChanged   bool                 `json:"other_values_changed"`
	OtherSourcesChanged  bool                 `json:"other_sources_changed"`
}

func configurationEffort(value any) ConfigurationEffort {
	if value == nil {
		return ConfigurationEffort{State: "unset"}
	}
	if text, ok := value.(string); ok && slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh"}, text) {
		return ConfigurationEffort{State: "set", Value: text}
	}
	return ConfigurationEffort{State: "unrecognized"}
}

func configurationWithout(source map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		if !slices.Contains(keys, key) {
			out[key] = value
		}
	}
	return out
}

func configurationSettings(config, response map[string]any) *ConfigurationSettings {
	const effort = "model_reasoning_effort"
	origins := response["origins"].(map[string]any)
	otherOrigins := make(map[string]any, len(origins))
	for key, value := range origins {
		if key != effort {
			// Native versions cover the entire declaring layer. Changing only
			// effort changes that version even for unrelated field origins.
			otherOrigins[key] = configurationWithout(value.(map[string]any), "version")
		}
	}
	otherLayers, effortLayers := []any{}, []any{}
	for _, item := range response["layers"].([]any) {
		layer := item.(map[string]any)
		values := layer["config"].(map[string]any)
		other := configurationWithout(layer, "version", "config")
		other["config"] = configurationWithout(values, effort)
		otherLayers = append(otherLayers, other)
		value, present := values[effort]
		effortLayers = append(effortLayers, map[string]any{"name": layer["name"], "present": present, "value": value})
	}
	var effortOrigin any
	if origin, ok := origins[effort]; ok {
		effortOrigin = configurationWithout(origin.(map[string]any), "version")
	}
	return &ConfigurationSettings{
		Version: 1, ReasoningEffort: configurationEffort(config[effort]),
		EffortValueSHA256:        configurationDigest(config[effort]),
		EffortSourcesSHA256:      configurationDigest(map[string]any{"layers": effortLayers, "origin": effortOrigin}),
		OtherConfigurationSHA256: configurationDigest(configurationWithout(config, effort)),
		OtherOriginsSHA256:       configurationDigest(otherOrigins), OtherLayersSHA256: configurationDigest(otherLayers),
	}
}

func validateConfigurationSettings(settings *ConfigurationSettings) error {
	if settings == nil {
		return nil // Older snapshots keep their original, narrower evidence.
	}
	effort := settings.ReasoningEffort
	validEffort := effort.State == "set" && configurationEffort(effort.Value) == effort || (effort.State == "unset" || effort.State == "unrecognized") && effort.Value == ""
	if settings.Version != 1 || !validEffort {
		return fmt.Errorf("配置设置比较依据的版本或 reasoning effort 记录无效")
	}
	for _, hash := range []string{settings.EffortValueSHA256, settings.EffortSourcesSHA256, settings.OtherConfigurationSHA256, settings.OtherOriginsSHA256, settings.OtherLayersSHA256} {
		if !hookDefinitionHash.MatchString(hash) {
			return fmt.Errorf("配置设置比较依据的摘要无效")
		}
	}
	return nil
}

// This classifies recorded disk facts only. Even a single-field change still
// needs an explicit native application and read-back before it can take effect.
func CompareConfigurationSettings(before, after *ConfigurationSettings) ConfigurationSettingsDifference {
	out := ConfigurationSettingsDifference{Status: "unrecorded"}
	if before == nil || after == nil {
		return out
	}
	if validateConfigurationSettings(before) != nil || validateConfigurationSettings(after) != nil {
		out.Status = "unsupported"
		return out
	}
	a, b := before.ReasoningEffort, after.ReasoningEffort
	out.Before, out.After = &a, &b
	out.EffortChanged = a != b || before.EffortValueSHA256 != after.EffortValueSHA256
	out.EffortSourcesChanged = before.EffortSourcesSHA256 != after.EffortSourcesSHA256
	out.OtherValuesChanged = before.OtherConfigurationSHA256 != after.OtherConfigurationSHA256
	out.OtherSourcesChanged = before.OtherOriginsSHA256 != after.OtherOriginsSHA256 || before.OtherLayersSHA256 != after.OtherLayersSHA256
	out.Status = "unchanged"
	if !reflect.DeepEqual(before, after) {
		out.Status = "changed"
	}
	return out
}
