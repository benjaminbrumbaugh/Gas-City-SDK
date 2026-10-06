package routingdecision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bbolt "go.etcd.io/bbolt"
)

func TestTerminalProjectionFailureDoesNotRollbackLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testDecisionPayload(t)
	payload.DecisionID = "decision-poison"
	payload.WorkBeadID = "sk-1234"
	payload.Target = "demo/code reviewer"
	payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	payload.CreatedAt = now.Add(-time.Hour)
	payload.ExpiresAt = now.Add(time.Hour)
	payload.BindingID = BindingID(payload)
	record, err := store.Create(payload, "create-poison")
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := store.Transition(TransitionRequest{
		DecisionID: payload.DecisionID, ExpectedRevision: record.RecordRevision,
		From: StateProposed, To: StateRevoked, IdempotencyToken: "revoke-poison",
		Reason: "poison projection regression",
	}, Verifier{})
	if receipt.State != StateRevoked {
		t.Fatalf("receipt = %+v, want committed revocation", receipt)
	}
	if !errors.Is(err, ErrDeliveryProjection) {
		t.Fatalf("projection error = %v, want ErrDeliveryProjection", err)
	}
	got, getErr := store.Get(payload.DecisionID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.State != StateRevoked {
		t.Fatalf("stored state = %q, want revoked after projection failure", got.State)
	}
}

func TestExpireDueContinuesPastDeliveryInvalidDecision(t *testing.T) {
	now := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	poison := testDecisionPayload(t)
	poison.DecisionID = "decision-poison-expiry"
	poison.WorkBeadID = "sk-1234"
	poison.Target = "demo/code reviewer"
	poison.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
	poison.CreatedAt = now.Add(-3 * time.Hour)
	poison.ExpiresAt = now.Add(-2 * time.Hour)
	poison.BindingID = BindingID(poison)
	valid := testDecisionPayload(t)
	valid.DecisionID = "decision-valid-expiry"
	valid.RecommendationID = "routing/v2:" + strings.Repeat("d", 64)
	valid.CreatedAt = now.Add(-2 * time.Hour)
	valid.ExpiresAt = now.Add(-time.Hour)
	valid.BindingID = BindingID(valid)
	for token, payload := range map[string]DecisionPayload{"create-poison-expiry": poison, "create-valid-expiry": valid} {
		if _, err := store.Create(payload, token); err != nil {
			t.Fatal(err)
		}
	}

	expired, err := store.ExpireDue(now, 2, func(id string) string { return "expire-" + id })
	if expired != 2 {
		t.Fatalf("expired = %d, want both decisions committed despite one bad projection (err=%v)", expired, err)
	}
	if !errors.Is(err, ErrDeliveryProjection) {
		t.Fatalf("projection error = %v, want ErrDeliveryProjection", err)
	}
	for _, id := range []string{poison.DecisionID, valid.DecisionID} {
		record, getErr := store.Get(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if record.State != StateExpired {
			t.Fatalf("%s state = %q, want expired", id, record.State)
		}
	}
}

func TestLaunchReceiptSurvivesDeliveryProjectionFailure(t *testing.T) {
	now := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	payload := testExecutionPayload(t)
	payload.DecisionID = "decision-poison-launch"
	payload.WorkBeadID = "sk-1234"
	payload.Target = "demo/code reviewer"
	payload.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	payload.CreatedAt = now.Add(-time.Hour)
	payload.ExpiresAt = now.Add(time.Hour)
	payload.Execution.Target = payload.Target
	payload.BindingID = BindingID(payload)
	if _, err := store.AdmitLocal(payload, "admit-poison-launch", func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "test admission"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	auth := ExecutionSessionAuthorization{
		DecisionID: payload.DecisionID, BindingID: payload.BindingID, SessionID: "session-poison-launch",
		Generation: "1", InstanceToken: "instance-poison-launch", WorkID: payload.WorkBeadID,
		ClaimFence: payload.ClaimFence, Execution: *payload.Execution,
	}
	if err := store.BindExecutionSession(auth); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.RecordExecutionLaunchAttempt(auth, "launch-poison-attempt")
	if receipt.ExecutionID == "" {
		t.Fatalf("launch receipt = %+v, want durable receipt despite projection failure", receipt)
	}
	if !errors.Is(err, ErrDeliveryProjection) {
		t.Fatalf("projection error = %v, want ErrDeliveryProjection", err)
	}
	launches, launchErr := store.ExecutionLaunches(payload.DecisionID)
	if launchErr != nil {
		t.Fatal(launchErr)
	}
	if len(launches) != 1 || launches[0] != receipt {
		t.Fatalf("launch receipts = %+v, want committed receipt %+v", launches, receipt)
	}
}

func TestExecutionSessionBoundAcceptsLegacyLedgerWithoutDeliveryBuckets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, StoreRelativePath)
	legacyCreateLedger(t, path, true)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := ExecutionSessionBound(root, "session")
	if err != nil {
		t.Fatalf("legacy read-only probe: %v", err)
	}
	if bound {
		t.Fatal("legacy ledger without an execution session reported a bound session")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("read-only legacy probe mutated the ledger")
	}
}

func TestExecutionSessionBoundStillRejectsCorruptLegacyLedger(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, StoreRelativePath)
	legacyCreateLedger(t, path, false)
	if bound, err := ExecutionSessionBound(root, "session"); !errors.Is(err, ErrStoreCorrupt) || bound {
		t.Fatalf("corrupt legacy probe = (%v, %v), want false/ErrStoreCorrupt", bound, err)
	}
}

func legacyCreateLedger(t *testing.T, path string, complete bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		legacyBuckets := [][]byte{bucketMeta, bucketDecisions, bucketStateExpiry, bucketStateCounts, bucketIdempotency, bucketReceiptByDecision, bucketTransitions, bucketImports, bucketPurgedDecisions}
		if !complete {
			legacyBuckets = legacyBuckets[:len(legacyBuckets)-1]
		}
		for _, name := range legacyBuckets {
			if _, err := tx.CreateBucket(name); err != nil {
				return err
			}
		}
		meta := tx.Bucket(bucketMeta)
		if meta == nil {
			return ErrStoreCorrupt
		}
		if err := meta.Put(keySchemaVersion, encodeUint64(SchemaVersion)); err != nil {
			return err
		}
		if err := meta.Put(keyStoreRevision, encodeUint64(0)); err != nil {
			return err
		}
		if err := meta.Put(keyReceiptIndexFloor, encodeUint64(0)); err != nil {
			return err
		}
		counts := tx.Bucket(bucketStateCounts)
		if counts == nil {
			return nil
		}
		for _, state := range AllStates() {
			if err := counts.Put([]byte(state), encodeUint64(0)); err != nil {
				return err
			}
		}
		return nil
	})
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}
