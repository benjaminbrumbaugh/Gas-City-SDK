package externalcoordination

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPAdapter delivers the provider-neutral callback envelope to an external
// coordinator over HTTP. Authentication is supplied at runtime; it is never persisted
// in Request or city configuration.
type HTTPAdapter struct {
	name          string
	callbackURL   string
	capabilities  Capability
	authorization string
	client        *http.Client
}

// HTTPAdapterOption configures transport behavior without putting credentials
// into the durable callback envelope.
type HTTPAdapterOption func(*HTTPAdapter)

// WithAuthorizationHeader supplies an in-memory Authorization header for the
// adapter. Callers should obtain it from their own credential boundary.
func WithAuthorizationHeader(value string) HTTPAdapterOption {
	return func(adapter *HTTPAdapter) { adapter.authorization = strings.TrimSpace(value) }
}

// WithHTTPClient supplies a client, primarily useful for tests and custom TLS.
func WithHTTPClient(client *http.Client) HTTPAdapterOption {
	return func(adapter *HTTPAdapter) {
		if client != nil {
			adapter.client = client
		}
	}
}

// NewHTTPAdapter creates an external coordinator adapter.
func NewHTTPAdapter(name, callbackURL string, capabilities Capability, opts ...HTTPAdapterOption) *HTTPAdapter {
	adapter := &HTTPAdapter{
		name:         strings.TrimSpace(name),
		callbackURL:  strings.TrimRight(strings.TrimSpace(callbackURL), "/"),
		capabilities: capabilities,
		client:       &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(adapter)
		}
	}
	return adapter
}

// Name returns the configured adapter name.
func (a *HTTPAdapter) Name() string { return a.name }

// Capabilities returns the adapter's declared capabilities.
func (a *HTTPAdapter) Capabilities() Capability { return a.capabilities }

// Deliver sends one callback. A successful HTTP response is only accepted if
// its body contains a valid non-terminal delivery receipt; malformed success
// bodies are treated as ambiguous because the remote may have accepted the
// request before producing an unreadable response.
func (a *HTTPAdapter) Deliver(ctx context.Context, request Request) (DeliveryReceipt, error) {
	if a == nil || a.callbackURL == "" {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "callback URL is not configured"}, ErrUnavailable
	}
	body, err := json.Marshal(request)
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("marshal external coordination request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.callbackURL, bytes.NewReader(body))
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("create external coordination request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-GC-Coordination-Request-ID", request.RequestID)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	if a.authorization != "" {
		httpRequest.Header.Set("Authorization", a.authorization)
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain, Error: "delivery outcome is ambiguous"}, fmt.Errorf("%w: deliver external coordination request: %w", ErrAmbiguousDelivery, err)
	}
	defer response.Body.Close() //nolint:errcheck
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain, Error: "delivery outcome is ambiguous"}, fmt.Errorf("%w: read external coordination response: %w", ErrAmbiguousDelivery, err)
	}
	if response.StatusCode >= 400 {
		if response.StatusCode >= 500 {
			return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain, Error: fmt.Sprintf("adapter returned HTTP %d; delivery outcome is ambiguous", response.StatusCode)}, fmt.Errorf("%w: adapter returned HTTP %d", ErrAmbiguousDelivery, response.StatusCode)
		}
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: fmt.Sprintf("adapter returned HTTP %d", response.StatusCode)}, nil
	}
	var receipt DeliveryReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain, Error: "delivery outcome is ambiguous"}, fmt.Errorf("%w: decode external coordination receipt: %w", ErrAmbiguousDelivery, err)
	}
	if receipt.RequestID != request.RequestID {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("external coordination receipt request_id %q does not match %q", receipt.RequestID, request.RequestID)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("external coordination receipt causal fence does not match request")
	}
	if receipt.State == StateCompleted {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "adapter reported completion at delivery boundary"}, ErrPrematureCompletion
	}
	if receipt.State == "" {
		receipt.State = StateQueued
	}
	if !receipt.Accepted && receipt.State != StateUncertain {
		receipt.State = StateFailed
		if receipt.Error == "" {
			receipt.Error = "adapter rejected request"
		}
	}
	return receipt, nil
}

// Dispatcher claims and delivers one queued request. It is intentionally
// explicit: the controller decides when to run it, while the SDK owns the
// durable lifecycle and adapter semantics.
type Dispatcher struct {
	Queue   *Service
	Adapter Adapter
	Worker  string
}

// DeliverNext claims and delivers the oldest queued request available to the
// supplied adapter. A nil result means the queue is currently empty.
func (d *Dispatcher) DeliverNext(ctx context.Context, now time.Time) (*RequestRecord, *DeliveryReceipt, error) {
	if d == nil || d.Queue == nil || d.Adapter == nil {
		return nil, nil, ErrUnavailable
	}
	if now.IsZero() {
		now = d.Queue.nowTime()
	}
	if _, err := d.Queue.RecoverAbandoned(ctx, now); err != nil {
		return nil, nil, err
	}
	worker := strings.TrimSpace(d.Worker)
	if worker == "" {
		worker = d.Adapter.Name()
	}
	queued, err := d.Queue.List(ctx, StateQueued)
	if err != nil {
		return nil, nil, err
	}
	var next *RequestRecord
	for index := range queued {
		candidate := queued[index]
		if !candidate.RetryAt().IsZero() && candidate.RetryAt().After(now) {
			continue
		}
		next = &candidate
		break
	}
	if next == nil {
		return nil, nil, nil
	}
	if err := validateCapabilities(d.Adapter.Capabilities(), next.Request); err != nil {
		if failErr := d.Queue.Fail(ctx, next.ID, err, now); failErr != nil {
			return next, nil, failErr
		}
		return nil, nil, err
	}
	record, err := d.Queue.Claim(ctx, next.ID, worker, now)
	if err != nil {
		return nil, nil, err
	}
	receipt, deliverErr := d.Adapter.Deliver(ctx, record.Request)
	if deliverErr != nil {
		if receipt.State == StateUncertain || errors.Is(deliverErr, ErrAmbiguousDelivery) {
			uncertaintyClass := receipt.Error
			if uncertaintyClass == "" {
				uncertaintyClass = "ambiguous_delivery"
			}
			if markErr := d.Queue.MarkUncertain(ctx, record.ID, uncertaintyClass, now); markErr != nil {
				return &record, &receipt, markErr
			}
			return &record, &receipt, fmt.Errorf("%w: %w", ErrAmbiguousDelivery, deliverErr)
		}
		if errors.Is(deliverErr, ErrUnavailable) {
			retryClass := receipt.retryClass
			if retryClass == "" {
				retryClass = "unavailable"
			}
			delay, requeueErr := d.Queue.requeueWithClass(ctx, record.ID, deliverErr, retryClass, now)
			if requeueErr != nil {
				return &record, &receipt, requeueErr
			}
			receipt.RetryAfter = delay
		} else {
			if failErr := d.Queue.Fail(ctx, record.ID, deliverErr, now); failErr != nil {
				return &record, &receipt, failErr
			}
		}
		return &record, &receipt, deliverErr
	}
	if receipt.State == StateUncertain {
		if markErr := d.Queue.MarkUncertain(ctx, record.ID, receipt.Error, now); markErr != nil {
			return &record, &receipt, markErr
		}
		return &record, &receipt, ErrAmbiguousDelivery
	}
	if receipt.State == StateFailed {
		if receipt.Error == "" {
			receipt.Error = "adapter rejected request"
		}
		if failErr := d.Queue.Fail(ctx, record.ID, fmt.Errorf("%s", receipt.Error), now); failErr != nil {
			return &record, &receipt, failErr
		}
		return &record, &receipt, nil
	}
	if err := d.Queue.Complete(ctx, record.ID, receipt, now); err != nil {
		return &record, &receipt, err
	}
	return &record, &receipt, nil
}

func validateCapabilities(capabilities Capability, request Request) error {
	if request.DeliveryMode == DeliveryInterrupt && !capabilities.CanInterrupt {
		return fmt.Errorf("%w: adapter cannot interrupt an active coordinator turn", ErrInvalidInput)
	}
	switch request.SessionMode {
	case SessionNew:
		if !capabilities.CanCreateSession {
			return fmt.Errorf("%w: adapter cannot create coordinator sessions", ErrInvalidInput)
		}
	case SessionResume:
		if !capabilities.CanResumeSession {
			return fmt.Errorf("%w: adapter cannot resume coordinator sessions", ErrInvalidInput)
		}
	case SessionSubmit:
		if !capabilities.CanSubmitPrompt {
			return fmt.Errorf("%w: adapter cannot submit coordinator prompts", ErrInvalidInput)
		}
	case SessionResumeOrCreate:
		if !capabilities.CanResumeSession && !capabilities.CanCreateSession {
			return fmt.Errorf("%w: adapter can neither resume nor create coordinator sessions", ErrInvalidInput)
		}
	}
	return nil
}
