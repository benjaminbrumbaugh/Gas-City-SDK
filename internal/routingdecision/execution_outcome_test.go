package routingdecision

import (
	"testing"
	"time"
)

func TestOutcomeObservationNeverUsesReadClockAsEvidence(t *testing.T) {
	p := testDecisionPayload(t)
	item := DecisionWithAudits{Record: Record{Payload: p, State: StateRevoked}}
	first := ProjectOutcome(item, OutcomeWorkSnapshot{}, OutcomeAuthoritySnapshot{}, time.Unix(100, 0))
	later := ProjectOutcome(item, OutcomeWorkSnapshot{}, OutcomeAuthoritySnapshot{}, time.Unix(200, 0))
	if first.ObservedAtUnix != p.CreatedAt.Unix() || first.OutcomeID != later.OutcomeID {
		t.Fatalf("read fabricated evidence time: first=%+v later=%+v", first, later)
	}
}

func TestExecutionOutcomeProjectsDurableFactsWithoutSuccessInference(t *testing.T) {
	p := testExecutionPayload(t)
	store, err := OpenStore(t.TempDir(), StoreOptions{Now: func() time.Time { return p.CreatedAt }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	created, err := store.Create(p, "create-v3-observation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(TransitionRequest{DecisionID: p.DecisionID, ExpectedRevision: created.RecordRevision, From: StateProposed, To: StateRevoked, IdempotencyToken: "revoke-v3-observation", Reason: "test"}, Verifier{}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListExecutionOutcomeDecisions(OutcomeListOptions{Limit: 100})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("durable v3 outcome candidates: %+v %v", page, err)
	}
	observation, err := ProjectExecutionOutcome(page.Items[0], OutcomeAuthoritySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Disposition != OutcomeDispositionNotAdmitted || observation.Actual != nil || observation.ObservedAt != p.CreatedAt || observation.Requested != *p.Execution {
		t.Fatalf("invented execution fact: %+v", observation)
	}
	page.Items[0].Record.State = StateOutcomeRecorded
	observation, err = ProjectExecutionOutcome(page.Items[0], OutcomeAuthoritySnapshot{})
	if err != nil || observation.Disposition != OutcomeDispositionUnknown || observation.Actual != nil {
		t.Fatalf("closure inferred success: %+v %v", observation, err)
	}
}

func TestV2OutcomePageDoesNotMislabelV3ExecutionDecisions(t *testing.T) {
	store, err := OpenStore(t.TempDir(), StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	p := testExecutionPayload(t)
	created, err := store.Create(p, "v3-create")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Transition(TransitionRequest{DecisionID: p.DecisionID, ExpectedRevision: created.RecordRevision, From: StateProposed, To: StateRevoked, IdempotencyToken: "v3-revoke", Reason: "test"}, Verifier{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListOutcomeDecisions(OutcomeListOptions{Limit: 100})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("v3 record leaked into typed v2 outcome page: %+v %v", page, err)
	}
}
