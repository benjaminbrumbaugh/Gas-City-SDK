package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

func writeRoutingAuthority(t *testing.T, cityRoot string, publicKey ed25519.PublicKey) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cityRoot, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := struct {
		Schema      int `json:"schema"`
		Authorities []struct {
			AuthorityID string `json:"authority_id"`
			PublicKey   string `json:"public_key"`
		} `json:"authorities"`
	}{Schema: routingdecision.SchemaVersion}
	document.Authorities = append(document.Authorities, struct {
		AuthorityID string `json:"authority_id"`
		PublicKey   string `json:"public_key"`
	}{AuthorityID: "board", PublicKey: base64.StdEncoding.EncodeToString(publicKey)})
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityRoot, routingdecision.AuthorityRelativePath), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingDecisionServiceIsSharedWithAPIAndClosedBeforePreserveReturn(t *testing.T) {
	cityRoot := t.TempDir()
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	writeRoutingAuthority(t, cityRoot, publicKey)
	cr := &CityRuntime{
		cityPath: cityRoot, cityName: "test-city", cfg: &config.City{},
		stdout: io.Discard, stderr: io.Discard,
	}
	initializeRoutingDecisionService(cr)
	cs := &controllerState{}
	cr.setControllerState(cs)
	var provider gcapi.RoutingDecisionProvider = cs
	if got := provider.RoutingDecisionStatus(); got.Status != routingdecision.AvailabilityReady {
		t.Fatalf("API capability status = %+v", got)
	}
	cr.preserveSessionsShutdown.Store(true)
	cr.shutdown()
	if cr.routingDecisionStore != nil || cr.routingDecisionVerifier != nil {
		t.Fatal("shutdown retained routing mutation handles")
	}
	if got := provider.RoutingDecisionStatus(); got.Status != routingdecision.AvailabilityDenied || got.Reason != routingdecision.ReasonServiceClosed {
		t.Fatalf("preserve shutdown status = %+v", got)
	}
}

func TestRoutingDecisionServiceBootLatchesAuthorityBeforeOpeningLedger(t *testing.T) {
	missingRoot := t.TempDir()
	missing := &CityRuntime{cityPath: missingRoot, cityName: "test-city", stderr: io.Discard, cfg: &config.City{}}
	initializeRoutingDecisionService(missing)
	status := missing.routingDecisionService.Status()
	if status.Status != routingdecision.AvailabilityDenied || status.Reason != routingdecision.ReasonAuthorityUnavailable || status.AuthorityReady {
		t.Fatalf("missing authority status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(missingRoot, routingdecision.StoreRelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default-deny boot created ledger: %v", err)
	}

	readyRoot := t.TempDir()
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	writeRoutingAuthority(t, readyRoot, publicKey)
	ready := &CityRuntime{cityPath: readyRoot, cityName: "test-city", stderr: io.Discard, cfg: &config.City{}}
	initializeRoutingDecisionService(ready)
	t.Cleanup(func() { ready.routingDecisionService.Close() })
	status = ready.routingDecisionService.Status()
	if status.Status != routingdecision.AvailabilityReady || status.Reason != routingdecision.ReasonReady || !status.AuthorityReady || ready.routingDecisionStore == nil || ready.routingDecisionVerifier == nil {
		t.Fatalf("ready service = status=%+v store=%p verifier=%p", status, ready.routingDecisionStore, ready.routingDecisionVerifier)
	}
}

func TestRoutingDecisionSnapshotsAreDeterministicAndSelectionSafe(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	max, min := 2, 1
	cfg := &config.City{
		Workspace: config.Workspace{Provider: "safe"},
		Providers: map[string]config.ProviderSpec{"safe": {Command: "provider-binary"}},
		Agents: []config.Agent{
			{Name: "zeta", Dir: "z-rig", Description: "Z", MaxActiveSessions: &max, MinActiveSessions: &min, Env: map[string]string{"TOKEN": "secret-z"}},
			{Name: "alpha", Dir: "a-rig", Description: "A", MaxActiveSessions: &max, MinActiveSessions: &min, Env: map[string]string{"TOKEN": "secret-a"}},
			{Name: "disabled", Dir: "a-rig", Suspended: true, MaxActiveSessions: &max, MinActiveSessions: &min},
		},
	}
	cityStore := beads.NewMemStoreFrom(0, []beads.Bead{{ID: "CITY-READY", Status: "open", Revision: -7}, {ID: "CITY-ROUTED", Status: "open", Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "a-rig/alpha"}}}, nil)
	rigStore := beads.NewMemStoreFrom(0, []beads.Bead{{ID: "RIG-READY", Status: "open", Revision: -1 << 63}, {ID: "RIG-ASSIGNED", Status: "in_progress", Assignee: "worker-1"}}, nil)
	cr := &CityRuntime{
		cityName: "test-city", cfg: cfg, standaloneCityStore: cityStore,
		standaloneRigStores: map[string]beads.Store{"a-rig": rigStore}, routingDecisionNowFn: func() time.Time { return now },
	}

	targets, err := cr.routingDecisionTargetSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Target != "a-rig/alpha" || targets[1].Target != "z-rig/zeta" || targets[0].ResolvedProvider != "safe" {
		t.Fatalf("targets = %+v", targets)
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "secret-a") || strings.Contains(string(encoded), "provider-binary") || strings.Contains(string(encoded), "TOKEN") {
		t.Fatalf("target snapshot leaked config: %s", encoded)
	}

	snapshot, err := cr.routingDecisionEligibleSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	wantWork := []routingdecision.EligibleWorkSnapshot{
		{Rig: "", Scope: "city", WorkBeadID: "CITY-READY", WorkRevision: -7, ClaimFence: 0, WorkStateDigest: routingdecision.WorkStateDigest(routingdecision.WorkStateFrom("CITY-READY", "open", "", map[string]string(nil), 0))},
		{Rig: "a-rig", Scope: "rig", WorkBeadID: "RIG-READY", WorkRevision: -1 << 63, ClaimFence: 0, WorkStateDigest: routingdecision.WorkStateDigest(routingdecision.WorkStateFrom("RIG-READY", "open", "", map[string]string(nil), 0))},
	}
	if !snapshot.ObservedAt.Equal(now) || !reflect.DeepEqual(snapshot.Work, wantWork) || !reflect.DeepEqual(snapshot.Targets, targets) {
		t.Fatalf("eligible snapshot = %+v, want work=%+v targets=%+v", snapshot, wantWork, targets)
	}
}

func TestRoutingDecisionLifecycleRequiresExactClaimAndOutcomeFacts(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-lifecycle")
	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || applied != 1 {
		t.Fatalf("admit = (%d, %v)", applied, err)
	}
	if _, err := fixture.cr.reconcileRoutingDecisionLifecycle(); err != nil {
		t.Fatal(err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateAdmitted {
		t.Fatalf("ambiguous admitted carrier transitioned: (%+v, %v)", record, err)
	}

	status, assignee := "in_progress", "worker-session"
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{
		Status: &status, Assignee: &assignee,
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.cr.reconcileRoutingDecisionLifecycle(); err != nil {
		t.Fatal(err)
	}
	record, err = fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateClaimed {
		t.Fatalf("exact claim = (%+v, %v)", record, err)
	}
	if err := fixture.base.Close(fixture.payload.WorkBeadID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.cr.reconcileRoutingDecisionLifecycle(); err != nil {
		t.Fatal(err)
	}
	record, err = fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateOutcomeRecorded {
		t.Fatalf("exact outcome = (%+v, %v)", record, err)
	}
}
