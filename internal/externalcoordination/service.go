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
)

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
	if record.State == StateQueued && !record.Request.ExpiresAt.After(time.Now()) {
		_ = s.setState(record.ID, StateExpired, time.Now(), "request expired")
		record.State = StateExpired
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

	record, err := s.Get(ctx, id)
	if err != nil {
		return RequestRecord{}, err
	}
	if record.State != StateQueued {
		return RequestRecord{}, fmt.Errorf("%w: %s is %s", ErrNotQueued, record.ID, record.State)
	}
	now = zeroTime(now)
	if !record.Request.ExpiresAt.After(now) {
		_ = s.setState(record.ID, StateExpired, now, "request expired")
		return RequestRecord{}, ErrExpired
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
	if err := s.store.Update(record.ID, beads.UpdateOpts{
		Status: strPtr("in_progress"),
		Metadata: map[string]string{
			metadataRequest:   string(payload),
			metadataState:     string(StateRunning),
			metadataAttempt:   fmt.Sprintf("%d", record.Attempt),
			metadataClaimedBy: worker,
			metadataClaimedAt: now.UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		return RequestRecord{}, fmt.Errorf("claim external coordination request %s: %w", record.ID, err)
	}
	return record, nil
}

// Complete records the adapter's delivery result. A queued/running receipt is
// deliberately not terminal: transport acceptance is not external coordinator execution.
func (s *Service) Complete(ctx context.Context, id string, receipt DeliveryReceipt, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: request id mismatch", ErrInvalidInput)
	}
	record, err := s.Get(ctx, id)
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
	now = zeroTime(now)
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
	status := "in_progress"
	meta := map[string]string{
		metadataState:     string(StateRunning),
		metadataDelivered: now.UTC().Format(time.RFC3339Nano),
	}
	if receipt.Error != "" {
		meta[metadataError] = receipt.Error
	}
	if err := s.store.Update(id, beads.UpdateOpts{Status: &status, Metadata: meta}); err != nil {
		return fmt.Errorf("record external coordination delivery %s: %w", id, err)
	}
	if record.Request.ContentRetention == RetentionEphemeral {
		return s.scrubContent(id, now)
	}
	return nil
}

// RecordResponse records an execution outcome returned by the external coordinator. This is a
// separate transition from Complete: an adapter can acknowledge a queued or
// running delivery without the external coordinator having answered yet.
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
	if record.State == StateCompleted && record.responseCommitment == commitment {
		if record.responseScrubPending {
			return s.scrubContent(record.ID, response.ReceivedAt)
		}
		return nil
	}
	if record.State != StateRunning {
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
	status := "closed"
	metadata := map[string]string{
		metadataState:              string(StateCompleted),
		metadataDelivered:          zeroTime(response.ReceivedAt).UTC().Format(time.RFC3339Nano),
		metadataResponseCommitment: commitment,
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
			if current.State == StateCompleted && current.responseCommitment == commitment {
				if current.responseScrubPending {
					return s.scrubContent(current.ID, response.ReceivedAt)
				}
				return nil
			}
			return fmt.Errorf("%w: cannot record response for state %s", ErrNotQueued, current.State)
		}
		return fmt.Errorf("record external coordination response %s: %w", record.ID, err)
	}
	if mustScrub {
		return s.scrubContent(record.ID, response.ReceivedAt)
	}
	return nil
}

func responseCommitment(response Response) (string, error) {
	response.ReceivedAt = response.ReceivedAt.UTC()
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
	if record, err := s.Get(ctx, requestID); err == nil {
		return record, nil
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
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	return s.setState(id, StateFailed, zeroTime(now), message)
}

// Cancel terminally cancels a request. It never deletes the causal record.
func (s *Service) Cancel(ctx context.Context, id string, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	status := "closed"
	return s.store.Update(id, beads.UpdateOpts{Status: &status, Metadata: map[string]string{
		metadataState:     string(StateCancelled),
		metadataError:     "cancelled by operator", //nolint:misspell // public wire spelling
		metadataDelivered: zeroTime(now).UTC().Format(time.RFC3339Nano),
	}})
}

func (s *Service) setState(id string, state DeliveryState, now time.Time, message string) error {
	status := "open"
	if state == StateExpired || state == StateCancelled || state == StateCompleted {
		status = "closed"
	}
	return s.store.Update(id, beads.UpdateOpts{Status: &status, Metadata: map[string]string{
		metadataState:     string(state),
		metadataError:     message,
		metadataDelivered: zeroTime(now).UTC().Format(time.RFC3339Nano),
	}})
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
		RouteIdentity:     cloneMap(input.RouteIdentity),
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

func cloneMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
