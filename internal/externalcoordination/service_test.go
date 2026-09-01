package externalcoordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type responseRaceStore struct {
	beads.Store
	writer  beads.ConditionalWriter
	arrived chan struct{}
	release chan struct{}
	lists   atomic.Int32
}

func (s *responseRaceStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	items, err := s.Store.List(query)
	if s.lists.Add(1) <= 2 {
		s.arrived <- struct{}{}
		<-s.release
	}
	return items, err
}

func (s *responseRaceStore) ConditionalWriterHandle() (beads.ConditionalWriter, bool) {
	return s.writer, s.writer != nil
}

type noConditionalResponseStore struct{ beads.Store }

type failOnceScrubStore struct {
	beads.Store
	writer beads.ConditionalWriter
	fail   atomic.Bool
}

func (s *failOnceScrubStore) Update(id string, opts beads.UpdateOpts) error {
	if opts.Description != nil && s.fail.CompareAndSwap(true, false) {
		return errors.New("injected scrub failure")
	}
	return s.Store.Update(id, opts)
}

func (s *failOnceScrubStore) ConditionalWriterHandle() (beads.ConditionalWriter, bool) {
	return s.writer, s.writer != nil
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

func TestRecordResponseExactReplaySurvivesServiceRestart(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Request.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-restart-1",
		State:            "answered",
		Summary:          "sensitive answer content",
		ContentRetention: RetentionEphemeral,
		ReceivedAt:       now.Add(2 * time.Second),
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("first RecordResponse: %v", err)
	}

	restarted := NewService(store)
	if err := restarted.RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("exact replay after service restart: %v", err)
	}
	stored, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	commitment := stored.Metadata["external_coordination.response_commitment"]
	if !strings.HasPrefix(commitment, "sha256:") || len(commitment) != len("sha256:")+64 {
		t.Fatalf("response commitment = %q, want canonical SHA-256 commitment", commitment)
	}
	if strings.Contains(fmt.Sprint(stored.Metadata), response.Summary) {
		t.Fatalf("ephemeral response summary persisted in metadata: %v", stored.Metadata)
	}
}

func TestRecordResponsePersistsCommitmentWithoutDurableResponseContent(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.ContentRetention = RetentionDurable
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:        claimed.Request.RequestID,
		Attempt:          claimed.Attempt,
		CorrelationID:    claimed.Request.CorrelationID,
		ResponseID:       "response-durable-commitment",
		State:            "answered",
		Summary:          "durable but sensitive response content",
		ContentRetention: RetentionDurable,
		ReceivedAt:       now.Add(2 * time.Second),
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Metadata[metadataResponseCommitment] == "" || stored.Metadata["external_coordination.response_id"] != response.ResponseID {
		t.Fatalf("durable response identity = %v", stored.Metadata)
	}
	if strings.Contains(fmt.Sprint(stored.Metadata), response.Summary) {
		t.Fatalf("raw response summary persisted instead of commitment: %v", stored.Metadata)
	}
}

func TestRecordResponseRetriesRequiredEphemeralScrubAfterTerminalCommit(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	writer, ok := beads.ConditionalWriterFor(store)
	if !ok {
		t.Fatal("MemStore lost conditional writer")
	}
	failing := &failOnceScrubStore{Store: store, writer: writer}
	failing.fail.Store(true)
	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-scrub-retry",
		State:         "answered",
		Summary:       "ephemeral response content",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	if err := NewService(failing).RecordResponse(context.Background(), response); err == nil || !strings.Contains(err.Error(), "injected scrub failure") {
		t.Fatalf("first RecordResponse error = %v, want injected scrub failure", err)
	}
	committed, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Metadata[metadataState] != string(StateCompleted) || committed.Metadata[metadataResponseCommitment] == "" {
		t.Fatalf("terminal response was not committed before scrub: %v", committed.Metadata)
	}
	if committed.Metadata["external_coordination.response_scrub_pending"] != "true" {
		t.Fatalf("scrub pending marker = %q, want true", committed.Metadata["external_coordination.response_scrub_pending"])
	}

	if err := NewService(failing).RecordResponse(context.Background(), response); err != nil {
		t.Fatalf("exact replay did not retry scrub: %v", err)
	}
	scrubbed, err := NewService(store).Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scrubbed.Request.Prompt != "" {
		t.Fatalf("prompt after scrub retry = %q, want empty", scrubbed.Request.Prompt)
	}
	stored, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Metadata["external_coordination.response_scrub_pending"] != "" {
		t.Fatalf("scrub pending marker after retry = %q, want cleared", stored.Metadata["external_coordination.response_scrub_pending"])
	}
}

func TestRecordResponseFailsClosedWithoutConditionalWriter(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-unsupported",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	err = NewService(noConditionalResponseStore{Store: store}).RecordResponse(context.Background(), response)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("RecordResponse without conditional writer error = %v, want ErrUnavailable", err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateRunning {
		t.Fatalf("state after unsupported response write = %q, want running", stored.State)
	}
}

func TestRecordResponseResolvesConditionalWriterThroughDeclaredStoreWrapper(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	record, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	// WorkStore has the same capability-hiding Store embedding and declared
	// resolution target as the production city bead-policy wrapper.
	wrapped := beads.WorkStore{Store: store}
	err = NewService(wrapped).RecordResponse(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-policy-wrapper",
		State:         "answered",
		Summary:       "wrapped store response",
		ReceivedAt:    now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("RecordResponse through declared store wrapper: %v", err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateCompleted {
		t.Fatalf("state after wrapped response = %q, want completed", stored.State)
	}
}

func TestRecordResponseConcurrentOutcomesHaveOneWinner(t *testing.T) {
	now := time.Now().UTC()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.IdempotencyKey = "response-race"
	record, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), record.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	writer, ok := beads.ConditionalWriterFor(store)
	if !ok {
		t.Fatal("MemStore lost conditional writer")
	}
	raceStore := &responseRaceStore{
		Store:   store,
		writer:  writer,
		arrived: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	responses := []Response{
		{RequestID: claimed.Request.RequestID, Attempt: claimed.Attempt, CorrelationID: claimed.Request.CorrelationID, ResponseID: "response-a", State: "answered", Summary: "answer a", ReceivedAt: now.Add(2 * time.Second)},
		{RequestID: claimed.Request.RequestID, Attempt: claimed.Attempt, CorrelationID: claimed.Request.CorrelationID, ResponseID: "response-b", State: "answered", Summary: "answer b", ReceivedAt: now.Add(2 * time.Second)},
	}
	results := make(chan error, len(responses))
	for _, response := range responses {
		go func(response Response) {
			results <- NewService(raceStore).RecordResponse(context.Background(), response)
		}(response)
	}
	<-raceStore.arrived
	<-raceStore.arrived
	close(raceStore.release)

	var succeeded, conflicted int
	for range responses {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrNotQueued):
			conflicted++
		default:
			t.Fatalf("RecordResponse race error = %v, want success or ErrNotQueued", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("race results = %d success, %d conflict; want one each", succeeded, conflicted)
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
