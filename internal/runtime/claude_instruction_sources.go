package runtime

import (
	"path/filepath"
	"strings"
)

type claudeInstructionSources struct {
	Available bool `json:"available"`
	Items     []struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Tokens *int64 `json:"tokens"`
	} `json:"items"`
}

func projectClaudeInstructions(source *claudeInstructionSources) *InstructionSourceSample {
	if source == nil {
		return nil
	}
	out := &InstructionSourceSample{Items: []InstructionSourceItem{}}
	if !source.Available || source.Items == nil || len(source.Items) > 256 {
		return out
	}
	seen := map[string]bool{}
	items := make([]InstructionSourceItem, 0, len(source.Items))
	for _, item := range source.Items {
		key := item.Path + "\x00" + item.Source
		if !filepath.IsAbs(item.Path) || len(item.Path) > 4096 || strings.ContainsRune(item.Path, '\x00') ||
			item.Source == "" || len(item.Source) > 100 || strings.ContainsRune(item.Source, '\x00') ||
			item.Tokens == nil || *item.Tokens < 0 || *item.Tokens > 9007199254740991 || seen[key] {
			return out
		}
		seen[key] = true
		items = append(items, InstructionSourceItem{Path: item.Path, Source: item.Source, Tokens: *item.Tokens})
	}
	out.Available, out.Items = true, items
	return out
}
