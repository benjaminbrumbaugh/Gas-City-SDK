package routingdecision

import (
	"crypto/ed25519"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func signedDecision(t *testing.T, payload DecisionPayload, approvedAt time.Time) (ApprovalPayload, Signature, Verifier) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	approval := ApprovalPayload{
		Schema:      SchemaVersion,
		DecisionID:  payload.DecisionID,
		BindingID:   payload.BindingID,
		AuthorityID: "board",
		ApprovedAt:  approvedAt,
	}
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		t.Fatal(err)
	}
	signature := Signature{
		Algorithm:   SignatureAlgorithmEd25519,
		AuthorityID: approval.AuthorityID,
		Value:       ed25519.Sign(privateKey, signing),
	}
	return approval, signature, NewVerifier(map[string]ed25519.PublicKey{"board": publicKey})
}

func currentTestDecision(t *testing.T, id string, now time.Time) (DecisionPayload, ApprovalPayload, Signature, Verifier) {
	t.Helper()
	payload := testDecisionPayload(t)
	payload.DecisionID = id
	payload.WorkRevision = -7
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Hour)
	payload.BindingID = BindingID(payload)
	approval, signature, verifier := signedDecision(t, payload, now.Add(-time.Second))
	return payload, approval, signature, verifier
}

func TestIngestApprovedValidatesCurrentSignedEnvelopeAtomically(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload, approval, signature, verifier := currentTestDecision(t, "decision-live", now)

	got, err := store.IngestApproved(IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "ingest-live",
	}, verifier)
	if err != nil {
		t.Fatalf("IngestApproved: %v", err)
	}
	if got.Record.State != StateApproved || got.Record.RecordRevision != 2 || got.Receipt.State != StateApproved {
		t.Fatalf("ingest result = %+v", got)
	}
	if got.Record.Payload.WorkRevision != -7 {
		t.Fatalf("ingest normalized signed work revision to %d", got.Record.Payload.WorkRevision)
	}
	replay, err := store.IngestApproved(IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "ingest-live",
	}, verifier)
	if err != nil || !reflect.DeepEqual(replay, got) {
		t.Fatalf("idempotent replay = (%+v, %v), want %+v", replay, err, got)
	}

	tampered := payload
	tampered.Reason = "tampered after signing"
	if _, err := store.IngestApproved(IngestApprovedRequest{
		Payload: tampered, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "ingest-tampered",
	}, verifier); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered ingest error = %v, want ErrInvalidSignature", err)
	}
	if _, err := store.Get("decision-live"); err != nil {
		t.Fatalf("valid ingest disappeared after refused write: %v", err)
	}
}

func TestIngestApprovedRejectsFutureApprovalAndInactiveDecisionWithoutWriting(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)

	for _, tc := range []struct {
		name       string
		createdAt  time.Time
		expiresAt  time.Time
		approvedAt time.Time
	}{
		{name: "future-decision", createdAt: now.Add(time.Second), expiresAt: now.Add(time.Hour), approvedAt: now.Add(time.Second)},
		{name: "expired-decision", createdAt: now.Add(-time.Hour), expiresAt: now, approvedAt: now.Add(-time.Minute)},
		{name: "future-approval", createdAt: now.Add(-time.Minute), expiresAt: now.Add(time.Hour), approvedAt: now.Add(time.Second)},
		{name: "approval-before-decision", createdAt: now.Add(-time.Minute), expiresAt: now.Add(time.Hour), approvedAt: now.Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := testDecisionPayload(t)
			payload.DecisionID = tc.name
			payload.CreatedAt = tc.createdAt
			payload.ExpiresAt = tc.expiresAt
			payload.BindingID = BindingID(payload)
			approval, signature, verifier := signedDecision(t, payload, tc.approvedAt)
			_, err := store.IngestApproved(IngestApprovedRequest{
				Payload: payload, Approval: approval, Signature: signature,
				Now: now, IdempotencyToken: "ingest-" + tc.name,
			}, verifier)
			if !errors.Is(err, ErrInvalidDecision) {
				t.Fatalf("IngestApproved error = %v, want ErrInvalidDecision", err)
			}
			if _, err := store.Get(tc.name); !errors.Is(err, ErrDecisionNotFound) {
				t.Fatalf("invalid ingest persisted record: %v", err)
			}
		})
	}
}

func TestClaimedRemainsActiveAndCanRecordOutcome(t *testing.T) {
	if IsTerminalState(StateClaimed) {
		t.Fatal("claimed must remain active for lifecycle reconciliation")
	}
	if !IsActiveState(StateClaimed) || !IsAllowedTransition(StateClaimed, StateOutcomeRecorded) {
		t.Fatal("claimed must be active and allow outcome recording")
	}
}

func TestListDecisionsUsesStableIDCursorAndReturnsAudits(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	for _, id := range []string{"decision-c", "decision-a", "decision-b"} {
		payload, approval, signature, itemVerifier := currentTestDecision(t, id, now)
		if _, err := store.IngestApproved(IngestApprovedRequest{
			Payload: payload, Approval: approval, Signature: signature,
			Now: now, IdempotencyToken: "ingest-" + id,
		}, itemVerifier); err != nil {
			t.Fatal(err)
		}
	}

	first, err := store.ListDecisions(ListOptions{State: StateApproved, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].Record.Payload.DecisionID != "decision-a" || first.Items[1].Record.Payload.DecisionID != "decision-b" || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	if len(first.Items[0].Audits) != 2 || first.Items[0].Audits[0].To != StateProposed || first.Items[0].Audits[1].To != StateApproved {
		t.Fatalf("first decision audits = %+v", first.Items[0].Audits)
	}
	second, err := store.ListDecisions(ListOptions{State: StateApproved, Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Record.Payload.DecisionID != "decision-c" || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestPurgeTerminalIsCalendarBoundedAndCascadesNewReceipts(t *testing.T) {
	current := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return current }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	create := func(id string, terminal State) {
		t.Helper()
		payload, approval, signature, verifier := currentTestDecision(t, id, current)
		result, err := store.IngestApproved(IngestApprovedRequest{
			Payload: payload, Approval: approval, Signature: signature,
			Now: current, IdempotencyToken: "ingest-" + id,
		}, verifier)
		if err != nil {
			t.Fatal(err)
		}
		if terminal == StateApproved {
			return
		}
		receipt, err := store.Transition(TransitionRequest{
			DecisionID: id, ExpectedRevision: result.Record.RecordRevision,
			From: StateApproved, To: terminal, IdempotencyToken: "terminal-" + id,
			Reason: "test terminal transition",
		}, Verifier{})
		if err != nil {
			t.Fatal(err)
		}
		if terminal == StateAdmitted {
			if _, err := store.Transition(TransitionRequest{
				DecisionID: id, ExpectedRevision: receipt.RecordRevision,
				From: StateAdmitted, To: StateClaimed, IdempotencyToken: "claimed-" + id,
				Reason: "exact carrier claimed",
			}, Verifier{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("decision-a-terminal", StateRevoked)
	create("decision-b-active", StateAdmitted)

	current = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	page, err := store.PurgeTerminal(PurgeOptions{Now: current, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Scanned != 1 || page.Deleted != 1 || page.NextCursor == "" {
		t.Fatalf("first purge page = %+v", page)
	}
	if _, err := store.Get("decision-a-terminal"); !errors.Is(err, ErrDecisionNotFound) {
		t.Fatalf("terminal decision survived purge: %v", err)
	}
	if _, err := store.Get("decision-b-active"); err != nil {
		t.Fatalf("active claimed decision was purged: %v", err)
	}
	if err := store.db.View(func(tx *boltTx) error {
		if tx.Bucket(bucketIdempotency).Get([]byte("ingest-decision-a-terminal")) != nil || tx.Bucket(bucketIdempotency).Get([]byte("terminal-decision-a-terminal")) != nil {
			t.Fatal("purge retained indexed idempotency receipts")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err = store.PurgeTerminal(PurgeOptions{Now: current, Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if page.Scanned != 1 || page.Deleted != 0 || page.NextCursor != "" {
		t.Fatalf("second purge page = %+v", page)
	}
}

func TestConcurrentIngestReplaysOneAtomicRecord(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload, approval, signature, verifier := currentTestDecision(t, "decision-race", now)
	request := IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "ingest-race",
	}
	start := make(chan struct{})
	results := make(chan IngestApprovedResult, 2)
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			result, err := store.IngestApproved(request, verifier)
			results <- result
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("concurrent results differ: %+v %+v", first, second)
	}
	page, err := store.ListDecisions(ListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("concurrent ingest records = (%+v, %v)", page, err)
	}
}

func TestIngestApprovedCanonicalReplayNormalizesEquivalentEnvelope(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testDecisionPayload(t)
	payload.DecisionID = "decision-canonical-replay"
	payload.CreatedAt = now.Add(-time.Minute)
	payload.ExpiresAt = now.Add(time.Hour)
	payload.Evidence = nil
	payload.Alternatives = nil
	payload.Options = []AuditOption{{Key: "z", Value: "last"}, {Key: "a", Value: "first"}}
	payload.BindingID = BindingID(payload)
	approval, signature, verifier := signedDecision(t, payload, now.Add(-time.Second))
	first, err := store.IngestApproved(IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "canonical-replay",
	}, verifier)
	if err != nil {
		t.Fatal(err)
	}

	equivalent := payload
	equivalent.Evidence = []string{}
	equivalent.Alternatives = []Alternative{}
	equivalent.Options = []AuditOption{{Key: "a", Value: "first"}, {Key: "z", Value: "last"}}
	equivalent.CreatedAt = equivalent.CreatedAt.In(time.FixedZone("west", -7*60*60))
	equivalent.ExpiresAt = equivalent.ExpiresAt.In(time.FixedZone("west", -7*60*60))
	equivalent.BindingID = BindingID(equivalent)
	equivalentApproval := approval
	equivalentApproval.ApprovedAt = approval.ApprovedAt.In(time.FixedZone("west", -7*60*60))
	replay, err := store.IngestApproved(IngestApprovedRequest{
		Payload: equivalent, Approval: equivalentApproval, Signature: signature,
		Now: now, IdempotencyToken: "canonical-replay",
	}, verifier)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("canonical replay = (%+v, %v), want %+v", replay, err, first)
	}
	if first.Record.Payload.Evidence == nil || first.Record.Payload.Alternatives == nil || first.Record.Payload.Options[0].Key != "a" || first.Record.Payload.CreatedAt.Location() != time.UTC {
		t.Fatalf("stored payload was not canonicalized: %+v", first.Record.Payload)
	}
}

func TestReceiptIndexIsVerifiedAndBaselineBucketsReopenAdditively(t *testing.T) {
	now := time.Date(2026, 8, 7, 20, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	payload, approval, signature, verifier := currentTestDecision(t, "decision-reopen", now)
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.IngestApproved(IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: now, IdempotencyToken: "ingest-reopen",
	}, verifier); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *boltTx) error {
		return tx.Bucket(bucketReceiptByDecision).Delete(receiptIndexKey(payload.DecisionID, "ingest-reopen"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(verifier); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatalf("Verify missing receipt index error = %v, want ErrStoreCorrupt", err)
	}
	if err := store.db.Update(func(tx *boltTx) error {
		if err := tx.DeleteBucket(bucketReceiptByDecision); err != nil {
			return err
		}
		if err := tx.DeleteBucket(bucketStateCounts); err != nil {
			return err
		}
		if err := tx.DeleteBucket(bucketPurgedDecisions); err != nil {
			return err
		}
		return tx.Bucket(bucketMeta).Delete(keyReceiptIndexFloor)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("OpenStore baseline buckets: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Verify(verifier); err != nil {
		t.Fatalf("Verify additive reopen: %v", err)
	}
	status, err := reopened.Status()
	if err != nil {
		t.Fatal(err)
	}
	var approved uint64
	for _, count := range status.StateCounts {
		if count.State == StateApproved {
			approved = count.Count
		}
	}
	if status.StoreRevision != 2 || approved != 1 {
		t.Fatalf("reopened status = %+v", status)
	}
}

func TestPurgedDecisionTombstonePreventsStillCurrentReingest(t *testing.T) {
	current := time.Date(2025, 12, 1, 12, 0, 0, 0, time.UTC)
	cityRoot := t.TempDir()
	store, err := OpenStore(cityRoot, StoreOptions{Now: func() time.Time { return current }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	payload, _, _, _ := currentTestDecision(t, "decision-long-valid", current)
	payload.ExpiresAt = current.AddDate(1, 0, 0)
	payload.BindingID = BindingID(payload)
	approval, signature, verifier := signedDecision(t, payload, current.Add(-time.Second))
	request := IngestApprovedRequest{
		Payload: payload, Approval: approval, Signature: signature,
		Now: current, IdempotencyToken: "ingest-long-valid",
	}
	result, err := store.IngestApproved(request, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(TransitionRequest{
		DecisionID: payload.DecisionID, ExpectedRevision: result.Record.RecordRevision,
		From: StateApproved, To: StateRevoked, IdempotencyToken: "revoke-long-valid", Reason: "revoked",
	}, Verifier{}); err != nil {
		t.Fatal(err)
	}
	current = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	page, err := store.PurgeTerminal(PurgeOptions{Now: current, Limit: 10})
	if err != nil || page.Deleted != 1 {
		t.Fatalf("PurgeTerminal = (%+v, %v)", page, err)
	}
	request.Now = current
	request.IdempotencyToken = "ingest-long-valid-again"
	if _, err := store.IngestApproved(request, verifier); !errors.Is(err, ErrDecisionExists) {
		t.Fatalf("reingest purged current decision error = %v, want ErrDecisionExists", err)
	}
}
