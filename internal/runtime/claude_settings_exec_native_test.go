//go:build nativeclaude || nativecodex

package runtime_test

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if handled, err := runtime.RunMCPStdioExec(ctx, os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
