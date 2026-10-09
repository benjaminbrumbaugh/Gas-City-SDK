package routingdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeliveryStorePersistsExactBytesAcrossReopenAndAckReplay(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	store, err := OpenStore(root, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	item := testDeliveryItem(t, "source-launch-1", "work-1")
	exact := append([]byte(nil), item.Payload...)
	if err := store.RecordDelivery(item); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}
	item.Payload[0] = '{'
	page, err := store.ListPendingDeliveries(DeliveryListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListPendingDeliveries: %v", err)
	}
	if len(page.Items) != 1 || string(page.Items[0].Payload) != string(exact) {
		t.Fatalf("pending payload changed: got %q want %q", page.Items[0].Payload, exact)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(root, StoreOptions{Now: func() time.Time { return now.Add(time.Minute) }})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store.Close() }()
	page, err = store.ListPendingDeliveries(DeliveryListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 || string(page.Items[0].Payload) != string(exact) {
		t.Fatalf("reopened pending = %+v, err=%v", page, err)
	}

	ack, err := store.AcknowledgeDelivery(DeliveryAckRequest{DeliveryID: item.DeliveryID, PayloadSHA256: item.PayloadSHA256})
	if err != nil {
		t.Fatalf("AcknowledgeDelivery: %v", err)
	}
	if ack.Replay || ack.Ack.AcknowledgedAtUnix != now.Add(time.Minute).Unix() {
		t.Fatalf("ack = %+v", ack)
	}
	page, err = store.ListPendingDeliveries(DeliveryListOptions{Limit: 10})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("acked item remained pending: %+v, err=%v", page, err)
	}
	replay, err := store.AcknowledgeDelivery(DeliveryAckRequest{DeliveryID: item.DeliveryID, PayloadSHA256: item.PayloadSHA256})
	if err != nil || !replay.Replay || replay.Ack != ack.Ack {
		t.Fatalf("ack replay = %+v, err=%v; first=%+v", replay, err, ack)
	}
	if _, err := store.AcknowledgeDelivery(DeliveryAckRequest{DeliveryID: item.DeliveryID, PayloadSHA256: "sha256:" + strings.Repeat("f", 64)}); !errors.Is(err, ErrDeliveryAckConflict) {
		t.Fatalf("conflicting ack error = %v", err)
	}
}

func TestDeliveryStoreRejectsConflictingBytesAndSourceIdentity(t *testing.T) {
	store := openTestStore(t, time.Unix(1_700_000_000, 0).UTC())
	item := testDeliveryItem(t, "source-transition-1", "work-1")
	if err := store.RecordDelivery(item); err != nil {
		t.Fatal(err)
	}
	conflict := item
	conflict.Payload = append(append([]byte(nil), item.Payload...), '\n')
	digest := sha256.Sum256(conflict.Payload)
	conflict.PayloadSHA256 = "sha256:" + hex.EncodeToString(digest[:])
	if err := store.RecordDelivery(conflict); !errors.Is(err, ErrDeliveryConflict) {
		t.Fatalf("same ID conflict = %v", err)
	}
	other := testDeliveryItem(t, "source-transition-1", "work-2")
	if err := store.RecordDelivery(other); !errors.Is(err, ErrDeliveryConflict) {
		t.Fatalf("same source conflict = %v", err)
	}
}

func TestDeliveryStorePendingUsesBoundedStableCursor(t *testing.T) {
	store := openTestStore(t, time.Unix(1_700_000_000, 0).UTC())
	for _, source := range []string{"source-a", "source-b", "source-c"} {
		if err := store.RecordDelivery(testDeliveryItem(t, source, source)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListPendingDeliveries(DeliveryListOptions{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	second, err := store.ListPendingDeliveries(DeliveryListOptions{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
}

func TestTerminalTransitionsPersistSchemaPreservingDelivery(t *testing.T) {
	for _, schema := range []string{"v2", "v3"} {
		t.Run(schema, func(t *testing.T) {
			store := openTestStore(t, time.Unix(1_700_000_000, 0).UTC())
			var payload DecisionPayload
			if schema == "v3" {
				payload = testExecutionPayload(t)
			} else {
				payload = testDecisionPayload(t)
				payload.RecommendationID = "routing/v2:" + strings.Repeat("c", 64)
			}
			record, err := store.Create(payload, "create-"+schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Transition(TransitionRequest{
				DecisionID: payload.DecisionID, ExpectedRevision: record.RecordRevision,
				From: StateProposed, To: StateRevoked, IdempotencyToken: "revoke-" + schema, Reason: "test refusal",
			}, Verifier{}); err != nil {
				t.Fatal(err)
			}
			page, err := store.ListPendingDeliveries(DeliveryListOptions{Limit: 10})
			if err != nil || len(page.Items) != 1 || page.Items[0].OutcomeSchemaVersion != "routing/outcome/"+schema {
				t.Fatalf("terminal delivery = %+v, err=%v", page, err)
			}
		})
	}
}

func testDeliveryItem(t *testing.T, sourceID, workID string) DeliveryItem {
	t.Helper()
	decisionID := "decision-" + sourceID
	decision := stringPointer(decisionID)
	row := OutcomeRecord{
		SchemaVersion: OutcomeSchemaVersion, CorrelationID: sourceID,
		RecommendationID: "routing/v2:" + strings.Repeat("c", 64), RoutingDecisionID: decision,
		WorkID: workID, RequestedTargetID: "worker", RequestedConfigDigest: "sha256:" + strings.Repeat("b", 64),
		Status: OutcomeStatusFailed, Disposition: OutcomeDispositionNotAdmitted,
		FailureClass: OutcomeFailureUnknown, Coverage: OutcomeCoverageUnknown,
		Provenance: OutcomeProvenanceDecision, ObservedAtUnix: 1_700_000_000,
	}
	row.OutcomeID = outcomeID(row)
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	item := DeliveryItem{
		SourceKind: "routing-transition", SourceID: sourceID,
		OutcomeSchemaVersion: OutcomeSchemaVersion, OutcomeID: row.OutcomeID,
		RoutingDecisionID: decisionID, WorkID: workID, Payload: payload,
		PayloadSHA256: "sha256:" + hex.EncodeToString(digest[:]), EvidenceAtUnix: 1_700_000_000,
	}
	item.DeliveryID = DeliveryIDFor(item.SourceKind, item.SourceID, item.OutcomeSchemaVersion, item.OutcomeID)
	return item
}
