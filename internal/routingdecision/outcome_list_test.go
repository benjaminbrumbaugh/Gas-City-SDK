package routingdecision

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestListOutcomeDecisionsIncludesClaimedAndTerminalWithStableCursor(t *testing.T) {
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	states := map[string]State{
		"decision-a-approved": StateApproved,
		"decision-b-claimed":  StateClaimed,
		"decision-c-outcome":  StateOutcomeRecorded,
		"decision-d-refused":  StateRefusedAfterRace,
		"decision-e-legacy":   StateOutcomeRecorded,
	}
	for id, want := range states {
		payload, approval, signature, verifier := currentTestDecision(t, id, now)
		if id != "decision-a-approved" && id != "decision-e-legacy" {
			payload.RecommendationID = "recommendation-" + id
			payload.BindingID = BindingID(payload)
			approval.BindingID = payload.BindingID
			signing, err := SigningBytes(payload, approval)
			if err != nil {
				t.Fatal(err)
			}
			_, privateKey, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			publicKey := privateKey.Public().(ed25519.PublicKey)
			verifier = NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})
			signature.Value = ed25519.Sign(privateKey, signing)
		}
		result, err := store.IngestApproved(IngestApprovedRequest{
			Payload: payload, Approval: approval, Signature: signature, IdempotencyToken: "ingest-" + id,
		}, verifier)
		if err != nil {
			t.Fatal(err)
		}
		revision := result.Record.RecordRevision
		state := StateApproved
		transition := func(to State) {
			t.Helper()
			receipt, err := store.Transition(TransitionRequest{
				DecisionID: id, ExpectedRevision: revision, From: state, To: to,
				IdempotencyToken: "to-" + string(to) + "-" + id, Reason: "test transition",
			}, verifier)
			if err != nil {
				t.Fatal(err)
			}
			revision, state = receipt.RecordRevision, to
		}
		switch want {
		case StateClaimed:
			transition(StateAdmitted)
			transition(StateClaimed)
		case StateOutcomeRecorded:
			transition(StateAdmitted)
			transition(StateClaimed)
			transition(StateOutcomeRecorded)
		case StateRefusedAfterRace:
			transition(StateRefusedAfterRace)
		}
	}

	first, err := store.ListOutcomeDecisions(OutcomeListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Record.Payload.DecisionID != "decision-b-claimed" || first.NextCursor == "" {
		t.Fatalf("first = %+v", first)
	}
	second, err := store.ListOutcomeDecisions(OutcomeListOptions{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 || second.Items[0].Record.Payload.DecisionID != "decision-c-outcome" || second.Items[1].Record.Payload.DecisionID != "decision-d-refused" || second.NextCursor == "" {
		t.Fatalf("second = %+v", second)
	}
	third, err := store.ListOutcomeDecisions(OutcomeListOptions{Limit: 2, Cursor: second.NextCursor})
	if err != nil || len(third.Items) != 0 || third.NextCursor != "" {
		t.Fatalf("terminal legacy decision was not omitted: page=%+v err=%v", third, err)
	}
	for _, invalid := range []int{0, 101} {
		if _, err := store.ListOutcomeDecisions(OutcomeListOptions{Limit: invalid}); err == nil {
			t.Fatalf("limit %d accepted", invalid)
		}
	}
}
