package mcpstdio_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/mcpstdio"
)

func TestMCPFixtureProcess(t *testing.T) {
	if os.Getenv("NETCAP_MCP_STDIO_FIXTURE") != "echo" {
		t.Skip("owned process helper")
	}
	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			os.Exit(0)
		}
		if err != nil {
			os.Exit(11)
		}
		var request map[string]json.RawMessage
		if json.Unmarshal(line, &request) != nil {
			os.Exit(12)
		}
		id, exists := request["id"]
		if !exists {
			continue
		}
		var method string
		_ = json.Unmarshal(request["method"], &method)
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "owned", "version": "fixed"},
				"capabilities": map[string]any{"tools": map[string]any{"listChanged": true}, "experimental": map[string]any{"original": json.RawMessage(`{"exact":9007199254740993}`), "netcap/binding": "upstream-owned-value"}}}
		case "tools/call":
			// A server request can use the same ID as an outstanding client
			// request. It must not consume the client's result correlation.
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": "elicitation/create", "params": map[string]any{"message": "owned"}})
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("大", 40000)}},
				"structuredContent": map[string]any{"request": request, "credential_present": os.Getenv("MCP_TEST_VALUE") == "fixture-value", "cwd": func() string { p, _ := os.Getwd(); return p }()},
				"_meta":             map[string]any{"original": json.RawMessage(`{"exact":9007199254740993}`), "netcap/binding": "upstream-call-value"}, "isError": false}
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "owned unavailable method", "data": request}})
			continue
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
}

func fixtureBinding(t *testing.T) (mcpstdio.Binding, string, []string) {
	t.Helper()
	t.Setenv("NETCAP_MCP_STDIO_FIXTURE", "echo")
	t.Setenv("MCP_TEST_VALUE", "fixture-value")
	// The race runtime otherwise keeps an already-finished fixture process
	// alive for its diagnostic exit delay, which is not an MCP shutdown.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestMCPFixtureProcess$"}
	binding, err := mcpstdio.Bind(mcpstdio.Stamp{AssetID: "identity", AssetVersion: "v2", Server: "identity"}, strings.Repeat("a", 64), t.TempDir(), command, args,
		map[string]string{"MCP_TEST_VALUE": "fixture-value", "NETCAP_MCP_STDIO_FIXTURE": "echo"})
	if err != nil {
		t.Fatal(err)
	}
	return binding, command, args
}

func TestStdioBindingPreservesProtocolAndCorrelatesLaunchAndResults(t *testing.T) {
	binding, command, args := fixtureBinding(t)
	input := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\"}}\n" +
		"{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":\"call-1\",\"method\":\"tools/call\",\"params\":{\"name\":\"whoami\",\"arguments\":{\"exact\":9007199254740993}}}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"unknown\",\"params\":{}}\n"
	var out, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mcpstdio.Run(ctx, binding, command, args, mcpstdio.Streams{In: io.NopCloser(strings.NewReader(input)), Out: &out, Err: &stderr}); err != nil {
		t.Fatal(err, stderr.String())
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) != 4 {
		t.Fatalf("protocol frame count = %d", len(lines))
	}
	var initialized struct {
		Result struct {
			Capabilities struct {
				Experimental map[string]json.RawMessage `json:"experimental"`
			} `json:"capabilities"`
			ServerInfo map[string]string `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(lines[0], &initialized); err != nil {
		t.Fatal(err)
	}
	assertStamp := func(raw json.RawMessage, upstream string) {
		t.Helper()
		var got struct {
			mcpstdio.Stamp
			Upstream string `json:"upstream"`
		}
		if json.Unmarshal(raw, &got) != nil || got.Stamp != binding.Stamp || got.Upstream != upstream {
			t.Fatal("missing launch identity or displaced original metadata", string(raw))
		}
	}
	assertStamp(initialized.Result.Capabilities.Experimental["netcap/binding"], "upstream-owned-value")
	if initialized.Result.ServerInfo["version"] != "fixed" || !bytes.Contains(initialized.Result.Capabilities.Experimental["original"], []byte("9007199254740993")) {
		t.Fatal("server metadata was changed")
	}
	var serverRequest map[string]json.RawMessage
	_ = json.Unmarshal(lines[1], &serverRequest)
	if string(serverRequest["method"]) != `"elicitation/create"` || string(serverRequest["id"]) != `"call-1"` {
		t.Fatal("server request was not forwarded")
	}
	var called struct {
		Result struct {
			Content    []struct{ Text string }    `json:"content"`
			Meta       map[string]json.RawMessage `json:"_meta"`
			Structured struct {
				Request    json.RawMessage `json:"request"`
				Credential bool            `json:"credential_present"`
				Cwd        string          `json:"cwd"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	_ = json.Unmarshal(lines[2], &called)
	assertStamp(called.Result.Meta["netcap/binding"], "upstream-call-value")
	if len(called.Result.Content) != 1 || called.Result.Content[0].Text != strings.Repeat("大", 40000) || !called.Result.Structured.Credential || called.Result.Structured.Cwd != binding.Cwd ||
		!bytes.Contains(called.Result.Structured.Request, []byte("9007199254740993")) || !bytes.Contains(called.Result.Meta["original"], []byte("9007199254740993")) {
		t.Fatal("tool payload, environment, cwd or numeric precision changed")
	}
	if !bytes.Contains(lines[3], []byte("owned unavailable method")) || bytes.Contains(lines[3], []byte("netcap/binding")) {
		t.Fatal("unrelated protocol error was rewritten")
	}
	if stderr.Len() != 0 {
		t.Fatal("protocol leaked to diagnostic stream", stderr.String())
	}
}

func TestChangedLaunchValuesAreRejectedBeforeMCPExecution(t *testing.T) {
	for _, change := range []string{"argument", "environment", "context", "cwd", "stamp"} {
		t.Run(change, func(t *testing.T) {
			binding, command, args := fixtureBinding(t)
			switch change {
			case "argument":
				args = append(args, "unexpected")
			case "environment":
				t.Setenv("MCP_TEST_VALUE", "changed-fixture-value")
			case "context":
				binding.Context = strings.Repeat("b", 64)
			case "cwd":
				binding.Cwd = t.TempDir()
			case "stamp":
				binding.AssetVersion = "v3"
			}
			var out bytes.Buffer
			err := mcpstdio.Run(context.Background(), binding, command, args, mcpstdio.Streams{In: io.NopCloser(strings.NewReader("{}\n")), Out: &out, Err: &out})
			if err == nil || out.Len() != 0 || strings.Contains(fmt.Sprint(err), "fixture-value") || strings.Contains(fmt.Sprint(err), command) {
				t.Fatal("changed launch was executed or sensitive launch values were reported", err)
			}
		})
	}
}

func TestBindingRetainsExistingCapabilityIdentifierAndVersionRange(t *testing.T) {
	for _, identity := range []mcpstdio.Stamp{
		{AssetID: "_owned", AssetVersion: strings.Repeat("v", 200), Server: "-server"},
		{AssetID: "-owned", AssetVersion: " v1 ", Server: "_server"},
	} {
		binding, err := mcpstdio.Bind(identity, strings.Repeat("a", 64), t.TempDir(), "owned", nil, nil)
		if err != nil || !binding.Valid() {
			t.Fatalf("a valid existing capability identifier or version was rejected: %v", err)
		}
	}
}
