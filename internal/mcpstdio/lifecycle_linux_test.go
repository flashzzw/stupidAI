package mcpstdio_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"codex-netcap/internal/mcpstdio"
)

// Every process in these tests is a copy of this test executable, identified
// by an owned temporary PID file. No host process discovery or signals occur.
func TestStdioLifecycleProcess(t *testing.T) {
	if os.Getenv("NETCAP_MCP_LIFECYCLE_FIXTURE") != "owned" {
		t.Skip("owned process helper")
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || len(os.Args) < separator+4 {
		os.Exit(21)
	}
	mode, ready := os.Args[separator+1], os.Args[separator+2]
	if mode == "parent" {
		var binding mcpstdio.Binding
		if json.Unmarshal([]byte(os.Args[separator+3]), &binding) != nil {
			os.Exit(22)
		}
		command, _ := os.Executable()
		args := lifecycleArgs("blocked", ready)
		if mcpstdio.Run(context.Background(), binding, command, args, mcpstdio.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}) != nil {
			os.Exit(23)
		}
		os.Exit(0)
	}
	if mode == "descendants" {
		command, _ := os.Executable()
		child := exec.Command(command, lifecycleArgs("blocked", ready+".child")...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(24)
		}
	}
	if os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
		os.Exit(25)
	}
	if mode == "exit" {
		os.Exit(0)
	}
	if mode == "stdout-closed" {
		_ = os.Stdout.Close()
	}
	for {
		time.Sleep(time.Hour)
	}
}

func lifecycleArgs(mode, ready string) []string {
	return []string{"-test.run=^TestStdioLifecycleProcess$", "--", mode, ready, "owned"}
}

func lifecycleBinding(t *testing.T, mode string) (mcpstdio.Binding, string, []string, string) {
	t.Helper()
	t.Setenv("NETCAP_MCP_LIFECYCLE_FIXTURE", "owned")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	args := lifecycleArgs(mode, ready)
	binding, err := mcpstdio.Bind(mcpstdio.Stamp{AssetID: "owned", AssetVersion: "v1", Server: "owned"}, strings.Repeat("c", 64), dir, command, args,
		map[string]string{"NETCAP_MCP_LIFECYCLE_FIXTURE": "owned"})
	if err != nil {
		t.Fatal(err)
	}
	return binding, command, args, ready
}

func awaitOwnedPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(file)
		if err == nil {
			pid, err := strconv.Atoi(string(body))
			if err == nil && pid > 1 {
				started := ownedProcessStart(pid)
				t.Cleanup(func() {
					if started != "" && ownedProcessStart(pid) == started && ownedProcessRunning(pid) {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				})
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned helper did not become ready")
	return 0
}

func ownedProcessStart(pid int) string {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	end := strings.LastIndexByte(string(body), ')')
	if err != nil || end < 0 {
		return ""
	}
	fields := strings.Fields(string(body[end+1:]))
	if len(fields) <= 19 {
		return ""
	}
	return fields[19]
}

func ownedProcessRunning(pid int) bool {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return false
	}
	// An orphan may await the container's init reaper; it has already exited.
	end := strings.LastIndexByte(string(body), ')')
	return err != nil || end < 0 || len(body) < end+3 || body[end+2] != 'Z'
}

func requireOwnedExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !ownedProcessRunning(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned MCP process survived transport shutdown")
}

func TestStdioLifecycleBoundsTransportAndChildLifetime(t *testing.T) {
	for _, mode := range []string{"cancel", "eof", "stdout-closed", "descendants", "exit"} {
		t.Run(mode, func(t *testing.T) {
			childMode := mode
			if mode == "cancel" || mode == "eof" {
				childMode = "blocked"
			}
			binding, command, args, ready := lifecycleBinding(t, childMode)
			in, writer := io.Pipe()
			defer writer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- mcpstdio.Run(ctx, binding, command, args, mcpstdio.Streams{In: in, Out: io.Discard, Err: io.Discard})
			}()
			pid := awaitOwnedPID(t, ready)
			child := 0
			if mode == "descendants" {
				child = awaitOwnedPID(t, ready+".child")
			}
			switch mode {
			case "cancel", "descendants":
				cancel()
			case "eof":
				_ = writer.Close()
			}
			select {
			case err := <-done:
				if mode == "exit" && err != nil || mode != "exit" && err == nil {
					t.Fatalf("shutdown result does not match child outcome: %v", err)
				}
				if (mode == "cancel" || mode == "descendants") && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost its cause: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("transport did not return after shutdown")
			}
			requireOwnedExit(t, pid)
			if child != 0 {
				requireOwnedExit(t, child)
			}
			if _, err := writer.Write([]byte("closed")); err == nil {
				t.Fatal("transport left its owned input open")
			}
		})
	}
}

func TestStdioParentDeathStopsDirectMCPChild(t *testing.T) {
	binding, command, _, ready := lifecycleBinding(t, "blocked")
	body, _ := json.Marshal(binding)
	args := lifecycleArgs("parent", ready)
	args[len(args)-1] = string(body)
	parent := exec.Command(command, args...)
	in, err := parent.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	pid := awaitOwnedPID(t, ready)
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	requireOwnedExit(t, pid)
}
