package convoyfanout

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoycallback"
)

// DeliveryLabel marks every durable callback delivery record in the bead store.
const DeliveryLabel = "gc:convoy-callback-delivery"

const (
	deliveryBeadTitle          = "Convoy callback delivery"
	deliveryBeadDescription    = "Durable convoy lifecycle callback delivery admission"
	deliveryRecordMetadata     = "convoy.callback_delivery.record"
	deliveryEventMetadata      = "convoy.callback_delivery.event_id"
	deliveryRecipientMetadata  = "convoy.callback_delivery.recipient_id"
	deliveryKeyMetadata        = "convoy.callback_delivery.idempotency_key"
	deliveryStateMetadata      = "convoy.callback_delivery.state"
	deliverySupersededMetadata = "convoy.callback_delivery.superseded_by"
)

// DeliveryRecord is the durable admission record for one authorized logical
// recipient of one lifecycle event. It records what was authorized, which
// configured target was fenced, and the replay-stable idempotency key.
//
// It deliberately carries no submission receipt, no ReceivedAt, and no outcome:
// admission proves authorization, never delivery. The delivery-outcome boundary
// extends these records with submit, reconcile, response, and terminal outcome
// data.
type DeliveryRecord struct {
	ID                 string                   `json:"id,omitempty"`
	SchemaVersion      int                      `json:"schema_version"`
	EventID            string                   `json:"event_id"`
	EventType          convoycallback.EventType `json:"event_type"`
	ConvoyID           string                   `json:"convoy_id"`
	TaskID             string                   `json:"task_id,omitempty"`
	DeploymentID       string                   `json:"deployment_id,omitempty"`
	LaunchOrigin       string                   `json:"launch_origin"`
	CorrelationID      string                   `json:"correlation_id"`
	OccurredAt         time.Time                `json:"occurred_at"`
	City               string                   `json:"city"`
	Recipient          RecipientSnapshot        `json:"recipient"`
	TargetFence        TargetFence              `json:"target_fence"`
	EventRouteIdentity map[string]string        `json:"event_route_identity,omitempty"`
	IdempotencyKey     string                   `json:"idempotency_key"`
	Intent             DeliveryIntent           `json:"intent"`
	State              DeliveryState            `json:"state"`
	AdmittedAt         time.Time                `json:"admitted_at"`
}

// Admission is the durable fan-out result for one lifecycle event.
type Admission struct {
	EventID       string
	CorrelationID string
	TargetFence   TargetFence
	// Deliveries holds one queued record per authorized logical recipient, in
	// the deterministic admission order.
	Deliveries []DeliveryRecord
	// Rejections explains every candidate that was considered and refused.
	Rejections []RecipientRejection
}

// AdmissionService materializes callback delivery admissions in the bead store.
type AdmissionService struct {
	store beads.Store
}

// NewAdmissionService creates a fan-out admission service over store.
func NewAdmissionService(store beads.Store) *AdmissionService {
	return &AdmissionService{store: store}
}

// Admit authorizes the recipients of one lifecycle event and persists one
// queued delivery record per recipient before any transport side effect.
//
// Admit is idempotent per (event, logical recipient): re-admitting the same
// event returns the existing durable records unchanged, including after a
// process restart. A configured target that no longer matches a persisted fence
// fails closed with ErrStaleTargetFence rather than redirecting an existing
// record to a target that was never authorized for that event. When no
// recipient is authorized, Admit returns the populated rejections together with
// ErrNoAuthorizedRecipient and writes nothing.
func (s *AdmissionService) Admit(ctx context.Context, input AdmitInput) (Admission, error) {
	if ctx == nil {
		return Admission{}, invalidInput("nil context")
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	if s == nil || s.store == nil {
		return Admission{}, invalidInput("nil store")
	}
	normalized, err := normalizeAdmitInput(input)
	if err != nil {
		return Admission{}, err
	}
	recipients, rejections, err := selectNormalizedRecipients(normalized)
	admission := Admission{
		EventID:       normalized.Event.EventID,
		CorrelationID: normalized.Event.CorrelationID,
		TargetFence:   normalized.Target,
		Rejections:    rejections,
	}
	if err != nil {
		return admission, err
	}
	existing, err := s.loadEvent(ctx, normalized.Event.EventID)
	if err != nil {
		return admission, err
	}
	// An event ID identifies one immutable lifecycle event even when a replay's
	// authorization evidence selects a different recipient set. Check every
	// existing record before considering new recipients; checking only matching
	// idempotency keys would allow a changed event or target fence to accumulate
	// beside records created for the original admission.
	existingRecords := make([]DeliveryRecord, 0, len(existing))
	for _, prior := range existing {
		existingRecords = append(existingRecords, prior)
	}
	sortDeliveryRecords(existingRecords)
	for _, prior := range existingRecords {
		if err := checkReplay(prior, normalized); err != nil {
			return admission, err
		}
	}
	// Check every recipient's existing record against this admission before
	// creating any new one, so a stale fence on the last recipient cannot leave
	// behind a freshly written record for a fence that was just rejected.
	keys := make([]string, len(recipients))
	for i, recipient := range recipients {
		if err := ctx.Err(); err != nil {
			return admission, err
		}
		key, err := IdempotencyKey(normalized.Event.EventID, recipient.LogicalID)
		if err != nil {
			return admission, err
		}
		keys[i] = key
		if prior, found := existing[key]; found {
			if err := checkReplay(prior, normalized); err != nil {
				return admission, err
			}
		}
	}
	admission.Deliveries = make([]DeliveryRecord, 0, len(recipients))
	for i, recipient := range recipients {
		if err := ctx.Err(); err != nil {
			return admission, err
		}
		if prior, found := existing[keys[i]]; found {
			admission.Deliveries = append(admission.Deliveries, cloneDeliveryRecord(prior))
			continue
		}
		record, err := s.create(newDeliveryRecord(normalized, recipient, keys[i]))
		if err != nil {
			return admission, err
		}
		admission.Deliveries = append(admission.Deliveries, record)
	}
	return admission, nil
}

// Deliveries returns the durable delivery records for one event in the same
// deterministic order Admit used. Concurrent duplicate admissions of the same
// (event, recipient) pair converge here: the record that sorts first in the
// store's (created_at, id) order is canonical and every duplicate is closed and
// marked superseded, so a recipient can never be submitted to twice.
func (s *AdmissionService) Deliveries(ctx context.Context, eventID string) ([]DeliveryRecord, error) {
	if ctx == nil {
		return nil, invalidInput("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, invalidInput("nil store")
	}
	id, err := validateIdentity("event id", eventID, true)
	if err != nil {
		return nil, err
	}
	records, err := s.loadEvent(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]DeliveryRecord, 0, len(records))
	for _, record := range records {
		out = append(out, cloneDeliveryRecord(record))
	}
	sortDeliveryRecords(out)
	return out, nil
}

// loadEvent reads the canonical delivery records for one event, keyed by
// idempotency key, superseding any duplicate on the way.
func (s *AdmissionService) loadEvent(ctx context.Context, eventID string) (map[string]DeliveryRecord, error) {
	items, err := s.store.List(beads.ListQuery{
		Label:         DeliveryLabel,
		Metadata:      map[string]string{deliveryEventMetadata: eventID},
		IncludeClosed: true,
		AllowScan:     true,
		Sort:          beads.SortCreatedAsc,
	})
	if err != nil {
		return nil, fmt.Errorf("list convoy callback deliveries for %s: %w", eventID, err)
	}
	// Stores may push the label and metadata filters down only partially and
	// return a superset, so both are enforced here rather than assumed.
	scoped := make([]beads.Bead, 0, len(items))
	for _, item := range items {
		if !hasDeliveryLabel(item) {
			continue
		}
		if item.Metadata[deliveryEventMetadata] != eventID {
			continue
		}
		if strings.TrimSpace(item.Metadata[deliverySupersededMetadata]) != "" {
			continue
		}
		scoped = append(scoped, item)
	}
	sortDeliveryBeads(scoped)
	canonical := make(map[string]DeliveryRecord, len(scoped))
	for _, item := range scoped {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := decodeDeliveryBead(item, eventID)
		if err != nil {
			return nil, err
		}
		winner, duplicate := canonical[record.IdempotencyKey]
		if !duplicate {
			canonical[record.IdempotencyKey] = record
			continue
		}
		if err := s.supersede(item, winner.ID); err != nil {
			return nil, err
		}
	}
	return canonical, nil
}

func (s *AdmissionService) create(record DeliveryRecord) (DeliveryRecord, error) {
	payload, err := marshalDeliveryRecord(record)
	if err != nil {
		return DeliveryRecord{}, err
	}
	created, err := s.store.Create(beads.Bead{
		Title:       deliveryBeadTitle,
		Description: deliveryBeadDescription,
		Type:        "message",
		Labels:      []string{DeliveryLabel},
		Metadata:    deliveryMetadata(record, payload),
	})
	if err != nil {
		return DeliveryRecord{}, fmt.Errorf("create convoy callback delivery: %w", err)
	}
	record.ID = created.ID
	return record, nil
}

// supersede closes a duplicate admission record and points it at the canonical
// one. It never touches the canonical record and never records an outcome.
func (s *AdmissionService) supersede(duplicate beads.Bead, canonicalID string) error {
	closed := "closed"
	if err := s.store.Update(duplicate.ID, beads.UpdateOpts{
		Status:   &closed,
		Metadata: map[string]string{deliverySupersededMetadata: canonicalID},
	}); err != nil {
		return fmt.Errorf("supersede duplicate convoy callback delivery %s: %w", duplicate.ID, err)
	}
	return nil
}

func newDeliveryRecord(input AdmitInput, recipient RecipientSnapshot, key string) DeliveryRecord {
	return DeliveryRecord{
		SchemaVersion:      SchemaVersion,
		EventID:            input.Event.EventID,
		EventType:          input.Event.Type,
		ConvoyID:           input.Event.ConvoyID,
		TaskID:             input.Event.TaskID,
		DeploymentID:       input.Event.DeploymentID,
		LaunchOrigin:       input.Event.LaunchOrigin,
		CorrelationID:      input.Event.CorrelationID,
		OccurredAt:         input.Event.OccurredAt,
		City:               input.City,
		Recipient:          recipient,
		TargetFence:        input.Target,
		EventRouteIdentity: cloneRouteIdentity(input.Event.RouteIdentity),
		IdempotencyKey:     key,
		Intent:             input.Intent,
		State:              StateQueued,
		AdmittedAt:         input.Now,
	}
}

// checkReplay refuses to reuse a durable record whose immutable event identity
// or target fence disagrees with the incoming admission.
func checkReplay(prior DeliveryRecord, input AdmitInput) error {
	if !prior.TargetFence.Equal(input.Target) {
		return fmt.Errorf("%w: delivery %s is fenced to target %q revision %d",
			ErrStaleTargetFence, prior.ID, prior.TargetFence.TargetID, prior.TargetFence.ConfigRevision)
	}
	switch {
	case prior.CorrelationID != input.Event.CorrelationID,
		prior.EventType != input.Event.Type,
		prior.ConvoyID != input.Event.ConvoyID,
		prior.TaskID != input.Event.TaskID,
		prior.DeploymentID != input.Event.DeploymentID,
		prior.LaunchOrigin != input.Event.LaunchOrigin,
		!prior.OccurredAt.Equal(input.Event.OccurredAt),
		prior.City != input.City,
		!reflect.DeepEqual(prior.EventRouteIdentity, input.Event.RouteIdentity):
		return fmt.Errorf("%w: event %s already has durable deliveries with different immutable fields",
			ErrEventIdentityConflict, prior.EventID)
	}
	return nil
}

func decodeDeliveryBead(bead beads.Bead, eventID string) (DeliveryRecord, error) {
	payload := strings.TrimSpace(bead.Metadata[deliveryRecordMetadata])
	if payload == "" {
		return DeliveryRecord{}, fmt.Errorf("%w: delivery %s has no record metadata", ErrCorruptRecord, bead.ID)
	}
	var record DeliveryRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return DeliveryRecord{}, fmt.Errorf("%w: decode delivery %s: %w", ErrCorruptRecord, bead.ID, err)
	}
	if record.ID != "" && record.ID != bead.ID {
		return DeliveryRecord{}, fmt.Errorf("%w: delivery %s record id mismatch", ErrCorruptRecord, bead.ID)
	}
	record.ID = bead.ID
	if err := validateStoredDelivery(record, eventID); err != nil {
		return DeliveryRecord{}, fmt.Errorf("%w: delivery %s: %w", ErrCorruptRecord, bead.ID, err)
	}
	if key := bead.Metadata[deliveryKeyMetadata]; key != record.IdempotencyKey {
		return DeliveryRecord{}, fmt.Errorf("%w: delivery %s key metadata %q disagrees with its record", ErrCorruptRecord, bead.ID, key)
	}
	return record, nil
}

// validateStoredDelivery re-derives the idempotency key so a tampered or
// mismatched durable record cannot masquerade as another recipient's delivery.
//
// It requires only that a state is present, not that it is still StateQueued:
// fan-out writes queued and never anything else, but the delivery-outcome
// boundary advances these records past admission, and reading one back must not
// treat that progress as corruption.
func validateStoredDelivery(record DeliveryRecord, eventID string) error {
	if record.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema version %d is unsupported", record.SchemaVersion)
	}
	if record.EventID != eventID {
		return fmt.Errorf("event id %q does not match %q", record.EventID, eventID)
	}
	if record.Intent != IntentNotification {
		return fmt.Errorf("intent %q is unsupported", record.Intent)
	}
	if strings.TrimSpace(string(record.State)) == "" {
		return fmt.Errorf("delivery state required")
	}
	if strings.TrimSpace(record.CorrelationID) == "" {
		return fmt.Errorf("correlation id required")
	}
	if record.AdmittedAt.IsZero() {
		return fmt.Errorf("admitted_at required")
	}
	derived, err := IdempotencyKey(record.EventID, record.Recipient.LogicalID)
	if err != nil {
		return err
	}
	if derived != record.IdempotencyKey {
		return fmt.Errorf("idempotency key does not derive from recipient %q", record.Recipient.LogicalID)
	}
	return nil
}

func deliveryMetadata(record DeliveryRecord, payload string) map[string]string {
	return map[string]string{
		deliveryRecordMetadata:    payload,
		deliveryEventMetadata:     record.EventID,
		deliveryRecipientMetadata: record.Recipient.LogicalID,
		deliveryKeyMetadata:       record.IdempotencyKey,
		deliveryStateMetadata:     string(record.State),
	}
}

func marshalDeliveryRecord(record DeliveryRecord) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode convoy callback delivery: %w", err)
	}
	return string(encoded), nil
}

func cloneDeliveryRecord(record DeliveryRecord) DeliveryRecord {
	record.Recipient.Scope = cloneWorkScope(record.Recipient.Scope)
	record.EventRouteIdentity = cloneRouteIdentity(record.EventRouteIdentity)
	return record
}

// kindRank orders recipient kinds so admission and read-back agree: the fenced
// subscription evidence first, then the launching actor, then the configured
// default target.
func kindRank(kind RecipientKind) int {
	switch kind {
	case KindSubscription:
		return 0
	case KindLaunchOrigin:
		return 1
	case KindConfiguredDefault:
		return 2
	default:
		return 3
	}
}

func sortDeliveryRecords(records []DeliveryRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i].Recipient, records[j].Recipient
		if rank := kindRank(left.Kind); rank != kindRank(right.Kind) {
			return rank < kindRank(right.Kind)
		}
		if left.LogicalID != right.LogicalID {
			return left.LogicalID < right.LogicalID
		}
		return left.SubscriptionID < right.SubscriptionID
	})
}

func hasDeliveryLabel(bead beads.Bead) bool {
	for _, label := range bead.Labels {
		if label == DeliveryLabel {
			return true
		}
	}
	return false
}

// sortDeliveryBeads applies the store's canonical (created_at, id) total order
// so duplicate convergence always keeps the same winner.
func sortDeliveryBeads(items []beads.Bead) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
}
