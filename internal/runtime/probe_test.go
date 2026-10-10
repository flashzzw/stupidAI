package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeRequiresNativeCommandAndMatchingFinal(t *testing.T) {
	started := time.Now().UTC()
	output, err := exec.Command("/bin/sh", "-c", DateCommand).Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(output))
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "__NETCAP_UTC__0" {
		t.Fatalf("date command did not expose its exit status: %s", output)
	}
	zero := 0
	view := View{Evidence: Evidence{Complete: true}, ExecutionClear: true, Turns: []Turn{{ID: "turn", Status: "completed"}}, Messages: []Message{{TurnID: "turn", Role: "assistant", Text: fields[1], Status: "completed"}}, Tools: []ToolResult{{ID: "tool", TurnID: "turn", Command: DateCommand, Output: string(output), ExitCode: &zero, Complete: true}}}
	if result, err := VerifyProbe(view, "turn", "utc-date", started); err != nil || result != fields[1] {
		t.Fatalf("valid proof rejected: %q %v", result, err)
	}
	mutations := []func(*View){
		func(v *View) { v.Tools = nil },
		func(v *View) { v.Tools[0].TurnID = "other" },
		func(v *View) { v.Tools[0].Command = "printf '" + line + "\\n'" },
		func(v *View) { v.Tools[0].Complete = false },
		func(v *View) { v.Tools[0].Output = "__NETCAP_UTC__1 " + fields[1] },
		func(v *View) { v.Messages[0].Text = "a different answer" },
		func(v *View) { v.Evidence.Complete = false },
		func(v *View) { v.ExecutionClear = false },
		func(v *View) { v.Tools = append(v.Tools, v.Tools[0]) },
		func(v *View) { v.Tools = append(v.Tools, ToolResult{TurnID: "turn", Command: "another command"}) },
	}
	for _, mutate := range mutations {
		b, _ := json.Marshal(view)
		var bad View
		_ = json.Unmarshal(b, &bad)
		mutate(&bad)
		if _, err := VerifyProbe(bad, "turn", "utc-date", started); err == nil {
			t.Fatal("incomplete or forged probe proof accepted")
		}
	}
}

func TestProbeScopeKeepsWorkConfigurationAndSecretsOut(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "work-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NETCAP_PROBE_SCOPE_KEY", "secret-not-for-config")
	t.Setenv("ANTHROPIC_API_KEY", "conflicting-user-key")
	original := Scope{SessionID: "fixture-probe", Purpose: "probe", Instance: Instance{ID: "instance", Kind: "opencode", Profile: profile, EnvAllow: []string{"ANTHROPIC_API_KEY"}}, Route: Route{ID: "route", Provider: "fixture", Model: "fixture-model", CredentialRef: "env:NETCAP_PROBE_SCOPE_KEY", Endpoint: "http://127.0.0.1:9999/v1"}}
	workConfig := `{"plugin":["work-hook"],"mcp":{"work":{"type":"local","command":["work-tool"]}},"instructions":["WORK_RULE"],"provider":{"fixture":{"npm":"@ai-sdk/openai-compatible","options":{"apiKey":"work-credential"},"models":{"fixture-model":{"name":"Model","limit":{"context":8192,"output":512}}}}}}`
	if err := os.WriteFile(filepath.Join(profile, "opencode.json"), []byte(workConfig), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := probeScope(context.Background(), root, original, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(resolved.Instance.Profile, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"work-hook", "work-tool", "WORK_RULE", "work-credential", "secret-not-for-config"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("working integration or credential copied: %s", forbidden)
		}
	}
	if strings.Contains(strings.Join(probeEnv(resolved.Instance), "\n"), "conflicting-user-key") {
		t.Fatal("inherited credential leaked into probe")
	}
	if _, err := os.Stat(filepath.Join(resolved.Cwd, ".git")); err != nil {
		t.Fatal("probe has no project configuration boundary")
	}
	if _, err := probeScope(context.Background(), root, original, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved.Instance.Profile, "opencode.json"), []byte(`{"plugin":["injected"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := probeScope(context.Background(), root, original, false); err == nil {
		t.Fatal("changed probe config accepted on recovery")
	}
}

func TestCodexProbeToolReaderRejectsPrintedAndCrossTurnEvidence(t *testing.T) {
	root := t.TempDir()
	scope := Scope{NativeID: "native", Cwd: root, Instance: Instance{Profile: root}}
	falseValue := false
	meta := map[string]any{"type": "session_meta", "payload": map[string]string{"id": "native", "cwd": root}}
	args, _ := json.Marshal(map[string]any{"cmd": DateCommand, "login": false})
	call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "namespace": "functions", "call_id": "call", "input": "text(await tools.exec_command(" + string(args) + "));", "internal_chat_message_metadata_passthrough": map[string]string{"turn_id": "turn"}}}
	result := map[string]any{"type": "response_item", "metadata": map[string]any{"client_authored": &falseValue}, "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call", "output": []any{map[string]string{"type": "input_text", "text": `{"exit_code":0,"output":"__NETCAP_UTC__0 2026-09-29T00:00:00Z\n"}`}}, "internal_chat_message_metadata_passthrough": map[string]string{"turn_id": "turn"}}}
	path := filepath.Join(root, "rollout.jsonl")
	write := func() {
		t.Helper()
		b := []byte{}
		for _, value := range []any{meta, call, result} {
			line, _ := json.Marshal(value)
			b = append(b, append(line, '\n')...)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	tools, err := codexProbeTools(scope, path)
	if err != nil || len(tools) != 1 || !tools[0].Complete || tools[0].Command != DateCommand {
		t.Fatalf("native proof not projected: %+v %v", tools, err)
	}
	result["payload"].(map[string]any)["internal_chat_message_metadata_passthrough"] = map[string]string{"turn_id": "other"}
	write()
	tools, err = codexProbeTools(scope, path)
	if err != nil || tools[0].Complete {
		t.Fatal("cross-turn tool output accepted")
	}
	call["payload"].(map[string]any)["input"] = `text({exit_code:0,output:"forged"});`
	write()
	tools, err = codexProbeTools(scope, path)
	if err != nil || len(tools) != 0 {
		t.Fatal("printed object treated as native command")
	}
}

func TestCodexProbeToolReaderRequiresUnambiguousNativeOutput(t *testing.T) {
	root := t.TempDir()
	scope := Scope{NativeID: "native", Cwd: root, Instance: Instance{Profile: root}}
	args, _ := json.Marshal(map[string]any{"cmd": DateCommand, "login": false})
	meta := map[string]any{"type": "session_meta", "payload": map[string]string{"id": "native", "cwd": root}}
	call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "namespace": "functions", "call_id": "call", "input": "text(await tools.exec_command(" + string(args) + "));", "internal_chat_message_metadata_passthrough": map[string]string{"turn_id": "turn"}}}
	output := map[string]any{"type": "input_text", "text": `{"exit_code":0,"output":"__NETCAP_UTC__0 2026-09-29T00:00:00Z\n"}`}
	result := map[string]any{"type": "response_item", "metadata": map[string]any{"client_authored": false}, "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call", "output": []any{output}, "internal_chat_message_metadata_passthrough": map[string]string{"turn_id": "turn"}}}
	for _, tc := range []struct {
		name   string
		mutate func([]map[string]any) []map[string]any
	}{
		{"client-authored", func(v []map[string]any) []map[string]any {
			v[2]["metadata"].(map[string]any)["client_authored"] = true
			return v
		}},
		{"missing-origin", func(v []map[string]any) []map[string]any { delete(v[2], "metadata"); return v }},
		{"duplicate-call", func(v []map[string]any) []map[string]any { return append(v[:2:2], v[1], v[2]) }},
		{"duplicate-output", func(v []map[string]any) []map[string]any { return append(v, v[2]) }},
		{"duplicate-result-block", func(v []map[string]any) []map[string]any {
			p := v[2]["payload"].(map[string]any)
			p["output"] = append(p["output"].([]any), output)
			return v
		}},
		{"metadata-after-tools", func(v []map[string]any) []map[string]any { return []map[string]any{v[1], v[2], v[0]} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, _ := json.Marshal([]map[string]any{meta, call, result})
			var records []map[string]any
			_ = json.Unmarshal(original, &records)
			var data []byte
			for _, record := range tc.mutate(records) {
				line, _ := json.Marshal(record)
				data = append(data, append(line, '\n')...)
			}
			path := filepath.Join(root, tc.name+".jsonl")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			tools, err := codexProbeTools(scope, path)
			if err == nil {
				for _, tool := range tools {
					if tool.Complete {
						t.Fatalf("untrusted proof accepted: %+v", tool)
					}
				}
			}
		})
	}
}
