package convoy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	callbackSubscriptionLabel  = "gc:convoy-callback-subscription"
	subscriptionSchemaVersion  = 1
	subscriptionRecordMetadata = "convoy.callback_subscription.record"
	subscriptionOwnerMetadata  = "convoy.callback_subscription.owner"
	subscriptionRegMetadata    = "convoy.callback_subscription.registration_id"
	subscriptionGenMetadata    = "convoy.callback_subscription.generation"
	subscriptionStateMetadata  = "convoy.callback_subscription.state"
)

var (
	// ErrInvalidInput reports malformed subscription input.
	ErrInvalidInput = errors.New("callback subscription invalid input")
	// ErrNotFound reports that the requested subscription does not exist.
	ErrNotFound = errors.New("callback subscription not found")
	// ErrUnauthorized reports that the caller is not the subscription owner.
	ErrUnauthorized = errors.New("callback subscription owner is not authorized")
	// ErrFenceRequired reports a mutation without its registration fence.
	ErrFenceRequired = errors.New("callback subscription registration fence required")
	// ErrStaleFence reports a registration or generation mismatch.
	ErrStaleFence = errors.New("callback subscription registration fence is stale")
	// ErrInvalidState reports an operation that is not valid for the current state.
	ErrInvalidState = errors.New("callback subscription state does not permit this operation")
	// ErrFencingUnavailable reports a store that cannot provide conditional writes.
	ErrFencingUnavailable = errors.New("callback subscription fencing unavailable")
	// ErrCorruptRecord reports malformed durable subscription data.
	ErrCorruptRecord = errors.New("callback subscription durable record is corrupt")
)

// SubscriptionState is the durable lifecycle state of a callback subscription.
type SubscriptionState string

const (
	// SubscriptionActive accepts matching lifecycle callbacks.
	SubscriptionActive SubscriptionState = "active"
	// SubscriptionRevoked records an explicit owner revocation.
	SubscriptionRevoked SubscriptionState = "revoked"
	// SubscriptionUnregistered records an explicit registration removal.
	SubscriptionUnregistered SubscriptionState = "unregistered"
)

// LifecycleInterest names a convoy lifecycle event a subscription wants to observe.
// Values are intentionally harness-neutral and may be extended by callers.
type LifecycleInterest string

// WorkScope identifies the work a subscription is authorized to observe.
// At least one of City, ConvoyID, or WorkRefs must be present.
type WorkScope struct {
	City     string   `json:"city,omitempty"`
	ConvoyID string   `json:"convoy_id,omitempty"`
	WorkRefs []string `json:"work_refs,omitempty"`
}

// RegistrationFence identifies one registration incarnation.
type RegistrationFence struct {
	RegistrationID string `json:"registration_id"`
	Generation     uint64 `json:"generation"`
}

// SubscriptionRecord is the durable owner- and generation-fenced callback subscription.
// RouteIdentity is opaque and is never an authorization principal.
type SubscriptionRecord struct {
	ID                   string              `json:"id,omitempty"`
	SchemaVersion        int                 `json:"schema_version"`
	Owner                string              `json:"owner"`
	RegistrationID       string              `json:"registration_id"`
	Generation           uint64              `json:"generation"`
	Scope                WorkScope           `json:"scope"`
	RouteIdentity        string              `json:"route_identity"`
	Interests            []LifecycleInterest `json:"interests"`
	State                SubscriptionState   `json:"state"`
	RegisteredAt         time.Time           `json:"registered_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
	RevokedAt            *time.Time          `json:"revoked_at,omitempty"`
	RevocationReason     string              `json:"revocation_reason,omitempty"`
	UnregisteredAt       *time.Time          `json:"unregistered_at,omitempty"`
	UnregistrationReason string              `json:"unregistration_reason,omitempty"`
}

// CreateSubscriptionInput describes a new callback subscription.
type CreateSubscriptionInput struct {
	Owner          string
	RegistrationID string
	Generation     uint64
	Scope          WorkScope
	RouteIdentity  string
	Interests      []LifecycleInterest
	Now            time.Time
}

// SubscriptionPatch contains mutable subscription fields. Owner,
// RegistrationID, and Generation are immutable except for fenced generation
// advancement performed by the service.
type SubscriptionPatch struct {
	Scope         *WorkScope
	RouteIdentity *string
	Interests     []LifecycleInterest
}

// SubscriptionService persists callback subscriptions in the bead store.
type SubscriptionService struct {
	store beads.Store
}

// NewSubscriptionService creates a callback-subscription service over store.
func NewSubscriptionService(store beads.Store) *SubscriptionService {
	return &SubscriptionService{store: store}
}

// Create persists one active callback subscription.
func (s *SubscriptionService) Create(ctx context.Context, input CreateSubscriptionInput) (SubscriptionRecord, error) {
	if err := checkSubscriptionContext(ctx); err != nil {
		return SubscriptionRecord{}, err
	}
	if s == nil || s.store == nil {
		return SubscriptionRecord{}, invalidSubscriptionInput("nil store")
	}
	normalized, err := normalizeCreateInput(input)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	record := SubscriptionRecord{
		SchemaVersion:  subscriptionSchemaVersion,
		Owner:          normalized.Owner,
		RegistrationID: normalized.RegistrationID,
		Generation:     normalized.Generation,
		Scope:          normalized.Scope,
		RouteIdentity:  normalized.RouteIdentity,
		Interests:      normalized.Interests,
		State:          SubscriptionActive,
		RegisteredAt:   normalized.Now,
		UpdatedAt:      normalized.Now,
	}
	payload, err := marshalSubscriptionRecord(record)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	created, err := s.store.Create(beads.Bead{
		Title:       "Convoy callback subscription",
		Description: "Durable callback subscription",
		From:        record.Owner,
		Type:        "message",
		Labels:      []string{callbackSubscriptionLabel},
		Metadata:    subscriptionMetadata(record, payload),
	})
	if err != nil {
		return SubscriptionRecord{}, fmt.Errorf("create callback subscription: %w", err)
	}
	record.ID = created.ID
	return cloneSubscriptionRecord(record), nil
}

// Get returns a subscription only for its exact owner. RouteIdentity is not
// accepted as an alternate authorization credential.
func (s *SubscriptionService) Get(ctx context.Context, owner, id string) (SubscriptionRecord, error) {
	if err := checkSubscriptionContext(ctx); err != nil {
		return SubscriptionRecord{}, err
	}
	owner, err := validateIdentity("owner", owner, true)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	id, err = validateIdentity("subscription id", id, true)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	snapshot, err := s.load(ctx, id)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if snapshot.record.Owner != owner {
		return SubscriptionRecord{}, ErrUnauthorized
	}
	return cloneSubscriptionRecord(snapshot.record), nil
}

// List returns all durable subscriptions owned by owner, including revoked and
// unregistered records. A valid owner with no records gets an empty list.
func (s *SubscriptionService) List(ctx context.Context, owner string) ([]SubscriptionRecord, error) {
	if err := checkSubscriptionContext(ctx); err != nil {
		return nil, err
	}
	owner, err := validateIdentity("owner", owner, true)
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, invalidSubscriptionInput("nil store")
	}
	items, err := s.store.List(beads.ListQuery{
		Label:         callbackSubscriptionLabel,
		Metadata:      map[string]string{subscriptionOwnerMetadata: owner},
		IncludeClosed: true,
		Sort:          beads.SortCreatedAsc,
	})
	if err != nil {
		return nil, fmt.Errorf("list callback subscriptions: %w", err)
	}
	out := make([]SubscriptionRecord, 0, len(items))
	for _, item := range items {
		if err := checkSubscriptionContext(ctx); err != nil {
			return nil, err
		}
		// The indexed owner is the attribution key, and it is re-checked here
		// because a backend may satisfy the metadata filter as a superset. A
		// row attributed to another owner is skipped without decoding, so one
		// corrupt foreign record cannot deny this owner's reads; a row
		// attributed to this owner still fails closed.
		if item.Metadata[subscriptionOwnerMetadata] != owner {
			continue
		}
		snapshot, err := decodeSubscriptionBead(item)
		if err != nil {
			return nil, err
		}
		if snapshot.record.Owner != owner {
			return nil, fmt.Errorf("%w: indexed owner does not match record owner", ErrCorruptRecord)
		}
		out = append(out, cloneSubscriptionRecord(snapshot.record))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RegisteredAt.Equal(out[j].RegisteredAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RegisteredAt.Before(out[j].RegisteredAt)
	})
	return out, nil
}

// Renew advances the registration generation without changing its payload.
func (s *SubscriptionService) Renew(ctx context.Context, owner, id string, fence RegistrationFence, now time.Time) (SubscriptionRecord, error) {
	return s.mutate(ctx, owner, id, fence, func(current SubscriptionRecord) (SubscriptionRecord, error) {
		if current.State != SubscriptionActive {
			return SubscriptionRecord{}, invalidSubscriptionState(current.State)
		}
		return advanceSubscriptionGeneration(current, now)
	})
}

// Update changes mutable subscription fields and advances its generation.
func (s *SubscriptionService) Update(ctx context.Context, owner, id string, fence RegistrationFence, patch SubscriptionPatch, now time.Time) (SubscriptionRecord, error) {
	return s.mutate(ctx, owner, id, fence, func(current SubscriptionRecord) (SubscriptionRecord, error) {
		if current.State != SubscriptionActive {
			return SubscriptionRecord{}, invalidSubscriptionState(current.State)
		}
		if patch.Scope == nil && patch.RouteIdentity == nil && patch.Interests == nil {
			return SubscriptionRecord{}, invalidSubscriptionInput("empty update")
		}
		next := cloneSubscriptionRecord(current)
		if patch.Scope != nil {
			scope, err := normalizeWorkScope(*patch.Scope)
			if err != nil {
				return SubscriptionRecord{}, err
			}
			next.Scope = scope
		}
		if patch.RouteIdentity != nil {
			route, err := validateIdentity("route identity", *patch.RouteIdentity, true)
			if err != nil {
				return SubscriptionRecord{}, err
			}
			next.RouteIdentity = route
		}
		if patch.Interests != nil {
			interests, err := normalizeInterests(patch.Interests)
			if err != nil {
				return SubscriptionRecord{}, err
			}
			next.Interests = interests
		}
		return advanceSubscriptionGeneration(next, now)
	})
}

// Revoke records an explicit durable owner revocation. Replaying the same
// owner, fence, and reason is idempotent; a different fence or reason fails.
func (s *SubscriptionService) Revoke(ctx context.Context, owner, id string, fence RegistrationFence, reason string, now time.Time) (SubscriptionRecord, error) {
	return s.mutate(ctx, owner, id, fence, func(current SubscriptionRecord) (SubscriptionRecord, error) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return SubscriptionRecord{}, invalidSubscriptionInput("revocation reason required")
		}
		if current.State == SubscriptionRevoked {
			if current.RevocationReason != reason {
				return SubscriptionRecord{}, invalidSubscriptionState(current.State)
			}
			return current, nil
		}
		if current.State != SubscriptionActive {
			return SubscriptionRecord{}, invalidSubscriptionState(current.State)
		}
		revokedAt := normalizeSubscriptionTime(now)
		next := cloneSubscriptionRecord(current)
		next.State = SubscriptionRevoked
		next.UpdatedAt = revokedAt
		next.RevokedAt = &revokedAt
		next.RevocationReason = reason
		return next, nil
	})
}

// Unregister records explicit removal and closes the backing bead. The durable
// record remains owner-listable for audit and exact terminal replay.
func (s *SubscriptionService) Unregister(ctx context.Context, owner, id string, fence RegistrationFence, reason string, now time.Time) (SubscriptionRecord, error) {
	return s.mutate(ctx, owner, id, fence, func(current SubscriptionRecord) (SubscriptionRecord, error) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return SubscriptionRecord{}, invalidSubscriptionInput("unregistration reason required")
		}
		if current.State == SubscriptionUnregistered {
			if current.UnregistrationReason != reason {
				return SubscriptionRecord{}, invalidSubscriptionState(current.State)
			}
			return current, nil
		}
		if current.State != SubscriptionActive {
			return SubscriptionRecord{}, invalidSubscriptionState(current.State)
		}
		unregisteredAt := normalizeSubscriptionTime(now)
		next := cloneSubscriptionRecord(current)
		next.State = SubscriptionUnregistered
		next.UpdatedAt = unregisteredAt
		next.UnregisteredAt = &unregisteredAt
		next.UnregistrationReason = reason
		return next, nil
	})
}

type subscriptionSnapshot struct {
	record SubscriptionRecord
	bead   beads.Bead
}

func (s *SubscriptionService) mutate(ctx context.Context, owner, id string, fence RegistrationFence, fn func(SubscriptionRecord) (SubscriptionRecord, error)) (SubscriptionRecord, error) {
	if err := checkSubscriptionContext(ctx); err != nil {
		return SubscriptionRecord{}, err
	}
	owner, err := validateIdentity("owner", owner, true)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	id, err = validateIdentity("subscription id", id, true)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if err := validateFence(fence); err != nil {
		return SubscriptionRecord{}, err
	}
	if fn == nil {
		return SubscriptionRecord{}, invalidSubscriptionInput("nil mutation")
	}
	snapshot, err := s.load(ctx, id)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if snapshot.record.Owner != owner {
		return SubscriptionRecord{}, ErrUnauthorized
	}
	if snapshot.record.RegistrationID != fence.RegistrationID || snapshot.record.Generation != fence.Generation {
		return SubscriptionRecord{}, ErrStaleFence
	}
	next, err := fn(snapshot.record)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if next.ID == "" {
		next.ID = snapshot.record.ID
	}
	if reflect.DeepEqual(next, snapshot.record) {
		return cloneSubscriptionRecord(next), nil
	}
	if err := validateStoredSubscription(next); err != nil {
		return SubscriptionRecord{}, err
	}
	payload, err := marshalSubscriptionRecord(next)
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if err := s.writeFenced(snapshot, next, payload); err != nil {
		return SubscriptionRecord{}, err
	}
	return cloneSubscriptionRecord(next), nil
}

func (s *SubscriptionService) load(ctx context.Context, id string) (subscriptionSnapshot, error) {
	if s == nil || s.store == nil {
		return subscriptionSnapshot{}, invalidSubscriptionInput("nil store")
	}
	if err := checkSubscriptionContext(ctx); err != nil {
		return subscriptionSnapshot{}, err
	}
	bead, err := s.store.Get(id)
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return subscriptionSnapshot{}, ErrNotFound
		}
		return subscriptionSnapshot{}, fmt.Errorf("get callback subscription %s: %w", id, err)
	}
	if !hasCallbackSubscriptionLabel(bead) {
		return subscriptionSnapshot{}, ErrNotFound
	}
	return decodeSubscriptionBead(bead)
}

func (s *SubscriptionService) writeFenced(snapshot subscriptionSnapshot, next SubscriptionRecord, payload string) error {
	writer, ok := beads.ConditionalWriterFor(s.store)
	if !ok {
		return fmt.Errorf("%w: %w", ErrFencingUnavailable, beads.ErrConditionalWriteUnsupported)
	}
	status := "open"
	if next.State == SubscriptionUnregistered {
		status = "closed"
	}
	if err := writer.UpdateIfMatch(snapshot.bead.ID, snapshot.bead.Revision, beads.UpdateOpts{
		Status:   &status,
		Metadata: subscriptionMetadata(next, payload),
	}); err != nil {
		if errors.Is(err, beads.ErrConditionalWriteUnsupported) {
			return fmt.Errorf("%w: %w", ErrFencingUnavailable, err)
		}
		if beads.IsPreconditionFailed(err) {
			return fmt.Errorf("%w: subscription %s changed concurrently", ErrStaleFence, snapshot.record.ID)
		}
		return fmt.Errorf("update callback subscription %s: %w", snapshot.record.ID, err)
	}
	return nil
}

func decodeSubscriptionBead(bead beads.Bead) (subscriptionSnapshot, error) {
	if !hasCallbackSubscriptionLabel(bead) {
		return subscriptionSnapshot{}, ErrNotFound
	}
	payload := strings.TrimSpace(bead.Metadata[subscriptionRecordMetadata])
	if payload == "" {
		return subscriptionSnapshot{}, fmt.Errorf("%w: missing record metadata", ErrCorruptRecord)
	}
	var record SubscriptionRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return subscriptionSnapshot{}, fmt.Errorf("%w: decode record metadata: %w", ErrCorruptRecord, err)
	}
	if record.ID != "" && record.ID != bead.ID {
		return subscriptionSnapshot{}, fmt.Errorf("%w: record id mismatch", ErrCorruptRecord)
	}
	record.ID = bead.ID
	if err := validateStoredSubscription(record); err != nil {
		return subscriptionSnapshot{}, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	return subscriptionSnapshot{record: record, bead: bead}, nil
}

// subscriptionMetadata projects the durable record into bead metadata. payload
// must be the canonical encoding of record; Create passes an encoding without a
// bead ID because the store mints it, and decodeSubscriptionBead restores the
// ID from the enclosing bead identity.
func subscriptionMetadata(record SubscriptionRecord, payload string) map[string]string {
	return map[string]string{
		subscriptionRecordMetadata: payload,
		subscriptionOwnerMetadata:  record.Owner,
		subscriptionRegMetadata:    record.RegistrationID,
		subscriptionGenMetadata:    strconv.FormatUint(record.Generation, 10),
		subscriptionStateMetadata:  string(record.State),
	}
}

func marshalSubscriptionRecord(record SubscriptionRecord) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode callback subscription: %w", err)
	}
	return string(encoded), nil
}

func normalizeCreateInput(input CreateSubscriptionInput) (CreateSubscriptionInput, error) {
	owner, err := validateIdentity("owner", input.Owner, true)
	if err != nil {
		return CreateSubscriptionInput{}, err
	}
	registration, err := validateIdentity("registration id", input.RegistrationID, true)
	if err != nil {
		return CreateSubscriptionInput{}, err
	}
	if input.Generation == 0 {
		return CreateSubscriptionInput{}, invalidSubscriptionInput("generation must be positive")
	}
	scope, err := normalizeWorkScope(input.Scope)
	if err != nil {
		return CreateSubscriptionInput{}, err
	}
	route, err := validateIdentity("route identity", input.RouteIdentity, true)
	if err != nil {
		return CreateSubscriptionInput{}, err
	}
	interests, err := normalizeInterests(input.Interests)
	if err != nil {
		return CreateSubscriptionInput{}, err
	}
	return CreateSubscriptionInput{
		Owner:          owner,
		RegistrationID: registration,
		Generation:     input.Generation,
		Scope:          scope,
		RouteIdentity:  route,
		Interests:      interests,
		Now:            normalizeSubscriptionTime(input.Now),
	}, nil
}

func normalizeWorkScope(scope WorkScope) (WorkScope, error) {
	city, err := validateIdentity("scope city", scope.City, false)
	if err != nil {
		return WorkScope{}, err
	}
	convoyID, err := validateIdentity("scope convoy", scope.ConvoyID, false)
	if err != nil {
		return WorkScope{}, err
	}
	if city == "" && convoyID == "" && len(scope.WorkRefs) == 0 {
		return WorkScope{}, invalidSubscriptionInput("authorized work scope required")
	}
	refs := make([]string, len(scope.WorkRefs))
	seen := make(map[string]struct{}, len(scope.WorkRefs))
	for i, ref := range scope.WorkRefs {
		refs[i], err = validateIdentity("scope work reference", ref, true)
		if err != nil {
			return WorkScope{}, err
		}
		if _, duplicate := seen[refs[i]]; duplicate {
			return WorkScope{}, invalidSubscriptionInput("duplicate scope work reference")
		}
		seen[refs[i]] = struct{}{}
	}
	return WorkScope{City: city, ConvoyID: convoyID, WorkRefs: refs}, nil
}

func normalizeInterests(interests []LifecycleInterest) ([]LifecycleInterest, error) {
	if len(interests) == 0 {
		return nil, invalidSubscriptionInput("lifecycle interest required")
	}
	out := make([]LifecycleInterest, len(interests))
	seen := make(map[LifecycleInterest]struct{}, len(interests))
	for i, interest := range interests {
		value, err := validateIdentity("lifecycle interest", string(interest), true)
		if err != nil {
			return nil, err
		}
		out[i] = LifecycleInterest(value)
		if _, duplicate := seen[out[i]]; duplicate {
			return nil, invalidSubscriptionInput("duplicate lifecycle interest")
		}
		seen[out[i]] = struct{}{}
	}
	return out, nil
}

func validateStoredSubscription(record SubscriptionRecord) error {
	if record.SchemaVersion != subscriptionSchemaVersion {
		return fmt.Errorf("schema version %d is unsupported", record.SchemaVersion)
	}
	if _, err := validateIdentity("owner", record.Owner, true); err != nil {
		return err
	}
	if _, err := validateIdentity("registration id", record.RegistrationID, true); err != nil {
		return err
	}
	if record.Generation == 0 {
		return invalidSubscriptionInput("generation must be positive")
	}
	if _, err := normalizeWorkScope(record.Scope); err != nil {
		return err
	}
	if _, err := validateIdentity("route identity", record.RouteIdentity, true); err != nil {
		return err
	}
	if _, err := normalizeInterests(record.Interests); err != nil {
		return err
	}
	switch record.State {
	case SubscriptionActive:
		if record.RevokedAt != nil || record.UnregisteredAt != nil || record.RevocationReason != "" || record.UnregistrationReason != "" {
			return invalidSubscriptionInput("active record has terminal fields")
		}
	case SubscriptionRevoked:
		if record.RevokedAt == nil || strings.TrimSpace(record.RevocationReason) == "" || record.UnregisteredAt != nil {
			return invalidSubscriptionInput("revoked record lacks revocation fields")
		}
	case SubscriptionUnregistered:
		if record.UnregisteredAt == nil || strings.TrimSpace(record.UnregistrationReason) == "" || record.RevokedAt != nil {
			return invalidSubscriptionInput("unregistered record lacks unregistration fields")
		}
	default:
		return invalidSubscriptionInput("unknown subscription state")
	}
	if record.RegisteredAt.IsZero() || record.UpdatedAt.IsZero() {
		return invalidSubscriptionInput("subscription timestamps required")
	}
	return nil
}

func validateIdentity(kind, value string, required bool) (string, error) {
	if strings.TrimSpace(value) == "" {
		if required {
			return "", invalidSubscriptionInput(kind + " required")
		}
		return "", nil
	}
	if strings.Contains(value, ",") {
		return "", invalidSubscriptionInput(kind + " must identify exactly one value")
	}
	return value, nil
}

func validateFence(fence RegistrationFence) error {
	if strings.TrimSpace(fence.RegistrationID) == "" || fence.Generation == 0 {
		return ErrFenceRequired
	}
	if _, err := validateIdentity("registration id", fence.RegistrationID, true); err != nil {
		return err
	}
	return nil
}

func advanceSubscriptionGeneration(record SubscriptionRecord, now time.Time) (SubscriptionRecord, error) {
	record = cloneSubscriptionRecord(record)
	if record.Generation == ^uint64(0) {
		return SubscriptionRecord{}, invalidSubscriptionInput("generation exhausted")
	}
	record.Generation++
	record.UpdatedAt = normalizeSubscriptionTime(now)
	return record, nil
}

func cloneSubscriptionRecord(record SubscriptionRecord) SubscriptionRecord {
	record.Scope.WorkRefs = append([]string(nil), record.Scope.WorkRefs...)
	record.Interests = append([]LifecycleInterest(nil), record.Interests...)
	if record.RevokedAt != nil {
		value := *record.RevokedAt
		record.RevokedAt = &value
	}
	if record.UnregisteredAt != nil {
		value := *record.UnregisteredAt
		record.UnregisteredAt = &value
	}
	return record
}

func normalizeSubscriptionTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func checkSubscriptionContext(ctx context.Context) error {
	if ctx == nil {
		return invalidSubscriptionInput("nil context")
	}
	return ctx.Err()
}

func invalidSubscriptionInput(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, message)
}

func invalidSubscriptionState(state SubscriptionState) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, state)
}

func hasCallbackSubscriptionLabel(bead beads.Bead) bool {
	for _, label := range bead.Labels {
		if label == callbackSubscriptionLabel {
			return true
		}
	}
	return false
}
