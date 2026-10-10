package runtime

import "testing"

func TestClaudeUncommittedConfigurationBlocksOfflineInput(t *testing.T) {
	scope := Scope{NativeID: "original"}
	s := claudeSnapshot{Live: false, Entries: []claudeEntry{{Kind: "initialized"}}}
	s.Config.ID = "original"
	if !projectClaude(scope, s).ExecutionClear {
		t.Fatal("fixture has no idle history")
	}
	s.Configuration = &claudeConfigurationChange{Status: "applying", Operation: "change"}
	view := projectClaude(scope, s)
	if view.Status != "unknown" || view.ExecutionClear || view.Evidence.Complete {
		t.Fatal("missing configuration outcome admitted input")
	}
	s.Live = true
	view = projectClaude(scope, s)
	if view.ExecutionClear || view.Evidence.Complete {
		t.Fatal("live process substituted for a verified configuration outcome")
	}
}
