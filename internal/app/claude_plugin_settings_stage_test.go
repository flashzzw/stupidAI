package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"codex-netcap/internal/runtime"
)

func pluginSettingsStageRequest(t *testing.T, a *Application, id string) PluginSettingsStageRequest {
	t.Helper()
	catalog, err := a.ReadSessionInstalledPlugins(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return PluginSettingsStageRequest{CommandID: "save-settings", BaseSnapshotID: a.Snapshot().Sessions[0].ExecutionSnapshot.ID,
		PluginSettingsPreviewRequest: PluginSettingsPreviewRequest{SourceKey: "selected@local", Mode: "disabled", SourceVersion: catalog.SourceVersion}}
}

func assertNoPluginSettingsFile(t *testing.T, profile string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(profile, "custom/.netcap-settings-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("private settings remain: %v %v", files, err)
	}
}

func TestInstalledPluginSettingsStagePersistsAndDiscards(t *testing.T) {
	a, f, id, market, _ := pluginSettingsPreviewFixture(t, "valid")
	request := pluginSettingsStageRequest(t, a, id)
	before := a.Snapshot().Sessions[0]
	original, _ := os.ReadFile(before.Scope.Instance.SettingsFile)
	receipt, err := a.StageSessionPluginSettings(context.Background(), id, request)
	if err != nil || receipt.Status != "pending" || receipt.PendingChangeID == "" || receipt.OperationID != "" {
		t.Fatalf("stage: %+v %v", receipt, err)
	}
	after := a.Snapshot().Sessions[0]
	change := after.PendingExecutionChange
	if change == nil || change.ID != receipt.PendingChangeID || change.Snapshot.SchemaVersion != 2 || change.Snapshot.ID == before.ExecutionSnapshot.ID || change.Snapshot.PreviousID != before.ExecutionSnapshot.ID || change.Snapshot.Revision != 2 {
		t.Fatalf("candidate linkage: %+v", change)
	}
	proof := change.Snapshot.ClaudeInstalledPluginSettings
	if proof == nil || proof.Preview.SourceVersion != request.SourceVersion || proof.Preview.StartupSourceVersion != proof.StartupSettings.Fingerprint() || !proof.Preview.Validation.Preservation.Matches || proof.Preview.Complete || proof.Resource.Path != filepath.Join(market, "selected") || proof.Resource.SHA256 == "" {
		t.Fatal("candidate omitted server-side observations or declaration resources")
	}
	if !reflect.DeepEqual(before.ExecutionSnapshot, after.ExecutionSnapshot) || !reflect.DeepEqual(before.Scope, after.Scope) || before.Generation != after.Generation || before.ControlMode != after.ControlMode || before.Paused != after.Paused || after.ExecutionHold || f.creates.Load() != 1 || f.sends.Load() != 0 || len(a.Snapshot().Operations) != 1 {
		t.Fatal("save changed active execution or control")
	}
	encoded, _ := json.Marshal(change)
	if strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatal("candidate contains source contents")
	}
	current, _ := os.ReadFile(before.Scope.Instance.SettingsFile)
	if string(current) != string(original) {
		t.Fatal("save rewrote original settings")
	}
	assertNoPluginSettingsFile(t, before.Scope.Instance.Profile)
	// Query copies cannot alter the durable proposal.
	proof.Preview.Setting.Enabled = true
	durable, _ := a.ReadExecution(id)
	if durable.PendingChange.Snapshot.ClaudeInstalledPluginSettings.Preview.Setting.Enabled {
		t.Fatal("caller changed persisted selection")
	}
	cfg := queryCopy(a.config)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, cfg, f)
	reopened, _ := a.ReadExecution(id)
	if !reflect.DeepEqual(reopened.PendingChange, durable.PendingChange) || !reflect.DeepEqual(reopened.Snapshot, before.ExecutionSnapshot) {
		t.Fatal("restart lost proposal or changed effective snapshot")
	}
	// Neither idempotent retry nor discard may need a working native reader or
	// current source files once the durable receipt/candidate is known.
	writePluginMetadata(t, filepath.Join(before.Scope.Instance.Profile, "settings_validation.js"), "raise RuntimeError('must not run')")
	writePluginMetadata(t, before.Scope.Instance.SettingsFile, "{invalid")
	if again, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil || again != receipt {
		t.Fatalf("durable retry reran validation: %+v %v", again, err)
	}
	changedRequest := request
	changedRequest.Mode = "enabled"
	if _, err := a.StageSessionPluginSettings(context.Background(), id, changedRequest); err == nil {
		t.Fatal("command ID reused for changed selection")
	}
	discard := ExecutionChangeRequest{BaseSnapshotID: before.ExecutionSnapshot.ID, PendingChangeID: durable.PendingChange.ID}
	if result, err := a.Apply(Command{ID: "discard-settings", Kind: "discard_execution", SessionID: id, ExecutionChange: &discard}); err != nil || result.Status != "discarded" {
		t.Fatalf("discard needed source/native admission: %+v %v", result, err)
	}
	if a.Snapshot().Sessions[0].PendingExecutionChange != nil || f.sends.Load() != 0 {
		t.Fatal("discard did native work or kept candidate")
	}
	if again, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil || again != receipt || a.Snapshot().Sessions[0].PendingExecutionChange != nil {
		t.Fatal("retry after discard recreated candidate")
	}
}

func TestInstalledPluginSettingsStagePreservesInflightReceipt(t *testing.T) {
	a, f, id, _, _ := pluginSettingsPreviewFixture(t, "valid")
	request := pluginSettingsStageRequest(t, a, id)
	f.block = make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(f.block) })
	sent, err := a.Apply(Command{ID: "send", Kind: "send", SessionID: id, Text: "original configuration"})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool { return f.sends.Load() == 1 })
	before := a.Snapshot().Sessions[0]
	if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	after := a.Snapshot().Sessions[0]
	if before.Generation != after.Generation || before.Status != after.Status || before.Paused != after.Paused || !reflect.DeepEqual(before.Scope, after.Scope) {
		t.Fatal("candidate stole in-flight operation ownership")
	}
	release.Do(func() { close(f.block) })
	waitTest(t, func() bool { return a.Snapshot().Sessions[0].Status == "idle" })
	for _, operation := range a.Snapshot().Operations {
		if operation.ID == sent.OperationID && (operation.Status != "succeeded" || operation.ExecutionSnapshotID != before.ExecutionSnapshot.ID) {
			t.Fatal("original receipt failed to settle")
		}
	}
}

func TestInstalledPluginSettingsCandidateCannotBypassApply(t *testing.T) {
	a, f, id, _, _ := pluginSettingsPreviewFixture(t, "valid")
	request := pluginSettingsStageRequest(t, a, id)
	if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	// Earlier format-2 candidates did not bind a reusable execution file.
	// They remain non-executable after this reader gains the new protocol.
	a.mu.Lock()
	legacy := a.state.Sessions[id]
	legacy.PendingExecutionChange.Snapshot.ClaudeInstalledPluginSettings.Binding = nil
	a.state.Sessions[id] = legacy
	a.mu.Unlock()
	before := a.Snapshot()
	session := before.Sessions[0]
	ref := ExecutionChangeRequest{BaseSnapshotID: request.BaseSnapshotID, PendingChangeID: session.PendingExecutionChange.ID}
	for _, kind := range []string{"prepare_execution", "apply_execution", "stage_execution"} {
		if _, err := a.Apply(Command{ID: kind, Kind: kind, SessionID: id, ExecutionChange: &ref}); err == nil {
			t.Fatalf("unsupported candidate accepted via %s: %v", kind, err)
		}
	}
	if _, err := a.PreviewExecutionChange(id, ref); err == nil {
		t.Fatal("ordinary preview can replace the installed-plugin proposal")
	}
	request.CommandID = "second-candidate"
	if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err == nil {
		t.Fatal("new candidate overwrote pending version")
	}
	// A fabricated queued operation must also fail the dispatch-time check.
	copy := queryCopy(session)
	copy.ExecutionHold = true
	copy.PendingExecutionChange.OperationID = "queued"
	op := Operation{ID: "queued", Intent: runtime.Intent{Kind: "prepare_execution", Scope: copy.Scope, Permission: copy.Permission}}
	if err := a.verifyExecutionChangeOperation(copy, op); err == nil {
		t.Fatal("dispatch accepted unsupported candidate")
	}
	copy.PendingExecutionChange.Snapshot.SchemaVersion = 1
	if err := a.verifySavedExecutionChange(copy); err == nil {
		t.Fatal("downgrading schema bypassed installed settings restriction")
	}
	copy = queryCopy(session)
	copy.ExecutionSnapshot = copy.PendingExecutionChange.Snapshot
	if err := verifyExecutionBinding(copy); err == nil {
		t.Fatal("candidate treated as current executable snapshot")
	}
	after := a.Snapshot()
	if before.Sequence != after.Sequence || !reflect.DeepEqual(session, after.Sessions[0]) || len(after.Operations) != 1 || f.creates.Load() != 1 || f.sends.Load() != 0 {
		t.Fatal("rejected apply modified state or launched native work")
	}
}

func TestInstalledPluginSettingsStageRejectsChangesDuringObservation(t *testing.T) {
	for _, scenario := range []string{"resource", "registration", "ownership", "base", "other-candidate", "cancel", "close"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, id, market, registry := pluginSettingsPreviewFixture(t, "slow")
			request := pluginSettingsStageRequest(t, a, id)
			profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := a.StageSessionPluginSettings(ctx, id, request); done <- err }()
			waitTest(t, func() bool { _, err := os.Stat(filepath.Join(profile, "preview-started")); return err == nil })
			switch scenario {
			case "resource":
				writePluginMetadata(t, filepath.Join(market, "selected/skill.txt"), "changed after resource sampling")
			case "registration":
				body, _ := os.ReadFile(registry)
				writePluginMetadata(t, registry, string(body)+"\n")
			case "ownership":
				if _, err := a.Apply(Command{ID: "takeover", Kind: "takeover", SessionID: id}); err != nil {
					t.Fatal(err)
				}
			case "base", "other-candidate":
				a.mu.Lock()
				s := a.state.Sessions[id]
				if scenario == "base" {
					s.ExecutionSnapshot.ID = "another-effective-version"
				} else {
					s.PendingExecutionChange = &ExecutionChange{ID: "another-pending", Status: "pending", Snapshot: queryCopy(s.ExecutionSnapshot)}
				}
				a.state.Sessions[id] = s
				a.mu.Unlock()
			case "cancel":
				cancel()
			case "close":
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
			}
			writePluginMetadata(t, filepath.Join(profile, "preview-release"), "release")
			if err := <-done; err == nil {
				t.Fatal("changed observation saved a proposal")
			}
			session := a.Snapshot().Sessions[0]
			if session.PendingExecutionChange != nil && (scenario != "other-candidate" || session.PendingExecutionChange.ID != "another-pending") {
				t.Fatal("failed save created or overwrote a candidate")
			}
			if _, ok := a.state.Commands[request.CommandID]; ok {
				t.Fatal("failed save left a success receipt")
			}
			assertNoPluginSettingsFile(t, profile)
		})
	}
}

func TestInstalledPluginSettingsStageConcurrentRetry(t *testing.T) {
	a, _, id, _, _ := pluginSettingsPreviewFixture(t, "slow")
	request := pluginSettingsStageRequest(t, a, id)
	profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
	worker := filepath.Join(profile, "settings_validation.js")
	body, _ := os.ReadFile(worker)
	writePluginMetadata(t, worker, strings.Replace(string(body), "(root/'preview-started').write_text('started')", "with (root/'preview-started').open('a') as f:f.write('started\\n')", 1))
	type outcome struct {
		receipt CommandReceipt
		err     error
	}
	done := make(chan outcome, 2)
	call := func() { r, e := a.StageSessionPluginSettings(context.Background(), id, request); done <- outcome{r, e} }
	go call()
	waitTest(t, func() bool { _, e := os.Stat(filepath.Join(profile, "preview-started")); return e == nil })
	changed := request
	changed.Mode = "enabled"
	if _, err := a.StageSessionPluginSettings(context.Background(), id, changed); err == nil {
		t.Fatal("in-flight command ID reused with different content")
	}
	go call()
	writePluginMetadata(t, filepath.Join(profile, "preview-release"), "release")
	first, second := <-done, <-done
	if first.err != nil || second.err != nil || first.receipt != second.receipt {
		t.Fatalf("concurrent receipts diverged: %+v %+v", first, second)
	}
	started, _ := os.ReadFile(filepath.Join(profile, "preview-started"))
	if string(started) != "started\n" {
		t.Fatal("concurrent retry launched a second native validation")
	}
}

func TestInstalledPluginSettingsStageConcurrentCandidates(t *testing.T) {
	a, _, id, _, _ := pluginSettingsPreviewFixture(t, "slow")
	request := pluginSettingsStageRequest(t, a, id)
	profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
	worker := filepath.Join(profile, "settings_validation.js")
	body, _ := os.ReadFile(worker)
	writePluginMetadata(t, worker, strings.Replace(string(body), "(root/'preview-started').write_text('started')", "with (root/'preview-started').open('a') as f:f.write('started\\n')", 1))
	type outcome struct {
		receipt CommandReceipt
		err     error
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan outcome, 2)
	for _, commandID := range []string{"first-candidate", "second-candidate"} {
		copy := request
		copy.CommandID = commandID
		go func() { r, err := a.StageSessionPluginSettings(ctx, id, copy); done <- outcome{r, err} }()
	}
	waitTest(t, func() bool {
		body, _ := os.ReadFile(filepath.Join(profile, "preview-started"))
		return strings.Count(string(body), "started\n") == 2
	})
	writePluginMetadata(t, filepath.Join(profile, "preview-release"), "release")
	first, second := <-done, <-done
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || second.err == nil || !strings.Contains(second.err.Error(), "待应用选择已变化") {
		t.Fatalf("concurrent candidates did not produce exactly one winner: %+v %+v", first, second)
	}
	session := a.Snapshot().Sessions[0]
	if session.PendingExecutionChange.ID != first.receipt.PendingChangeID || session.PendingExecutionChange.CommandID != first.receipt.ID || len(a.Snapshot().Operations) != 1 {
		t.Fatal("concurrent candidate overwrote the first committed version")
	}
	assertNoPluginSettingsFile(t, profile)
}

func TestInstalledPluginSettingsStageBindsCompleteStartupReport(t *testing.T) {
	for _, kind := range []string{"timestamp-only", "registry", "source", "path-binding", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			a, _, id, _, _ := pluginSettingsPreviewFixture(t, "valid")
			request := pluginSettingsStageRequest(t, a, id)
			profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
			reader := fmt.Sprintf(`import json,sys
from pathlib import Path
r=json.load(sys.stdin)
p=Path(__file__).with_name('source-read-count')
n=int(p.read_text())+1 if p.exists() else 1
p.write_text(str(n))
out={'version':1,'sdk_version':'0.3.280','source':'claude.sdk.observed_startup_reads','cwd':r['cwd'],'at':f'2026-10-09T00:00:0{n}Z','resolver_returned':True,'read_complete':True,'complete':False,'files':[],'sources':[],'registry':[],'issues':[],'limitations':[],'policy_helpers_present':False,'startup_conflicts':[]}
kind=%q
if n==3:
 if kind=='registry':out['registry']=[{'hive':'HKLM','outcome':'present'}]
 if kind=='source':out['sources']=[{'source':'managed','settings_sha256':'a'*64,'has_policy_helpers':False}]
 if kind=='path-binding':out['path_bindings']=[{'path':r['cwd'],'resolved_path':r['cwd'],'identity_sha256':'b'*64}]
 if kind=='incomplete':out['read_complete']=False
print(json.dumps(out))
`, kind)
			writePluginMetadata(t, filepath.Join(profile, "settings_preflight.js"), reader)
			_, err := a.StageSessionPluginSettings(context.Background(), id, request)
			if (err == nil) != (kind == "timestamp-only") {
				t.Fatalf("complete report binding %s: %v", kind, err)
			}
			if kind != "timestamp-only" && a.Snapshot().Sessions[0].PendingExecutionChange != nil {
				t.Fatal("late startup drift saved a candidate")
			}
			assertNoPluginSettingsFile(t, profile)
		})
	}
}

func TestInstalledPluginSettingsStageRejectsUnusableProof(t *testing.T) {
	for _, scenario := range []string{"schema", "missing-preservation", "other-settings", "target", "stale-registration", "local-drift", "nested-link", "request", "command-collision"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, id, market, registry := pluginSettingsPreviewFixture(t, "valid")
			request := pluginSettingsStageRequest(t, a, id)
			profile := a.Snapshot().Sessions[0].Scope.Instance.Profile
			worker := filepath.Join(profile, "settings_validation.js")
			body, _ := os.ReadFile(worker)
			switch scenario {
			case "schema":
				writePluginMetadata(t, worker, strings.Replace(string(body), "'schema_valid':True,'errors':[]", "'schema_valid':False,'errors':[{'file':r['settings_file'],'field':'permissions.allow'}]", 1))
			case "missing-preservation":
				writePluginMetadata(t, worker, strings.Replace(string(body), "'preservation':preservation,", "", 1))
			case "other-settings":
				body = []byte(strings.Replace(string(body), "'candidate':signature,'differences':[],'matches':True", "'candidate':{**signature,'effective_sha256':'e'*64},'differences':['effective_settings'],'matches':False", 1))
				writePluginMetadata(t, worker, string(body))
			case "target":
				writePluginMetadata(t, worker, strings.Replace(string(body), "'actual_enabled':s['enabled'],'matches':True", "'actual_enabled':not s['enabled'],'matches':False", 1))
			case "stale-registration":
				body, _ := os.ReadFile(registry)
				writePluginMetadata(t, registry, string(body)+"\n")
			case "local-drift":
				writePluginMetadata(t, filepath.Join(a.Snapshot().Sessions[0].Scope.Cwd, ".claude/settings.local.json"), `{"env":{"NEW":"value"}}`)
			case "nested-link":
				if err := os.Symlink("/absent", filepath.Join(market, "selected/link")); err != nil {
					t.Fatal(err)
				}
			case "request":
				request.Mode = "inherit"
			case "command-collision":
				request.CommandID = "create"
			}
			if _, err := a.StageSessionPluginSettings(context.Background(), id, request); err == nil {
				t.Fatal("unusable observation saved a candidate")
			}
			if a.Snapshot().Sessions[0].PendingExecutionChange != nil {
				t.Fatal("failed observation persisted a candidate")
			}
			assertNoPluginSettingsFile(t, profile)
		})
	}
}
