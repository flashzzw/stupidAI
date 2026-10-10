package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLocalIPCResultKeepsEnvironmentAndServiceEvidenceSeparate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		status, code string
	}{
		{"write accepted", nil, "passed", ""}, {"send denied", syscall.EPERM, "blocked", "EPERM"},
		{"access denied", syscall.EACCES, "blocked", "EACCES"}, {"cancelled", context.Canceled, "unverified", "CANCELLED"},
		{"other failure", errors.New("must-not-expose-internal-detail"), "unverified", "CHECK_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := localIPCResult("send", tc.err)
			if result.Status != tc.status || result.ErrorCode != tc.code || result.At == "" {
				t.Fatalf("unexpected diagnostic: %+v", result)
			}
			data, _ := json.Marshal(result)
			if strings.Contains(string(data), "must-not-expose") {
				t.Fatal("raw error leaked")
			}
			if tc.err == nil && !strings.Contains(result.Detail, "未连接 MCP") {
				t.Fatal("OS check claimed a service connection")
			}
		})
	}
}

func TestClaudeDescribeReportsLocalIPCWithoutStartingQuery(t *testing.T) {
	for _, version := range []string{"2.1.280 (Claude Code)", "future version"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "claude")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = '--version' ] || exit 9\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			c := NewClaude(filepath.Join(root, "state"))
			defer c.Close()
			d, err := c.Describe(context.Background(), Instance{Kind: "claude", Executable: binary, Profile: root})
			if err != nil {
				t.Fatal(err)
			}
			if version == "future version" {
				if d.LocalIPC != nil {
					t.Fatal("unknown native version acquired a transport-specific diagnosis")
				}
				return
			}
			if d.LocalIPC == nil || d.LocalIPC.At == "" || d.LocalIPC.Operation == "" {
				t.Fatal("local IPC diagnostic missing")
			}
			if d.Compatibility != "supported" || d.Capabilities["probe"] != "unverified" {
				t.Fatal("environment check changed native capability grants")
			}
			if len(c.clients) != 0 {
				t.Fatal("version/environment diagnosis started a native query")
			}
		})
	}
}

func TestClaudeNativeLocalIPCDescription(t *testing.T) {
	binary := os.Getenv("NETCAP_NATIVE_CLAUDE_IPC_TEST")
	if binary == "" {
		t.Skip("set fixed native CLI path for environment diagnosis")
	}
	c := NewClaude(t.TempDir())
	defer c.Close()
	d, err := c.Describe(context.Background(), Instance{Kind: "claude", Executable: binary, Profile: t.TempDir()})
	if err != nil || d.Version != "2.1.280 (Claude Code)" || d.LocalIPC == nil {
		t.Fatalf("native diagnosis: %+v %v", d, err)
	}
	data, _ := json.MarshalIndent(d, "", "  ")
	if file := os.Getenv("NETCAP_NATIVE_CLAUDE_IPC_OUTPUT"); file != "" {
		if err = os.WriteFile(file, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(data))
}
