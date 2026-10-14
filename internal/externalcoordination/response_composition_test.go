package externalcoordination

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type responseCompositionStore struct {
	beads.Store
	writer beads.ConditionalWriter
}

func (s *responseCompositionStore) CreateDeterministic(key string, bead beads.Bead) (beads.Bead, bool, error) {
	return beads.CreateDeterministically(s.Store, key, bead)
}

func (s *responseCompositionStore) SupportsDeterministicCreate() bool {
	return beads.SupportsDeterministicCreate(s.Store)
}

func (s *responseCompositionStore) ConditionalWriterHandle() (beads.ConditionalWriter, bool) {
	return s.writer, s.writer != nil
}

type failOnceResponseSettlementWriter struct {
	beads.ConditionalWriter
	fail atomic.Bool
}

func (w *failOnceResponseSettlementWriter) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	if w.fail.CompareAndSwap(true, false) {
		return errors.New("injected response settlement failure")
	}
	return w.ConditionalWriter.UpdateIfMatch(id, revision, opts)
}

func compositionTestNow() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func TestResponseComposerPersistsBodyBeforeSettlementAndRetriesExactly(t *testing.T) {
	now := compositionTestNow()
	base := beads.NewMemStore()
	initial := NewService(base)
	input := testRequestInput(now)
	input.ContentRetention = RetentionDurable
	input.ResultDestination = "origin-conversation-a"
	input.RouteIdentity = map[string]string{"origin.owner": "owner-1"}
	created, err := initial.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := initial.Claim(context.Background(), created.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-composition-1",
		State:         "answered",
		Summary:       "the complete answer survives the settlement interruption",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	failingWriter := &failOnceResponseSettlementWriter{ConditionalWriter: base}
	failingWriter.fail.Store(true)
	store := &responseCompositionStore{Store: base, writer: failingWriter}
	handoff := NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
	composer := NewResponseComposer(NewService(store), handoff)

	first, err := composer.Record(context.Background(), response)
	if err == nil || !strings.Contains(err.Error(), "injected response settlement failure") {
		t.Fatalf("first composition error = %v, want injected settlement failure", err)
	}
	if first.Handoff == nil {
		t.Fatal("first composition lost the durable handoff result")
	}
	if first.Handoff.Request.ResultDestination != input.ResultDestination {
		t.Fatalf("handoff destination = %q, want stored origin %q", first.Handoff.Request.ResultDestination, input.ResultDestination)
	}
	storedHandoff, err := handoff.Get(context.Background(), first.Handoff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedHandoff.Response.Summary != response.Summary {
		t.Fatalf("durable body = %q, want %q", storedHandoff.Response.Summary, response.Summary)
	}
	storedRequest, err := NewService(store).Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRequest.State != StateRunning {
		t.Fatalf("request state after interrupted settlement = %q, want running", storedRequest.State)
	}

	retried, err := composer.Record(context.Background(), response)
	if err != nil {
		t.Fatalf("retry composition: %v", err)
	}
	if retried.Handoff == nil || retried.Handoff.ID != first.Handoff.ID {
		t.Fatalf("retry handoff = %+v, want original id %q", retried.Handoff, first.Handoff.ID)
	}
	completed, err := NewService(store).Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != StateCompleted {
		t.Fatalf("request state after retry = %q, want completed", completed.State)
	}
	items, err := store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("response handoff count = %d, want one idempotent record", len(items))
	}

	replay, err := composer.Record(context.Background(), response)
	if err != nil {
		t.Fatalf("exact response replay: %v", err)
	}
	if replay.Handoff == nil || replay.Handoff.ID != first.Handoff.ID {
		t.Fatalf("replay handoff = %+v, want original id %q", replay.Handoff, first.Handoff.ID)
	}
}

func TestResponseComposerPreservesTerminalOutcomeAndStoredOriginFence(t *testing.T) {
	for _, state := range []string{"refused", "failed", "expired"} {
		t.Run(state, func(t *testing.T) {
			now := compositionTestNow()
			store := beads.NewMemStore()
			service := NewService(store)
			input := testRequestInput(now)
			input.ResultDestination = "origin-conversation-" + state
			input.ContentRetention = RetentionDurable
			created, err := service.Enqueue(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := service.Claim(context.Background(), created.ID, "dispatcher-"+state, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			response := Response{
				RequestID:     claimed.Request.RequestID,
				Attempt:       claimed.Request.Attempt,
				CorrelationID: claimed.Request.CorrelationID,
				ResponseID:    "response-" + state,
				State:         state,
				Summary:       "truthful terminal outcome: " + state,
				ReceivedAt:    now.Add(2 * time.Second),
			}
			handoff := NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
			result, err := NewResponseComposer(service, handoff).Record(context.Background(), response)
			if err != nil {
				t.Fatal(err)
			}
			if result.Handoff == nil || result.Handoff.Response.State != state {
				t.Fatalf("handoff outcome = %+v, want %q", result.Handoff, state)
			}
			if result.Handoff.Request.ResultDestination != input.ResultDestination {
				t.Fatalf("handoff origin = %q, want stored %q", result.Handoff.Request.ResultDestination, input.ResultDestination)
			}
			completed, err := service.Get(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if completed.State != StateCompleted {
				t.Fatalf("request state = %q, want completed", completed.State)
			}
		})
	}
}

func TestResponseComposerReplaysTerminalSummaryWithExactCommitment(t *testing.T) {
	now := compositionTestNow()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.ResultDestination = "origin-conversation-exact-bytes"
	created, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), created.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-exact-bytes",
		State:         "failed",
		Summary:       strings.Repeat("a", 127) + "é",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	normalized, err := normalizeComposedResponse(response, claimed)
	if err != nil {
		t.Fatal(err)
	}
	handoff := NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
	composer := NewResponseComposer(service, handoff)
	first, err := composer.Record(context.Background(), response)
	if err != nil {
		t.Fatal(err)
	}
	if first.Handoff == nil {
		t.Fatal("first composition did not return a durable handoff")
	}
	if first.Handoff.Response.Summary != normalized.Summary {
		t.Fatalf("stored handoff summary = %q, want canonical composer summary %q", first.Handoff.Response.Summary, normalized.Summary)
	}
	commitment, err := responseCommitment(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if first.Handoff.ResponseCommitment != commitment {
		t.Fatalf("handoff commitment = %q, want composer commitment %q", first.Handoff.ResponseCommitment, commitment)
	}
	replay, err := composer.Record(context.Background(), response)
	if err != nil {
		t.Fatalf("exact terminal response replay: %v", err)
	}
	if replay.Handoff == nil || replay.Handoff.ID != first.Handoff.ID {
		t.Fatalf("replay handoff = %+v, want original id %q", replay.Handoff, first.Handoff.ID)
	}
	stored, err := handoff.Get(context.Background(), first.Handoff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ResponseCommitment != responseCommitmentForTest(t, stored.Response) {
		t.Fatalf("stored handoff commitment does not match stored response: %q", stored.ResponseCommitment)
	}
}

func responseCommitmentForTest(t *testing.T, response Response) string {
	t.Helper()
	commitment, err := responseCommitment(response)
	if err != nil {
		t.Fatal(err)
	}
	return commitment
}

func TestResponseComposerRecordsWithoutOriginHandoff(t *testing.T) {
	now := compositionTestNow()
	store := beads.NewMemStore()
	service := NewService(store)
	created, err := service.Enqueue(context.Background(), testRequestInput(now))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), created.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewResponseComposer(service, nil).Record(context.Background(), Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-no-origin",
		State:         "answered",
		Summary:       "no origin transport requested",
		ReceivedAt:    now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handoff != nil {
		t.Fatalf("handoff = %+v, want nil without result destination", result.Handoff)
	}
	completed, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != StateCompleted {
		t.Fatalf("request state = %q, want completed", completed.State)
	}
}

func TestResponseComposerRefusesCompletedRequestWithoutDurableHandoff(t *testing.T) {
	now := compositionTestNow()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.ResultDestination = "origin-conversation-missing"
	created, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), created.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	response := Response{
		RequestID:     claimed.Request.RequestID,
		Attempt:       claimed.Request.Attempt,
		CorrelationID: claimed.Request.CorrelationID,
		ResponseID:    "response-without-handoff",
		State:         "answered",
		Summary:       "the commitment alone is not a recoverable body",
		ReceivedAt:    now.Add(2 * time.Second),
	}
	if err := service.RecordResponse(context.Background(), response); err != nil {
		t.Fatal(err)
	}
	handoff := NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
	if _, err := NewResponseComposer(service, handoff).Record(context.Background(), response); !errors.Is(err, ErrHandoffUnavailable) {
		t.Fatalf("completed response without handoff error = %v, want ErrHandoffUnavailable", err)
	}
	items, err := store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("handoff count after refusing commitment-only recovery = %d, want zero", len(items))
	}
}

func TestResponseComposerRejectsStaleAttemptBeforeHandoff(t *testing.T) {
	now := compositionTestNow()
	store := beads.NewMemStore()
	service := NewService(store)
	input := testRequestInput(now)
	input.ResultDestination = "origin-conversation-successor"
	created, err := service.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(context.Background(), created.ID, "dispatcher-a", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Requeue(context.Background(), created.ID, errors.New("transient delivery"), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	second, err := service.Claim(context.Background(), created.ID, "dispatcher-b", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Request.Attempt == first.Request.Attempt {
		t.Fatalf("successor claim attempt = %d, want different from stale %d", second.Request.Attempt, first.Request.Attempt)
	}
	stale := Response{
		RequestID:     first.Request.RequestID,
		Attempt:       first.Request.Attempt,
		CorrelationID: first.Request.CorrelationID,
		ResponseID:    "stale-response",
		State:         "answered",
		Summary:       "must not reach the successor origin",
		ReceivedAt:    now.Add(4 * time.Second),
	}
	handoff := NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
	if _, err := NewResponseComposer(service, handoff).Record(context.Background(), stale); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("stale response error = %v, want ErrInvalidInput", err)
	}
	items, err := store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("handoff count after stale response = %d, want zero", len(items))
	}
}
