package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/rollout/gate"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

func requireRoutingDecisionWorkStore(t *testing.T, base beads.Store) beads.Store {
	t.Helper()
	result, err := beads.OpenStoreAtForCity(context.Background(), beads.StoreOpenOptions{
		ScopeRoot: t.TempDir(), Provider: "file", ConditionalWrites: gate.Require,
		OpenFileStore: func() (beads.Store, error) { return base, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Store
}

func approveRoutingDecision(t *testing.T, store *routingdecision.Store, payload routingdecision.DecisionPayload, now time.Time) *routingdecision.Verifier {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := routingdecision.NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})
	record, err := store.Create(payload, "create-"+payload.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	approval := routingdecision.ApprovalPayload{Schema: routingdecision.SchemaVersion, DecisionID: payload.DecisionID, BindingID: payload.BindingID, AuthorityID: "board", ApprovedAt: now}
	signing, err := routingdecision.SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	signature := routingdecision.Signature{Algorithm: routingdecision.SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(privateKey, signing)}
	if _, err := store.Transition(routingdecision.TransitionRequest{DecisionID: payload.DecisionID, ExpectedRevision: record.RecordRevision, From: routingdecision.StateProposed, To: routingdecision.StateApproved, Approval: &approval, Signature: &signature, IdempotencyToken: "approve-" + payload.DecisionID, Reason: "approved"}, verifier); err != nil {
		t.Fatal(err)
	}
	return &verifier
}

type routingDecisionFixture struct {
	cr      *CityRuntime
	base    *beads.MemStore
	ledger  *routingdecision.Store
	payload routingdecision.DecisionPayload
}

func newApprovedRoutingDecisionFixture(t *testing.T, decisionID string) routingDecisionFixture {
	return newApprovedRoutingDecisionFixtureWithOptions(t, decisionID, routingdecision.StoreOptions{})
}

func newApprovedRoutingDecisionFixtureWithOptions(t *testing.T, decisionID string, options routingdecision.StoreOptions) routingDecisionFixture {
	return newApprovedRoutingDecisionFixtureForRecommendation(t, decisionID, "", options)
}

func newApprovedRoutingDecisionFixtureForRecommendation(t *testing.T, decisionID, recommendationID string, options routingdecision.StoreOptions) routingDecisionFixture {
	t.Helper()
	const (
		city   = "test-city"
		rig    = "demo"
		target = "demo/reviewer"
		workID = "GC-READY"
	)
	now := time.Date(2026, 8, 7, 23, 30, 0, 0, time.UTC)
	maxActive, minActive := 2, 1
	cfg := &config.City{
		Rigs:   []config.Rig{{Name: rig, Path: rig}},
		Agents: []config.Agent{{Name: "reviewer", Dir: rig, StartCommand: "review", MaxActiveSessions: &maxActive, MinActiveSessions: &minActive}},
	}
	base := beads.NewMemStoreFrom(0, []beads.Bead{{ID: workID, Status: "open"}}, nil)
	seed, err := base.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	workStore := requireRoutingDecisionWorkStore(t, base)
	cityRoot := t.TempDir()
	options.Now = func() time.Time { return now }
	decisionStore, err := routingdecision.OpenStore(cityRoot, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = decisionStore.Close() })
	cr := &CityRuntime{
		cityPath: cityRoot, cityName: city, cfg: cfg, stderr: io.Discard, logPrefix: "test",
		standaloneRigStores: map[string]beads.Store{rig: workStore}, routingDecisionStore: decisionStore,
		routingDecisionNowFn: func() time.Time { return now },
	}
	_, targetDigest, ok := cr.resolveRoutingDecisionTarget(target, rig)
	if !ok {
		t.Fatal("target did not resolve")
	}
	payload := routingdecision.DecisionPayload{
		Schema: routingdecision.SchemaVersion, DecisionID: decisionID, RecommendationID: recommendationID, WorkBeadID: workID,
		WorkRevision: seed.Revision, ClaimFence: seed.ClaimFence,
		WorkStateDigest: routingdecision.WorkStateDigest(routingdecision.WorkStateFrom(seed.ID, seed.Status, seed.Assignee, seed.Metadata, seed.ClaimFence)),
		City:            city, Rig: rig, Target: target, TargetConfigDigest: targetDigest,
		PolicyDigest: strings.Repeat("a", sha256.Size*2), ObservationDigest: strings.Repeat("b", sha256.Size*2),
		Model: "model-a", Source: "governor-a", Account: "account-a", CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), NoMigration: true,
	}
	payload.BindingID = routingdecision.BindingID(payload)
	cr.routingDecisionVerifier = approveRoutingDecision(t, decisionStore, payload, now)
	return routingDecisionFixture{cr: cr, base: base, ledger: decisionStore, payload: payload}
}

func TestCityRuntimeCompensatesExactStampAfterLedgerCommitFault(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixtureWithOptions(t, "decision-commit-fault", routingdecision.StoreOptions{
		BeforeAdmissionCommit: func() error { return errors.New("page detail secret") },
	})
	applied, err := fixture.cr.applyApprovedRoutingDecisions()
	if err == nil || applied != 0 || strings.Contains(err.Error(), "page detail") {
		t.Fatalf("commit-fault admission = (%d, %v), want sanitized failure", applied, err)
	}
	work, getErr := fixture.base.Get(fixture.payload.WorkBeadID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	for _, key := range []string{
		beadmeta.RoutedToMetadataKey, beadmeta.RunTargetMetadataKey,
		beadmeta.RoutingDecisionIDMetadataKey, beadmeta.RoutingDecisionClaimFenceMetadataKey,
	} {
		if work.Metadata[key] != "" {
			t.Errorf("compensation left %s=%q", key, work.Metadata[key])
		}
	}
	record, getErr := fixture.ledger.Get(fixture.payload.DecisionID)
	if getErr != nil || record.State != routingdecision.StateApproved {
		t.Fatalf("decision lifecycle = (%+v, %v), want approved for retry", record, getErr)
	}
}

func TestCityRuntimeRepairsUncompensatedApprovedStamp(t *testing.T) {
	var base *beads.MemStore
	faulted := false
	fixture := newApprovedRoutingDecisionFixtureWithOptions(t, "decision-uncompensated", routingdecision.StoreOptions{
		BeforeAdmissionCommit: func() error {
			if faulted {
				return nil
			}
			faulted = true
			title := "concurrent non-ownership edit"
			if err := base.Update("GC-READY", beads.UpdateOpts{Title: &title}); err != nil {
				return err
			}
			return errors.New("commit outcome unavailable")
		},
	})
	base = fixture.base
	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err == nil || applied != 0 {
		t.Fatalf("first admission = (%d, %v), want compensated-race failure", applied, err)
	}
	marked, err := fixture.base.Get(fixture.payload.WorkBeadID)
	if err != nil || marked.Metadata[beadmeta.RoutingDecisionIDMetadataKey] != fixture.payload.DecisionID {
		t.Fatalf("approved crash-window marker = (%+v, %v)", marked.Metadata, err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateApproved {
		t.Fatalf("decision after fault = (%+v, %v), want approved", record, err)
	}

	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || applied != 1 {
		t.Fatalf("repair admission = (%d, %v), want (1, nil)", applied, err)
	}
	record, err = fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateAdmitted {
		t.Fatalf("repaired lifecycle = (%+v, %v)", record, err)
	}
}

func TestRoutingDecisionServiceIngestReconcilesApprovedImmediately(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-ingest-template")
	ledger, err := routingdecision.OpenStore(t.TempDir(), routingdecision.StoreOptions{Now: fixture.cr.routingDecisionNow})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	payload := fixture.payload
	payload.DecisionID = "decision-ingest-reconcile"
	payload.BindingID = routingdecision.BindingID(payload)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := routingdecision.NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})
	approval := routingdecision.ApprovalPayload{
		Schema: routingdecision.SchemaVersion, DecisionID: payload.DecisionID, BindingID: payload.BindingID,
		AuthorityID: "board", ApprovedAt: fixture.cr.routingDecisionNow(),
	}
	signing, err := routingdecision.SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	signature := routingdecision.Signature{
		Algorithm: routingdecision.SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(privateKey, signing),
	}
	fixture.cr.routingDecisionStore = ledger
	fixture.cr.routingDecisionVerifier = &verifier
	service := &cityRoutingDecisionService{
		store: ledger, verifier: &verifier, status: routingdecision.AvailabilityReady,
		reconcile: fixture.cr.reconcileRoutingDecisionsAndLog,
	}
	fixture.cr.routingDecisionService = service
	result, err := service.Ingest(context.Background(), routingdecision.IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature, IdempotencyToken: "ingest-" + payload.DecisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.State != routingdecision.StateAdmitted || result.Receipt.State != routingdecision.StateAdmitted || result.Record.RecordRevision != result.Receipt.RecordRevision {
		t.Fatalf("ingest response = %+v, want current admitted record and receipt", result)
	}
	record, err := ledger.Get(payload.DecisionID)
	if err != nil || record.State != routingdecision.StateAdmitted {
		t.Fatalf("post-ingest lifecycle = (%+v, %v), want admitted", record, err)
	}

	payload2 := payload
	payload2.DecisionID = "decision-ingest-close-race"
	payload2.BindingID = routingdecision.BindingID(payload2)
	approval2 := approval
	approval2.DecisionID = payload2.DecisionID
	approval2.BindingID = payload2.BindingID
	signing2, err := routingdecision.SigningBytes(payload2, approval2)
	if err != nil {
		t.Fatal(err)
	}
	signature2 := signature
	signature2.Value = ed25519.Sign(privateKey, signing2)
	entered := make(chan struct{})
	release := make(chan struct{})
	raceService := &cityRoutingDecisionService{
		store: ledger, verifier: &verifier, status: routingdecision.AvailabilityReady,
		reconcile: func() { close(entered); <-release },
	}
	ingestDone := make(chan error, 1)
	go func() {
		_, ingestErr := raceService.Ingest(context.Background(), routingdecision.IngestApprovedRequest{
			Payload: payload2, Approval: approval2, Signature: signature2, IdempotencyToken: "ingest-" + payload2.DecisionID,
		})
		ingestDone <- ingestErr
	}()
	<-entered
	closeDone := make(chan struct{})
	go func() { raceService.Close(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Fatal("Close returned while accepted ingest reconciliation was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-ingestDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not complete after ingest reconciliation returned")
	}
}

func TestCityRuntimeAppliesApprovedRoutingDecisionMetadataOnly(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-ready")

	applied, err := fixture.cr.applyApprovedRoutingDecisions()
	if err != nil || applied != 1 {
		t.Fatalf("applyApprovedRoutingDecisions = (%d, %v), want (1, nil)", applied, err)
	}
	got, err := fixture.base.Get(fixture.payload.WorkBeadID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata[beadmeta.RoutedToMetadataKey] != fixture.payload.Target || got.Metadata[beadmeta.RunTargetMetadataKey] != fixture.payload.Target || got.Metadata[beadmeta.RoutingDecisionIDMetadataKey] != fixture.payload.DecisionID {
		t.Fatalf("route metadata = %+v", got.Metadata)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateAdmitted {
		t.Fatalf("decision lifecycle = (%+v, %v)", record, err)
	}

	if err := fixture.base.SetMetadata(fixture.payload.WorkBeadID, beadmeta.RoutedToMetadataKey, ""); err != nil {
		t.Fatal(err)
	}
	fixture.cr.recoverUnroutedWorkRoutes()
	recovered, err := fixture.base.Get(fixture.payload.WorkBeadID)
	if err != nil || recovered.Metadata[beadmeta.RoutedToMetadataKey] != fixture.payload.Target {
		t.Fatalf("admitted route recovery = (%+v, %v)", recovered.Metadata, err)
	}
}

func TestCityRuntimeReconcilesApprovedExactlyStampedCrashWindow(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-crash-window")
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey: fixture.payload.Target, beadmeta.RunTargetMetadataKey: fixture.payload.Target,
		beadmeta.RoutingDecisionIDMetadataKey:         fixture.payload.DecisionID,
		beadmeta.RoutingDecisionClaimFenceMetadataKey: "0",
	}}); err != nil {
		t.Fatal(err)
	}
	applied, err := fixture.cr.applyApprovedRoutingDecisions()
	if err != nil || applied != 1 {
		t.Fatalf("crash-window reconciliation = (%d, %v), want admitted", applied, err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateAdmitted {
		t.Fatalf("decision lifecycle = (%+v, %v)", record, err)
	}
}

func TestCityRuntimeRefusesApprovedStampedWorkThatBecameUnready(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-crash-window-blocked")
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey: fixture.payload.Target, beadmeta.RunTargetMetadataKey: fixture.payload.Target,
		beadmeta.RoutingDecisionIDMetadataKey:         fixture.payload.DecisionID,
		beadmeta.RoutingDecisionClaimFenceMetadataKey: "0",
	}}); err != nil {
		t.Fatal(err)
	}
	blocker, err := fixture.base.Create(beads.Bead{Title: "blocking dependency"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.base.DepAdd(fixture.payload.WorkBeadID, blocker.ID, "blocks"); err != nil {
		t.Fatal(err)
	}
	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || applied != 0 {
		t.Fatalf("blocked crash-window reconciliation = (%d, %v), want refusal", applied, err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateRefusedAfterRace {
		t.Fatalf("decision lifecycle = (%+v, %v), want refused_after_race", record, err)
	}
}

func TestCityRuntimeRecordsRefusalAfterWorkRevisionDrift(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-race")
	title := "changed after approval"
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{Title: &title}); err != nil {
		t.Fatal(err)
	}
	applied, err := fixture.cr.applyApprovedRoutingDecisions()
	if err != nil || applied != 0 {
		t.Fatalf("revision-drift admission = (%d, %v), want (0, nil)", applied, err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateRefusedAfterRace {
		t.Fatalf("decision lifecycle = (%+v, %v), want refused_after_race", record, err)
	}
}

func TestCityRuntimeExpiresDueApprovedDecisionBeforeQuery(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-expired")
	fixture.cr.routingDecisionNowFn = func() time.Time { return fixture.payload.ExpiresAt.Add(time.Nanosecond) }
	applied, err := fixture.cr.applyApprovedRoutingDecisions()
	if err != nil || applied != 0 {
		t.Fatalf("expired admission = (%d, %v), want (0, nil)", applied, err)
	}
	record, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || record.State != routingdecision.StateExpired {
		t.Fatalf("decision lifecycle = (%+v, %v), want expired", record, err)
	}
}

func TestCityRuntimeRoutingDecisionDefaultDenyLogsNoRawError(t *testing.T) {
	var stderr bytes.Buffer
	cr := &CityRuntime{cityPath: t.TempDir(), cityName: "test-city", stderr: &stderr, logPrefix: "test"}
	cr.applyApprovedRoutingDecisionsAndLog()
	if strings.Contains(stderr.String(), "database") || strings.Contains(stderr.String(), "provider") || strings.Contains(stderr.String(), "endpoint") {
		t.Fatalf("sanitized log leaked raw detail: %q", stderr.String())
	}
}

func TestRoutingDecisionControllerSurfaceHasNoLaunchDependency(t *testing.T) {
	for _, path := range []string{"routing_decision_controller.go", "routing_decision_recovery.go", "routing_decision_target.go"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"internal/runtime", "internal/session", "internal/sling", "internal/worker"} {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("%s imports forbidden launch surface %q", path, forbidden)
			}
		}
	}
}
