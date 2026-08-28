package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

func TestRoutingDecisionServiceProjectsClaimedAndTerminalWithoutLifecycleMutation(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixtureForRecommendation(t, "decision-outcome-service", "routing/v2:"+strings.Repeat("c", 64), routingdecision.StoreOptions{})
	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || applied != 1 {
		t.Fatalf("admit = (%d, %v)", applied, err)
	}
	status, assignee := "in_progress", "session-service"
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{
		Status: &status, Assignee: &assignee,
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.cr.reconcileRoutingDecisionLifecycle(); err != nil {
		t.Fatal(err)
	}
	before, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || before.State != routingdecision.StateClaimed {
		t.Fatalf("before = (%+v, %v)", before, err)
	}

	observed := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	service := &cityRoutingDecisionService{
		store: fixture.ledger, status: routingdecision.AvailabilityReady,
		now: func() time.Time { return observed },
		outcomeWork: func(_ context.Context, payload routingdecision.DecisionPayload) (routingdecision.OutcomeWorkSnapshot, error) {
			work, err := beads.HandlesFor(fixture.cr.standaloneRigStores[payload.Rig]).Live.Get(payload.WorkBeadID)
			if err != nil {
				return routingdecision.OutcomeWorkSnapshot{}, err
			}
			metadata := make(map[string]string, len(work.Metadata))
			for key, value := range work.Metadata {
				metadata[key] = value
			}
			return routingdecision.OutcomeWorkSnapshot{Found: true, WorkID: work.ID, Status: work.Status, Assignee: work.Assignee, ClaimFence: work.ClaimFence, Metadata: metadata}, nil
		},
		// The claimed decision carries no authoritative execution facts in this
		// fixture, so identity stays unavailable (fail closed) — exactly what
		// the causal boundary requires.
		outcomeAuthority: func(_ context.Context, _ routingdecision.DecisionPayload) routingdecision.OutcomeAuthoritySnapshot {
			return routingdecision.OutcomeAuthoritySnapshot{}
		},
	}
	page, err := service.Outcomes(context.Background(), routingdecision.OutcomeListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("Outcomes = (%+v, %v)", page, err)
	}
	wantObserved := time.Date(2026, 8, 7, 23, 30, 0, 0, time.UTC).Unix()
	if page.Items[0].Status != routingdecision.OutcomeStatusClaimed || page.Items[0].SessionID != nil || page.Items[0].ExecutionID != nil || page.Items[0].ObservedAtUnix != wantObserved {
		t.Fatalf("item = %+v", page.Items[0])
	}
	after, err := fixture.ledger.Get(fixture.payload.DecisionID)
	if err != nil || after.State != before.State || after.RecordRevision != before.RecordRevision {
		t.Fatalf("read projection mutated lifecycle: before=%+v after=%+v err=%v", before, after, err)
	}
}
