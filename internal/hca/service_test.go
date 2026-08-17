package hca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

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
	if record.Request.Target.LogicalRole != "human-coordinator" {
		t.Fatalf("logical role = %q, want human-coordinator", record.Request.Target.LogicalRole)
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
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
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
		RequestID: record.Request.RequestID,
		State:     StateQueued,
		Accepted:  true,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateQueued {
		t.Fatalf("after accepted delivery state = %q, want queued", stored.State)
	}
	if err := service.RecordResponse(context.Background(), Response{
		RequestID:  record.Request.RequestID,
		ResponseID: "response-1",
		State:      "answered",
		Summary:    "Proceed with the approved recovery path.",
		ReceivedAt: now.Add(3 * time.Second),
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
		if r.Header.Get("X-HCA-Request-ID") == "" || r.Header.Get("Idempotency-Key") != "idem-123" {
			t.Errorf("identity headers missing: request-id=%q idempotency=%q", r.Header.Get("X-HCA-Request-ID"), r.Header.Get("Idempotency-Key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(DeliveryReceipt{RequestID: got.RequestID, State: StateQueued, Accepted: true})
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
		_ = json.NewEncoder(w).Encode(DeliveryReceipt{RequestID: record.Request.RequestID, State: StateCompleted})
	}))
	defer premature.Close()
	adapter = NewHTTPAdapter("bad", premature.URL, Capability{})
	if _, err := adapter.Deliver(context.Background(), record.Request); err == nil {
		t.Fatal("premature completion was accepted")
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
