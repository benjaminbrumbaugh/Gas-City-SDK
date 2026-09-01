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
// bodies are treated as transient failures so transport acceptance cannot be
// misreported as execution completion.
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
		return indeterminateReceipt(request), fmt.Errorf("%w: send callback: %w", ErrDeliveryIndeterminate, err)
	}
	defer response.Body.Close() //nolint:errcheck
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return indeterminateReceipt(request), fmt.Errorf("%w: read callback receipt: %w", ErrDeliveryIndeterminate, err)
	}
	if response.StatusCode >= 400 {
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return DeliveryReceipt{RequestID: request.RequestID, Attempt: request.Attempt, CorrelationID: request.CorrelationID, State: StateQueued, Error: fmt.Sprintf("adapter returned HTTP %d", response.StatusCode)}, ErrUnavailable
		}
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: fmt.Sprintf("adapter returned HTTP %d", response.StatusCode)}, nil
	}
	var receipt DeliveryReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return indeterminateReceipt(request), fmt.Errorf("%w: decode callback receipt: %w", ErrDeliveryIndeterminate, err)
	}
	if receipt.RequestID != request.RequestID {
		return indeterminateReceipt(request), fmt.Errorf("%w: callback receipt request_id %q does not match %q", ErrDeliveryIndeterminate, receipt.RequestID, request.RequestID)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		return indeterminateReceipt(request), fmt.Errorf("%w: callback receipt causal fence does not match request", ErrDeliveryIndeterminate)
	}
	if receipt.State == StateCompleted {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "adapter reported completion at delivery boundary"}, ErrPrematureCompletion
	}
	if receipt.State == "" {
		receipt.State = StateQueued
	}
	return receipt, nil
}

func indeterminateReceipt(request Request) DeliveryReceipt {
	return DeliveryReceipt{
		RequestID:     request.RequestID,
		Attempt:       request.Attempt,
		CorrelationID: request.CorrelationID,
		State:         StateRunning,
	}
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
	worker := strings.TrimSpace(d.Worker)
	if worker == "" {
		worker = d.Adapter.Name()
	}
	running, err := d.Queue.List(ctx, StateRunning)
	if err != nil {
		return nil, nil, err
	}
	var record RequestRecord
	for _, candidate := range running {
		if candidate.DeliveryIndeterminate {
			record = candidate
			break
		}
	}
	queued, err := d.Queue.List(ctx, StateQueued)
	if err != nil {
		return nil, nil, err
	}
	if record.ID == "" && len(queued) == 0 {
		return nil, nil, nil
	}
	if record.ID == "" {
		record = queued[0]
	}
	if err := ValidateCapabilities(d.Adapter.Capabilities(), record.Request); err != nil {
		_ = d.Queue.Fail(ctx, record.ID, err, now)
		return nil, nil, err
	}
	if record.State == StateQueued {
		record, err = d.Queue.Claim(ctx, record.ID, worker, now)
		if err != nil {
			return nil, nil, err
		}
	}
	receipt, deliverErr := d.Adapter.Deliver(ctx, record.Request)
	if deliverErr != nil {
		if errors.Is(deliverErr, ErrDeliveryIndeterminate) {
			if err := d.Queue.MarkDeliveryIndeterminate(ctx, record.ID, deliverErr, now); err != nil {
				return &record, &receipt, err
			}
			return &record, &receipt, deliverErr
		}
		// ErrUnavailable is a confirmed rejection before acceptance, so the
		// same durable request may safely return to the queue for a later attempt.
		if errors.Is(deliverErr, ErrUnavailable) {
			_ = d.Queue.Requeue(ctx, record.ID, deliverErr, now)
		} else {
			_ = d.Queue.Fail(ctx, record.ID, deliverErr, now)
		}
		return &record, &receipt, deliverErr
	}
	if receipt.State == StateFailed {
		if receipt.Error == "" {
			receipt.Error = "adapter rejected request"
		}
		_ = d.Queue.Fail(ctx, record.ID, fmt.Errorf("%s", receipt.Error), now)
		return &record, &receipt, nil
	}
	if err := d.Queue.Complete(ctx, record.ID, receipt, now); err != nil {
		return &record, &receipt, err
	}
	return &record, &receipt, nil
}

// ValidateCapabilities checks whether an adapter can execute the exact request policy.
func ValidateCapabilities(capabilities Capability, request Request) error {
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
