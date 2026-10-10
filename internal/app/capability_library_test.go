package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func libraryAsset(t *testing.T, id, version, mode string) CapabilityAsset {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Fixture\nUse the local helper.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.py"), []byte("print('"+version+"')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return CapabilityAsset{ID: id, Name: "Shared method", Kind: "skill", Version: version, Source: dir, Backends: []string{"codex", "claude"}, Management: mode}
}
func libraryChange(t *testing.T, a *Application, key string, r CapabilityChangeRequest) CommandReceipt {
	t.Helper()
	preview, err := a.PreviewCapabilityChange(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.PreviewHash = preview.PreviewHash
	receipt, err := a.Apply(Command{ID: key, Kind: "manage_capability", CapabilityChange: &r})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func libraryRead(t *testing.T, a *Application, id string) CapabilityRead {
	t.Helper()
	items, err := a.ReadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Asset.ID == id {
			return item
		}
	}
	t.Fatal("asset not in library")
	return CapabilityRead{}
}
func createWithAsset(t *testing.T, a *Application, key, asset string) string {
	t.Helper()
	r, err := a.Apply(Command{ID: key, Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{asset}})
	if err != nil {
		t.Fatal(err)
	}
	waitTest(t, func() bool {
		d, err := a.ReadSession(r.SessionID, "", 50)
		return err == nil && d.Session.Status == "idle"
	})
	return r.SessionID
}
func TestManagedCapabilityVersionsKeepHistoricalSourcesAndAdmission(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	asset := libraryAsset(t, "method", "v1", "managed")
	libraryChange(t, a, "import-v1", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	v1 := libraryRead(t, a, asset.ID)
	if v1.Asset.Source == asset.Source || v1.Source == nil || v1.SourceState != "readable" {
		t.Fatal("managed import did not produce verified separate version")
	}
	sid := createWithAsset(t, a, "use-v1", asset.ID)
	before, _ := a.ReadExecution(sid)
	if err := os.WriteFile(filepath.Join(asset.Source, "helper.py"), []byte("changed original"), 0700); err != nil {
		t.Fatal(err)
	}
	read, _ := a.ReadExecution(sid)
	if read.Status != "unchanged" {
		t.Fatal("editing import source changed fixed version")
	}
	next := libraryAsset(t, asset.ID, "v2", "managed")
	preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "update", AssetID: asset.ID, BaseRevision: v1.Revision, Asset: &next})
	if err != nil || len(preview.Uses) != 1 {
		t.Fatalf("update impact: %+v %v", preview, err)
	}
	libraryChange(t, a, "import-v2", preview.Request)
	v2 := libraryRead(t, a, asset.ID)
	if len(v2.Versions) != 2 || v2.Asset.Source == v1.Asset.Source {
		t.Fatal("update overwrote fixed version")
	}
	sid2 := createWithAsset(t, a, "use-v2", asset.ID)
	read2, _ := a.ReadExecution(sid2)
	if read2.Snapshot.CapabilityRefs[0].Asset.Version != "v2" {
		t.Fatal("new execution did not choose new version")
	}
	read, _ = a.ReadExecution(sid)
	if digest(before.Snapshot) != digest(read.Snapshot) || read.Status != "unchanged" {
		t.Fatal("update changed old snapshot")
	}
	libraryChange(t, a, "disable", CapabilityChangeRequest{Action: "disable", AssetID: asset.ID, BaseRevision: v2.Revision})
	if _, err := a.Apply(Command{ID: "use-disabled", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{asset.ID}}); err == nil {
		t.Fatal("disabled asset admitted a new reference")
	}
	disabled := libraryRead(t, a, asset.ID)
	libraryChange(t, a, "remove", CapabilityChangeRequest{Action: "remove", AssetID: asset.ID, BaseRevision: disabled.Revision})
	if len(a.Workspace().CapabilityAssets) != 0 {
		t.Fatal("removed asset remains selectable")
	}
	removed := libraryRead(t, a, asset.ID)
	if !removed.Removed || len(removed.Versions) != 2 || len(removed.Uses) != 2 {
		t.Fatal("remove erased versions or references")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openFixture(t, c, f)
	read, _ = a.ReadExecution(sid)
	if read.Status != "unchanged" || digest(read.Snapshot) != digest(before.Snapshot) {
		t.Fatal("restart/removal changed original source")
	}
	if _, err := os.Stat(v1.Asset.Source); err != nil {
		t.Fatal("remove deleted a referenced fixed version")
	}
	if _, err := a.Apply(Command{ID: "continue-old", Kind: "send", SessionID: sid, Text: "old pinned source"}); err != nil {
		t.Fatal(err)
	}
}
func TestExternalLibraryChangesKeepSnapshotsAndReportSourceDrift(t *testing.T) {
	c, _ := executionFixture(t)
	a := openFixture(t, c, newFixture())
	sid := createExecution(t, a).SessionID
	initial := libraryRead(t, a, "shared")
	next := libraryAsset(t, "shared", "v2", "")
	libraryChange(t, a, "update-external", CapabilityChangeRequest{Action: "update", AssetID: "shared", BaseRevision: initial.Revision, Asset: &next})
	updated := libraryRead(t, a, "shared")
	if len(updated.Versions) != 2 || updated.Asset.Source != next.Source {
		t.Fatal("external source was moved or history lost")
	}
	read, _ := a.ReadExecution(sid)
	if read.Status != "unchanged" || read.Snapshot.CapabilityRefs[0].Asset.Version != "v1" {
		t.Fatal("catalog update rebound old execution")
	}
	if err := os.WriteFile(filepath.Join(c.CapabilityAssets[0].Source, "tool.py"), []byte("changed dependent script"), 0600); err != nil {
		t.Fatal(err)
	}
	read, _ = a.ReadExecution(sid)
	if read.Status != "drifted" {
		t.Fatal("external script change hidden")
	}
	if _, err := a.Apply(Command{ID: "drifted-send", Kind: "send", SessionID: sid, Text: "must not send"}); err == nil {
		t.Fatal("external drift accepted")
	}
}
func TestLibraryPreviewConflictsAndConcurrentCommandDedupe(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := libraryAsset(t, "same", "v1", "")
	preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Workspace().CapabilityAssets) != 0 {
		t.Fatal("preview persisted")
	}
	r := preview.Request
	r.PreviewHash = preview.PreviewHash
	cmd := Command{ID: "register", Kind: "manage_capability", CapabilityChange: &r}
	first, err := a.Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := a.Apply(cmd)
			if e != nil || got != first {
				t.Errorf("duplicate: %+v %v", got, e)
			}
		}()
	}
	wg.Wait()
	item := libraryRead(t, a, asset.ID)
	if _, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "update", AssetID: asset.ID, Asset: &asset, BaseRevision: item.Revision}); err == nil {
		t.Fatal("version label reused")
	}
	disabled := CapabilityChangeRequest{Action: "disable", AssetID: asset.ID, BaseRevision: item.Revision}
	preview, err = a.PreviewCapabilityChange(context.Background(), disabled)
	if err != nil {
		t.Fatal(err)
	}
	libraryChange(t, a, "remove", CapabilityChangeRequest{Action: "remove", AssetID: asset.ID, BaseRevision: item.Revision})
	disabled.PreviewHash = preview.PreviewHash
	if _, err = a.Apply(Command{ID: "stale", Kind: "manage_capability", CapabilityChange: &disabled}); err == nil {
		t.Fatal("stale preview overwrote remove")
	}
}
func TestManagedImportFailureLeavesNoSelectableVersion(t *testing.T) {
	c := fixtureConfig(t)
	a := openFixture(t, c, newFixture())
	asset := libraryAsset(t, "fails", "v1", "managed")
	preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	store := &failingStore{Persistence: a.store}
	store.fail.Store(true)
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
	r := preview.Request
	r.PreviewHash = preview.PreviewHash
	if _, err = a.Apply(Command{ID: "disk-failure", Kind: "manage_capability", CapabilityChange: &r}); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if len(a.Workspace().CapabilityAssets) != 0 {
		t.Fatal("failed import was published")
	}
	paths, err := filepath.Glob(filepath.Join(c.StateDir, "capabilities", "versions", "*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("uncommitted import not cleaned: %v %v", paths, err)
	}
}
func TestManagedVersionTamperingAndUnsafeNativeStateImportAreRejected(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := libraryAsset(t, "method", "v1", "managed")
	if err := os.WriteFile(filepath.Join(asset.Source, "auth.json"), []byte(`{"api_key":"fixture-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset}); err == nil {
		t.Fatal("CLI account state imported as capability")
	}
	if err := os.Remove(filepath.Join(asset.Source, "auth.json")); err != nil {
		t.Fatal(err)
	}
	libraryChange(t, a, "import", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	current := libraryRead(t, a, asset.ID)
	if err := os.WriteFile(filepath.Join(current.Asset.Source, "helper.py"), []byte("changed fixed version"), 0700); err != nil {
		t.Fatal(err)
	}
	if libraryRead(t, a, asset.ID).SourceState != "changed" {
		t.Fatal("fixed source tampering hidden")
	}
	if _, err := a.Apply(Command{ID: "tampered", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only", CapabilityRefs: []string{asset.ID}}); err == nil {
		t.Fatal("tampered version admitted")
	}
}
func TestCapabilityCatalogExportOmitsFilesPathsAndState(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := libraryAsset(t, "shared", "v1", "")
	if err := os.WriteFile(filepath.Join(asset.Source, "auth.json"), []byte(`{"api_key":"secret-not-for-export"}`), 0600); err != nil {
		t.Fatal(err)
	}
	libraryChange(t, a, "register", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	out, err := a.ExportCapabilityCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), asset.Source) || strings.Contains(string(b), "secret-not-for-export") || strings.Contains(string(b), "api_key") || len(out.Assets) != 1 || !out.Assets[0].SourceRequired {
		t.Fatalf("unsafe or misleading catalog export: %s", b)
	}
}

func TestLibraryConcurrentManagedRegistrationPublishesOnlyOneVersion(t *testing.T) {
	c := fixtureConfig(t)
	a := openFixture(t, c, newFixture())
	asset := libraryAsset(t, "parallel", "v1", "managed")
	preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	r := preview.Request
	r.PreviewHash = preview.PreviewHash
	cmd := Command{ID: "same-create", Kind: "manage_capability", CapabilityChange: &r}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := a.Apply(cmd); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	item := libraryRead(t, a, asset.ID)
	if len(item.Versions) != 1 {
		t.Fatal("retry added extra versions")
	}
	files, err := filepath.Glob(filepath.Join(c.StateDir, "capabilities", "versions", "*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("concurrent retry leaked imports: %v %v", files, err)
	}
}

func TestCapabilitySchemaUpgradePreservesExecutionRegistryAndCommands(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := newState()
	profile := registrationFixture(t, "saved")
	old.ExecutionProfiles[profile.Instance.ID] = profile
	old.Commands["old"] = CommandReceipt{ID: "old", Hash: "saved-hash", Status: "registered"}
	if err = store.Save(old); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec("DROP TABLE capability_library"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Load()
	if err != nil || digest(got.ExecutionProfiles) != digest(old.ExecutionProfiles) || digest(got.Commands) != digest(old.Commands) || len(got.CapabilityLibrary) != 0 {
		t.Fatalf("schema 4 migration lost state: %v", err)
	}
}

func TestLibraryRejectsSourceChangesAfterPreviewAndReportsNameCollisions(t *testing.T) {
	a := openFixture(t, fixtureConfig(t), newFixture())
	asset := libraryAsset(t, "one", "v1", "managed")
	preview, err := a.PreviewCapabilityChange(context.Background(), CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(asset.Source, "helper.py"), []byte("changed after preview"), 0700); err != nil {
		t.Fatal(err)
	}
	r := preview.Request
	r.PreviewHash = preview.PreviewHash
	if _, err := a.Apply(Command{ID: "stale-source", Kind: "manage_capability", CapabilityChange: &r}); err == nil {
		t.Fatal("stale source preview accepted")
	}
	libraryChange(t, a, "one", CapabilityChangeRequest{Action: "register", AssetID: asset.ID, Asset: &asset})
	other := libraryAsset(t, "two", "v1", "")
	libraryChange(t, a, "two", CapabilityChangeRequest{Action: "register", AssetID: other.ID, Asset: &other})
	for _, id := range []string{"one", "two"} {
		item := libraryRead(t, a, id)
		if len(item.SameNameWith) != 1 || item.SameNameWith[0] == id {
			t.Fatal("same-name identities merged or conflict hidden")
		}
	}
}

func TestLibraryStartupEditCannotOverridePersistedHistory(t *testing.T) {
	c, _ := executionFixture(t)
	f := newFixture()
	a := openFixture(t, c, f)
	old := libraryRead(t, a, "shared")
	next := libraryAsset(t, "shared", "v2", "")
	libraryChange(t, a, "update", CapabilityChangeRequest{Action: "update", AssetID: "shared", BaseRevision: old.Revision, Asset: &next})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Original startup definition is a stable base; a new conflicting definition
	// must not silently erase the saved v2 choice when the process restarts.
	c.CapabilityAssets[0].Version = "unrelated-change"
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if opened, err := Open(c, store, f); err == nil {
		opened.Close()
		t.Fatal("startup edit replaced persisted asset history")
	}
}

func TestCapabilityFailedConfigurationAttemptRetainsReference(t *testing.T) {
	c := fixtureConfig(t)
	asset := libraryAsset(t, "method", "v2", "external_reference")
	c.CapabilityAssets = []CapabilityAsset{asset}
	a := openFixture(t, c, newFixture())
	a.mu.Lock()
	next := clone(a.state)
	next.Sessions["failed-session"] = Session{ID: "failed-session", Title: "Failed candidate", ExecutionChangeAttempts: []ExecutionChangeAttempt{{Change: ExecutionChange{ID: "change", Snapshot: &ExecutionSnapshot{ID: "candidate", CapabilityRefs: []CapabilityReference{{Asset: asset}}}}}}}
	err := a.commit(next, "fixture.failed_candidate", "failed-session")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := libraryRead(t, a, asset.ID)
	if len(got.Uses) != 1 || got.Uses[0].Phase != "failed_attempt" || got.Uses[0].SnapshotID != "candidate" || got.Uses[0].Version != "v2" {
		t.Fatalf("failed candidate reference lost: %+v", got.Uses)
	}
}
