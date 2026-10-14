package externalcoordination

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/extmsg"
)

const (
	responseHandoffLabel          = "gc:external-coordination-response-handoff"
	metadataHandoffRequest        = "external_coordination.handoff.request"
	metadataHandoffResponse       = "external_coordination.handoff.response"
	metadataHandoffKey            = "external_coordination.handoff.key"
	metadataHandoffState          = "external_coordination.handoff.state"
	metadataHandoffAttempt        = "external_coordination.handoff.attempt"
	metadataHandoffClaimedBy      = "external_coordination.handoff.claimed_by"
	metadataHandoffClaimedAt      = "external_coordination.handoff.claimed_at"
	metadataHandoffClaimExpiresAt = "external_coordination.handoff.claim_expires_at"
	metadataHandoffRetryAt        = "external_coordination.handoff.retry_at"
	metadataHandoffError          = "external_coordination.handoff.error"
	metadataHandoffMessageID      = "external_coordination.handoff.message_id"
	metadataHandoffProviderState  = "external_coordination.handoff.provider_state"
	metadataHandoffCommitment     = "external_coordination.handoff.response_commitment"
	metadataHandoffScrubPending   = "external_coordination.handoff.response_scrub_pending"
	metadataHandoffReleasedAt     = "external_coordination.handoff.content_released_at"
)

const (
	responseHandoffClaimLease  = 5 * time.Minute
	responseHandoffMaxAttempts = 3
	responseHandoffRetryBase   = time.Second
	responseHandoffRetryMax    = time.Minute
)

var (
	// ErrHandoffNotRequested indicates that a request has no explicit origin destination.
	ErrHandoffNotRequested = errors.New("external coordination response origin handoff not requested")
	// ErrHandoffConflict indicates that a replay changed the response for one handoff identity.
	ErrHandoffConflict = errors.New("external coordination response origin handoff conflicts with existing record")
	// ErrHandoffNotQueued indicates that a handoff cannot be claimed in its current state.
	ErrHandoffNotQueued = errors.New("external coordination response origin handoff is not queued")
	// ErrHandoffUncertain indicates that a provider result must be reconciled before retry.
	ErrHandoffUncertain = errors.New("external coordination response origin handoff is uncertain")
	// ErrHandoffUnavailable indicates that no origin publication port is configured.
	ErrHandoffUnavailable = errors.New("external coordination response origin handoff port unavailable")
	// ErrHandoffRetryNotDue indicates that a transient provider failure is waiting for its retry time.
	ErrHandoffRetryNotDue = errors.New("external coordination response origin handoff retry is not due")
)

var routeIdentityKeyPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// HandoffState is the durable lifecycle of a response-to-origin publication.
// It deliberately does not use "completed": coordinator response recording,
// provider delivery, and origin acknowledgement are different facts.
type HandoffState string

const (
	// HandoffNotRequested indicates that no explicit origin destination was supplied.
	HandoffNotRequested HandoffState = "not_requested"
	// HandoffQueued indicates that a destination-qualified handoff is pending.
	HandoffQueued HandoffState = "queued"
	// HandoffClaimed indicates that a worker holds the handoff lease.
	HandoffClaimed HandoffState = "claimed"
	// HandoffAccepted indicates that the provider accepted or queued the publication.
	HandoffAccepted HandoffState = "accepted"
	// HandoffDelivered indicates that the provider reported delivery.
	HandoffDelivered HandoffState = "delivered"
	// HandoffUncertain indicates that the provider result requires reconciliation.
	HandoffUncertain HandoffState = "uncertain"
	// HandoffReconciled indicates that reconciliation confirmed the original identity.
	HandoffReconciled HandoffState = "reconciled"
	// HandoffFailed indicates that publication failed permanently or authorization failed.
	HandoffFailed HandoffState = "failed"
	// HandoffExpired indicates that no valid provider result arrived before expiry.
	HandoffExpired HandoffState = "expired"
)

// HandoffRequest is the redacted request identity required to recover a
// response handoff. It intentionally excludes the original prompt.
type HandoffRequest struct {
	RequestID         string            `json:"request_id"`
	Attempt           int               `json:"attempt"`
	SourceAgent       string            `json:"source_agent,omitempty"`
	Target            Target            `json:"target"`
	CorrelationID     string            `json:"correlation_id"`
	ResultDestination string            `json:"result_destination"`
	RouteIdentity     map[string]string `json:"route_identity,omitempty"`
	ContentRetention  ContentRetention  `json:"content_retention"`
	CreatedAt         time.Time         `json:"created_at,omitempty"`
	ExpiresAt         time.Time         `json:"expires_at"`
}

// OriginDestination is the typed result of resolving an opaque logical
// result_destination through the existing session/extmsg authorization seam.
// Generic coordination code never interprets result_destination as a URL or
// command.
type OriginDestination struct {
	SessionID    string
	Conversation extmsg.ConversationRef
	Caller       extmsg.Caller
}

// OriginDestinationResolver resolves and authorizes one explicit origin. It
// must reject stale, cross-owner, or cross-conversation destinations.
type OriginDestinationResolver func(context.Context, HandoffRequest, Response) (OriginDestination, error)

// OriginHandoffRequest is the input to the existing extmsg/session delivery
// boundary. IdempotencyKey is stable across retries and provider reconciliation.
type OriginHandoffRequest struct {
	Request        HandoffRequest
	Response       Response
	Destination    OriginDestination
	IdempotencyKey string
}

// OriginHandoffPort adapts the existing origin publication boundary. Publish
// performs one publication; Reconcile confirms an ambiguous publication
// without sending a new logical turn.
type OriginHandoffPort struct {
	Publish   func(context.Context, OriginHandoffRequest) (*extmsg.PublishReceipt, error)
	Reconcile func(context.Context, OriginHandoffRequest) (*extmsg.PublishReceipt, error)
}

// ResponseHandoffRecord is the durable response-to-origin projection.
type ResponseHandoffRecord struct {
	ID                   string
	Key                  string
	Request              HandoffRequest
	Response             Response
	State                HandoffState
	DeliveryAttempt      int
	ClaimedBy            string
	ClaimedAt            time.Time
	ClaimExpiresAt       time.Time
	RetryAt              time.Time
	ProviderMessageID    string
	ProviderState        string
	ResponseCommitment   string
	ResponseScrubPending bool
	Error                string

	revision int64
}

// ResponseHandoff persists and delivers authenticated coordinator responses to
// their explicit origins. It owns handoff state only; request admission and
// coordinator response recording remain in Service.
type ResponseHandoff struct {
	store   beads.Store
	resolve OriginDestinationResolver
	port    OriginHandoffPort
	claimMu sync.Mutex
}

// NewResponseHandoff creates a response-to-origin handoff over a durable bead
// store and the existing typed destination/publication boundaries.
func NewResponseHandoff(store beads.Store, resolve OriginDestinationResolver, port OriginHandoffPort) *ResponseHandoff {
	return &ResponseHandoff{store: store, resolve: resolve, port: port}
}

// Enqueue records one response handoff. Repeating the same identity and
// response is idempotent; changing the response or destination is rejected.
func (h *ResponseHandoff) Enqueue(ctx context.Context, request Request, response Response) (ResponseHandoffRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ResponseHandoffRecord{}, err
	}
	if h == nil || h.store == nil {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	handoffRequest, err := normalizeHandoffRequest(request)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	response, err = normalizeHandoffResponse(response, handoffRequest)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	return h.enqueueNormalized(ctx, handoffRequest, response)
}

// enqueueNormalized persists a response whose canonical form was established
// by the caller. It validates the identity fence but never rewrites the body,
// so the handoff and request-settlement commitments use identical bytes.
func (h *ResponseHandoff) enqueueNormalized(ctx context.Context, handoffRequest HandoffRequest, response Response) (ResponseHandoffRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ResponseHandoffRecord{}, err
	}
	if h == nil || h.store == nil {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	if err := validateHandoffResponse(response, handoffRequest); err != nil {
		return ResponseHandoffRecord{}, err
	}
	commitment, err := responseCommitment(response)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	key := responseHandoffKey(handoffRequest)
	identityKey := responseHandoffIdentityKey(handoffRequest)
	requestPayload, err := json.Marshal(handoffRequest)
	if err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("encode response handoff request: %w", err)
	}
	responsePayload, err := json.Marshal(response)
	if err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("encode response handoff response: %w", err)
	}
	created, _, err := beads.CreateDeterministically(h.store, identityKey, beads.Bead{
		Title:  fmt.Sprintf("External coordination response handoff: %s", handoffRequest.RequestID),
		Type:   "task",
		Labels: []string{responseHandoffLabel},
		Metadata: map[string]string{
			metadataHandoffRequest:    string(requestPayload),
			metadataHandoffResponse:   string(responsePayload),
			metadataHandoffKey:        key,
			metadataHandoffState:      string(HandoffQueued),
			metadataHandoffAttempt:    "0",
			metadataHandoffCommitment: commitment,
		},
	})
	if err != nil {
		if errors.Is(err, beads.ErrDeterministicCreateConflict) {
			existing, findErr := h.findByIdentity(handoffRequest)
			if findErr != nil {
				return ResponseHandoffRecord{}, findErr
			}
			if existing.Key != key || existing.ResponseCommitment != commitment {
				return ResponseHandoffRecord{}, fmt.Errorf("%w: key %q", ErrHandoffConflict, key)
			}
			return h.Get(ctx, existing.ID)
		}
		if errors.Is(err, beads.ErrDeterministicCreateUnsupported) {
			return ResponseHandoffRecord{}, fmt.Errorf("%w: durable idempotent handoff creation unavailable", ErrHandoffUnavailable)
		}
		return ResponseHandoffRecord{}, fmt.Errorf("create response handoff: %w", err)
	}
	return decodeResponseHandoff(created)
}

func (h *ResponseHandoff) findByIdentity(request HandoffRequest) (ResponseHandoffRecord, error) {
	items, err := h.store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("list response handoffs: %w", err)
	}
	for _, item := range items {
		record, decodeErr := decodeResponseHandoff(item)
		if decodeErr != nil {
			return ResponseHandoffRecord{}, decodeErr
		}
		if record.Request.RequestID == request.RequestID &&
			record.Request.Attempt == request.Attempt &&
			record.Request.CorrelationID == request.CorrelationID {
			return record, nil
		}
	}
	return ResponseHandoffRecord{}, fmt.Errorf("%w: response identity disappeared after deterministic conflict", ErrHandoffUnavailable)
}

// Get loads one response handoff by durable bead ID.
func (h *ResponseHandoff) Get(ctx context.Context, id string) (ResponseHandoffRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ResponseHandoffRecord{}, err
	}
	if h == nil || h.store == nil {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	item, err := h.store.Get(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return ResponseHandoffRecord{}, ErrNotFound
		}
		return ResponseHandoffRecord{}, err
	}
	if !hasResponseHandoffLabel(item) {
		return ResponseHandoffRecord{}, ErrNotFound
	}
	record, err := decodeResponseHandoff(item)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	if record.ResponseScrubPending {
		if err := h.scrubContent(record, time.Now().UTC()); err != nil {
			return ResponseHandoffRecord{}, err
		}
		record.Response.Summary = ""
		record.ResponseScrubPending = false
	}
	return record, nil
}

// Claim acquires one queued handoff. Claims are conditional and expire into
// uncertain rather than becoming silently retryable after a crash.
func (h *ResponseHandoff) Claim(ctx context.Context, id, worker string, now time.Time) (ResponseHandoffRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ResponseHandoffRecord{}, err
	}
	if strings.TrimSpace(worker) == "" {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: worker required", ErrInvalidInput)
	}
	if h == nil || h.store == nil {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	now = zeroTime(now)
	h.claimMu.Lock()
	defer h.claimMu.Unlock()
	record, err := h.Get(ctx, id)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	if record.State == HandoffClaimed {
		if record.ClaimExpiresAt.After(now) {
			return ResponseHandoffRecord{}, fmt.Errorf("%w: %s", ErrHandoffNotQueued, id)
		}
		if err := h.markUncertain(record, "claim lease expired"); err != nil {
			return ResponseHandoffRecord{}, err
		}
		return ResponseHandoffRecord{}, fmt.Errorf("%w: %s", ErrHandoffUncertain, id)
	}
	if record.State == HandoffUncertain {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: %s", ErrHandoffUncertain, id)
	}
	if record.State != HandoffQueued {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: %s is %s", ErrHandoffNotQueued, id, record.State)
	}
	if !record.RetryAt.IsZero() && record.RetryAt.After(now) {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: retry at %s", ErrHandoffRetryNotDue, record.RetryAt.UTC().Format(time.RFC3339Nano))
	}
	if !record.Request.ExpiresAt.After(now) && !isExpiredResponse(record.Response) {
		if err := h.transition(record, HandoffExpired, now, "response handoff expired", nil); err != nil {
			return ResponseHandoffRecord{}, err
		}
		return ResponseHandoffRecord{}, fmt.Errorf("%w: %s", ErrHandoffNotQueued, id)
	}
	record.State = HandoffClaimed
	record.DeliveryAttempt++
	record.ClaimedBy = strings.TrimSpace(worker)
	record.ClaimedAt = now
	record.ClaimExpiresAt = now.Add(responseHandoffClaimLease)
	record.RetryAt = time.Time{}
	if err := h.update(record, "open", map[string]string{
		metadataHandoffState:          string(record.State),
		metadataHandoffAttempt:        fmt.Sprintf("%d", record.DeliveryAttempt),
		metadataHandoffClaimedBy:      record.ClaimedBy,
		metadataHandoffClaimedAt:      record.ClaimedAt.UTC().Format(time.RFC3339Nano),
		metadataHandoffClaimExpiresAt: record.ClaimExpiresAt.UTC().Format(time.RFC3339Nano),
		metadataHandoffRetryAt:        "",
		metadataHandoffError:          "",
	}); err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("claim response handoff %s: %w", id, err)
	}
	claimed, err := h.Get(ctx, id)
	if err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("read claimed response handoff %s: %w", id, err)
	}
	return claimed, nil
}

// Deliver resolves the explicit destination and publishes one response. A
// provider error is uncertain by default: recovery must reconcile rather than
// blindly send a second logical turn.
func (h *ResponseHandoff) Deliver(ctx context.Context, id, worker string, now time.Time) (ResponseHandoffRecord, error) {
	record, err := h.Get(ctx, id)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	if record.State == HandoffDelivered || record.State == HandoffAccepted || record.State == HandoffReconciled {
		return record, nil
	}
	if record.State == HandoffUncertain {
		return record, fmt.Errorf("%w: %s", ErrHandoffUncertain, id)
	}
	claimed, err := h.Claim(ctx, id, worker, now)
	if err != nil {
		return record, err
	}
	if h.resolve == nil {
		return h.failClaimed(ctx, claimed, now, ErrHandoffUnavailable)
	}
	destination, err := h.resolve(ctx, claimed.Request, claimed.Response)
	if err != nil {
		return h.failClaimed(ctx, claimed, now, errors.New("origin destination unauthorized or unavailable"))
	}
	if h.port.Publish == nil {
		return h.failClaimed(ctx, claimed, now, ErrHandoffUnavailable)
	}
	input := OriginHandoffRequest{
		Request:        claimed.Request,
		Response:       claimed.Response,
		Destination:    destination,
		IdempotencyKey: claimed.Key,
	}
	receipt, publishErr := h.port.Publish(ctx, input)
	if publishErr != nil {
		if err := h.markUncertain(claimed, "provider result is ambiguous"); err != nil {
			return claimed, err
		}
		uncertain, err := h.Get(ctx, claimed.ID)
		if err != nil {
			return claimed, err
		}
		return uncertain, ErrHandoffUncertain
	}
	return h.applyReceipt(ctx, claimed, receipt, now, false)
}

// Reconcile resolves an uncertain publication with the same identity. It never
// calls Publish and therefore cannot emit a duplicate logical turn.
func (h *ResponseHandoff) Reconcile(ctx context.Context, id, worker string, now time.Time) (ResponseHandoffRecord, error) {
	record, err := h.Get(ctx, id)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	if record.State != HandoffUncertain {
		if record.State == HandoffDelivered || record.State == HandoffAccepted || record.State == HandoffReconciled {
			return record, nil
		}
		return record, fmt.Errorf("%w: %s is %s", ErrHandoffUncertain, id, record.State)
	}
	if h.resolve == nil || h.port.Reconcile == nil {
		return record, fmt.Errorf("%w: reconciliation port unavailable", ErrHandoffUncertain)
	}
	now = zeroTime(now)
	claimed, err := h.claimUncertain(ctx, record, worker, now)
	if err != nil {
		return record, err
	}
	destination, err := h.resolve(ctx, claimed.Request, claimed.Response)
	if err != nil {
		_ = h.markUncertain(claimed, "origin destination could not be reconciled")
		return claimed, ErrHandoffUncertain
	}
	receipt, reconcileErr := h.port.Reconcile(ctx, OriginHandoffRequest{
		Request:        claimed.Request,
		Response:       claimed.Response,
		Destination:    destination,
		IdempotencyKey: claimed.Key,
	})
	if reconcileErr != nil {
		_ = h.markUncertain(claimed, "origin reconciliation failed")
		return claimed, ErrHandoffUncertain
	}
	return h.applyReceipt(ctx, claimed, receipt, now, true)
}

// Recover fences abandoned claims. It returns uncertain records for an owner
// to reconcile; it never republishes them.
func (h *ResponseHandoff) Recover(ctx context.Context, now time.Time) ([]ResponseHandoffRecord, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	now = zeroTime(now)
	items, err := h.store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		return nil, fmt.Errorf("list response handoffs for recovery: %w", err)
	}
	var recovered []ResponseHandoffRecord
	for _, item := range items {
		record, err := decodeResponseHandoff(item)
		if err != nil {
			return nil, err
		}
		switch {
		case record.State == HandoffClaimed && !record.ClaimExpiresAt.After(now):
			if err := h.markUncertain(record, "claim lease expired"); err != nil {
				return recovered, err
			}
			updated, err := h.Get(ctx, record.ID)
			if err != nil {
				return recovered, err
			}
			recovered = append(recovered, updated)
		case record.State == HandoffQueued && !record.Request.ExpiresAt.After(now) && !isExpiredResponse(record.Response):
			if err := h.transition(record, HandoffExpired, now, "response handoff expired", nil); err != nil {
				return recovered, err
			}
		}
	}
	return recovered, nil
}

func (h *ResponseHandoff) claimUncertain(ctx context.Context, record ResponseHandoffRecord, worker string, now time.Time) (ResponseHandoffRecord, error) {
	if strings.TrimSpace(worker) == "" {
		return ResponseHandoffRecord{}, fmt.Errorf("%w: worker required", ErrInvalidInput)
	}
	record.State = HandoffClaimed
	record.DeliveryAttempt++
	record.ClaimedBy = strings.TrimSpace(worker)
	record.ClaimedAt = now
	record.ClaimExpiresAt = now.Add(responseHandoffClaimLease)
	if err := h.update(record, "open", map[string]string{
		metadataHandoffState:          string(record.State),
		metadataHandoffAttempt:        fmt.Sprintf("%d", record.DeliveryAttempt),
		metadataHandoffClaimedBy:      record.ClaimedBy,
		metadataHandoffClaimedAt:      record.ClaimedAt.UTC().Format(time.RFC3339Nano),
		metadataHandoffClaimExpiresAt: record.ClaimExpiresAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("claim uncertain response handoff %s: %w", record.ID, err)
	}
	claimed, err := h.Get(ctx, record.ID)
	if err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("read reconciled response handoff %s: %w", record.ID, err)
	}
	return claimed, nil
}

func (h *ResponseHandoff) applyReceipt(ctx context.Context, record ResponseHandoffRecord, receipt *extmsg.PublishReceipt, now time.Time, reconciled bool) (ResponseHandoffRecord, error) {
	if receipt == nil {
		if err := h.markUncertain(record, "provider returned no receipt"); err != nil {
			return record, err
		}
		uncertain, err := h.Get(ctx, record.ID)
		if err != nil {
			return record, err
		}
		return uncertain, ErrHandoffUncertain
	}
	if receipt.Delivered {
		state := HandoffDelivered
		if reconciled {
			state = HandoffReconciled
		}
		if err := h.transition(record, state, zeroTime(now), "", receipt); err != nil {
			return record, err
		}
		updated, err := h.Get(ctx, record.ID)
		return updated, err
	}
	if receipt.Accepted || receipt.Queued {
		state := HandoffAccepted
		if reconciled {
			state = HandoffReconciled
		}
		if err := h.transition(record, state, zeroTime(now), "", receipt); err != nil {
			return record, err
		}
		updated, err := h.Get(ctx, record.ID)
		return updated, err
	}
	message := string(receipt.FailureKind)
	if message == "" {
		message = "origin provider rejected response publication"
	}
	message = sanitizeHandoffError(message)
	if receipt.FailureKind == extmsg.PublishFailureTransient || receipt.FailureKind == extmsg.PublishFailureRateLimited {
		if record.DeliveryAttempt < responseHandoffMaxAttempts && record.Request.ExpiresAt.After(zeroTime(now)) {
			delay := responseHandoffRetryDelay(record.DeliveryAttempt)
			record.State = HandoffQueued
			record.ClaimedBy = ""
			record.ClaimedAt = time.Time{}
			record.ClaimExpiresAt = time.Time{}
			record.RetryAt = zeroTime(now).Add(delay)
			if err := h.update(record, "open", map[string]string{
				metadataHandoffState:          string(record.State),
				metadataHandoffClaimedBy:      "",
				metadataHandoffClaimedAt:      "",
				metadataHandoffClaimExpiresAt: "",
				metadataHandoffRetryAt:        record.RetryAt.UTC().Format(time.RFC3339Nano),
				metadataHandoffError:          message,
			}); err != nil {
				return record, err
			}
			return record, fmt.Errorf("%w: retry at %s", ErrHandoffRetryNotDue, record.RetryAt.UTC().Format(time.RFC3339Nano))
		}
	}
	return h.failClaimed(ctx, record, now, errors.New(message))
}

func (h *ResponseHandoff) failClaimed(ctx context.Context, record ResponseHandoffRecord, now time.Time, cause error) (ResponseHandoffRecord, error) {
	message := "origin response publication failed"
	if cause != nil {
		message = sanitizeHandoffError(cause.Error())
	}
	if err := h.transition(record, HandoffFailed, zeroTime(now), message, nil); err != nil {
		return record, err
	}
	updated, err := h.Get(ctx, record.ID)
	if err != nil {
		return record, fmt.Errorf("read failed response handoff %s: %w", record.ID, err)
	}
	return updated, errors.New(message)
}

func (h *ResponseHandoff) markUncertain(record ResponseHandoffRecord, message string) error {
	record.State = HandoffUncertain
	record.ClaimedBy = ""
	record.ClaimedAt = time.Time{}
	record.ClaimExpiresAt = time.Time{}
	record.Error = sanitizeHandoffError(message)
	return h.update(record, "open", map[string]string{
		metadataHandoffState:          string(record.State),
		metadataHandoffClaimedBy:      "",
		metadataHandoffClaimedAt:      "",
		metadataHandoffClaimExpiresAt: "",
		metadataHandoffError:          record.Error,
	})
}

func (h *ResponseHandoff) transition(record ResponseHandoffRecord, state HandoffState, now time.Time, message string, receipt *extmsg.PublishReceipt) error {
	record.State = state
	record.ClaimedBy = ""
	record.ClaimedAt = time.Time{}
	record.ClaimExpiresAt = time.Time{}
	record.RetryAt = time.Time{}
	if message != "" {
		record.Error = sanitizeHandoffError(message)
	}
	metadata := map[string]string{
		metadataHandoffState:          string(state),
		metadataHandoffClaimedBy:      "",
		metadataHandoffClaimedAt:      "",
		metadataHandoffClaimExpiresAt: "",
		metadataHandoffRetryAt:        "",
		metadataHandoffError:          record.Error,
	}
	status := "open"
	if state == HandoffDelivered || state == HandoffAccepted || state == HandoffReconciled || state == HandoffFailed || state == HandoffExpired {
		status = "closed"
	}
	if receipt != nil {
		metadata[metadataHandoffMessageID] = receipt.MessageID
		metadata[metadataHandoffProviderState] = providerState(receipt)
	}
	mustScrub := (state == HandoffDelivered || state == HandoffAccepted || state == HandoffReconciled || state == HandoffFailed || state == HandoffExpired) && ephemeralResponse(record.Request, record.Response)
	if mustScrub {
		metadata[metadataHandoffScrubPending] = "true"
	}
	if err := h.update(record, status, metadata); err != nil {
		return err
	}
	if mustScrub {
		return h.scrubContent(record, now)
	}
	return nil
}

func (h *ResponseHandoff) scrubContent(record ResponseHandoffRecord, now time.Time) error {
	record.Response.Summary = ""
	payload, err := json.Marshal(record.Response)
	if err != nil {
		return fmt.Errorf("encode scrubbed response handoff %s: %w", record.ID, err)
	}
	return h.store.Update(record.ID, beads.UpdateOpts{Metadata: map[string]string{
		metadataHandoffResponse:     string(payload),
		metadataHandoffScrubPending: "",
		metadataHandoffReleasedAt:   zeroTime(now).UTC().Format(time.RFC3339Nano),
	}})
}

func (h *ResponseHandoff) update(record ResponseHandoffRecord, status string, metadata map[string]string) error {
	writer, ok := beads.ConditionalWriterFor(h.store)
	if !ok {
		return fmt.Errorf("%w: conditional handoff transition unavailable", ErrHandoffUnavailable)
	}
	if err := writer.UpdateIfMatch(record.ID, record.revision, beads.UpdateOpts{Status: &status, Metadata: metadata}); err != nil {
		return err
	}
	return nil
}

func normalizeHandoffRequest(request Request) (HandoffRequest, error) {
	requestID := strings.TrimSpace(request.RequestID)
	correlationID := strings.TrimSpace(request.CorrelationID)
	destination := strings.TrimSpace(request.ResultDestination)
	if destination == "" {
		return HandoffRequest{}, ErrHandoffNotRequested
	}
	if requestID == "" || request.Attempt <= 0 || correlationID == "" {
		return HandoffRequest{}, fmt.Errorf("%w: response handoff identity is incomplete", ErrInvalidInput)
	}
	retention := request.ContentRetention
	if retention == "" {
		retention = RetentionEphemeral
	}
	if retention != RetentionDurable && retention != RetentionEphemeral {
		return HandoffRequest{}, fmt.Errorf("%w: invalid content_retention %q", ErrInvalidInput, retention)
	}
	if err := validateRouteIdentity(request.RouteIdentity); err != nil {
		return HandoffRequest{}, err
	}
	return HandoffRequest{
		RequestID:         requestID,
		Attempt:           request.Attempt,
		SourceAgent:       strings.TrimSpace(request.SourceAgent),
		Target:            request.Target,
		CorrelationID:     correlationID,
		ResultDestination: destination,
		RouteIdentity:     cloneMap(request.RouteIdentity),
		ContentRetention:  retention,
		CreatedAt:         request.CreatedAt,
		ExpiresAt:         request.ExpiresAt,
	}, nil
}

func normalizeHandoffResponse(response Response, request HandoffRequest) (Response, error) {
	response.RequestID = strings.TrimSpace(response.RequestID)
	response.CorrelationID = strings.TrimSpace(response.CorrelationID)
	response.ResponseID = strings.TrimSpace(response.ResponseID)
	response.State = strings.ToLower(strings.TrimSpace(response.State))
	if response.State == "" {
		response.State = "answered"
	}
	if err := validateHandoffResponse(response, request); err != nil {
		return Response{}, err
	}
	if response.State == "failed" || response.State == "expired" {
		response.Summary = sanitizeOutcomeSummary(response.State, response.Summary)
	}
	if response.ReceivedAt.IsZero() {
		response.ReceivedAt = request.CreatedAt
		if response.ReceivedAt.IsZero() {
			response.ReceivedAt = time.Now().UTC()
		}
	}
	return response, nil
}

func validateHandoffResponse(response Response, request HandoffRequest) error {
	switch response.State {
	case "answered", "refused", "failed", "expired":
	default:
		return fmt.Errorf("%w: invalid response outcome %q", ErrInvalidInput, response.State)
	}
	if response.RequestID != request.RequestID || response.Attempt != request.Attempt || response.CorrelationID != request.CorrelationID {
		return fmt.Errorf("%w: response does not match handoff identity", ErrInvalidInput)
	}
	if response.ContentRetention != "" && response.ContentRetention != RetentionDurable && response.ContentRetention != RetentionEphemeral {
		return fmt.Errorf("%w: invalid response content_retention %q", ErrInvalidInput, response.ContentRetention)
	}
	return nil
}

func validateRouteIdentity(identity map[string]string) error {
	if len(identity) > 16 {
		return fmt.Errorf("%w: route_identity has too many entries", ErrInvalidInput)
	}
	for key, value := range identity {
		if !routeIdentityKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: invalid route_identity key %q", ErrInvalidInput, key)
		}
		lowerKey := strings.ToLower(key)
		for _, marker := range []string{"url", "token", "secret", "password", "credential", "authorization", "command", "shell"} {
			if strings.Contains(lowerKey, marker) {
				return fmt.Errorf("%w: route_identity key %q names forbidden transport or credential data", ErrInvalidInput, key)
			}
		}
		if len(value) > 256 {
			return fmt.Errorf("%w: route_identity value %q is too long", ErrInvalidInput, key)
		}
		for _, r := range value {
			if !unicode.IsPrint(r) {
				return fmt.Errorf("%w: route_identity value %q contains non-printable characters", ErrInvalidInput, key)
			}
		}
		lower := strings.ToLower(value)
		for _, marker := range []string{"http://", "https://", "bearer ", "authorization:", "curl ", "sh -c", "powershell", "token=", "secret=", "password=", "api_key=", "private_key=", "x-api-key"} {
			if strings.Contains(lower, marker) {
				return fmt.Errorf("%w: route_identity value %q contains forbidden transport or command data", ErrInvalidInput, key)
			}
		}
	}
	return nil
}

func responseHandoffKey(request HandoffRequest) string {
	hash := sha256.New()
	hash.Write([]byte("response-origin-handoff-v1"))
	for _, value := range []string{request.RequestID, fmt.Sprintf("%d", request.Attempt), request.CorrelationID, request.ResultDestination} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func responseHandoffIdentityKey(request HandoffRequest) string {
	hash := sha256.New()
	hash.Write([]byte("response-origin-handoff-identity-v1"))
	for _, value := range []string{request.RequestID, fmt.Sprintf("%d", request.Attempt), request.CorrelationID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func decodeResponseHandoff(item beads.Bead) (ResponseHandoffRecord, error) {
	var request HandoffRequest
	if err := json.Unmarshal([]byte(item.Metadata[metadataHandoffRequest]), &request); err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("decode response handoff request %s: %w", item.ID, err)
	}
	var response Response
	if err := json.Unmarshal([]byte(item.Metadata[metadataHandoffResponse]), &response); err != nil {
		return ResponseHandoffRecord{}, fmt.Errorf("decode response handoff response %s: %w", item.ID, err)
	}
	return ResponseHandoffRecord{
		ID:                   item.ID,
		Key:                  item.Metadata[metadataHandoffKey],
		Request:              request,
		Response:             response,
		State:                HandoffState(defaultString(item.Metadata[metadataHandoffState], string(HandoffQueued))),
		DeliveryAttempt:      parseInt(item.Metadata[metadataHandoffAttempt]),
		ClaimedBy:            item.Metadata[metadataHandoffClaimedBy],
		ClaimedAt:            parseTime(item.Metadata[metadataHandoffClaimedAt]),
		ClaimExpiresAt:       parseTime(item.Metadata[metadataHandoffClaimExpiresAt]),
		RetryAt:              parseTime(item.Metadata[metadataHandoffRetryAt]),
		ProviderMessageID:    item.Metadata[metadataHandoffMessageID],
		ProviderState:        item.Metadata[metadataHandoffProviderState],
		ResponseCommitment:   item.Metadata[metadataHandoffCommitment],
		ResponseScrubPending: item.Metadata[metadataHandoffScrubPending] == "true",
		Error:                item.Metadata[metadataHandoffError],
		revision:             item.Revision,
	}, nil
}

func hasResponseHandoffLabel(item beads.Bead) bool {
	for _, label := range item.Labels {
		if label == responseHandoffLabel {
			return true
		}
	}
	return false
}

func providerState(receipt *extmsg.PublishReceipt) string {
	switch {
	case receipt.Delivered:
		return string(HandoffDelivered)
	case receipt.Accepted || receipt.Queued:
		return string(HandoffAccepted)
	default:
		return string(receipt.FailureKind)
	}
}

func responseHandoffRetryDelay(attempt int) time.Duration {
	delay := responseHandoffRetryBase
	for i := 1; i < attempt; i++ {
		if delay >= responseHandoffRetryMax/2 {
			return responseHandoffRetryMax
		}
		delay *= 2
	}
	if delay > responseHandoffRetryMax {
		return responseHandoffRetryMax
	}
	return delay
}

func ephemeralResponse(request HandoffRequest, response Response) bool {
	return requestRetention(request) == RetentionEphemeral || response.ContentRetention == RetentionEphemeral
}

func requestRetention(request HandoffRequest) ContentRetention {
	return request.ContentRetention
}

func isExpiredResponse(response Response) bool {
	return strings.EqualFold(strings.TrimSpace(response.State), "expired")
}

func sanitizeHandoffError(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "origin response publication failed"
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"http://", "https://", "bearer ", "authorization:", "curl ", "sh -c", "powershell", "token=", "secret=", "password=", "api_key=", "private_key=", "x-api-key"} {
		if strings.Contains(lower, marker) {
			return "origin response publication failed"
		}
	}
	if len(value) > 128 {
		value = value[:128]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func sanitizeOutcomeSummary(state, value string) string {
	value = sanitizeHandoffError(value)
	if value == "origin response publication failed" && strings.TrimSpace(value) != "" {
		return "external coordination outcome: " + state
	}
	return value
}
