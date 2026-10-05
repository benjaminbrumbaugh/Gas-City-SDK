package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	bbolt "go.etcd.io/bbolt"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/subprocess"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
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

func TestRoutingDecisionServiceAdmitsLocalLaneWithoutExternalAuthority(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "account-a")
	contents := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(executable, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(contents)
	binding := config.RoutingExecutionBinding{
		CanonicalModel: "model-a", ServeAs: "model-a", ReasoningEffort: "high", Account: "account-a",
		Provider: "local", AdapterID: "adapter-a", Executable: executable, ExecutableDigest: hex.EncodeToString(hash[:]),
		Args: []string{"--model", "model-a", "--effort", "high"}, ModelArgIndex: 1, EffortArgIndex: 3,
		WorkDir: root, Environment: map[string]string{"HOME": root}, Transport: "subprocess",
	}
	command, err := routingExecutionCommand(binding)
	if err != nil {
		t.Fatal(err)
	}
	maxActive, minActive := 2, 1
	cr := &CityRuntime{
		cityPath: root, cityName: "city-a", stderr: io.Discard, sp: subprocess.NewProvider(),
		cfg: &config.City{
			Rigs:             []config.Rig{{Name: "rig-a", Path: filepath.Join(root, "rig-a")}},
			Agents:           []config.Agent{{Name: "worker", Dir: "rig-a", Provider: "local", Session: "subprocess", MaxActiveSessions: &maxActive, MinActiveSessions: &minActive}},
			Providers:        map[string]config.ProviderSpec{"local": {Command: command}},
			RoutingExecution: &config.RoutingExecutionConfig{Enabled: true, Bindings: map[string]config.RoutingExecutionBinding{"rig-a/worker": binding}},
		},
	}
	initializeRoutingDecisionService(cr)
	t.Cleanup(func() { cr.routingDecisionService.Close() })
	status := cr.routingDecisionService.Status()
	if status.Status != routingdecision.AvailabilityReady || status.Reason != routingdecision.ReasonLocalReady || status.AuthorityReady || cr.routingDecisionStore == nil || cr.routingDecisionVerifier != nil {
		t.Fatalf("local-only routing status = %+v store=%p verifier=%p", status, cr.routingDecisionStore, cr.routingDecisionVerifier)
	}
	selection, err := cr.routingDecisionEligibleSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Candidates) != 1 || selection.Candidates[0].Target != "rig-a/worker" {
		t.Fatalf("local candidates = %+v", selection.Candidates)
	}
	encoded, err := json.Marshal(selection.Candidates[0])
	if err != nil || strings.Contains(string(encoded), executable) || strings.Contains(string(encoded), "HOME") {
		t.Fatalf("candidate leaked local invocation details: %s err=%v", encoded, err)
	}
	base := beads.NewMemStoreFrom(0, []beads.Bead{{ID: "work-1", Status: "open"}}, nil)
	cr.standaloneRigStores = map[string]beads.Store{"rig-a": requireRoutingDecisionWorkStore(t, base)}
	selection, err = cr.routingDecisionEligibleSnapshot()
	if err != nil || len(selection.Work) != 1 || len(selection.Candidates) != 1 {
		t.Fatalf("local admission inputs = %+v err=%v", selection, err)
	}
	request := routingdecision.LocalAdmissionRequest{
		RecommendationID: "routing/v3:" + strings.Repeat("a", 64), Work: selection.Work[0], Candidate: selection.Candidates[0],
	}
	stale := request
	stale.Candidate.ConfigDigest = strings.Repeat("f", 64)
	if _, err := cr.admitLocalRoutingDecision(context.Background(), stale, "local-stale"); err == nil {
		t.Fatal("stale local execution candidate was admitted")
	}
	result, err := cr.admitLocalRoutingDecision(context.Background(), request, "local-admit-1")
	if err != nil || result.Receipt.State != routingdecision.StateAdmitted || !result.Record.Local {
		t.Fatalf("local admission = %+v err=%v", result, err)
	}
	work, err := cr.standaloneRigStores["rig-a"].Get("work-1")
	if err != nil || work.Metadata[beadmeta.RoutingDecisionIDMetadataKey] != result.Record.Payload.DecisionID {
		t.Fatalf("local admission marker = %+v err=%v", work, err)
	}
	final := runtime.Config{Command: command, WorkDir: root}
	if err := cr.checkRoutingExecutionLaunch("rig-a/worker", "rig-a", sessionpkg.Info{TriggerBeadID: "work-1"}, final); err != nil {
		t.Fatalf("local launch refused without external verifier: %v", err)
	}
	final.Command = "changed invocation"
	if err := cr.checkRoutingExecutionLaunch("rig-a/worker", "rig-a", sessionpkg.Info{TriggerBeadID: "work-1"}, final); err == nil {
		t.Fatal("local launch accepted a changed invocation")
	}
	if err := cr.standaloneRigStores["rig-a"].SetMetadata("work-1", beadmeta.RoutedToMetadataKey, ""); err != nil {
		t.Fatal(err)
	}
	work, err = cr.standaloneRigStores["rig-a"].Get("work-1")
	if err != nil {
		t.Fatal(err)
	}
	if !cr.newRoutingDecisionRecoveryAuthorizer(cr.routingDecisionNow()).Allows("rig-a", work) {
		t.Fatal("local admitted carrier was not eligible for restart recovery")
	}
	replay, err := cr.admitLocalRoutingDecision(context.Background(), request, "local-admit-1")
	if err != nil || replay.Receipt != result.Receipt {
		t.Fatalf("local admission replay = %+v err=%v", replay, err)
	}
}

func TestRoutingDecisionServiceCloseWaitsForLocalAdmission(t *testing.T) {
	store, err := routingdecision.OpenStore(t.TempDir(), routingdecision.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	service := &cityRoutingDecisionService{
		store:            store,
		status:           routingdecision.AvailabilityReady,
		executionEnabled: true,
		localAdmit: func(context.Context, routingdecision.LocalAdmissionRequest, string) (routingdecision.LocalAdmissionResult, error) {
			close(started)
			<-release
			return routingdecision.LocalAdmissionResult{}, nil
		},
	}
	admitDone := make(chan error, 1)
	go func() {
		_, admitErr := service.AdmitLocal(context.Background(), routingdecision.LocalAdmissionRequest{}, "local-close")
		admitDone <- admitErr
	}()
	<-started
	closeDone := make(chan struct{})
	go func() {
		service.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
		t.Fatal("Close returned while local admission was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-admitDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not complete after local admission returned")
	}
}

func TestRoutingDecisionServiceDistinguishesStoredValidatorRejection(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-validator-status")
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	cityRoot := fixture.cr.cityPath
	if err := fixture.ledger.Close(); err != nil {
		t.Fatal(err)
	}
	writeRoutingAuthority(t, cityRoot, publicKey)
	mutateStoredRoutingDecisionWorkRevision(t, cityRoot, fixture.payload.DecisionID, -1)

	cr := &CityRuntime{cityPath: cityRoot, cityName: "test-city", stderr: io.Discard, cfg: &config.City{}}
	initializeRoutingDecisionService(cr)
	status := cr.routingDecisionService.Status()
	if status.Status != routingdecision.AvailabilityDenied || status.Reason != routingdecision.ReasonLedgerValidatorRejected {
		t.Fatalf("stored validator rejection status = %+v", status)
	}
}

func TestRoutingDecisionAdmissionLogClassifiesStoredValidatorRejection(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "decision-validator-log")
	cityRoot := fixture.cr.cityPath
	if err := fixture.ledger.Close(); err != nil {
		t.Fatal(err)
	}
	mutateStoredRoutingDecisionWorkRevision(t, cityRoot, fixture.payload.DecisionID, -1)
	reopened, err := routingdecision.OpenStore(cityRoot, routingdecision.StoreOptions{Now: fixture.cr.routingDecisionNowFn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	fixture.cr.routingDecisionStore = reopened
	var stderr bytes.Buffer
	fixture.cr.stderr = &stderr

	fixture.cr.applyApprovedRoutingDecisionsAndLog()
	if !strings.Contains(stderr.String(), "routing decision admission refused: validator rejected stored payload") {
		t.Fatalf("validator rejection log = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "work revision") || strings.Contains(stderr.String(), fixture.payload.DecisionID) {
		t.Fatalf("validator rejection log leaked payload details: %q", stderr.String())
	}
}

func mutateStoredRoutingDecisionWorkRevision(t *testing.T, cityRoot, decisionID string, workRevision int64) {
	t.Helper()
	path := filepath.Join(cityRoot, routingdecision.StoreRelativePath)
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("decisions"))
		value := bucket.Get([]byte(decisionID))
		if value == nil {
			return errors.New("decision record missing")
		}
		var record map[string]any
		if err := json.Unmarshal(value, &record); err != nil {
			return err
		}
		payload, ok := record["payload"].(map[string]any)
		if !ok {
			return errors.New("decision payload missing")
		}
		payload["work_revision"] = workRevision
		updated, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(decisionID), updated)
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingDecisionSnapshotsAreDeterministicAndSelectionSafe(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	maxActive, minActive := 2, 1
	cfg := &config.City{
		Workspace: config.Workspace{Provider: "safe"},
		Providers: map[string]config.ProviderSpec{"safe": {Command: "provider-binary"}},
		Agents: []config.Agent{
			{Name: "zeta", Dir: "z-rig", Description: "Z", MaxActiveSessions: &maxActive, MinActiveSessions: &minActive, Env: map[string]string{"TOKEN": "secret-z"}},
			{Name: "alpha", Dir: "a-rig", Description: "A", MaxActiveSessions: &maxActive, MinActiveSessions: &minActive, Env: map[string]string{"TOKEN": "secret-a"}},
			{Name: "disabled", Dir: "a-rig", Suspended: true, MaxActiveSessions: &maxActive, MinActiveSessions: &minActive},
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
