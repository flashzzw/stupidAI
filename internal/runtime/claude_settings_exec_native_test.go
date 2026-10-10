//go:build nativeclaude

package runtime_test

import (
	"fmt"
	"os"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestMain(m *testing.M) {
	if handled, err := runtime.RunClaudeSettingsFileExec(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
