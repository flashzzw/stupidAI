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
	"slices"
	"strings"
	"testing"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/console"
	"codex-netcap/internal/runtime"
)

func TestCodexNativeHookSourceAdoption(t *testing.T) {
	for _, source := range []string{"user", "project", "new-project", "native-loss"} {
		t.Run(source, func(t *testing.T) { nativeHookSourceAdoption(t, source) })
	}
}
func nativeHookSourceAdoption(t *testing.T, source string) {
	variant := source
	mixed := strings.HasPrefix(source, "mixed")
	worktree := source == "worktree"
	recoverBoundary := strings.TrimPrefix(variant, "mixed-cold-recover-")
	if recoverBoundary == variant {
		recoverBoundary = ""
	}
	recoveryLossBoundary := strings.TrimPrefix(variant, "mixed-cold-double-")
	if recoveryLossBoundary == variant {
		recoveryLossBoundary = ""
	} else {
		recoverBoundary = "policy"
	}
	coldPending := recoverBoundary != "" || source == "cold-pending" || source == "mixed-cold-pending" || source == "mixed-cold-policy-probe" || source == "mixed-cold-loss"
	coldPrepared := source == "mixed-cold-prepared"
	if mixed || coldPending {
		source = "user"
	}
	if worktree {
		source = "project"
	}
	nativeLoss := source == "native-loss"
	newProject := source == "new-project"
	if newProject {
		source = "project"
	}
	if nativeLoss {
		source = "user"
	}
	root, err := os.MkdirTemp("", "netcap-w0209-hook-adoption-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Evidence directory:", root)
	f := runtime.NewNativeApplicationFixtureAt(t, "read_only", root)
	f.Instance.ServerExecutable = os.Getenv("NETCAP_NATIVE_CODEX_TEST")
	checks := map[string]bool{}
	samples := map[string]any{}
	responses := map[string]any{}
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
	t.Cleanup(func() {
		_ = f.Runtime.Close()
		b, _ := json.MarshalIndent(map[string]any{"root": root, "source": source, "variant": variant, "checks": checks, "samples": samples, "processes": f.Records, "passed": !t.Failed(), "scope": "production same-ID inherited hook source adoption; pre-existing exact profile trust; owned intentional timeout markers and cancelled unavailable-provider inputs; no successful model/hook execution"}, "", "  ")
		write(filepath.Join(root, "result.json"), string(b))
		b, _ = json.MarshalIndent(map[string]any{"samples": samples, "responses": responses}, "", "  ")
		write(filepath.Join(root, "browser-responses.json"), string(b))
	})
	cwd := filepath.Join(root, "work")
	path := filepath.Join(f.Instance.Profile, "hooks.json")
	if source == "project" {
		path = filepath.Join(cwd, ".codex/hooks.json")
	}
	if worktree {
		cwd, path = hookAdoptionWorktree(t, root, write)
	}
	runner := filepath.Join(root, "marker.py")
	marker := filepath.Join(root, "marker.jsonl")
	write(runner, fmt.Sprintf("import json,os,sys\nfrom pathlib import Path\nd=json.load(sys.stdin)\nwith Path(%q).open('a') as f:f.write(json.dumps({'version':sys.argv[1],'input':d})+'\\n')\nos.execl('/usr/bin/sleep','sleep','2')\n", marker))
	definition := func(version string) map[string]any {
		return map[string]any{"type": "command", "command": fmt.Sprintf("python3 %q %s", runner, version), "timeout": 1}
	}
	encode := func(value any) string {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := func(versions ...string) string {
		hooks := []any{}
		for _, v := range versions {
			hooks = append(hooks, definition(v))
		}
		return encode(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": hooks}}}})
	}
	v1, v2 := body("v1"), body("v1", "v2")
	write(path, v2)
	configPath := filepath.Join(f.Instance.Profile, "config.toml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Replace(string(configBytes), "plugins=false", "plugins=true\nhooks=true\nplugin_sharing=false", 1) + fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cwd)
	if worktree {
		config += fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n[projects.%q]\ntrust_level=\"trusted\"\n", filepath.Join(root, "linked"), filepath.Join(root, "main-repo"))
	}
	write(configPath, config)
	f.Restart(t)
	var catalog struct {
		Data []struct {
			Hooks []struct {
				Key  string `json:"key"`
				Hash string `json:"currentHash"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if err := f.Call(context.Background(), "hooks/list", map[string]any{"cwds": []string{cwd}}, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Data) != 1 || len(catalog.Data[0].Hooks) != 2 {
		t.Fatal("owned future definitions missing")
	}
	// Pre-existing trust is identical throughout the test: adoption itself must
	// never grant a new hash. v1 does not yet contain the second trusted key.
	for _, h := range catalog.Data[0].Hooks {
		config += fmt.Sprintf("\n[hooks.state.%q]\nenabled=true\ntrusted_hash=%q\n", h.Key, h.Hash)
	}
	write(configPath, config)
	write(path, v1)
	if newProject {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
	}
	f.Restart(t)
	cfg := app.Config{Version: 1, StateDir: filepath.Join(root, "application"), Instances: []runtime.Instance{f.Instance}, Routes: []runtime.Route{f.Route}, Projects: []app.Project{{ID: "work", Cwd: cwd}}}
	if mixed {
		cfg.CapabilityAssets = hookAdoptionMixedAssets(t, root, marker, write)
	}
	open := func() *app.Application {
		t.Helper()
		store, err := app.OpenStore(cfg.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		a, err := app.Open(cfg, store, f.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	t.Cleanup(func() { _ = a.Close() })
	created, err := a.Apply(app.Command{ID: "create", Kind: "create", ProjectID: "work", RouteID: f.Route.ID, Permission: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	session := func(s app.Snapshot) app.Session {
		for _, v := range s.Sessions {
			if v.ID == created.SessionID {
				return v
			}
		}
		t.Fatal("session missing")
		return app.Session{}
	}
	waitIdle := func() app.Session {
		return session(waitNativeApplication(t, a, func(s app.Snapshot) bool { v := session(s); return v.Status == "idle" && v.View.ExecutionClear }))
	}
	initial := waitIdle()
	if strings.HasPrefix(variant, "mixed-cold-") && variant != "mixed-cold-policy-probe" {
		if err := f.Call(context.Background(), "thread/settings/update", map[string]any{"threadId": initial.Scope.NativeID, "disabledPluginIds": []string{"retained@owned"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	export := func(phase string) {
		t.Helper()
		handler := console.Handler(a, "127.0.0.1:9463", strings.Repeat("b", 64), os.DirFS("."))
		rows := map[string]json.RawMessage{}
		for _, path := range []string{"/api/workspace", "/api/capabilities", "/api/sessions/" + initial.ID, "/api/sessions/" + initial.ID + "/execution"} {
			req := httptest.NewRequest("GET", "http://127.0.0.1:9463"+path, nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("export %s: %d", path, response.Code)
			}
			rows[path] = append(json.RawMessage(nil), response.Body.Bytes()...)
		}
		responses[phase] = rows
	}

	turn := func(label string, expected []string) {
		t.Helper()
		sent, err := a.Apply(app.Command{ID: label, Kind: "send", SessionID: initial.ID, Text: "OWNED ADOPTION " + label})
		if err != nil {
			t.Fatal(err)
		}
		var nativeTurn string
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == sent.OperationID {
					nativeTurn = op.Receipt.NativeTurn
				}
			}
			checkpoint, err := f.Checkpoint()
			if err != nil || nativeTurn == "" {
				return false
			}
			var rows []struct {
				Events []struct {
					Method string `json:"method"`
					Params struct {
						TurnID string `json:"turnId"`
					} `json:"params"`
				} `json:"events"`
			}
			if json.Unmarshal(checkpoint.Trace, &rows) != nil {
				return false
			}
			for _, r := range rows {
				for _, e := range r.Events {
					if e.Method == "error" && e.Params.TurnID == nativeTurn {
						return true
					}
				}
			}
			return false
		})
		if _, err := a.Apply(app.Command{ID: label + "-cancel", Kind: "interrupt", SessionID: initial.ID}); err != nil {
			t.Fatal(err)
		}
		v := waitIdle()
		samples[label] = v
		data, err := os.ReadFile(marker)
		if err != nil && !(os.IsNotExist(err) && len(expected) == 0) {
			t.Fatal(err)
		}
		actual := []string{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var row struct {
				Version string `json:"version"`
				Input   struct {
					Prompt string `json:"prompt"`
					ID     string `json:"session_id"`
				} `json:"input"`
			}
			if json.Unmarshal([]byte(line), &row) == nil && row.Input.Prompt == "OWNED ADOPTION "+label && row.Input.ID == initial.Scope.NativeID {
				actual = append(actual, row.Version)
			}
		}
		slices.Sort(actual)
		check(label+"_exact_file_invocations", reflect.DeepEqual(actual, expected))
	}
	if newProject {
		turn("before", []string{})
	} else {
		turn("before", []string{"v1"})
	}
	initial = session(a.Snapshot())
	samples["initial"] = initial
	export("initial")
	write(path, v2)
	drifted := session(waitNativeApplication(t, a, func(s app.Snapshot) bool { v := session(s); return v.Status == "unknown" && !v.View.ExecutionClear }))
	samples["drifted"] = drifted
	export("drifted")
	request := app.ExecutionChangeRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, AdoptCodexHookSources: true}
	if mixed {
		request.Selections = hookAdoptionMixedSelections(t, a, f.Instance)
	}
	preview, err := a.PreviewExecutionChange(initial.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	samples["preview"] = preview
	check("exact_diff", len(preview.CodexHookSourceChanges) == 1 && preview.CodexHookSourceChanges[0].Source == source)
	request.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(app.Command{ID: "stage", Kind: "stage_execution", SessionID: initial.ID, ExecutionChange: &request}); err != nil {
		t.Fatal(err)
	}
	staged := session(a.Snapshot())
	samples["staged"] = staged
	export("staged")
	check("saved_original_proof", runtime.ValidateCodexHookSourceProof(initial.Scope, staged.PendingExecutionChange.HookSourceProof) == nil)
	check("save_keeps_old_snapshot", reflect.DeepEqual(staged.ExecutionSnapshot, initial.ExecutionSnapshot) && staged.PendingExecutionChange.AdoptCodexHookSources)
	commandPrefix := ""
	command := func(kind string) {
		t.Helper()
		s := session(a.Snapshot())
		r, err := a.Apply(app.Command{ID: commandPrefix + kind, Kind: kind, SessionID: s.ID, ExecutionChange: &app.ExecutionChangeRequest{BaseSnapshotID: s.ExecutionSnapshot.ID, PendingChangeID: s.PendingExecutionChange.ID}})
		if err != nil {
			t.Fatal(err)
		}
		waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
			for _, op := range snapshot.Operations {
				if op.ID == r.OperationID {
					if op.Status == "not_sent" || op.Status == "uncertain" || op.Status == "failed" {
						t.Fatalf("%s: %s: %s", kind, op.Status, op.Error)
					}
					return op.Status == "succeeded"
				}
			}
			return false
		})
	}

	reopenCandidate := func(label string, expected app.Session) {
		t.Helper()
		calls := nativeConfigurationCallCount(t, f, initial.Scope.NativeID)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		var unloaded struct {
			Thread struct {
				ID     string `json:"id"`
				Status struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"thread"`
		}
		if err := f.Call(context.Background(), "thread/read", map[string]any{"threadId": initial.Scope.NativeID, "includeTurns": true}, &unloaded); err != nil {
			t.Fatal(err)
		}
		check(label+"_original_unloaded", unloaded.Thread.ID == initial.Scope.NativeID && unloaded.Thread.Status.Type == "notLoaded")
		reopened := session(a.Snapshot())
		samples[label] = reopened
		export(label)
		check(label+"_preserves_candidate_and_proof", reflect.DeepEqual(reopened.PendingExecutionChange, expected.PendingExecutionChange))
		check(label+"_preserves_original_snapshot", reflect.DeepEqual(reopened.ExecutionSnapshot, expected.ExecutionSnapshot) && reflect.DeepEqual(reopened.Scope, expected.Scope) && reflect.DeepEqual(reopened.ExecutionHistory, expected.ExecutionHistory))
		check(label+"_no_configuration_replay", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == calls)
		check(label+"_holds_preserved", reopened.ExecutionHold == expected.ExecutionHold && reopened.Paused == expected.Paused)
	}
	if coldPending {
		reopenCandidate("cold_pending", staged)
		calls := nativeConfigurationCallCount(t, f, initial.Scope.NativeID)
		write(path, v2+"\n")
		_, rejected := a.Apply(app.Command{ID: "reject-candidate-drift", Kind: "prepare_execution", SessionID: initial.ID, ExecutionChange: &app.ExecutionChangeRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, PendingChangeID: staged.PendingExecutionChange.ID}})
		check("cold_rejects_changed_candidate", rejected != nil && nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == calls)
		write(path, v2)
	}
	command("prepare_execution")
	prepared := session(a.Snapshot())
	samples["prepared"] = prepared
	export("prepared")
	if coldPending {
		var native struct {
			Thread struct {
				Status struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"thread"`
		}
		if err := f.Call(context.Background(), "thread/read", map[string]any{"threadId": initial.Scope.NativeID, "includeTurns": true}, &native); err != nil {
			t.Fatal(err)
		}
		check("cold_prepare_does_not_load", native.Thread.Status.Type == "notLoaded")
		check("prepare_preserves_saved_proof", reflect.DeepEqual(prepared.PendingExecutionChange.HookSourceProof, staged.PendingExecutionChange.HookSourceProof))
	}
	check("prepare_keeps_old_snapshot", reflect.DeepEqual(prepared.ExecutionSnapshot, initial.ExecutionSnapshot) && prepared.ExecutionHold && prepared.PendingExecutionChange.Baseline != "")
	if coldPrepared {
		reopenCandidate("cold_prepared", prepared)
	}
	if nativeLoss {
		calls := nativeConfigurationCallCount(t, f, initial.Scope.NativeID)
		lost := f.DropConfigurationReceipt(initial.Scope.NativeID)
		ref := app.ExecutionChangeRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, PendingChangeID: prepared.PendingExecutionChange.ID}
		cmd := app.Command{ID: "apply-lost", Kind: "apply_execution", SessionID: initial.ID, ExecutionChange: &ref}
		accepted, err := a.Apply(cmd)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case reply := <-lost:
			samples["lost_native_reply"] = reply
		case <-time.After(15 * time.Second):
			t.Fatal("reply not dropped")
		}
		uncertain := waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == accepted.OperationID {
					return op.Status == "uncertain"
				}
			}
			return false
		})
		old := session(uncertain)
		samples["uncertain"] = uncertain
		export("uncertain")
		check("lost_reply_preserves_old_and_candidate", old.ExecutionSnapshot.ID == initial.ExecutionSnapshot.ID && old.PendingExecutionChange != nil && old.ExecutionHold)
		check("lost_reply_single_apply", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == calls+1)
		duplicate, err := a.Apply(cmd)
		check("lost_reply_idempotent", err == nil && duplicate.OperationID == accepted.OperationID)
		_, err = a.Apply(app.Command{ID: "must-not-send", Kind: "send", SessionID: initial.ID, Text: "must not send"})
		check("lost_reply_blocks_input", err != nil)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		f.Restart(t)
		a = open()
		recoveryRequest := app.ExecutionRecoveryRequest{BaseSnapshotID: initial.ExecutionSnapshot.ID, PendingChangeID: old.PendingExecutionChange.ID, OperationID: accepted.OperationID}
		unavailable, err := a.ReadExecutionRecovery(context.Background(), initial.ID, recoveryRequest)
		check("original_file_required_for_recovery", err == nil && !unavailable.Satisfied)
		write(path, v1)
		ready, err := a.ReadExecutionRecovery(context.Background(), initial.ID, recoveryRequest)
		if err != nil {
			t.Fatal(err)
		}
		samples["recovery_check"] = ready
		check("restored_original_recovery_ready", ready.Satisfied)
		restored, err := a.Apply(app.Command{ID: "recover-original", Kind: "recover_execution", SessionID: initial.ID, ExecutionRecovery: &recoveryRequest})
		if err != nil {
			t.Fatal(err)
		}
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == restored.OperationID {
					if op.Status == "not_sent" || op.Status == "failed" || op.Status == "uncertain" {
						t.Fatalf("recover: %s: %s", op.Status, op.Error)
					}
					return op.Status == "succeeded"
				}
			}
			return false
		})
		current := session(a.Snapshot())
		samples["recovered"] = current
		check("recovery_keeps_original_version", current.ExecutionSnapshot.ID == initial.ExecutionSnapshot.ID && current.PendingExecutionChange == nil && current.Scope.NativeID == initial.Scope.NativeID)
		resumed, err := a.Apply(app.Command{ID: "resume-recovered", Kind: "resume", SessionID: initial.ID})
		if err != nil {
			t.Fatal(err)
		}
		waitNativeApplication(t, a, func(s app.Snapshot) bool {
			for _, op := range s.Operations {
				if op.ID == resumed.OperationID {
					return op.Status == "succeeded"
				}
			}
			return false
		})
		waitIdle()
		turn("recovered", []string{"v1"})
		return
	}
	command("apply_execution")
	applied := waitIdle()
	samples["applied"] = applied
	export("applied")
	check("same_id_new_snapshot", applied.Scope.NativeID == initial.Scope.NativeID && applied.ExecutionSnapshot.ID != initial.ExecutionSnapshot.ID && applied.ExecutionSnapshot.PreviousID == initial.ExecutionSnapshot.ID && applied.PendingExecutionChange == nil)
	check("same_profile_trust", func() bool { b, _ := os.ReadFile(configPath); return string(b) == config }())
	expected := []string{"v1", "v2"}
	if mixed {
		expected = []string{"pack", "v1", "v2"}
		bundle, loose := false, false
		for _, e := range applied.View.PluginEvidence {
			if e.AssetID == "bundle" {
				bundle = e.Verified && len(e.Components) == 3
			}
			if e.AssetID == "loose" {
				loose = e.Verified && len(e.Components) == 1 && e.Components[0].Kind == "skill"
			}
		}
		check("mixed_components_verified", bundle && loose)
		check("independent_skill_asset_recorded", slices.ContainsFunc(applied.ExecutionSnapshot.CapabilityRefs, func(r app.CapabilityReference) bool { return r.Asset.ID == "loose" && r.Asset.Kind == "skill" }))
	}
	if worktree {
		files := applied.ExecutionSnapshot.CodexProjectHooks.Files
		check("worktree_source_union", len(files) == 3)
		found := false
		for _, file := range files {
			if file.File.Path == path {
				found = file.Discovered && !file.Declared
			}
		}
		check("worktree_actual_main_source", found)
	}
	turn("after", expected)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	f.Restart(t)
	a = open()
	resumed, err := a.Apply(app.Command{ID: "cold-resume", Kind: "resume", SessionID: initial.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitNativeApplication(t, a, func(s app.Snapshot) bool {
		for _, op := range s.Operations {
			if op.ID == resumed.OperationID {
				return op.Status == "succeeded"
			}
		}
		return false
	})
	restored := waitIdle()
	samples["restored"] = restored
	check("cold_keeps_new_snapshot", restored.ExecutionSnapshot.ID == applied.ExecutionSnapshot.ID && restored.Scope.NativeID == initial.Scope.NativeID)
	turn("cold", expected)
	if mixed {
		write(path, v1)
		current := session(a.Snapshot())
		retiring := app.ExecutionChangeRequest{BaseSnapshotID: current.ExecutionSnapshot.ID, AdoptCodexHookSources: true, Selections: []app.CapabilitySelection{{AssetID: "bundle", Mode: "inherit"}, {AssetID: "loose", Mode: "inherit"}}}
		preview, err := a.PreviewExecutionChange(initial.ID, retiring)
		if err != nil {
			t.Fatal(err)
		}
		samples["retire_preview"] = preview
		retiring.PreviewHash = preview.PreviewHash
		if _, err := a.Apply(app.Command{ID: "retire-stage", Kind: "stage_execution", SessionID: initial.ID, ExecutionChange: &retiring}); err != nil {
			t.Fatal(err)
		}
		retireStaged := session(a.Snapshot())
		samples["retire_staged"] = retireStaged
		export("retire_staged")
		check("retire_staged_keeps_mixed_current", reflect.DeepEqual(retireStaged.ExecutionSnapshot, current.ExecutionSnapshot) && len(retireStaged.Scope.PluginSelections) == 2)
		commandPrefix = "retire-"
		if coldPending {
			reopenCandidate("retire_cold_pending", retireStaged)
		}
		if variant == "mixed-cold-policy-probe" {
			samples["cold_policy_probe"] = hookAdoptionColdPolicyProbe(t, f, retireStaged)
			return
		}
		command("prepare_execution")
		retirePrepared := session(a.Snapshot())
		samples["retire_prepared"] = retirePrepared
		export("retire_prepared")
		if coldPrepared {
			reopenCandidate("retire_cold_prepared", retirePrepared)
		}
		if variant == "mixed-cold-loss" || recoverBoundary != "" {
			calls := nativeConfigurationCallCount(t, f, initial.Scope.NativeID)
			boundary := recoverBoundary
			if boundary == "" {
				boundary = "bootstrap"
			}
			lost := f.DropPluginChangeReceipt(initial.Scope.NativeID, boundary)
			configCalls := 1
			if boundary == "final" {
				configCalls = 2
			}
			ref := app.ExecutionChangeRequest{BaseSnapshotID: retirePrepared.ExecutionSnapshot.ID, PendingChangeID: retirePrepared.PendingExecutionChange.ID}
			cmd := app.Command{ID: "retire-cold-lost", Kind: "apply_execution", SessionID: initial.ID, ExecutionChange: &ref}
			accepted, err := a.Apply(cmd)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case reply := <-lost:
				samples["lost_bootstrap_reply"] = reply
			case <-time.After(15 * time.Second):
				t.Fatal("cold bootstrap reply not dropped")
			}
			waitNativeApplication(t, a, func(s app.Snapshot) bool {
				for _, op := range s.Operations {
					if op.ID == accepted.OperationID {
						return op.Status == "uncertain"
					}
				}
				return false
			})
			uncertain := session(a.Snapshot())
			samples["cold_uncertain"] = uncertain
			export("cold_uncertain")
			check("cold_loss_no_promotion", uncertain.ExecutionSnapshot.ID == retirePrepared.ExecutionSnapshot.ID && uncertain.PendingExecutionChange.Snapshot.ID == retirePrepared.PendingExecutionChange.Snapshot.ID && uncertain.ExecutionHold)
			duplicate, err := a.Apply(cmd)
			check("cold_loss_idempotent", err == nil && duplicate.OperationID == accepted.OperationID)
			_, err = a.Apply(app.Command{ID: "cold-loss-no-input", Kind: "send", SessionID: initial.ID, Text: "must not send"})
			check("cold_loss_blocks_input", err != nil)
			check("cold_loss_expected_configuration_calls", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == calls+configCalls)
			reopenCandidate("cold_loss_reopened", uncertain)
			still := session(a.Snapshot())
			check("cold_loss_still_unknown", still.Status == "unknown" && still.PendingExecutionChange.Status == "applying")
			check("cold_loss_no_replay_after_restart", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == calls+configCalls)
			if recoverBoundary != "" {
				request := app.ExecutionRecoveryRequest{BaseSnapshotID: retirePrepared.ExecutionSnapshot.ID, PendingChangeID: uncertain.PendingExecutionChange.ID, OperationID: accepted.OperationID}
				denied, err := a.ReadExecutionRecovery(context.Background(), initial.ID, request)
				check("recovery_requires_original_file", err == nil && !denied.Satisfied)
				write(path, v2)
				ready, err := a.ReadExecutionRecovery(context.Background(), initial.ID, request)
				if err != nil {
					t.Fatal(err)
				}
				samples["cold_recovery_check"] = ready
				check("cold_recovery_ready", ready.Satisfied)
				beforeRecoverCalls := nativeConfigurationCallCount(t, f, initial.Scope.NativeID)
				var recoveryLost <-chan runtime.NativeConfigurationReply
				if recoveryLossBoundary != "" {
					recoveryLost = f.DropPluginChangeReceipt(initial.Scope.NativeID, recoveryLossBoundary)
				}
				recovering, err := a.Apply(app.Command{ID: "recover-cold-original", Kind: "recover_execution", SessionID: initial.ID, ExecutionRecovery: &request})
				if err != nil {
					t.Fatal(err)
				}
				if recoveryLossBoundary != "" {
					select {
					case reply := <-recoveryLost:
						samples["lost_recovery_reply"] = reply
					case <-time.After(15 * time.Second):
						t.Fatal("recovery reply not dropped")
					}
					waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
						for _, op := range snapshot.Operations {
							if op.ID == recovering.OperationID {
								if op.Status == "not_sent" || op.Status == "failed" || op.Status == "succeeded" {
									t.Fatalf("recovery loss misclassified: %s %s", op.Status, op.Error)
								}
								return op.Status == "uncertain"
							}
						}
						return false
					})
					lostState := session(a.Snapshot())
					samples["recovery_uncertain"] = lostState
					export("recovery_uncertain")
					check("double_loss_keeps_original", reflect.DeepEqual(lostState.Scope, retirePrepared.Scope) && reflect.DeepEqual(lostState.ExecutionSnapshot, retirePrepared.ExecutionSnapshot) && reflect.DeepEqual(lostState.ExecutionHistory, retirePrepared.ExecutionHistory))
					check("double_loss_keeps_both_operations", lostState.PendingExecutionChange.OperationID == accepted.OperationID && lostState.PendingExecutionChange.RecoveryOperationID == recovering.OperationID && accepted.OperationID != recovering.OperationID)
					check("double_loss_not_archived", lostState.PendingExecutionChange.Status == "recovering" && len(lostState.ExecutionChangeAttempts) == 0 && lostState.ExecutionHold)
					expectedRecoveryCalls := 1
					if recoveryLossBoundary == "final" {
						expectedRecoveryCalls = 2
					}
					check("double_loss_expected_native_calls", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == beforeRecoverCalls+expectedRecoveryCalls)
					duplicate, err := a.Apply(app.Command{ID: "recover-cold-original", Kind: "recover_execution", SessionID: initial.ID, ExecutionRecovery: &request})
					check("double_loss_idempotent", err == nil && duplicate.OperationID == recovering.OperationID)
					reopenCandidate("recovery_loss_reopened", lostState)
					restored := session(a.Snapshot())
					check("double_loss_durable_unknown", restored.Status == "unknown" && restored.PendingExecutionChange.Status == "recovering" && restored.PendingExecutionChange.RecoveryOperationID == recovering.OperationID)
					noAck := false
					for _, op := range a.Snapshot().Operations {
						if op.ID == recovering.OperationID {
							noAck = op.Status == "uncertain" && op.Receipt.ConfigurationRecovery == nil
						}
					}
					check("double_loss_no_fabricated_ack", noAck)
					_, err = a.ReadExecutionRecovery(context.Background(), initial.ID, request)
					check("double_loss_cannot_reopen_first_recovery_gate", err != nil)
					ref := app.ExecutionChangeRequest{BaseSnapshotID: retirePrepared.ExecutionSnapshot.ID, PendingChangeID: uncertain.PendingExecutionChange.ID}
					for _, kind := range []string{"send", "resume", "prepare_execution", "apply_execution", "discard_execution", "recover_execution"} {
						cmd := app.Command{ID: "double-loss-reject-" + kind, Kind: kind, SessionID: initial.ID}
						if kind == "send" {
							cmd.Text = "must not send"
						}
						if kind == "prepare_execution" || kind == "apply_execution" || kind == "discard_execution" {
							cmd.ExecutionChange = &ref
						}
						if kind == "recover_execution" {
							cmd.ExecutionRecovery = &request
						}
						_, err = a.Apply(cmd)
						check("double_loss_rejects_"+kind, err != nil)
					}
					check("double_loss_no_replay_after_restart", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == beforeRecoverCalls+expectedRecoveryCalls)
					return
				}
				waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
					for _, op := range snapshot.Operations {
						if op.ID == recovering.OperationID {
							if op.Status == "not_sent" || op.Status == "uncertain" || op.Status == "failed" {
								t.Fatalf("cold recovery %s: %s", op.Status, op.Error)
							}
							return op.Status == "succeeded"
						}
					}
					return false
				})
				recovered := session(a.Snapshot())
				samples["cold_recovered"] = recovered
				export("cold_recovered")
				check("cold_recovery_keeps_scope_snapshot", reflect.DeepEqual(recovered.Scope, retirePrepared.Scope) && reflect.DeepEqual(recovered.ExecutionSnapshot, retirePrepared.ExecutionSnapshot))
				check("cold_recovery_archives_candidate", recovered.PendingExecutionChange == nil && len(recovered.ExecutionChangeAttempts) == 1 && recovered.ExecutionChangeAttempts[0].Change.Snapshot.ID == uncertain.PendingExecutionChange.Snapshot.ID)
				check("cold_recovery_keeps_history", reflect.DeepEqual(recovered.ExecutionHistory, retirePrepared.ExecutionHistory) && reflect.DeepEqual(recovered.View.Turns, retirePrepared.View.Turns))
				check("cold_recovery_paused", recovered.Paused && !recovered.ExecutionHold)
				wantRecoverCalls := 1
				if boundary != "bootstrap" {
					wantRecoverCalls = 2
				}
				check("cold_recovery_exact_native_boundary", nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == beforeRecoverCalls+wantRecoverCalls)
				duplicate, err := a.Apply(app.Command{ID: "recover-cold-original", Kind: "recover_execution", SessionID: initial.ID, ExecutionRecovery: &request})
				check("cold_recovery_command_idempotent", err == nil && duplicate.OperationID == recovering.OperationID && nativeConfigurationCallCount(t, f, initial.Scope.NativeID) == beforeRecoverCalls+wantRecoverCalls)
				resumed, err := a.Apply(app.Command{ID: "resume-cold-recovered", Kind: "resume", SessionID: initial.ID})
				if err != nil {
					t.Fatal(err)
				}
				waitNativeApplication(t, a, func(snapshot app.Snapshot) bool {
					for _, op := range snapshot.Operations {
						if op.ID == resumed.OperationID {
							return op.Status == "succeeded"
						}
					}
					return false
				})
				waitIdle()
				turn("cold-recovered", []string{"pack", "v1", "v2"})
				samples["cold_recovered_resumed"] = session(a.Snapshot())
				export("cold_recovered_resumed")
			}
			return
		}
		command("apply_execution")
		retired := waitIdle()
		samples["retired"] = retired
		export("retired")
		check("retired_sources_absent", len(retired.Scope.PluginSelections) == 0 && len(retired.Scope.SkillSelections) == 0 && len(retired.View.PluginEvidence) == 0 && len(retired.View.SkillEvidence) == 0)
		check("third_snapshot_same_id", retired.ExecutionSnapshot.Revision == 3 && retired.ExecutionSnapshot.PreviousID == applied.ExecutionSnapshot.ID && retired.Scope.NativeID == initial.Scope.NativeID)
		turn("retired", []string{"v1"})
	}

}
