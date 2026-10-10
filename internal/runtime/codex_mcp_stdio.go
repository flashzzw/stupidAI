package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"codex-netcap/internal/mcpstdio"
)

const codexMCPStdioCommand = "__mcp-stdio-v1"

// RunMCPStdioExec is the private executable entry used by a bound stdio MCP.
// Its metadata is public provenance; original command arguments and the MCP
// environment remain process inputs and are not stored in execution snapshots.
func RunMCPStdioExec(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || args[0] != codexMCPStdioCommand {
		return false, nil
	}
	if len(args) < 4 || args[2] != "--" || len(args[1]) > 65536 {
		return true, fmt.Errorf("MCP stdio 交付参数无效")
	}
	var binding mcpstdio.Binding
	if json.Unmarshal([]byte(args[1]), &binding) != nil {
		return true, fmt.Errorf("MCP stdio 交付绑定无效")
	}
	return true, mcpstdio.Run(ctx, binding, args[3], args[4:], mcpstdio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
}
