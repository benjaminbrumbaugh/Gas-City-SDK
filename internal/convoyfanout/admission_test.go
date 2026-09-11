package convoyfanout

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoycallback"
)

func testAdmissionService() (*AdmissionService, *beads.MemStore) {
	store := beads.NewMemStore()
	return NewAdmissionService(store), store
}

func deliveryKeys(records []DeliveryRecord) []string {
	out := make([]string, len(records))
	for i, record := range records {
		out[i] = record.IdempotencyKey
	}
	return out
}

func deliveryRecipients(records []DeliveryRecord) []string {
	out := make([]string, len(records))
	for i, record := range records {
		out[i] = record.Recipient.LogicalID
	}
	return out
}

func listDeliveryBeads(t *testing.T, store *beads.MemStore) []beads.Bead {
	t.Helper()
	items, err := store.List(beads.ListQuery{Label: DeliveryLabel, IncludeClosed: true, AllowScan: true, Sort: beads.SortCreatedAsc})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestAdmitPersistsOneQueuedRecordPerAuthorizedRecipient(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	admission, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if got := deliveryRecipients(admission.Deliveries); !reflect.DeepEqual(got, []string{"principal:agent-a", "principal:launcher-a"}) {
		t.Fatalf("deliveries = %v", got)
	}
	if admission.EventID != "event-1" || admission.CorrelationID != "correlation-1" {
		t.Fatalf("admission identity = %#v", admission)
	}
	if !admission.TargetFence.Equal(TargetFence{TargetID: "target-1", ConfigRevision: 7}) {
		t.Fatalf("target fence = %#v", admission.TargetFence)
	}
	for _, record := range admission.Deliveries {
		if record.State != StateQueued {
			t.Fatalf("state = %q, want queued: admission never declares delivery", record.State)
		}
		if record.Intent != IntentNotification {
			t.Fatalf("intent = %q, want notification", record.Intent)
		}
		if record.ID == "" || record.IdempotencyKey == "" {
			t.Fatalf("record identity = %#v", record)
		}
		if !record.AdmittedAt.Equal(now) {
			t.Fatalf("admitted_at = %s, want the injected clock %s", record.AdmittedAt, now)
		}
		if record.CorrelationID != "correlation-1" {
			t.Fatalf("correlation = %q, want the event correlation on every recipient", record.CorrelationID)
		}
		if !record.TargetFence.Equal(admission.TargetFence) {
			t.Fatalf("record fence = %#v, want the admission fence", record.TargetFence)
		}
		if record.EventType != convoycallback.EventConvoyCreated || record.ConvoyID != "convoy-a" {
			t.Fatalf("record event projection = %#v", record)
		}
	}
	if keys := deliveryKeys(admission.Deliveries); keys[0] == keys[1] {
		t.Fatal("each recipient must get its own idempotency key")
	}
	items := listDeliveryBeads(t, store)
	if len(items) != 2 {
		t.Fatalf("durable beads = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.Status == "closed" {
			t.Fatalf("bead %s is closed at admission", item.ID)
		}
		if item.Metadata[deliveryEventMetadata] != "event-1" {
			t.Fatalf("event metadata = %q", item.Metadata[deliveryEventMetadata])
		}
		if item.Metadata[deliveryStateMetadata] != string(StateQueued) {
			t.Fatalf("state metadata = %q", item.Metadata[deliveryStateMetadata])
		}
		if item.Metadata[deliveryKeyMetadata] == "" || item.Metadata[deliveryRecordMetadata] == "" {
			t.Fatalf("delivery metadata = %#v", item.Metadata)
		}
	}
}

func TestAdmitDoesNotRecordDeliveryOutcomeOrReceipt(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	for _, item := range listDeliveryBeads(t, store) {
		payload := item.Metadata[deliveryRecordMetadata]
		var decoded map[string]any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"received_at", "outcome", "delivered_at", "submitted_at", "response_id"} {
			if _, present := decoded[forbidden]; present {
				t.Fatalf("admission record carries %q; queue admission is not delivery", forbidden)
			}
		}
		for key, value := range item.Metadata {
			if strings.Contains(strings.ToLower(value), "delivered") {
				t.Fatalf("metadata %q claims delivery: %q", key, value)
			}
		}
	}
}

func TestAdmitIsReplaySafeAcrossRestart(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	first, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	replayInput := testAdmitInput(now)
	replayInput.Now = now.Add(time.Hour)
	restarted := NewAdmissionService(store)
	second, err := restarted.Admit(context.Background(), replayInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Deliveries, second.Deliveries) {
		t.Fatalf("replay changed the durable records:\nfirst  = %#v\nsecond = %#v", first.Deliveries, second.Deliveries)
	}
	if items := listDeliveryBeads(t, store); len(items) != 2 {
		t.Fatalf("durable beads after replay = %d, want 2", len(items))
	}
}

func TestAdmitRejectsAChangedTargetFenceForAnExistingRecord(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	first, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	moved := testAdmitInput(now)
	moved.Target = TargetFence{TargetID: "target-2", ConfigRevision: 9}
	if _, err := service.Admit(context.Background(), moved); !errors.Is(err, ErrStaleTargetFence) {
		t.Fatalf("err = %v, want ErrStaleTargetFence", err)
	}
	after, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Deliveries, after) {
		t.Fatal("a changed configured target must never redirect an existing delivery record")
	}
	if items := listDeliveryBeads(t, store); len(items) != 2 {
		t.Fatalf("durable beads = %d, want the original 2", len(items))
	}
}

func TestAdmitWritesNothingWhenAnyRecipientHasAStaleFence(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	first := testAdmitInput(now)
	first.Subscriptions = nil
	if _, err := service.Admit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	// The new subscription recipient is admitted before the existing
	// launch-origin recipient, whose persisted fence no longer matches.
	moved := testAdmitInput(now)
	moved.Target = TargetFence{TargetID: "target-2", ConfigRevision: 9}
	if _, err := service.Admit(context.Background(), moved); !errors.Is(err, ErrStaleTargetFence) {
		t.Fatalf("err = %v, want ErrStaleTargetFence", err)
	}
	items := listDeliveryBeads(t, store)
	if len(items) != 1 {
		t.Fatalf("durable beads = %d, want only the original record: a rejected fence must write nothing", len(items))
	}
	if items[0].Metadata[deliveryRecipientMetadata] != "principal:launcher-a" {
		t.Fatalf("surviving record = %q, want the original launch-origin delivery", items[0].Metadata[deliveryRecipientMetadata])
	}
}

func TestAdmitRejectsAConflictingEventIdentityOnReplay(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	forged := testAdmitInput(now)
	forged.Event.CorrelationID = "correlation-forged"
	if _, err := service.Admit(context.Background(), forged); !errors.Is(err, ErrEventIdentityConflict) {
		t.Fatalf("err = %v, want ErrEventIdentityConflict", err)
	}
}

func TestAdmitRejectsAConflictingEventRouteIdentityOnReplay(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	forged := testAdmitInput(now)
	forged.Event.RouteIdentity["route"] = "different-opaque-route"
	if _, err := service.Admit(context.Background(), forged); !errors.Is(err, ErrEventIdentityConflict) {
		t.Fatalf("err = %v, want ErrEventIdentityConflict for changed route identity", err)
	}
}

func TestAdmitCopiesOpaqueRouteDataByValue(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	input := testAdmitInput(now)
	admission, err := service.Admit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Event.RouteIdentity["route"] = "mutated-after-admission"
	stored, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range stored {
		if record.EventRouteIdentity["route"] != "opaque-event" {
			t.Fatalf("stored route = %q, want the admitted copy", record.EventRouteIdentity["route"])
		}
	}
	admission.Deliveries[0].EventRouteIdentity["route"] = "mutated-in-result"
	reread, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range reread {
		if record.EventRouteIdentity["route"] != "opaque-event" {
			t.Fatal("returned records must not alias durable route data")
		}
	}
}

func TestAdmitConvergesConcurrentDuplicateRecords(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	first, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	canonical := first.Deliveries[0]
	duplicate := canonical
	duplicate.ID = ""
	payload, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	racer, err := store.Create(beads.Bead{
		Title:  deliveryBeadTitle,
		Type:   "message",
		Labels: []string{DeliveryLabel},
		Metadata: map[string]string{
			deliveryRecordMetadata: string(payload),
			deliveryEventMetadata:  duplicate.EventID,
			deliveryKeyMetadata:    duplicate.IdempotencyKey,
			deliveryStateMetadata:  string(StateQueued),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	winner, err := store.Get(canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !winner.CreatedAt.Before(racer.CreatedAt) {
		t.Fatalf("test precondition: the racer (%s) must be created after the canonical record (%s)", racer.CreatedAt, winner.CreatedAt)
	}
	converged, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(converged) != 2 {
		t.Fatalf("converged deliveries = %v, want one record per recipient", deliveryRecipients(converged))
	}
	if converged[0].ID != canonical.ID {
		t.Fatalf("canonical record = %q, want the first admitted %q", converged[0].ID, canonical.ID)
	}
	loser, err := store.Get(racer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loser.Status != "closed" {
		t.Fatalf("duplicate status = %q, want closed", loser.Status)
	}
	if loser.Metadata[deliverySupersededMetadata] != canonical.ID {
		t.Fatalf("superseded_by = %q, want %q", loser.Metadata[deliverySupersededMetadata], canonical.ID)
	}
}

func TestAdmitPersistsTheAdmissionSnapshotBeforeAnySideEffect(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	input.Subscriptions = nil
	admission, err := service.Admit(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got := deliveryRecipients(admission.Deliveries); !reflect.DeepEqual(got, []string{"default:target-1"}) {
		t.Fatalf("deliveries = %v", got)
	}
	items := listDeliveryBeads(t, store)
	if len(items) != 1 {
		t.Fatalf("durable beads = %d, want 1", len(items))
	}
	var record DeliveryRecord
	if err := json.Unmarshal([]byte(items[0].Metadata[deliveryRecordMetadata]), &record); err != nil {
		t.Fatal(err)
	}
	if !record.Recipient.IsDefault || record.Recipient.Reason != ReasonConfiguredDefaultTarget {
		t.Fatalf("durable default recipient = %#v", record.Recipient)
	}
	if record.State != StateQueued {
		t.Fatalf("durable state = %q, want queued", record.State)
	}
}

func TestAdmitReturnsRejectionsWhenNothingIsAuthorized(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	input := testAdmitInput(now)
	input.LaunchOrigin.Trusted = false
	input.Subscriptions = nil
	input.Target = TargetFence{}
	admission, err := service.Admit(context.Background(), input)
	if !errors.Is(err, ErrNoAuthorizedRecipient) {
		t.Fatalf("err = %v, want ErrNoAuthorizedRecipient", err)
	}
	if len(admission.Deliveries) != 0 {
		t.Fatalf("deliveries = %v, want none", deliveryRecipients(admission.Deliveries))
	}
	reasons := rejectionReasons(admission.Rejections)
	if reasons[defaultPrefix] != RejectDefaultTargetUnconfigured {
		t.Fatalf("rejections = %#v, want the auditable unconfigured-default reason", admission.Rejections)
	}
	if reasons["principal:launcher-a"] != RejectLaunchOriginUntrusted {
		t.Fatalf("rejections = %#v, want the auditable launch-origin reason", admission.Rejections)
	}
	if items := listDeliveryBeads(t, store); len(items) != 0 {
		t.Fatalf("durable beads = %d, want none", len(items))
	}
}

func TestAdmitRejectsInvalidEventBeforePersisting(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	input := testAdmitInput(now)
	input.Event.EventID = ""
	if _, err := service.Admit(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if items := listDeliveryBeads(t, store); len(items) != 0 {
		t.Fatalf("durable beads = %d, want none", len(items))
	}
}

func TestAdmitHonoursContextCancellation(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Admit(ctx, testAdmitInput(now)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if items := listDeliveryBeads(t, store); len(items) != 0 {
		t.Fatalf("durable beads = %d, want none", len(items))
	}
}

func TestAdmissionRejectsNilContexts(t *testing.T) {
	service, _ := testAdmissionService()
	if _, err := service.Admit(nil, testAdmitInput(testTime())); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Admit error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.Deliveries(nil, "event-1"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Deliveries error = %v, want ErrInvalidInput", err)
	}
}

func TestAdmitRejectsANilStore(t *testing.T) {
	now := testTime()
	service := NewAdmissionService(nil)
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeliveriesRejectsCorruptDurableRecords(t *testing.T) {
	service, store := testAdmissionService()
	if _, err := store.Create(beads.Bead{
		Title:  deliveryBeadTitle,
		Type:   "message",
		Labels: []string{DeliveryLabel},
		Metadata: map[string]string{
			deliveryRecordMetadata: "{not json",
			deliveryEventMetadata:  "event-1",
			deliveryKeyMetadata:    "key-1",
			deliveryStateMetadata:  string(StateQueued),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Deliveries(context.Background(), "event-1"); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("err = %v, want ErrCorruptRecord", err)
	}
}

func TestDeliveriesRejectsARecordWhoseKeyDoesNotDerive(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	items := listDeliveryBeads(t, store)
	tampered := items[0]
	var record DeliveryRecord
	if err := json.Unmarshal([]byte(tampered.Metadata[deliveryRecordMetadata]), &record); err != nil {
		t.Fatal(err)
	}
	record.Recipient.LogicalID = "principal:attacker"
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(tampered.ID, beads.UpdateOpts{Metadata: map[string]string{deliveryRecordMetadata: string(payload)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Deliveries(context.Background(), "event-1"); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("err = %v, want ErrCorruptRecord for a key that does not derive from its recipient", err)
	}
}

func TestAdmitPreservesARecordTheDeliveryBoundaryAdvanced(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	first, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatal(err)
	}
	advanced := first.Deliveries[0]
	advanced.State = DeliveryState("submitted")
	payload, err := json.Marshal(advanced)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(advanced.ID, beads.UpdateOpts{Metadata: map[string]string{
		deliveryRecordMetadata: string(payload),
		deliveryStateMetadata:  "submitted",
	}}); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Admit(context.Background(), testAdmitInput(now))
	if err != nil {
		t.Fatalf("re-admitting an advanced delivery must not fail: %v", err)
	}
	if replayed.Deliveries[0].State != DeliveryState("submitted") {
		t.Fatalf("state = %q, want the downstream boundary's progress preserved", replayed.Deliveries[0].State)
	}
	if items := listDeliveryBeads(t, store); len(items) != 2 {
		t.Fatalf("durable beads = %d, want no duplicate for an advanced delivery", len(items))
	}
}

func TestDeliveriesIgnoresBeadsWithoutTheDeliveryLabel(t *testing.T) {
	now := testTime()
	service, store := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(beads.Bead{
		Title: "unrelated bead that merely carries the same metadata",
		Type:  "task",
		Metadata: map[string]string{
			deliveryEventMetadata:  "event-1",
			deliveryRecordMetadata: "{not a delivery}",
		},
	}); err != nil {
		t.Fatal(err)
	}
	records, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("deliveries = %d, want only the labeled delivery records", len(records))
	}
}

func TestDeliveriesRequiresAnEventID(t *testing.T) {
	service, _ := testAdmissionService()
	if _, err := service.Deliveries(context.Background(), " "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeliveriesIsScopedToOneEvent(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	if _, err := service.Admit(context.Background(), testAdmitInput(now)); err != nil {
		t.Fatal(err)
	}
	other := testAdmitInput(now)
	other.Event.EventID = "event-2"
	other.Event.CorrelationID = "correlation-2"
	if _, err := service.Admit(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	first, err := service.Deliveries(context.Background(), "event-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range first {
		if record.EventID != "event-1" {
			t.Fatalf("record event = %q, want event-1", record.EventID)
		}
	}
	if len(first) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(first))
	}
}

func TestAdmitRejectsEventConflictAcrossChangedRecipientSet(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	first := testAdmitInput(now)
	first.LaunchOrigin.Trusted = false
	first.Subscriptions = nil

	initial, err := service.Admit(context.Background(), first)
	if err != nil {
		t.Fatalf("initial default admission: %v", err)
	}
	if len(initial.Deliveries) != 1 || initial.Deliveries[0].Recipient.Kind != KindConfiguredDefault {
		t.Fatalf("initial admission = %#v, want one configured-default delivery", initial.Deliveries)
	}

	second := first
	second.Event.ConvoyID = "convoy-b"
	second.LaunchOrigin.Trusted = true
	second.LaunchOrigin.Principal = "launcher-a"
	second.LaunchOrigin.Scope = WorkScope{City: "city-a"}
	second.LaunchOrigin.Interests = []convoycallback.EventType{convoycallback.EventConvoyCreated}
	if _, err := service.Admit(context.Background(), second); !errors.Is(err, ErrEventIdentityConflict) {
		t.Fatalf("changed recipient-set admission error = %v, want ErrEventIdentityConflict", err)
	}
}

func TestAdmitRejectsTargetFenceConflictAcrossChangedRecipientSet(t *testing.T) {
	now := testTime()
	service, _ := testAdmissionService()
	first := testAdmitInput(now)
	first.LaunchOrigin.Trusted = false
	first.Subscriptions = nil

	if _, err := service.Admit(context.Background(), first); err != nil {
		t.Fatalf("initial default admission: %v", err)
	}

	second := first
	second.Target = TargetFence{TargetID: "target-2", ConfigRevision: 7}
	second.LaunchOrigin.Trusted = true
	second.LaunchOrigin.Principal = "launcher-a"
	second.LaunchOrigin.Scope = WorkScope{City: "city-a"}
	second.LaunchOrigin.Interests = []convoycallback.EventType{convoycallback.EventConvoyCreated}
	if _, err := service.Admit(context.Background(), second); !errors.Is(err, ErrStaleTargetFence) {
		t.Fatalf("changed recipient-set target error = %v, want ErrStaleTargetFence", err)
	}
}
