package routingdecision

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func approvedTestRecord(t *testing.T, store *Store, now time.Time) (Record, Verifier) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})
	payload := testDecisionPayload(t)
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Minute)
	payload.BindingID = BindingID(payload)
	record, err := store.Create(payload, "admission-create")
	if err != nil {
		t.Fatal(err)
	}
	approval := ApprovalPayload{Schema: SchemaVersion, DecisionID: payload.DecisionID, BindingID: payload.BindingID, AuthorityID: "board", ApprovedAt: now}
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	signature := Signature{Algorithm: SignatureAlgorithmEd25519, AuthorityID: "board", Value: ed25519.Sign(privateKey, signing)}
	if _, err := store.Transition(TransitionRequest{DecisionID: payload.DecisionID, ExpectedRevision: record.RecordRevision, From: StateProposed, To: StateApproved, Approval: &approval, Signature: &signature, IdempotencyToken: "admission-approve", Reason: "approved"}, verifier); err != nil {
		t.Fatal(err)
	}
	record, err = store.Get(payload.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	return record, verifier
}

func TestFinalAdmissionSerializesAgainstConcurrentRevocation(t *testing.T) {
	now := time.Date(2026, 8, 7, 23, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	record, verifier := approvedTestRecord(t, store, now)
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	admissionResult := make(chan error, 1)
	go func() {
		_, err := store.FinalAdmission(FinalAdmissionRequest{
			DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
			Now: now, IdempotencyToken: "admission-race-final",
		}, verifier, func(Record) (AdmissionCallbackResult, error) {
			close(callbackEntered)
			<-releaseCallback
			return AdmissionCallbackResult{State: StateAdmitted, Reason: "exact bead CAS committed"}, nil
		})
		admissionResult <- err
	}()
	<-callbackEntered

	revocationStarted := make(chan struct{})
	revocationResult := make(chan error, 1)
	var once sync.Once
	go func() {
		once.Do(func() { close(revocationStarted) })
		_, err := store.Transition(TransitionRequest{
			DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
			From: StateApproved, To: StateRevoked, IdempotencyToken: "admission-race-revoke", Reason: "revoked",
		}, Verifier{})
		revocationResult <- err
	}()
	<-revocationStarted
	close(releaseCallback)
	if err := <-admissionResult; err != nil {
		t.Fatalf("FinalAdmission: %v", err)
	}
	if err := <-revocationResult; !errors.Is(err, ErrStaleRevision) && !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("concurrent revocation error = %v, want stale revision or invalid transition", err)
	}
	after, err := store.Get(record.Payload.DecisionID)
	if err != nil || after.State != StateAdmitted {
		t.Fatalf("final record = (%+v, %v), want admitted", after, err)
	}
}

func TestFinalAdmissionSerializesVerifierCallbackAndLifecycleCommit(t *testing.T) {
	now := time.Date(2026, 8, 7, 23, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	record, verifier := approvedTestRecord(t, store, now)
	called := 0
	receipt, err := store.FinalAdmission(FinalAdmissionRequest{
		DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
		Now: now, IdempotencyToken: "admission-final",
	}, verifier, func(gated Record) (AdmissionCallbackResult, error) {
		called++
		if gated.State != StateApproved || gated.RecordRevision != record.RecordRevision {
			t.Fatalf("gated record = %+v", gated)
		}
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "exact bead CAS committed"}, nil
	})
	if err != nil {
		t.Fatalf("FinalAdmission: %v", err)
	}
	if called != 1 || receipt.State != StateAdmitted {
		t.Fatalf("called=%d receipt=%+v", called, receipt)
	}
	replay, err := store.FinalAdmission(FinalAdmissionRequest{
		DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
		Now: now, IdempotencyToken: "admission-final",
	}, verifier, func(Record) (AdmissionCallbackResult, error) {
		t.Fatal("idempotent replay called admission callback")
		return AdmissionCallbackResult{}, nil
	})
	if err != nil || replay != receipt {
		t.Fatalf("admission replay = (%+v, %v), want %+v", replay, err, receipt)
	}
}

func TestFinalAdmissionCallbackFailureRollsBackLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 7, 23, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	record, verifier := approvedTestRecord(t, store, now)
	secretErr := errors.New("bead-secret")
	if _, err := store.FinalAdmission(FinalAdmissionRequest{
		DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
		Now: now, IdempotencyToken: "admission-fail",
	}, verifier, func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{}, secretErr
	}); !errors.Is(err, ErrAdmissionCallback) {
		t.Fatalf("FinalAdmission error = %v, want ErrAdmissionCallback", err)
	}
	after, err := store.Get(record.Payload.DecisionID)
	if err != nil || after.State != StateApproved || after.RecordRevision != record.RecordRevision {
		t.Fatalf("failed callback mutated lifecycle: (%+v, %v)", after, err)
	}
}

func TestFinalAdmissionCommitFaultRollsBackLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 7, 23, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	store, err := OpenStore(cityRoot, StoreOptions{
		Now:                   func() time.Time { return now },
		BeforeAdmissionCommit: func() error { return errors.New("page detail secret") },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record, verifier := approvedTestRecord(t, store, now)
	if _, err := store.FinalAdmission(FinalAdmissionRequest{
		DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
		Now: now, IdempotencyToken: "admission-commit-fault",
	}, verifier, func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "exact bead CAS committed"}, nil
	}); !errors.Is(err, ErrAdmissionCommit) || strings.Contains(err.Error(), "page detail") {
		t.Fatalf("FinalAdmission error = %v, want sanitized ErrAdmissionCommit", err)
	}
	after, err := store.Get(record.Payload.DecisionID)
	if err != nil || after.State != StateApproved || after.RecordRevision != record.RecordRevision {
		t.Fatalf("commit fault mutated lifecycle: (%+v, %v)", after, err)
	}
}
