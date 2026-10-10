//go:build linux && nativecodex

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeConfigurationAdoption(t *testing.T) {
	for _, mode := range []string{"user", "project", "file-only", "cold", "native-loss"} {
		t.Run(mode, func(t *testing.T) { nativeConfigurationAdoption(t, mode) })
	}
}

func nativeConfigurationAdoption(t *testing.T, mode string) {
	mixed := strings.HasPrefix(mode, "mixed")
	coldPending := mode == "cold" || mode == "mixed-cold"
	coldPrepared := mode == "mixed-cold-prepared"
	nativeLoss := mode == "native-loss" || mode == "mixed-native-loss"
	root, err := os.MkdirTemp("", "netcap-w0209-config-apply-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	checks, samples, responses := map[string]bool{}, map[string]any{}, map[string]any{}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, ok bool) {
		t.Helper()
		checks[name] = ok
		if !ok {
			t.Errorf("failed: %s", name)
		}
	}
	configPath, cwd := filepath.Join(f.Instance.Profile, "config.toml"), filepath.Join(root, "work")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	base := string(config) + fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cwd)
	if mixed {
		base = strings.Replace(base, "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1)
	}
	path, suffix := configPath, base
	if mode == "project" {
		path = filepath.Join(cwd, ".codex/config.toml")
		suffix = ""
		write(configPath, base)
	}
	originalBody, targetBody := "model_reasoning_effort=\"low\"\n"+suffix, "model_reasoning_effort=\"high\"\n"+suffix
	targetEffort := "high"
	if mode == "file-only" {
		targetBody = "# explicitly adopted file revision\n" + originalBody
		targetEffort = "low"
	}
	write(path, originalBody)
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: cwd}}}
	if mixed {
		cfg.CapabilityAssets = hookAdoptionMixedAssets(t, root, filepath.Join(root, "marker.jsonl"), write)
	}
	open := func() *app.Application {
		t.Helper()
		store, e := app.OpenStore(cfg.StateDir)
		if e != nil {
			t.Fatal(e)
		}
		a, e := app.Open(cfg, store, f.Runtime)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a := open()
	t.Cleanup(func() {
		_ = a.Close()
		_ = f.Runtime.Close()
		body, _ := json.MarshalIndent(map[string]any{"root": root, "mode": mode, "checks": checks, "samples": samples, "responses": responses, "processes": f.Records, "passed": !t.Failed(), "scope": "production Application same-ID config adoption; owned cancelled unavailable-provider turns and optional intentional hook timeout; no successful model/tool/hook"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(body))
	})
	apply := func(cmd app.Command) app.CommandReceipt {
		t.Helper()
		r, e := a.Apply(cmd)
		if e != nil {
			t.Fatal(cmd.Kind, e)
		}
		return r
	}
	waitOp := func(id string) {
		t.Helper()
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == id {
					if op.Status == "not_sent" || op.Status == "failed" || op.Status == "uncertain" {
						t.Fatalf("%s: %s %s", op.Intent.Kind, op.Status, op.Error)
					}
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}
	waitIdle := func() app.Session {
		return waitNativeApplication(t, a, func(s app.Snapshot) bool {
			return len(s.Sessions) == 1 && s.Sessions[0].Status == "idle" && s.Sessions[0].View.ExecutionClear
		}).Sessions[0]
	}
	created := apply(app.Command{ID: "create", Kind: "create", ProjectID: "work", RouteID: f.Route.ID, Permission: "read_only"})
	initial := waitIdle()
	seed := apply(app.Command{ID: "seed", Kind: "send", SessionID: created.SessionID, Text: "OWNED CONFIG ADOPTION HISTORY; provider unavailable"})
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == seed.OperationID && op.Receipt.NativeTurn != "" {
				return f.HasTurnEvent("error", initial.Scope.NativeID, op.Receipt.NativeTurn)
			}
		}
		return false
	})
	apply(app.Command{ID: "cancel", Kind: "interrupt", SessionID: initial.ID})
	initial = waitIdle()
	samples["initial"] = initial
	thread := func(label string) map[string]any {
		t.Helper()
		var out map[string]any
		if e := f.Call(context.Background(), "thread/read", map[string]any{"threadId": initial.Scope.NativeID, "includeTurns": true}, &out); e != nil {
			t.Fatal(e)
		}
		samples[label] = out
		return out["thread"].(map[string]any)
	}
	before := thread("before")
	export := func(phase string) {
		t.Helper()
		handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
		rows := map[string]json.RawMessage{}
		for _, p := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + initial.ID, "/api/sessions/" + initial.ID + "/execution"} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+p, nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatal(phase, p, rec.Code)
			}
			rows[p] = append(json.RawMessage(nil), rec.Body.Bytes()...)
		}
		responses[phase] = rows
	}
	export("initial")
	write(path, targetBody)
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		return s.Sessions[0].Status == "unknown" && !s.Sessions[0].View.ExecutionClear
	})
	export("drifted")
	request := app.ExecutionChangeRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, AdoptCodexConfigurationSources: true}
	if mixed {
		request.Selections = hookAdoptionMixedSelections(t, a, f.Instance)
	}
	if mode == "user" {
		for _, rejected := range []struct{ name, body string }{
			{"other-setting", "personality=\"friendly\"\n" + targetBody},
			{"custom-source", "PRIVATE_UNRECOGNIZED_KEY=\"PRIVATE_VALUE\"\n" + targetBody},
			{"trust-change", strings.Replace(targetBody, "trust_level=\"trusted\"", "trust_level=\"untrusted\"", 1)},
			{"clear-effort", suffix},
		} {
			write(path, rejected.body)
			_, e := a.PreviewExecutionChange(initial.ID, request)
			check("reject_"+rejected.name, e != nil)
		}
		write(path, targetBody)
		hookPath := filepath.Join(f.Instance.Profile, "hooks.json")
		write(hookPath, `{"hooks":{}}`)
		_, e := a.PreviewExecutionChange(initial.ID, request)
		check("reject_hook_file_change", e != nil)
		if e := os.Remove(hookPath); e != nil {
			t.Fatal(e)
		}
		check("rejected_changes_preserve_original", reflect.DeepEqual(a.Snapshot().Sessions[0].ExecutionSnapshot, initial.ExecutionSnapshot) && a.Snapshot().Sessions[0].PendingExecutionChange == nil)
	}
	preview, err := a.PreviewExecutionChange(initial.ID, request)
	if err != nil {
		t.Fatal("preview:", err)
	}
	samples["preview"] = preview
	check("explicit_setting_and_file_diff", preview.CodexConfigurationChange != nil && preview.CodexConfigurationChange.Settings.Before.Value == "low" && preview.CodexConfigurationChange.Settings.After.Value == targetEffort && len(preview.CodexConfigurationChange.Files) == 1)
	request.PreviewHash = preview.PreviewHash
	if mode == "user" {
		write(path, "# changed after preview\n"+targetBody)
		_, e := a.Apply(app.Command{ID: "stale-save", Kind: "stage_execution", SessionID: initial.ID, ExecutionChange: &request})
		check("stale_preview_rejected", e != nil && a.Snapshot().Sessions[0].PendingExecutionChange == nil)
		write(path, targetBody)
	}
	samples["stage_receipt"] = apply(app.Command{ID: "stage", Kind: "stage_execution", SessionID: initial.ID, ExecutionChange: &request})
	staged := a.Snapshot().Sessions[0]
	samples["staged"] = staged
	export("staged")
	check("save_preserves_scope_and_snapshot", reflect.DeepEqual(staged.Scope, initial.Scope) && reflect.DeepEqual(staged.ExecutionSnapshot, initial.ExecutionSnapshot) && staged.PendingExecutionChange.AdoptCodexConfigurationSources)
	check("proof_binds_target", staged.PendingExecutionChange.HookSourceProof.ConfigurationTargetHash != "")
	check("save_does_not_apply", thread("after_save")["reasoningEffort"] == "low")
	_, err = a.Apply(app.Command{ID: "blocked-send", Kind: "send", SessionID: initial.ID, Text: "MUST NOT SEND"})
	check("drift_blocks_input", err != nil)
	ref := app.ExecutionChangeRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}
	if coldPending {
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		check("cold_keeps_candidate", reflect.DeepEqual(a.Snapshot().Sessions[0].PendingExecutionChange, staged.PendingExecutionChange))
	}
	prepared := apply(app.Command{ID: "prepare", Kind: "prepare_execution", SessionID: initial.ID, ExecutionChange: &ref})
	waitOp(prepared.OperationID)
	samples["prepare_receipt"] = prepared
	current := a.Snapshot().Sessions[0]
	samples["prepared"] = current
	export("prepared")
	check("prepare_keeps_original_and_hold", current.ExecutionHold && current.Paused && reflect.DeepEqual(current.ExecutionSnapshot, initial.ExecutionSnapshot) && current.PendingExecutionChange.Baseline != "")
	if coldPrepared {
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		check("cold_prepared_keeps_candidate_and_hold", reflect.DeepEqual(a.Snapshot().Sessions[0].PendingExecutionChange, current.PendingExecutionChange) && a.Snapshot().Sessions[0].ExecutionHold)
		beforeResumes := f.CountMethod("thread/resume")
		write(path, "# candidate changed after prepare\n"+targetBody)
		_, e := a.Apply(app.Command{ID: "reject-prepared-source-drift", Kind: "apply_execution", SessionID: initial.ID, ExecutionChange: &ref})
		check("prepared_candidate_drift_rejected", e != nil && f.CountMethod("thread/resume") == beforeResumes)
		check("drift_does_not_rewrite_candidate", reflect.DeepEqual(a.Snapshot().Sessions[0].PendingExecutionChange, current.PendingExecutionChange) && reflect.DeepEqual(a.Snapshot().Sessions[0].ExecutionSnapshot, initial.ExecutionSnapshot))
		write(path, targetBody)
	}
	if nativeLoss {
		lost := f.DropConfigurationReceipt(initial.Scope.NativeID)
		cmd := app.Command{ID: "apply", Kind: "apply_execution", SessionID: initial.ID, ExecutionChange: &ref}
		receipt := apply(cmd)
		samples["apply_receipt"] = receipt
		select {
		case reply := <-lost:
			samples["lost_reply"] = reply
		case <-time.After(15 * time.Second):
			t.Fatal("configuration reply not dropped")
		}
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == receipt.OperationID {
					return op.Status == "uncertain"
				}
			}
			return false
		})
		uncertain := a.Snapshot().Sessions[0]
		samples["uncertain"] = uncertain
		export("uncertain")
		check("lost_keeps_original_and_candidate", reflect.DeepEqual(uncertain.ExecutionSnapshot, initial.ExecutionSnapshot) && uncertain.ExecutionHold && uncertain.PendingExecutionChange != nil)
		duplicate := apply(cmd)
		check("lost_apply_idempotent", duplicate.OperationID == receipt.OperationID)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		recovery := app.ExecutionRecoveryRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, PendingChangeID: ref.PendingChangeID, OperationID: receipt.OperationID}
		blocked, e := a.ReadExecutionRecovery(context.Background(), initial.ID, recovery)
		check("recovery_requires_original_file", e == nil && !blocked.Satisfied)
		write(path, originalBody)
		ready, e := a.ReadExecutionRecovery(context.Background(), initial.ID, recovery)
		if e != nil {
			t.Fatal(e)
		}
		samples["recovery_check"] = ready
		check("recovery_ready", ready.Satisfied)
		r := apply(app.Command{ID: "recover", Kind: "recover_execution", SessionID: initial.ID, ExecutionRecovery: &recovery})
		waitOp(r.OperationID)
		current = a.Snapshot().Sessions[0]
		samples["recovered"] = current
		export("recovered")
		check("original_version_recovered", current.PendingExecutionChange == nil && reflect.DeepEqual(current.Scope, initial.Scope) && reflect.DeepEqual(current.ExecutionSnapshot, initial.ExecutionSnapshot) && current.Paused)
		after := thread("after_recovery")
		check("actual_original_effort_restored", after["reasoningEffort"] == "low")
		check("recovery_preserves_history", reflect.DeepEqual(before["turns"], after["turns"]))
	} else {
		r := apply(app.Command{ID: "apply", Kind: "apply_execution", SessionID: initial.ID, ExecutionChange: &ref})
		samples["apply_receipt"] = r
		waitOp(r.OperationID)
		current = a.Snapshot().Sessions[0]
		samples["applied"] = current
		export("applied")
		check("target_promoted_same_id", current.PendingExecutionChange == nil && current.Scope.NativeID == initial.Scope.NativeID && current.ExecutionSnapshot.ID == staged.PendingExecutionChange.Snapshot.ID && current.Scope.CodexConfigurationSources.Settings.ReasoningEffort.Value == targetEffort)
		check("old_snapshot_archived_exactly", len(current.ExecutionHistory) == 1 && reflect.DeepEqual(current.ExecutionHistory[0], initial.ExecutionSnapshot) && current.Paused && !current.ExecutionHold)
		after := thread("after_apply")
		check("actual_target_effort", after["reasoningEffort"] == targetEffort)
		check("history_preserved", reflect.DeepEqual(before["turns"], after["turns"]))
		if mixed {
			bundle, loose := false, false
			for _, e := range current.View.PluginEvidence {
				if e.AssetID == "bundle" {
					bundle = e.Verified && len(e.Components) == 3 && e.Mode == "enabled"
				}
				if e.AssetID == "loose" {
					loose = e.Verified && len(e.Components) == 1 && e.Mode == "enabled"
				}
			}
			check("mixed_components_verified", bundle && loose && len(current.Scope.PluginSelections) == 2)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		r = apply(app.Command{ID: "resume", Kind: "resume", SessionID: initial.ID})
		waitOp(r.OperationID)
		check("restart_keeps_applied_value", thread("after_restart")["reasoningEffort"] == targetEffort && a.Snapshot().Sessions[0].ExecutionSnapshot.ID == current.ExecutionSnapshot.ID)
		if strings.HasPrefix(mode, "mixed-retire-") {
			nativeConfigurationMixedRetirement(t, f, &a, open, write, thread, export, apply, waitOp, current, path, targetBody, samples, check, strings.TrimPrefix(mode, "mixed-retire-"))
		}
	}
	if strings.HasPrefix(mode, "mixed-retire-recovery-") {
		check("one_thread_two_cancelled_inputs", f.CountMethod("thread/start") == 1 && f.CountMethod("turn/start") == 2)
	} else if strings.HasPrefix(mode, "mixed-retire-") {
		check("one_thread_three_cancelled_inputs", f.CountMethod("thread/start") == 1 && f.CountMethod("turn/start") == 3)
	} else {
		check("single_original_thread_and_input", f.CountMethod("thread/start") == 1 && f.CountMethod("turn/start") == 1)
	}
}
