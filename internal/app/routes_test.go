package app

import (
	"encoding/json"
	"strings"
	"testing"

	"codex-netcap/internal/runtime"
)

func TestRouteIdentitySeparatesConfigurationEvidence(t *testing.T) {
	base := runtime.Route{ID: "route", InstanceID: "account-a", Provider: "fixture", Endpoint: "https://a.example/v1", Model: "same-model", CredentialRef: "env:KEY_A", CredentialRevision: "1", ConfigRevision: "1"}
	mutations := []func(*runtime.Route){
		func(r *runtime.Route) { r.InstanceID = "account-b" },
		func(r *runtime.Route) { r.Provider = "other" },
		func(r *runtime.Route) { r.Endpoint = "https://b.example/v1" },
		func(r *runtime.Route) { r.Model = "other-model" },
		func(r *runtime.Route) { r.CredentialRef = "env:KEY_B" },
		func(r *runtime.Route) { r.CredentialRevision = "2" },
		func(r *runtime.Route) { r.ConfigRevision = "2" },
	}
	for _, mutate := range mutations {
		other := base
		mutate(&other)
		if routeIdentity(other) == routeIdentity(base) {
			t.Fatalf("different route reused health identity: %+v", other)
		}
	}
}

func TestCredentialValueRotationInvalidatesHealthAndOldSession(t *testing.T) {
	c := probeConfig(t)
	f := &healthRuntime{&probeRuntime{fixtureRuntime: newFixture()}}
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	sendHealthFixture(t, a, created.SessionID, "success", "work")
	if a.Workspace().RouteStatuses[0].Health != "healthy" {
		t.Fatal("missing initial health")
	}
	t.Setenv("NETCAP_PROBE_TEST_KEY", "rotated-value-without-revision")
	if a.Workspace().RouteStatuses[0].Health != "unknown" {
		t.Fatal("rotated credential reused old health")
	}
	if _, err := a.Apply(Command{ID: "old-send", Kind: "send", SessionID: created.SessionID, Text: "must not send"}); err == nil {
		t.Fatal("old session adopted new credential")
	}
	if f.sends.Load() != 1 {
		t.Fatal("credential drift reached runtime")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openFixture(t, c, f)
	if _, err := b.Apply(Command{ID: "restart-send", Kind: "send", SessionID: created.SessionID, Text: "must not send"}); err == nil {
		t.Fatal("restart silently rebound old credential")
	}
	if _, err := b.Apply(Command{ID: "fresh-create", Kind: "create", ProjectID: "project", RouteID: "route", Permission: "read_only"}); err != nil {
		t.Fatal(err)
	}
	read, err := b.ReadSession(created.SessionID, "", 50)
	if err != nil || len(read.Session.View.Messages) != 2 {
		t.Fatalf("rotation removed original history: %+v %v", read, err)
	}
	encoded, _ := json.Marshal(b.Snapshot())
	for _, secret := range []string{"fixture-credential", "rotated-value-without-revision"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("credential value escaped into stored/query state")
		}
	}
}

func TestLegacyEnvSessionCannotInventCredentialSnapshot(t *testing.T) {
	c := probeConfig(t)
	f := &probeRuntime{fixtureRuntime: newFixture()}
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	session := state.Sessions[created.SessionID]
	session.Scope.CredentialStamp, session.RouteFingerprint = "", ""
	state.Sessions[created.SessionID] = session
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	b := openFixture(t, c, f)
	if _, err := b.Apply(Command{ID: "legacy-send", Kind: "send", SessionID: created.SessionID, Text: "work"}); err == nil {
		t.Fatal("legacy record invented historical credential proof")
	}
	if f.sends.Load() != 0 {
		t.Fatal("unverified legacy credential reached runtime")
	}
}

func TestRouteChangeAcrossRestartRejectsOldSessionInput(t *testing.T) {
	c := fixtureConfig(t)
	f := newFixture()
	a := openFixture(t, c, f)
	created := createFixture(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	c.Routes[0].Endpoint = "http://127.0.0.1:19876/v1"
	c.Routes[0].ConfigRevision = "new-revision"
	b := openFixture(t, c, f)
	waitTest(t, func() bool { return b.Snapshot().Sessions[0].Connection == "ready" })
	if _, err := b.Apply(Command{ID: "old-route-send", Kind: "send", SessionID: created.SessionID, Text: "must not send"}); err == nil {
		t.Fatal("old session silently adopted new route")
	}
	if f.sends.Load() != 0 {
		t.Fatal("configuration drift reached runtime")
	}
	read, err := b.ReadSession(created.SessionID, "", 50)
	if err != nil || read.Session.Scope.Route.Endpoint == c.Routes[0].Endpoint {
		t.Fatal("original route identity was overwritten")
	}
	if _, err := b.Apply(Command{ID: "takeover-drift", Kind: "takeover", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
}

func TestExistingAlphaBindingIsAdoptedOnlyFromMatchingSnapshot(t *testing.T) {
	for _, changed := range []bool{false, true} {
		c := fixtureConfig(t)
		c.Instances[0].EnvAllow = []string{}
		f := newFixture()
		a := openFixture(t, c, f)
		created := createFixture(t, a)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := OpenStore(c.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		state, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		session := state.Sessions[created.SessionID]
		session.RouteFingerprint = ""
		state.Sessions[session.ID] = session
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		store.Close()
		if changed {
			c.Instances[0].PolicyRevision = "different-policy"
		}
		b := openFixture(t, c, f)
		waitTest(t, func() bool { return b.Snapshot().Sessions[0].Connection == "ready" })
		_, err = b.Apply(Command{ID: "alpha-send", Kind: "send", SessionID: created.SessionID, Text: "original route"})
		if (err != nil) != changed {
			t.Fatalf("changed=%v, err=%v", changed, err)
		}
		b.Close()
	}
}
