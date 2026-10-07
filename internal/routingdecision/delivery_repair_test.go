package routingdecision

import (
	"encoding/json"
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

func TestLaunchDeliveryBindsProjectedOutcomeToReceipt(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	store := openTestStore(t, now)
	clock := now
	store.now = func() time.Time { return clock }
	payload := testExecutionPayload(t)
	payload.DecisionID = "decision-two-launches"
	payload.WorkBeadID = "work-two-launches"
	payload.Target = "demo/code-reviewer"
	payload.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	payload.CreatedAt = now.Add(-time.Hour)
	payload.ExpiresAt = now.Add(time.Hour)
	payload.Execution.Target = payload.Target
	payload.BindingID = BindingID(payload)
	if _, err := store.AdmitLocal(payload, "admit-two-launches", func(Record) (AdmissionCallbackResult, error) {
		return AdmissionCallbackResult{State: StateAdmitted, Reason: "two launch delivery regression"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	authA := ExecutionSessionAuthorization{
		DecisionID: payload.DecisionID, BindingID: payload.BindingID, SessionID: "session-a",
		Generation: "1", InstanceToken: "instance-a", WorkID: payload.WorkBeadID,
		ClaimFence: payload.ClaimFence, Execution: *payload.Execution,
	}
	authB := authA
	authB.SessionID = "session-b"
	authB.InstanceToken = "instance-b"
	for _, auth := range []ExecutionSessionAuthorization{authA, authB} {
		if err := store.BindExecutionSession(auth); err != nil {
			t.Fatal(err)
		}
	}
	receiptA, err := store.RecordExecutionLaunchAttempt(authA, "attempt-a")
	if err != nil {
		t.Fatalf("record launch A: %v", err)
	}
	clock = now.Add(time.Second)
	receiptB, err := store.RecordExecutionLaunchAttempt(authB, "attempt-b")
	if err != nil {
		t.Fatalf("record launch B: %v", err)
	}
	if !receiptB.StartedAt.After(receiptA.StartedAt) {
		t.Fatalf("launch timestamps = (%s, %s), want B after A", receiptA.StartedAt, receiptB.StartedAt)
	}
	// Remove the first projections only to arrange the inter-transaction window
	// in which both already-committed launch receipts exist before A retries.
	if err := store.db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{bucketDeliveryItems, bucketDeliverySource, bucketDeliveryAcks} {
			bucket := tx.Bucket(name)
			if bucket == nil {
				continue
			}
			var keys [][]byte
			if err := bucket.ForEach(func(key, _ []byte) error {
				keys = append(keys, append([]byte(nil), key...))
				return nil
			}); err != nil {
				return err
			}
			for _, key := range keys {
				if err := bucket.Delete(key); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.recordExecutionLaunchDelivery(receiptA); err != nil {
		t.Fatalf("record launch A delivery: %v", err)
	}
	page, err := store.ListPendingDeliveries(DeliveryListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SourceID != receiptA.ExecutionID {
		t.Fatalf("pending deliveries = %+v, want one item sourced by launch A", page.Items)
	}
	var outcome ProducerExecutionOutcome
	if err := json.Unmarshal(page.Items[0].Payload, &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.ExecutionID == nil || *outcome.ExecutionID != receiptA.ExecutionID || outcome.SessionID == nil || *outcome.SessionID != authA.SessionID {
		t.Fatalf("launch A delivery projected another receipt: source=%q outcome=%+v", page.Items[0].SourceID, outcome)
	}
}

func TestV3DeliveryValidationRecomputesOutcomeID(t *testing.T) {
	payload := testExecutionPayload(t)
	item := DecisionWithAudits{Record: Record{Payload: payload, State: StateClaimed}, AdmissionReceiptID: "admission-real"}
	auth := ExecutionSessionAuthorization{
		DecisionID: payload.DecisionID, BindingID: payload.BindingID, SessionID: "session-id",
		Generation: "1", InstanceToken: "instance-token", WorkID: payload.WorkBeadID,
		ClaimFence: payload.ClaimFence, Execution: *payload.Execution,
	}
	receipt := ExecutionLaunchReceipt{ExecutionID: "execution-id", Authorization: auth, StartedAt: payload.CreatedAt.Add(time.Second)}
	outcome, available, err := ProjectProducerExecutionOutcome(item, []ExecutionLaunchReceipt{receipt})
	if err != nil || !available {
		t.Fatalf("project outcome: available=%v err=%v", available, err)
	}
	outcome.Status = "succeeded"
	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	delivery := DeliveryItem{
		SourceKind: "execution-launch", SourceID: receipt.ExecutionID,
		OutcomeSchemaVersion: outcome.SchemaVersion, OutcomeID: outcome.OutcomeID,
		RoutingDecisionID: *outcome.RoutingDecisionID, WorkID: outcome.WorkID,
		Payload: encoded, PayloadSHA256: payloadDigest(encoded), EvidenceAtUnix: outcome.ObservedAtUnix,
	}
	delivery.DeliveryID = DeliveryIDFor(delivery.SourceKind, delivery.SourceID, delivery.OutcomeSchemaVersion, delivery.OutcomeID)
	if err := validateDeliveryItem(delivery); !errors.Is(err, ErrDeliveryInvalid) {
		t.Fatalf("shape-valid but recomputed-invalid v3 outcome accepted: %v", err)
	}
}

func TestDeliveryValidationRejectsUppercasePayloadDigest(t *testing.T) {
	item := testDeliveryItem(t, "uppercase-digest", "work-1")
	item.PayloadSHA256 = "sha256:" + strings.ToUpper(strings.TrimPrefix(item.PayloadSHA256, "sha256:"))
	if err := validateDeliveryItem(item); !errors.Is(err, ErrDeliveryInvalid) {
		t.Fatalf("uppercase payload digest accepted: %v", err)
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
