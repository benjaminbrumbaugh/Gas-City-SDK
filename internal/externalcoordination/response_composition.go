package externalcoordination

import (
	"context"
	"fmt"
	"strings"
)

// ResponseCompositionResult reports the stored request snapshot and the
// durable origin handoff created for the response. Handoff is nil when the
// stored request has no explicit result destination.
type ResponseCompositionResult struct {
	Request RequestRecord
	Handoff *ResponseHandoffRecord
}

// ResponseComposer is the response-to-origin composition boundary. It first
// persists the complete response in ResponseHandoff, then settles the request
// through Service. The response contains only a request identity; origin and
// route fences always come from the stored request snapshot.
type ResponseComposer struct {
	service *Service
	handoff *ResponseHandoff
}

// NewResponseComposer creates a response composition boundary over the durable
// request service and, when configured, its existing origin handoff.
func NewResponseComposer(service *Service, handoff *ResponseHandoff) *ResponseComposer {
	return &ResponseComposer{service: service, handoff: handoff}
}

// Record durably preserves a response body before settling its request. A
// failure after the handoff is persisted returns the handoff in the result so
// a caller can observe the durable recovery record and retry this exact
// operation without creating another logical response.
func (c *ResponseComposer) Record(ctx context.Context, response Response) (ResponseCompositionResult, error) {
	if err := checkContext(ctx); err != nil {
		return ResponseCompositionResult{}, err
	}
	if c == nil || c.service == nil {
		return ResponseCompositionResult{}, fmt.Errorf("%w: response service unavailable", ErrInvalidInput)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return ResponseCompositionResult{}, fmt.Errorf("%w: response request_id required", ErrInvalidInput)
	}

	request, err := c.service.findByRequestID(ctx, response.RequestID)
	if err != nil {
		return ResponseCompositionResult{}, err
	}
	response, err = normalizeComposedResponse(response, request)
	if err != nil {
		return ResponseCompositionResult{Request: request}, err
	}
	result := ResponseCompositionResult{Request: request}
	if request.State != StateRunning && request.State != StateCompleted {
		return result, fmt.Errorf("%w: cannot record response for state %s", ErrNotQueued, request.State)
	}

	if strings.TrimSpace(request.Request.ResultDestination) != "" {
		if c.handoff == nil {
			return result, fmt.Errorf("%w: explicit response origin requires a handoff", ErrHandoffUnavailable)
		}
		handoff, enqueueErr := c.enqueueHandoff(ctx, request, response)
		if enqueueErr != nil {
			return result, enqueueErr
		}
		result.Handoff = &handoff
	}

	if err := c.service.RecordResponse(ctx, response); err != nil {
		return result, fmt.Errorf("record response after durable origin handoff: %w", err)
	}
	settled, err := c.service.Get(ctx, request.ID)
	if err != nil {
		return result, fmt.Errorf("read settled external coordination request %s: %w", request.ID, err)
	}
	result.Request = settled
	return result, nil
}

func (c *ResponseComposer) enqueueHandoff(ctx context.Context, request RequestRecord, response Response) (ResponseHandoffRecord, error) {
	handoffRequest, err := normalizeHandoffRequest(request.Request)
	if err != nil {
		return ResponseHandoffRecord{}, err
	}
	if request.State == StateCompleted {
		commitment, err := responseCommitment(response)
		if err != nil {
			return ResponseHandoffRecord{}, err
		}
		existing, err := c.handoff.findByIdentity(handoffRequest)
		if err != nil {
			return ResponseHandoffRecord{}, fmt.Errorf("%w: completed request has no durable origin handoff", ErrHandoffUnavailable)
		}
		if existing.ResponseCommitment != commitment {
			return ResponseHandoffRecord{}, fmt.Errorf("%w: completed response differs from durable origin handoff", ErrHandoffConflict)
		}
		return c.handoff.Get(ctx, existing.ID)
	}
	return c.handoff.enqueueNormalized(ctx, handoffRequest, response)
}

func normalizeComposedResponse(response Response, request RequestRecord) (Response, error) {
	response.RequestID = request.Request.RequestID
	response.CorrelationID = strings.TrimSpace(response.CorrelationID)
	response.ResponseID = strings.TrimSpace(response.ResponseID)
	response.State = strings.ToLower(strings.TrimSpace(response.State))
	if response.State == "" {
		response.State = "answered"
	}
	switch response.State {
	case "answered", "refused", "failed", "expired":
	default:
		return Response{}, fmt.Errorf("%w: invalid response outcome %q", ErrInvalidInput, response.State)
	}
	if request.Attempt <= 0 || response.Attempt != request.Attempt {
		return Response{}, fmt.Errorf("%w: response attempt does not match current claim", ErrInvalidInput)
	}
	if response.CorrelationID == "" || response.CorrelationID != request.Request.CorrelationID {
		return Response{}, fmt.Errorf("%w: response correlation_id does not match request", ErrInvalidInput)
	}
	if response.ContentRetention != "" && response.ContentRetention != RetentionDurable && response.ContentRetention != RetentionEphemeral {
		return Response{}, fmt.Errorf("%w: invalid response content_retention %q", ErrInvalidInput, response.ContentRetention)
	}
	if response.State == "failed" || response.State == "expired" {
		response.Summary = sanitizeOutcomeSummary(response.State, response.Summary)
	}
	if response.ReceivedAt.IsZero() {
		response.ReceivedAt = request.Request.CreatedAt
		if response.ReceivedAt.IsZero() {
			return Response{}, fmt.Errorf("%w: response received_at required when request created_at is absent", ErrInvalidInput)
		}
	}
	return response, nil
}
