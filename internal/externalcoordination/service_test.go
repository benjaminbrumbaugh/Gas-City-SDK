package externalcoordination

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testRequestInput(now time.Time) RequestInput {
	return RequestInput{
		SourceAgent:    "orchestrator/primary",
		Target:         Target{TargetID: "coord-a", Adapter: "hermes", ConfigRevision: 7},
		City:           "city-a",
		WorkRef:        "gc-123",
		Reason:         ReasonEscalation,
		Prompt:         "Need a human decision about this blocked work.",
		CorrelationID:  "corr-123",
		IdempotencyKey: "idem-123",
		Now:            now,
	}
}

func TestEnqueueDefaultsToQueuedAndResumeOrCreate(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service := NewService(beads.NewMemStore())
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateQueued {
		t.Fatalf("state = %q, want queued", record.State)
	}
	if record.Request.DeliveryMode != DeliveryQueued {
		t.Fatalf("delivery mode = %q, want queued", record.Request.DeliveryMode)
	}
	if record.Request.SessionMode != SessionResumeOrCreate {
		t.Fatalf("session mode = %q, want resume_or_create", record.Request.SessionMode)
	}
	if record.Request.Target.LogicalRole != "external-coordination" {
		t.Fatalf("logical role = %q, want external-coordination", record.Request.Target.LogicalRole)
	}
}

func TestEnqueueRejectsBlankCorrelationID(t *testing.T) {
	service := NewService(beads.NewMemStore())
	input := testRequestInput(time.Now())
	input.CorrelationID = " 	 "

	if _, err := service.Enqueue(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Enqueue blank correlation_id error = %v, want ErrInvalidInput", err)
	}
}

func TestClaimRequiresAttemptAndCorrelationFences(t *testing.T) {
	now := time.Now()
	service := NewService(beads.NewMemStore())
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Request.Attempt != 1 {
		t.Fatalf("claimed request attempt = %d, want 1", claimed.Request.Attempt)
	}
	if err := service.Complete(context.Background(), record.ID, DeliveryReceipt{
		RequestID:     claimed.Request.RequestID,
		Attempt:       2,
		CorrelationID: claimed.Request.CorrelationID,
		State:         StateRunning,
	}, now.Add(2*time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Complete wrong attempt error = %v, want ErrInvalidInput", err)
	}
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: "wrong-correlation",
		ReceivedAt:    now.Add(2 * time.Second),
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RecordResponse wrong correlation error = %v, want ErrInvalidInput", err)
	}
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ReceivedAt:    now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("RecordResponse matching fence: %v", err)
	}
}

func TestEnqueueIsIdempotentAndPreservesCausalEnvelope(t *testing.T) {
	service := NewService(beads.NewMemStore())
	first, err := service.Enqueue(context.Background(), testRequestInput(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := testRequestInput(time.Now().Add(time.Minute))
	secondInput.Prompt = "A duplicate retry must not replace the original prompt."
	second, err := service.Enqueue(context.Background(), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Request.RequestID != first.Request.RequestID {
		t.Fatalf("idempotent enqueue created a new request: first=%+v second=%+v", first, second)
	}
	if second.Request.Prompt != first.Request.Prompt {
		t.Fatalf("idempotent retry changed prompt: %q", second.Request.Prompt)
	}
	if second.Request.WorkRef != "gc-123" || second.Request.CorrelationID != "corr-123" {
		t.Fatalf("causal envelope lost: %+v", second.Request)
	}
}

func TestClaimDeliveryAndResponseBoundaries(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(beads.NewMemStore())
	input := testRequestInput(now)
	input.IdempotencyKey = "boundary-test"
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != StateRunning || claimed.Attempt != 1 {
		t.Fatalf("claimed = %+v, want running attempt 1", claimed)
	}
	if err := service.Complete(context.Background(), record.ID, DeliveryReceipt{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		State:         StateQueued,
		Accepted:      true,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateRunning {
		t.Fatalf("after accepted delivery state = %q, want running", stored.State)
	}
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-1",
		State:         "answered",
		Summary:       "Proceed with the approved recovery path.",
		ReceivedAt:    now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	stored, err = service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateCompleted {
		t.Fatalf("after response state = %q, want completed", stored.State)
	}
}

func TestInterruptRequiresExplicitTargetPolicy(t *testing.T) {
	service := NewService(beads.NewMemStore())
	input := testRequestInput(time.Now())
	input.DeliveryMode = DeliveryInterrupt
	if _, err := service.Enqueue(context.Background(), input); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("interrupt enqueue error = %v, want explicit policy rejection", err)
	}
	input.Target.InterruptAllowed = true
	if _, err := service.Enqueue(context.Background(), input); err != nil {
		t.Fatalf("authorized interrupt enqueue: %v", err)
	}
}

func TestHTTPAdapterPreservesRequestIdentityAndRejectsPrematureCompletion(t *testing.T) {
	var got Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-GC-Coordination-Request-ID") == "" || r.Header.Get("Idempotency-Key") != "idem-123" {
			t.Errorf("identity headers missing: request-id=%q idempotency=%q", r.Header.Get("X-GC-Coordination-Request-ID"), r.Header.Get("Idempotency-Key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(DeliveryReceipt{RequestID: got.RequestID, Attempt: got.Attempt, CorrelationID: got.CorrelationID, State: StateQueued, Accepted: true})
	}))
	defer server.Close()

	input := testRequestInput(time.Now())
	service := NewService(beads.NewMemStore())
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewHTTPAdapter("hermes", server.URL, Capability{CanResumeSession: true, CanSubmitPrompt: true})
	receipt, err := adapter.Deliver(context.Background(), record.Request)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != StateQueued || got.RequestID != record.Request.RequestID || got.WorkRef != "gc-123" {
		t.Fatalf("receipt=%+v request=%+v", receipt, got)
	}

	premature := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(DeliveryReceipt{RequestID: record.Request.RequestID, Attempt: record.Request.Attempt, CorrelationID: record.Request.CorrelationID, State: StateCompleted})
	}))
	defer premature.Close()
	adapter = NewHTTPAdapter("bad", premature.URL, Capability{})
	if _, err := adapter.Deliver(context.Background(), record.Request); err == nil {
		t.Fatal("premature completion was accepted")
	}
}

func TestHTTPAdapterRejectsOmittedOrMismatchedReceiptFence(t *testing.T) {
	request := Request{RequestID: "request-1", Attempt: 7, CorrelationID: "corr-1"}
	var responseBody string
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Header:     make(http.Header),
		}, nil
	})}
	adapter := NewHTTPAdapter("hermes", "https://coordination.invalid/callback", Capability{})
	adapter.client = client

	tests := []struct {
		name    string
		receipt string
		wantErr bool
	}{
		{name: "matching", receipt: `{"request_id":"request-1","attempt":7,"correlation_id":"corr-1","state":"queued","accepted":true}`},
		{name: "omitted request_id", receipt: `{"attempt":7,"correlation_id":"corr-1","state":"queued","accepted":true}`, wantErr: true},
		{name: "mismatched request_id", receipt: `{"request_id":"other-request","attempt":7,"correlation_id":"corr-1","state":"queued","accepted":true}`, wantErr: true},
		{name: "omitted attempt", receipt: `{"request_id":"request-1","correlation_id":"corr-1","state":"queued","accepted":true}`, wantErr: true},
		{name: "mismatched attempt", receipt: `{"request_id":"request-1","attempt":8,"correlation_id":"corr-1","state":"queued","accepted":true}`, wantErr: true},
		{name: "omitted correlation_id", receipt: `{"request_id":"request-1","attempt":7,"state":"queued","accepted":true}`, wantErr: true},
		{name: "mismatched correlation_id", receipt: `{"request_id":"request-1","attempt":7,"correlation_id":"other-correlation","state":"queued","accepted":true}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responseBody = tt.receipt
			receipt, err := adapter.Deliver(context.Background(), request)
			if tt.wantErr {
				if err == nil || receipt.State != StateFailed {
					t.Fatalf("Deliver() receipt=%+v err=%v, want failed receipt and error", receipt, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Deliver() error = %v", err)
			}
			if receipt.RequestID != request.RequestID || receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
				t.Fatalf("Deliver() receipt fence = (%q, %d, %q), want (%q, %d, %q)", receipt.RequestID, receipt.Attempt, receipt.CorrelationID, request.RequestID, request.Attempt, request.CorrelationID)
			}
		})
	}
}

func TestDispatcherMarksRejectedDeliveryFailed(t *testing.T) {
	service := NewService(beads.NewMemStore())
	record, err := service.Enqueue(context.Background(), testRequestInput(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewHTTPAdapter("rejecting", "http://127.0.0.1:1", Capability{})
	dispatcher := Dispatcher{Queue: service, Adapter: adapter, Worker: "test-dispatcher"}
	_, _, _ = dispatcher.DeliverNext(context.Background(), time.Now())
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateFailed {
		t.Fatalf("state = %q, want failed", stored.State)
	}
}
