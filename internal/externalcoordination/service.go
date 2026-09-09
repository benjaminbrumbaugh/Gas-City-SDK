package externalcoordination

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/google/uuid"
)

const (
	metadataRequest              = "external_coordination.request"
	metadataState                = "external_coordination.state"
	metadataAttempt              = "external_coordination.attempt"
	metadataClaimedBy            = "external_coordination.claimed_by"
	metadataClaimedAt            = "external_coordination.claimed_at"
	metadataDelivered            = "external_coordination.delivered_at"
	metadataError                = "external_coordination.error"
	metadataResponseCommitment   = "external_coordination.response_commitment"
	metadataResponseScrubPending = "external_coordination.response_scrub_pending"
	metadataOutcome              = "external_coordination.outcome"
	metadataSubmittedAt          = "external_coordination.submitted_at"
	metadataUncertainAt          = "external_coordination.uncertain_at"
	metadataUncertaintyClass     = "external_coordination.uncertainty_class"
	metadataReconciledAt         = "external_coordination.reconciled_at"
	metadataRespondedAt          = "external_coordination.responded_at"
	metadataReceivedAt           = "external_coordination.received_at"
)

// maxUncertaintyClassLen bounds the sanitized failure class persisted with an
// uncertain submission. The class is a short diagnostic label, never a URL,
// credential, response body, or redirect location.
const maxUncertaintyClassLen = 200

var (
	// ErrInvalidInput indicates a malformed or unauthorized ExternalCoordination operation.
	ErrInvalidInput = errors.New("external coordination invalid input")
	// ErrNotFound indicates that a request record does not exist.
	ErrNotFound = errors.New("external coordination request not found")
	// ErrNotQueued indicates that a request is not available for claiming.
	ErrNotQueued = errors.New("external coordination request is not queued")
	// ErrExpired indicates that a request passed its expiry deadline.
	ErrExpired = errors.New("external coordination request expired")
	// ErrCancelled distinguishes an operator cancellation.
	ErrCancelled = errors.New("external coordination request cancelled") //nolint:misspell // public wire spelling
	// ErrUnavailable indicates that no adapter can currently deliver the request.
	ErrUnavailable = errors.New("external coordination adapter unavailable")
	// ErrPrematureCompletion indicates a transport falsely claimed execution completion.
	ErrPrematureCompletion = errors.New("external coordination adapter reported completion at delivery boundary")
	// ErrStaleTarget indicates the configured target changed after the request
	// was admitted. The existing record fails closed and is never redirected.
	ErrStaleTarget = errors.New("external coordination target fence is stale")
	// ErrUnreconciled indicates an operation was refused because an uncertain
	// submission has not been reconciled against the same target and key.
	ErrUnreconciled = errors.New("external coordination submission is uncertain and must be reconciled")
	// ErrDeliveryNotAttempted marks a definitive pre-submission failure: no
	// request bytes reached the target, so no duplicate delivery is possible
	// and the request may fail without reconciliation.
	ErrDeliveryNotAttempted = errors.New("external coordination delivery was not attempted")
	// ErrIllegalTransition indicates a delivery state change the policy forbids.
	ErrIllegalTransition = errors.New("external coordination delivery transition is not permitted")
)

// Enqueue persists one request. A repeated idempotency key returns the original
// record, including terminal records, without creating another request bead.
func (s *Service) Enqueue(ctx context.Context, input RequestInput) (RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return RequestRecord{}, err
	}
	if s == nil || s.store == nil {
		return RequestRecord{}, fmt.Errorf("%w: nil store", ErrInvalidInput)
	}
	request, err := normalizeRequest(input)
	if err != nil {
		return RequestRecord{}, err
	}

	// The store is the durable idempotency boundary. This lookup is intentionally
	// done before Create; callers may safely retry after a lost HTTP response.
	if request.IdempotencyKey != "" {
		items, listErr := s.store.List(beads.ListQuery{Label: requestLabel, IncludeClosed: true, AllowScan: true})
		if listErr != nil {
			return RequestRecord{}, fmt.Errorf("list external coordination requests for idempotency: %w", listErr)
		}
		for _, item := range items {
			if strings.TrimSpace(item.Metadata["external_coordination.idempotency_key"]) != request.IdempotencyKey {
				continue
			}
			return decodeRecord(item)
		}
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return RequestRecord{}, fmt.Errorf("encode external coordination request: %w", err)
	}
	created, err := s.store.Create(beads.Bead{
		Title:       fmt.Sprintf("External coordination request: %s", request.Reason),
		Type:        "message",
		Description: request.Prompt,
		From:        request.SourceAgent,
		Labels:      []string{requestLabel},
		Metadata: map[string]string{
			metadataRequest:                           string(payload),
			"external_coordination.idempotency_key":   request.IdempotencyKey,
			"external_coordination.content_retention": string(request.ContentRetention),
			metadataState:                             string(StateQueued),
			metadataAttempt:                           "0",
			"external_coordination.logical_role":      request.Target.LogicalRole,
			"external_coordination.target_id":         request.Target.TargetID,
			"external_coordination.config_revision":   fmt.Sprintf("%d", request.Target.ConfigRevision),
		},
	})
	if err != nil {
		return RequestRecord{}, fmt.Errorf("create external coordination request: %w", err)
	}
	return decodeRecord(created)
}

// Get loads one request and projects an expired queued request to expired.
func (s *Service) Get(ctx context.Context, id string) (RequestRecord, error) {
	return s.getAt(ctx, id, time.Time{})
}

// getAt loads one request and projects expiry against the supplied instant.
// A caller that already has an authoritative timestamp should use this helper
// so a stale wall clock cannot change a transition's meaning.
func (s *Service) getAt(ctx context.Context, id string, now time.Time) (RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return RequestRecord{}, err
	}
	item, err := s.store.Get(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return RequestRecord{}, ErrNotFound
		}
		return RequestRecord{}, err
	}
	if !hasRequestLabel(item) {
		return RequestRecord{}, ErrNotFound
	}
	record, err := decodeRecord(item)
	if err != nil {
		return RequestRecord{}, err
	}
	at := zeroTime(now)
	if record.State == StateQueued && !record.Request.ExpiresAt.After(at) {
		if err := s.setStateWithOutcomeForRecord(record, StateExpired, OutcomeExpired, at, "request expired"); err != nil {
			if !beads.IsPreconditionFailed(err) {
				return RequestRecord{}, fmt.Errorf("expire external coordination request %s: %w", id, err)
			}
			// A concurrent delivery won the revision fence. Re-read so callers
			// observe its state instead of returning a stale expiry projection.
			return s.getAt(ctx, id, at)
		}
		record.State = StateExpired
		record.outcome = OutcomeExpired
		record.Error = "request expired"
		record.DeliveredAt = at
	}
	return record, nil
}

// List returns request records. With no states it returns every request,
// including terminal records, so diagnostics do not mistake an empty queue for
// an unavailable coordinator.
func (s *Service) List(ctx context.Context, states ...DeliveryState) ([]RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	items, err := s.store.List(beads.ListQuery{Label: requestLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		return nil, fmt.Errorf("list external coordination requests: %w", err)
	}
	wanted := make(map[DeliveryState]bool, len(states))
	for _, state := range states {
		wanted[state] = true
	}
	out := make([]RequestRecord, 0, len(items))
	for _, item := range items {
		record, decodeErr := decodeRecord(item)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if len(wanted) > 0 && !wanted[record.State] {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

// Claim atomically marks a queued request running from this process's point of
// view. Stores that support conditional writes get a revision guard; the local
// mutex protects MemStore/FileStore and prevents two in-process dispatchers from
// claiming the same request.
func (s *Service) Claim(ctx context.Context, id, worker string, now time.Time) (RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return RequestRecord{}, err
	}
	if strings.TrimSpace(worker) == "" {
		return RequestRecord{}, fmt.Errorf("%w: worker required", ErrInvalidInput)
	}
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return RequestRecord{}, err
	}
	// getAt durably marks a queued request expired before returning its
	// projection. Preserve the distinct ErrExpired contract for callers that
	// need to distinguish expiry from a competing claim.
	if record.State == StateExpired && !record.Request.ExpiresAt.After(now) {
		return RequestRecord{}, ErrExpired
	}
	if record.State != StateQueued {
		return RequestRecord{}, fmt.Errorf("%w: %s is %s", ErrNotQueued, record.ID, record.State)
	}
	record.Attempt++
	record.Request.Attempt = record.Attempt
	record.State = StateRunning
	record.ClaimedBy = worker
	record.ClaimedAt = now
	payload, err := json.Marshal(record.Request)
	if err != nil {
		return RequestRecord{}, fmt.Errorf("encode claimed external coordination request %s: %w", record.ID, err)
	}
	writer, ok := beads.ConditionalWriterFor(s.store)
	if !ok {
		return RequestRecord{}, fmt.Errorf("%w: conditional claim transition unavailable", ErrUnavailable)
	}
	if err := writer.UpdateIfMatch(record.ID, record.revision, beads.UpdateOpts{
		Status: strPtr("in_progress"),
		Metadata: map[string]string{
			metadataRequest:   string(payload),
			metadataState:     string(StateRunning),
			metadataAttempt:   fmt.Sprintf("%d", record.Attempt),
			metadataClaimedBy: worker,
			metadataClaimedAt: now.UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return RequestRecord{}, fmt.Errorf("%w: %s was claimed concurrently", ErrNotQueued, record.ID)
		}
		return RequestRecord{}, fmt.Errorf("claim external coordination request %s: %w", record.ID, err)
	}
	return record, nil
}

// Complete records the adapter's trustworthy acceptance receipt and moves the
// request to submitted. Submitted is deliberately not terminal and carries no
// outcome: transport acceptance is not external coordinator execution.
func (s *Service) Complete(ctx context.Context, id string, receipt DeliveryReceipt, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: request id mismatch", ErrInvalidInput)
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if record.State != StateRunning {
		return fmt.Errorf("%w: %s is %s", ErrNotQueued, id, record.State)
	}
	if receipt.RequestID == "" || (receipt.RequestID != id && receipt.RequestID != record.Request.RequestID) {
		return fmt.Errorf("%w: receipt request id does not match %q", ErrInvalidInput, id)
	}
	if receipt.Attempt <= 0 || receipt.Attempt != record.Attempt {
		return fmt.Errorf("%w: receipt attempt does not match current claim", ErrInvalidInput)
	}
	if receipt.CorrelationID == "" || receipt.CorrelationID != record.Request.CorrelationID {
		return fmt.Errorf("%w: receipt correlation_id does not match request", ErrInvalidInput)
	}
	if !receipt.Accepted {
		return fmt.Errorf("%w: delivery receipt does not confirm target acceptance", ErrInvalidInput)
	}
	state := receipt.State
	if state == "" {
		state = StateQueued
	}
	if state == StateCompleted {
		return ErrPrematureCompletion
	}
	if state != StateQueued && state != StateRunning {
		return fmt.Errorf("%w: invalid delivery state %q", ErrInvalidInput, state)
	}
	if !CanTransition(record.State, StateSubmitted) {
		return fmt.Errorf("%w: %s cannot be submitted from %s", ErrIllegalTransition, id, record.State)
	}
	status := "in_progress"
	meta := map[string]string{
		metadataState:       string(StateSubmitted),
		metadataSubmittedAt: now.UTC().Format(time.RFC3339Nano),
		metadataDelivered:   now.UTC().Format(time.RFC3339Nano),
	}
	if receipt.Error != "" {
		meta[metadataError] = sanitizeDurableError(receipt.Error)
	}
	if err := s.updateRecordIfMatch(record, status, meta, "delivery"); err != nil {
		if beads.IsPreconditionFailed(err) {
			current, readErr := s.getAt(ctx, id, now)
			if readErr != nil {
				return fmt.Errorf("re-read external coordination delivery %s after conflict: %w", id, readErr)
			}
			return fmt.Errorf("%w: cannot record delivery for state %s", ErrNotQueued, current.State)
		}
		return fmt.Errorf("record external coordination delivery %s: %w", id, err)
	}
	if record.Request.ContentRetention == RetentionEphemeral {
		return s.scrubContent(id, now)
	}
	return nil
}

// RecordResponse records an execution outcome returned by the external
// coordinator. This is a separate transition from Complete: an adapter can
// acknowledge a delivery without the external coordinator having answered yet.
//
// The first valid correlated response fixes ReceivedAt. An exact replay returns
// the stored timestamp unchanged, while a different response body for the same
// request is rejected rather than overwriting history. A response that requires
// follow-up stops at responded; otherwise the request reaches the terminal
// outcome_recorded state.
func (s *Service) RecordResponse(ctx context.Context, response Response) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return fmt.Errorf("%w: response request_id required", ErrInvalidInput)
	}
	record, err := s.findByRequestID(ctx, response.RequestID)
	if err != nil {
		return err
	}
	commitment, err := responseCommitment(response)
	if err != nil {
		return err
	}
	if isRecordedResponseState(record.State) && record.responseCommitment == commitment {
		if record.responseScrubPending {
			return s.scrubContent(record.ID, record.receivedAt)
		}
		return nil
	}
	if record.State == StateUncertain {
		return fmt.Errorf("%w: %s must be reconciled before a response is recorded", ErrUnreconciled, record.ID)
	}
	if record.State != StateRunning && record.State != StateSubmitted && record.State != StateResponded {
		return fmt.Errorf("%w: cannot record response for state %s", ErrNotQueued, record.State)
	}
	if response.Attempt <= 0 || response.Attempt != record.Attempt {
		return fmt.Errorf("%w: response attempt does not match current claim", ErrInvalidInput)
	}
	if response.CorrelationID == "" || response.CorrelationID != record.Request.CorrelationID {
		return fmt.Errorf("%w: response correlation_id does not match request", ErrInvalidInput)
	}
	writer, ok := beads.ConditionalWriterFor(s.store)
	if !ok {
		return fmt.Errorf("%w: conditional response transition unavailable", ErrUnavailable)
	}
	receivedAt := zeroTime(response.ReceivedAt)
	resolved := StateOutcomeRecorded
	status := "closed"
	if response.FollowUpRequired {
		// The coordinator has answered but is not done. The request stays
		// open at responded so a later response can still be correlated.
		resolved = StateResponded
		status = "in_progress"
	}
	if !CanTransition(record.State, resolved) {
		return fmt.Errorf("%w: %s cannot become %s from %s", ErrIllegalTransition, record.ID, resolved, record.State)
	}
	metadata := map[string]string{
		metadataState:              string(resolved),
		metadataRespondedAt:        receivedAt.UTC().Format(time.RFC3339Nano),
		metadataDelivered:          receivedAt.UTC().Format(time.RFC3339Nano),
		metadataResponseCommitment: commitment,
	}
	// ReceivedAt is fixed by the first valid correlated response. A follow-up
	// response advances RespondedAt but never rewrites that observation.
	if record.receivedAt.IsZero() {
		metadata[metadataReceivedAt] = receivedAt.UTC().Format(time.RFC3339Nano)
	}
	if resolved == StateOutcomeRecorded {
		metadata[metadataOutcome] = string(OutcomeResponseRecorded)
	}
	if response.ResponseID != "" {
		metadata["external_coordination.response_id"] = response.ResponseID
	}
	mustScrub := record.Request.ContentRetention == RetentionEphemeral || response.ContentRetention == RetentionEphemeral
	if mustScrub {
		metadata[metadataResponseScrubPending] = "true"
	}
	if err := writer.UpdateIfMatch(record.ID, record.revision, beads.UpdateOpts{Status: &status, Metadata: metadata}); err != nil {
		if beads.IsPreconditionFailed(err) {
			current, readErr := s.findByRequestID(ctx, response.RequestID)
			if readErr != nil {
				return fmt.Errorf("re-read external coordination response %s after conflict: %w", record.ID, readErr)
			}
			if isRecordedResponseState(current.State) && current.responseCommitment == commitment {
				if current.responseScrubPending {
					return s.scrubContent(current.ID, current.receivedAt)
				}
				return nil
			}
			return fmt.Errorf("%w: cannot record response for state %s", ErrNotQueued, current.State)
		}
		return fmt.Errorf("record external coordination response %s: %w", record.ID, err)
	}
	if mustScrub {
		return s.scrubContent(record.ID, receivedAt)
	}
	return nil
}

// isRecordedResponseState reports whether a validated response is already
// durable for this request, so an identical replay is idempotent. StateCompleted
// is the legacy spelling retained so records written before the policy state
// vocabulary still replay correctly.
func isRecordedResponseState(state DeliveryState) bool {
	return state == StateOutcomeRecorded || state == StateResponded || state == StateCompleted
}

// responseCommitment is the replay-stable digest of a response body. The
// observation time is deliberately excluded: the same response seen again at a
// later time is an exact replay, not a conflicting response, and must not move
// the persisted first-valid ReceivedAt.
func responseCommitment(response Response) (string, error) {
	response.ReceivedAt = time.Time{}
	canonical, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize response: %w", ErrInvalidInput, err)
	}
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func (s *Service) scrubContent(id string, now time.Time) error {
	record, err := s.Get(context.Background(), id)
	if err != nil {
		return err
	}
	record.Request.Prompt = ""
	payload, err := json.Marshal(record.Request)
	if err != nil {
		return fmt.Errorf("encode scrubbed external coordination request %s: %w", id, err)
	}
	description := "[ephemeral external coordination content released]"
	return s.store.Update(id, beads.UpdateOpts{
		Description: &description,
		Metadata: map[string]string{
			metadataRequest:                          string(payload),
			"external_coordination.content_released": zeroTime(now).UTC().Format(time.RFC3339Nano),
			metadataResponseScrubPending:             "",
		},
	})
}

func (s *Service) findByRequestID(ctx context.Context, requestID string) (RequestRecord, error) {
	record, err := s.Get(ctx, requestID)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return RequestRecord{}, err
	}
	items, err := s.store.List(beads.ListQuery{Label: requestLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		return RequestRecord{}, err
	}
	for _, item := range items {
		record, decodeErr := decodeRecord(item)
		if decodeErr != nil {
			return RequestRecord{}, decodeErr
		}
		if record.Request.RequestID == requestID {
			return record, nil
		}
	}
	return RequestRecord{}, ErrNotFound
}

// Fail records a failed delivery while keeping the request durable for
// inspection and an explicit retry policy.
func (s *Service) Fail(ctx context.Context, id string, cause error, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if record.State == StateUncertain {
		return fmt.Errorf("%w: %s must be reconciled before it can fail", ErrUnreconciled, id)
	}
	if IsTerminal(record.State) {
		return fmt.Errorf("%w: %s is already terminal at %s", ErrIllegalTransition, id, record.State)
	}
	message := "delivery failed"
	if cause != nil {
		message = sanitizeDurableError(cause.Error())
	}
	if err := s.setStateWithOutcomeForRecord(record, StateFailed, OutcomeRejected, zeroTime(now), message); err != nil {
		if beads.IsPreconditionFailed(err) {
			return fmt.Errorf("%w: failure for %s changed concurrently", ErrIllegalTransition, id)
		}
		return fmt.Errorf("fail external coordination request %s: %w", id, err)
	}
	return nil
}

// MarkUncertain records that a submission may have been observed by the
// recipient but produced no trustworthy receipt. The attempt identity and
// idempotency key are deliberately left unchanged: the same key must be reused
// to reconcile. Repeating the call keeps the first uncertainty timestamp.
func (s *Service) MarkUncertain(ctx context.Context, id, failureClass string, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if record.State == StateUncertain {
		return nil
	}
	if !CanTransition(record.State, StateUncertain) {
		return fmt.Errorf("%w: %s cannot become uncertain from %s", ErrIllegalTransition, id, record.State)
	}
	if err := s.updateRecordIfMatch(record, "in_progress", map[string]string{
		metadataState:            string(StateUncertain),
		metadataUncertainAt:      now.UTC().Format(time.RFC3339Nano),
		metadataUncertaintyClass: sanitizeFailureClass(failureClass),
	}, "uncertainty"); err != nil {
		if beads.IsPreconditionFailed(err) {
			current, readErr := s.getAt(ctx, id, now)
			if readErr == nil && current.State == StateUncertain {
				return nil
			}
			if readErr != nil {
				return fmt.Errorf("re-read external coordination uncertainty %s after conflict: %w", id, readErr)
			}
			return fmt.Errorf("%w: cannot mark uncertainty for state %s", ErrNotQueued, current.State)
		}
		return fmt.Errorf("mark external coordination request %s uncertain: %w", id, err)
	}
	return nil
}

// Reconcile resolves an uncertain submission by checking the same target and
// idempotency key. The record passes through the reconciled observation, which
// is persisted as ReconciledAt, and the resolved state is written in the same
// durable update so no torn intermediate state is observable.
//
// An unknown result keeps the request uncertain and therefore still ineligible
// for retry or default fallback.
func (s *Service) Reconcile(ctx context.Context, id string, result ReconcileResult, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if !CanTransition(record.State, StateReconciled) {
		return fmt.Errorf("%w: %s is %s, only an uncertain submission can be reconciled", ErrIllegalTransition, id, record.State)
	}
	metadata := map[string]string{metadataReconciledAt: now.UTC().Format(time.RFC3339Nano)}
	status := "in_progress"
	var resolved DeliveryState
	switch result {
	case ReconcileAccepted:
		resolved = StateSubmitted
		if record.submittedAt.IsZero() {
			metadata[metadataSubmittedAt] = now.UTC().Format(time.RFC3339Nano)
		}
	case ReconcileRejected:
		resolved = StateFailed
		metadata[metadataOutcome] = string(OutcomeRejected)
		metadata[metadataError] = "reconciliation found no accepted submission"
		status = "open"
	case ReconcileUnknown:
		resolved = StateUncertain
	default:
		return fmt.Errorf("%w: invalid reconcile result %q", ErrInvalidInput, result)
	}
	if !CanTransition(StateReconciled, resolved) {
		return fmt.Errorf("%w: reconciled cannot resolve to %s", ErrIllegalTransition, resolved)
	}
	metadata[metadataState] = string(resolved)
	if err := s.updateRecordIfMatch(record, status, metadata, "reconciliation"); err != nil {
		if beads.IsPreconditionFailed(err) {
			return fmt.Errorf("%w: reconciliation changed concurrently", ErrNotQueued)
		}
		return fmt.Errorf("record external coordination reconciliation %s: %w", id, err)
	}
	return nil
}

// RecordNotificationDelivered records the terminal outcome for a lifecycle
// notification that the configured target accepted. It asserts delivery only;
// it never asserts that the recipient acted on the notification, which is why
// an intervention request cannot reach a terminal outcome this way.
func (s *Service) RecordNotificationDelivered(ctx context.Context, id string, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if record.State == StateOutcomeRecorded && record.outcome == OutcomeNotificationDelivered {
		return nil
	}
	if !CanTransition(record.State, StateOutcomeRecorded) {
		return fmt.Errorf("%w: %s cannot record a delivered notification from %s", ErrIllegalTransition, id, record.State)
	}
	return s.setStateWithOutcomeForRecord(record, StateOutcomeRecorded, OutcomeNotificationDelivered, now, "")
}

// VerifyTargetFence fails closed when the configured target changed after this
// request was admitted. A stale fence is terminal for the existing record: the
// record is never redirected onto a newly configured target, because that
// target was never authorized for this request. Only a fresh admission may use
// the new configuration.
func (s *Service) VerifyTargetFence(ctx context.Context, id string, current Target, now time.Time) (RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return RequestRecord{}, err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return RequestRecord{}, err
	}
	if targetFenceMatches(record.Request.Target, current) {
		return record, nil
	}
	staleErr := fmt.Errorf("%w: %s was admitted for target %q revision %d", ErrStaleTarget, id, record.Request.Target.TargetID, record.Request.Target.ConfigRevision)
	if IsTerminal(record.State) {
		return RequestRecord{}, staleErr
	}
	if err := s.setStateWithOutcomeForRecord(record, StateFailed, OutcomeStaleTarget, now, "configured external coordination target changed after admission"); err != nil {
		if beads.IsPreconditionFailed(err) {
			return RequestRecord{}, staleErr
		}
		return RequestRecord{}, fmt.Errorf("record stale target fence for %s: %w", id, err)
	}
	return RequestRecord{}, staleErr
}

// targetFenceMatches compares the authorization-relevant identity of the
// configured target. Opaque route data is deliberately excluded: a route never
// participates in selecting or validating a target.
func targetFenceMatches(admitted, current Target) bool {
	return admitted.LogicalRole == current.LogicalRole &&
		admitted.TargetID == current.TargetID &&
		admitted.Adapter == current.Adapter &&
		admitted.Provider == current.Provider &&
		admitted.AccountID == current.AccountID &&
		admitted.ConversationID == current.ConversationID &&
		admitted.SessionMode == current.SessionMode &&
		admitted.DeliveryMode == current.DeliveryMode &&
		admitted.InterruptAllowed == current.InterruptAllowed &&
		admitted.ConfigRevision == current.ConfigRevision
}

// sanitizeFailureClass bounds a diagnostic label and strips the forms that must
// never reach a durable record: URLs, credentials, and control characters.
func sanitizeFailureClass(class string) string {
	class = strings.Map(func(char rune) rune {
		if char == '\n' || char == '\r' || char == '\t' {
			return ' '
		}
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, strings.TrimSpace(class))
	if class == "" {
		return "unclassified"
	}
	if looksLikeURL(class) {
		return "redacted"
	}
	lowered := strings.ToLower(class)
	for _, fragment := range credentialKeyFragments {
		if strings.Contains(lowered, fragment) {
			return "redacted"
		}
	}
	for _, prefix := range credentialValuePrefixes {
		if strings.Contains(lowered, prefix) {
			return "redacted"
		}
	}
	if len(class) > maxUncertaintyClassLen {
		class = class[:maxUncertaintyClassLen]
	}
	return class
}

func sanitizeDurableError(message string) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	return sanitizeFailureClass(message)
}

// Cancel terminally cancels a request. It never deletes the causal record.
func (s *Service) Cancel(ctx context.Context, id string, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	now = zeroTime(now)
	record, err := s.getAt(ctx, id, now)
	if err != nil {
		return err
	}
	if IsTerminal(record.State) {
		return fmt.Errorf("%w: %s is already terminal at %s", ErrIllegalTransition, id, record.State)
	}
	if !CanTransition(record.State, StateCancelled) {
		return fmt.Errorf("%w: %s cannot be canceled from %s", ErrIllegalTransition, id, record.State)
	}
	status := "closed"
	if err := s.updateRecordIfMatch(record, status, map[string]string{
		metadataState:     string(StateCancelled),
		metadataOutcome:   string(OutcomeCancelled),
		metadataError:     "cancelled by operator", //nolint:misspell // public wire spelling
		metadataDelivered: now.UTC().Format(time.RFC3339Nano),
	}, "cancellation"); err != nil {
		if beads.IsPreconditionFailed(err) {
			return fmt.Errorf("%w: cancellation changed concurrently", ErrIllegalTransition)
		}
		return fmt.Errorf("cancel external coordination request %s: %w", id, err)
	}
	return nil
}

func (s *Service) setStateWithOutcomeForRecord(record RequestRecord, state DeliveryState, outcome Outcome, now time.Time, message string) error {
	status := "open"
	if state == StateExpired || state == StateCancelled || state == StateCompleted || state == StateOutcomeRecorded {
		status = "closed"
	}
	metadata := map[string]string{
		metadataState:     string(state),
		metadataError:     sanitizeDurableError(message),
		metadataDelivered: zeroTime(now).UTC().Format(time.RFC3339Nano),
	}
	if outcome != OutcomeNone {
		metadata[metadataOutcome] = string(outcome)
	}
	return s.updateRecordIfMatch(record, status, metadata, "state")
}

func (s *Service) updateRecordIfMatch(record RequestRecord, status string, metadata map[string]string, operation string) error {
	writer, ok := beads.ConditionalWriterFor(s.store)
	if !ok {
		return fmt.Errorf("%w: conditional %s transition unavailable", ErrUnavailable, operation)
	}
	return writer.UpdateIfMatch(record.ID, record.revision, beads.UpdateOpts{
		Status:   &status,
		Metadata: metadata,
	})
}

func normalizeRequest(input RequestInput) (Request, error) {
	if strings.TrimSpace(input.SourceAgent) == "" {
		return Request{}, fmt.Errorf("%w: source_agent required", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Request{}, fmt.Errorf("%w: prompt required", ErrInvalidInput)
	}
	if input.Reason == "" {
		return Request{}, fmt.Errorf("%w: reason required", ErrInvalidInput)
	}
	route, err := sanitizeRouteIdentity(input.RouteIdentity)
	if err != nil {
		return Request{}, err
	}
	request := Request{
		RequestID:         uuid.NewString(),
		SourceAgent:       strings.TrimSpace(input.SourceAgent),
		Target:            input.Target,
		City:              strings.TrimSpace(input.City),
		WorkRef:           strings.TrimSpace(input.WorkRef),
		Repository:        strings.TrimSpace(input.Repository),
		Rig:               strings.TrimSpace(input.Rig),
		Reason:            input.Reason,
		DeliveryMode:      input.DeliveryMode,
		SessionMode:       input.SessionMode,
		Prompt:            input.Prompt,
		ContentRetention:  input.ContentRetention,
		AllowedTools:      append([]string(nil), input.AllowedTools...),
		CorrelationID:     strings.TrimSpace(input.CorrelationID),
		IdempotencyKey:    strings.TrimSpace(input.IdempotencyKey),
		ExpiresAt:         input.ExpiresAt,
		ResultDestination: strings.TrimSpace(input.ResultDestination),
		RouteIdentity:     route,
		CreatedAt:         input.Now,
	}
	if request.Target.LogicalRole == "" {
		request.Target.LogicalRole = "external-coordination"
	}
	if request.CorrelationID == "" {
		return Request{}, fmt.Errorf("%w: correlation_id required", ErrInvalidInput)
	}
	if request.DeliveryMode == "" {
		request.DeliveryMode = DeliveryQueued
	}
	if request.DeliveryMode != DeliveryQueued && request.DeliveryMode != DeliveryInterrupt {
		return Request{}, fmt.Errorf("%w: invalid delivery_mode %q", ErrInvalidInput, request.DeliveryMode)
	}
	if request.DeliveryMode == DeliveryInterrupt && !request.Target.InterruptAllowed {
		return Request{}, fmt.Errorf("%w: interrupt delivery is not authorized by target policy", ErrInvalidInput)
	}
	if request.SessionMode == "" {
		request.SessionMode = SessionResumeOrCreate
	}
	if request.ContentRetention == "" {
		request.ContentRetention = RetentionEphemeral
	}
	if request.ContentRetention != RetentionDurable && request.ContentRetention != RetentionEphemeral {
		return Request{}, fmt.Errorf("%w: invalid content_retention %q", ErrInvalidInput, request.ContentRetention)
	}
	switch request.SessionMode {
	case SessionNew, SessionResume, SessionSubmit, SessionResumeOrCreate:
	default:
		return Request{}, fmt.Errorf("%w: invalid session_mode %q", ErrInvalidInput, request.SessionMode)
	}
	request.CreatedAt = zeroTime(request.CreatedAt)
	if request.ExpiresAt.IsZero() {
		request.ExpiresAt = request.CreatedAt.Add(24 * time.Hour)
	}
	if !request.ExpiresAt.After(request.CreatedAt) {
		return Request{}, fmt.Errorf("%w: expires_at must be after created_at", ErrInvalidInput)
	}
	return request, nil
}

func decodeRecord(item beads.Bead) (RequestRecord, error) {
	var request Request
	if err := json.Unmarshal([]byte(item.Metadata[metadataRequest]), &request); err != nil {
		return RequestRecord{}, fmt.Errorf("decode external coordination request %s: %w", item.ID, err)
	}
	return RequestRecord{
		ID:                   item.ID,
		Request:              request,
		State:                DeliveryState(defaultString(item.Metadata[metadataState], string(StateQueued))),
		Attempt:              parseInt(item.Metadata[metadataAttempt]),
		ClaimedBy:            item.Metadata[metadataClaimedBy],
		ClaimedAt:            parseTime(item.Metadata[metadataClaimedAt]),
		DeliveredAt:          parseTime(item.Metadata[metadataDelivered]),
		Error:                item.Metadata[metadataError],
		revision:             item.Revision,
		responseCommitment:   item.Metadata[metadataResponseCommitment],
		responseScrubPending: item.Metadata[metadataResponseScrubPending] == "true",
		outcome:              Outcome(item.Metadata[metadataOutcome]),
		submittedAt:          parseTime(item.Metadata[metadataSubmittedAt]),
		uncertainAt:          parseTime(item.Metadata[metadataUncertainAt]),
		uncertaintyClass:     item.Metadata[metadataUncertaintyClass],
		reconciledAt:         parseTime(item.Metadata[metadataReconciledAt]),
		respondedAt:          parseTime(item.Metadata[metadataRespondedAt]),
		receivedAt:           parseTime(item.Metadata[metadataReceivedAt]),
	}, nil
}

func hasRequestLabel(item beads.Bead) bool {
	for _, label := range item.Labels {
		if label == requestLabel {
			return true
		}
	}
	return false
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func zeroTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}

func strPtr(value string) *string { return &value }

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseInt(value string) int {
	var out int
	_, _ = fmt.Sscan(value, &out)
	return out
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
