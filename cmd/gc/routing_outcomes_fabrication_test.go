package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// TestRoutingDecisionServiceRejectsFabricatedOutcomeMetadata drives the full
// service Outcomes path against a closed, exactly-bound work bead whose
// metadata fabricates a complete success story (typed outcome, session id,
// current run id). The service must fail closed: no succeeded/failed-known
// disposition, no identity, because the authority resolver finds no real
// execution.work_associated / execution.step_completed journal facts and no
// terminal-disposition record exists.
func TestRoutingDecisionServiceRejectsFabricatedOutcomeMetadata(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixtureForRecommendation(t, "decision-outcome-fabricated", "routing/v2:"+strings.Repeat("c", 64), routingdecision.StoreOptions{})
	if applied, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || applied != 1 {
		t.Fatalf("admit = (%d, %v)", applied, err)
	}
	status, assignee := "in_progress", "session-fabricated"
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{
		Status: &status, Assignee: &assignee,
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.cr.reconcileRoutingDecisionLifecycle(); err != nil {
		t.Fatal(err)
	}
	closed := "closed"
	emptyAssignee := ""
	if err := fixture.base.Update(fixture.payload.WorkBeadID, beads.UpdateOpts{
		Status: &closed, Assignee: &emptyAssignee,
		Metadata: map[string]string{
			beadmeta.WorkOutcomeMetadataKey:               "shipped",
			beadmeta.FailureClassMetadataKey:              "hard",
			beadmeta.SessionIDMetadataKey:                 "session-fabricated",
			beadmeta.CurrentRunIDMetadataKey:              "execution-fabricated",
			beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(fixture.payload.ClaimFence+1, 10),
		},
	}); err != nil {
		t.Fatal(err)
	}

	observed := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	service := &cityRoutingDecisionService{
		store: fixture.ledger, status: routingdecision.AvailabilityReady,
		now:              func() time.Time { return observed },
		outcomeWork:      fixture.cr.routingDecisionOutcomeWork,
		outcomeAuthority: fixture.cr.routingDecisionOutcomeAuthority,
	}
	page, err := service.Outcomes(context.Background(), routingdecision.OutcomeListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("Outcomes = (%+v, %v)", page, err)
	}
	item := page.Items[0]
	if item.Status == routingdecision.OutcomeStatusSucceeded || item.Disposition != routingdecision.OutcomeDispositionUnknown ||
		item.FailureClass != routingdecision.OutcomeFailureUnknown {
		t.Fatalf("fabricated metadata invented truth through the service: %+v", item)
	}
	if item.SessionID != nil || item.ExecutionID != nil {
		t.Fatalf("fabricated identity leaked through the service: %+v", item)
	}
}
