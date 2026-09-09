package externalcoordination

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	configErr     error
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
	adapter.configErr = validateCallbackURL(adapter.callbackURL)
	// Never follow a redirect. A 3xx would move the request, and its
	// Authorization header, to an origin the configuration never authorized.
	// Copy the client so a caller-supplied one is not mutated.
	client := *adapter.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	adapter.client = &client
	return adapter
}

// validateCallbackURL enforces the transport's URL fence. The callback URL is
// configuration, never route data, and unsafe forms are rejected before any
// request is built.
func validateCallbackURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%w: callback URL is not configured", ErrDeliveryNotAttempted)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: callback URL is malformed", ErrDeliveryNotAttempted)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: callback URL has no host", ErrDeliveryNotAttempted)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: callback URL must not carry userinfo", ErrDeliveryNotAttempted)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: callback URL must not carry a query or fragment", ErrDeliveryNotAttempted)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("%w: plain HTTP callback URL is only allowed on loopback", ErrDeliveryNotAttempted)
	default:
		return fmt.Errorf("%w: callback URL scheme %q is not allowed", ErrDeliveryNotAttempted, parsed.Scheme)
	}
}

// isLoopbackHost reports whether a host is a literal loopback address reserved
// for local operation.
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
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
	if a == nil {
		return DeliveryReceipt{State: StateFailed, Error: "adapter is not configured"}, fmt.Errorf("%w: nil adapter", ErrDeliveryNotAttempted)
	}
	if a.configErr != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: a.configErr.Error()}, a.configErr
	}
	route, err := sanitizeRouteIdentity(request.RouteIdentity)
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: err.Error()}, fmt.Errorf("%w: %w", ErrDeliveryNotAttempted, err)
	}
	request.RouteIdentity = route
	body, err := json.Marshal(request)
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("%w: marshal external coordination request: %w", ErrDeliveryNotAttempted, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.callbackURL, bytes.NewReader(body))
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed}, fmt.Errorf("%w: create external coordination request: %w", ErrDeliveryNotAttempted, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-GC-Coordination-Request-ID", request.RequestID)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	if a.authorization != "" {
		httpRequest.Header.Set("Authorization", a.authorization)
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		// The request may already have been observed by the coordinator. This
		// is deliberately not a definitive failure.
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("deliver external coordination request: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("read external coordination response: %w", err)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		// A redirect was returned rather than followed. The coordinator never
		// processed the request, so this is a definitive non-acceptance.
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: fmt.Sprintf("adapter returned redirect HTTP %d", response.StatusCode)}, nil
	}
	if response.StatusCode >= 500 {
		// A server error is ambiguous: the request may have been observed
		// before the failure, so it must be reconciled rather than retried.
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("adapter returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode >= 400 {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: fmt.Sprintf("adapter returned HTTP %d", response.StatusCode)}, nil
	}
	var receipt DeliveryReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("decode external coordination receipt: %w", err)
	}
	if receipt.RequestID != request.RequestID {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("external coordination receipt request_id %q does not match %q", receipt.RequestID, request.RequestID)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateUncertain}, fmt.Errorf("external coordination receipt causal fence does not match request")
	}
	if receipt.State == StateCompleted {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "adapter reported completion at delivery boundary"}, ErrPrematureCompletion
	}
	if receipt.State == "" {
		receipt.State = StateQueued
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
	// Fence, when set, is the currently configured target. It is re-checked
	// immediately before submission so a request admitted for an older target
	// fails closed instead of being delivered to a target that was never
	// authorized for it.
	Fence *Target
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
	queued, err := d.Queue.List(ctx, StateQueued)
	if err != nil {
		return nil, nil, err
	}
	if len(queued) == 0 {
		return nil, nil, nil
	}
	// Capability mismatch is a definitive pre-submission rejection: nothing was
	// ever sent, so failing cannot duplicate a delivery.
	if err := validateCapabilities(d.Adapter.Capabilities(), queued[0].Request); err != nil {
		_ = d.Queue.Fail(ctx, queued[0].ID, err, now)
		return nil, nil, err
	}
	if d.Fence != nil {
		if _, err := d.Queue.VerifyTargetFence(ctx, queued[0].ID, *d.Fence, now); err != nil {
			return nil, nil, err
		}
	}
	record, err := d.Queue.Claim(ctx, queued[0].ID, worker, now)
	if err != nil {
		return nil, nil, err
	}
	receipt, deliverErr := d.Adapter.Deliver(ctx, record.Request)
	if deliverErr != nil {
		// Fail closed toward uncertainty. Only an error that proves nothing was
		// transmitted may fail definitively; anything else may already have been
		// observed by the coordinator and must be reconciled before a retry or
		// fallback, or the recipient sees the same request twice.
		if errors.Is(deliverErr, ErrDeliveryNotAttempted) {
			_ = d.Queue.Fail(ctx, record.ID, deliverErr, now)
		} else {
			_ = d.Queue.MarkUncertain(ctx, record.ID, deliveryFailureClass(deliverErr), now)
		}
		return &record, &receipt, deliverErr
	}
	if receipt.State == StateUncertain {
		_ = d.Queue.MarkUncertain(ctx, record.ID, deliveryFailureClass(nil), now)
		return &record, &receipt, nil
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

// deliveryFailureClass reduces a transport error to a sanitized diagnostic
// label. The error text itself is not persisted, because it can carry a URL,
// a redirect location, or a credential echoed by the transport.
func deliveryFailureClass(err error) string {
	switch {
	case err == nil:
		return "adapter reported an untrustworthy receipt"
	case errors.Is(err, ErrPrematureCompletion):
		return "adapter claimed completion at the delivery boundary"
	case errors.Is(err, context.DeadlineExceeded):
		return "transport deadline exceeded after send"
	case errors.Is(err, context.Canceled):
		return "transport canceled after send"
	default:
		return "transport failed after send"
	}
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
