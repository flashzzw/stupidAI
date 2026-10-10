//go:build !linux

package mcpstdio

import "os/exec"

// Non-Linux platforms retain CommandContext's direct-child cancellation.
// Process-tree behavior needs each platform's native acceptance separately.
func configureChild(cmd *exec.Cmd) {}
