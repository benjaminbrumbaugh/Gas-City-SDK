package routingdecision

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestExecutionSessionAuthorizationSurvivesReopenAndCannotMigrate(t *testing.T) {
	p := testExecutionPayload(t)
	root := t.TempDir()
	now := p.CreatedAt
	store, err := OpenStore(root, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	approval := ApprovalPayload{Schema: p.Schema, DecisionID: p.DecisionID, BindingID: p.BindingID, AuthorityID: "board", ApprovedAt: now}
	data, err := SigningBytes(p, approval)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{"board": key.Public().(ed25519.PublicKey)})
	sig := Signature{Algorithm: SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(key, data)}
	result, err := store.IngestApproved(IngestApprovedRequest{Payload: p, Approval: approval, Signature: sig, Now: now, IdempotencyToken: "approved"}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.FinalAdmission(FinalAdmissionRequest{DecisionID: p.DecisionID, ExpectedRevision: result.Record.RecordRevision, IdempotencyToken: "admitted"}, verifier, func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "exact test admission"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := ExecutionSessionAuthorization{DecisionID: p.DecisionID, BindingID: p.BindingID, SessionID: "s1", Generation: "1", InstanceToken: "original-instance", WorkID: p.WorkBeadID, ClaimFence: p.ClaimFence, Execution: *p.Execution}
	if err = store.BindExecutionSession(auth); err != nil {
		t.Fatal(err)
	}
	original, err := store.RecordExecutionLaunchAttempt(auth, "successful-start-original-attempt")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root, StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close() //nolint:errcheck
	replayed, err := reopened.RecordExecutionLaunchAttempt(auth, original.AttemptID)
	if err != nil || replayed != original {
		t.Fatalf("successful-start replay changed receipt/time: %+v %+v %v", original, replayed, err)
	}
	got, err := reopened.ExecutionSession("s1")
	if err != nil || got == nil || *got != auth {
		t.Fatalf("authorization lost: %+v %v", got, err)
	}
	changed := auth
	changed.InstanceToken = "replacement"
	if err := reopened.BindExecutionSession(changed); err == nil {
		t.Fatal("active authorization migrated")
	}
	receipts, err := reopened.ExecutionLaunches(p.DecisionID)
	if err != nil || len(receipts) != 1 || receipts[0].Authorization != auth {
		t.Fatalf("authoritative receipts lost: %+v %v", receipts, err)
	}
}
