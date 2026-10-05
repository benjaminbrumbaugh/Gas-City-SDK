package externalcoordination

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/extmsg"
)

func testHandoffRequest(now time.Time) Request {
	return Request{
		RequestID:         "request-origin-1",
		Attempt:           1,
		SourceAgent:       "agent/source",
		Target:            Target{TargetID: "coordinator-1", Provider: "hermes", AccountID: "desktop"},
		Reason:            ReasonDirectRequest,
		CorrelationID:     "correlation-origin-1",
		ResultDestination: "origin-conversation-a",
		RouteIdentity:     map[string]string{"origin.owner": "owner-1"},
		ContentRetention:  RetentionEphemeral,
		ExpiresAt:         now.Add(time.Hour),
		CreatedAt:         now,
	}
}

func testHandoffResponse(request Request, summary string) Response {
	return Response{
		RequestID:        request.RequestID,
		Attempt:          request.Attempt,
		CorrelationID:    request.CorrelationID,
		ResponseID:       "response-origin-1",
		State:            "answered",
		Summary:          summary,
		ContentRetention: RetentionEphemeral,
		ReceivedAt:       request.CreatedAt.Add(time.Minute),
	}
}

func testOriginResolver(_ context.Context, request HandoffRequest, _ Response) (OriginDestination, error) {
	if strings.TrimSpace(request.ResultDestination) == "" {
		return OriginDestination{}, errors.New("result destination is required")
	}
	return OriginDestination{
		SessionID: request.ResultDestination,
		Conversation: extmsg.ConversationRef{
			ScopeID:        "city-1",
			Provider:       "hermes",
			AccountID:      "desktop",
			ConversationID: request.ResultDestination,
			Kind:           extmsg.ConversationDM,
		},
		Caller: extmsg.Caller{Kind: extmsg.CallerController, ID: request.SourceAgent},
	}, nil
}

func TestResponseHandoffPublishesOnceToExplicitDestination(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	response := testHandoffResponse(request, "exact answer from coordinator")
	var published []OriginHandoffRequest
	handoff := NewResponseHandoff(beads.NewMemStore(), testOriginResolver, OriginHandoffPort{
		Publish: func(_ context.Context, input OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			published = append(published, input)
			return &extmsg.PublishReceipt{
				MessageID:    "origin-message-1",
				Conversation: input.Destination.Conversation,
				Accepted:     true,
				Delivered:    true,
			}, nil
		},
	})

	queued, err := handoff.Enqueue(context.Background(), request, response)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != HandoffQueued {
		t.Fatalf("queued state = %q, want queued", queued.State)
	}
	if queued.Key == "" {
		t.Fatal("handoff key is empty")
	}

	delivered, err := handoff.Deliver(context.Background(), queued.ID, "handoff-worker", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if delivered.State != HandoffDelivered {
		t.Fatalf("delivered state = %q, want delivered", delivered.State)
	}
	if len(published) != 1 {
		t.Fatalf("publish count = %d, want 1", len(published))
	}
	if published[0].Response.Summary != response.Summary {
		t.Fatalf("published summary = %q, want %q", published[0].Response.Summary, response.Summary)
	}
	if published[0].Destination.Conversation.ConversationID != request.ResultDestination {
		t.Fatalf("published conversation = %+v, want %q", published[0].Destination.Conversation, request.ResultDestination)
	}
	if published[0].IdempotencyKey != queued.Key {
		t.Fatalf("publish idempotency key = %q, want %q", published[0].IdempotencyKey, queued.Key)
	}

	replay, err := handoff.Enqueue(context.Background(), request, response)
	if err != nil {
		t.Fatalf("exact enqueue replay: %v", err)
	}
	if replay.ID != queued.ID || replay.Key != queued.Key {
		t.Fatalf("replay = %+v, want original id=%q key=%q", replay, queued.ID, queued.Key)
	}
	if _, err := handoff.Deliver(context.Background(), queued.ID, "different-worker", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("delivering already terminal handoff: %v", err)
	}
	if len(published) != 1 {
		t.Fatalf("replay publish count = %d, want 1", len(published))
	}

	stored, err := handoff.Get(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Response.Summary != "" {
		t.Fatalf("ephemeral response summary retained after delivery: %q", stored.Response.Summary)
	}
	if stored.ResponseCommitment == "" {
		t.Fatal("response commitment was scrubbed with response content")
	}
}

func TestResponseHandoffRejectsConflictingReplayAndUnsafeRouteIdentity(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	handoff := NewResponseHandoff(beads.NewMemStore(), testOriginResolver, OriginHandoffPort{
		Publish: func(_ context.Context, _ OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			return &extmsg.PublishReceipt{Accepted: true, Delivered: true}, nil
		},
	})
	response := testHandoffResponse(request, "first answer")
	if _, err := handoff.Enqueue(context.Background(), request, response); err != nil {
		t.Fatal(err)
	}
	conflict := response
	conflict.Summary = "different answer for the same identity"
	if _, err := handoff.Enqueue(context.Background(), request, conflict); !errors.Is(err, ErrHandoffConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrHandoffConflict", err)
	}
	otherDestination := request
	otherDestination.ResultDestination = "origin-conversation-b"
	if _, err := handoff.Enqueue(context.Background(), otherDestination, testHandoffResponse(otherDestination, "cross-destination answer")); !errors.Is(err, ErrHandoffConflict) {
		t.Fatalf("cross-destination replay error = %v, want ErrHandoffConflict", err)
	}

	unsafe := request
	unsafe.RequestID = "request-unsafe-route"
	unsafe.RouteIdentity = map[string]string{"callback_url": "https://attacker.invalid/callback"}
	if _, err := handoff.Enqueue(context.Background(), unsafe, testHandoffResponse(unsafe, "answer")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe route identity error = %v, want ErrInvalidInput", err)
	}
}

func TestResponseHandoffRejectsCrossDestinationResolution(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	response := testHandoffResponse(request, "do not cross owners")
	var publishes int
	handoff := NewResponseHandoff(beads.NewMemStore(), func(context.Context, HandoffRequest, Response) (OriginDestination, error) {
		return OriginDestination{}, fmt.Errorf("destination belongs to another owner")
	}, OriginHandoffPort{
		Publish: func(_ context.Context, _ OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			publishes++
			return &extmsg.PublishReceipt{Accepted: true, Delivered: true}, nil
		},
	})
	queued, err := handoff.Enqueue(context.Background(), request, response)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := handoff.Deliver(context.Background(), queued.ID, "handoff-worker", now.Add(time.Minute))
	if err == nil {
		t.Fatal("cross-owner destination unexpectedly delivered")
	}
	if failed.State != HandoffFailed {
		t.Fatalf("failed state = %q, want failed", failed.State)
	}
	if publishes != 0 {
		t.Fatalf("publish count = %d, want 0", publishes)
	}
}

func TestResponseHandoffReconcilesAmbiguousDeliveryWithoutRepublish(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	response := testHandoffResponse(request, "answer after an ambiguous provider result")
	var publishes, reconciles int
	handoff := NewResponseHandoff(beads.NewMemStore(), testOriginResolver, OriginHandoffPort{
		Publish: func(_ context.Context, _ OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			publishes++
			return nil, errors.New("connection lost after provider acceptance")
		},
		Reconcile: func(_ context.Context, input OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			reconciles++
			if input.IdempotencyKey == "" {
				t.Fatal("reconcile lost idempotency key")
			}
			return &extmsg.PublishReceipt{MessageID: "origin-message-ambiguous", Accepted: true, Delivered: true}, nil
		},
	})
	queued, err := handoff.Enqueue(context.Background(), request, response)
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err := handoff.Deliver(context.Background(), queued.ID, "handoff-worker", now.Add(time.Minute))
	if !errors.Is(err, ErrHandoffUncertain) {
		t.Fatalf("ambiguous delivery error = %v, want ErrHandoffUncertain", err)
	}
	if uncertain.State != HandoffUncertain {
		t.Fatalf("uncertain state = %q, want uncertain", uncertain.State)
	}

	reconciled, err := handoff.Reconcile(context.Background(), queued.ID, "recovery-worker", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != HandoffReconciled {
		t.Fatalf("reconciled state = %q, want reconciled", reconciled.State)
	}
	if publishes != 1 || reconciles != 1 {
		t.Fatalf("publish/reconcile counts = %d/%d, want 1/1", publishes, reconciles)
	}
}

func TestResponseHandoffScrubsWhenRequestRetentionIsEphemeral(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	response := testHandoffResponse(request, "sensitive answer")
	response.ContentRetention = RetentionDurable
	handoff := NewResponseHandoff(beads.NewMemStore(), testOriginResolver, OriginHandoffPort{
		Publish: func(_ context.Context, input OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			if input.Response.Summary != response.Summary {
				t.Fatalf("published summary = %q, want %q", input.Response.Summary, response.Summary)
			}
			return &extmsg.PublishReceipt{Accepted: true, Delivered: true}, nil
		},
	})
	queued, err := handoff.Enqueue(context.Background(), request, response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handoff.Deliver(context.Background(), queued.ID, "handoff-worker", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stored, err := handoff.Get(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Response.Summary != "" {
		t.Fatalf("request-ephemeral response summary retained: %q", stored.Response.Summary)
	}
}

func TestResponseHandoffSanitizesDestinationFailure(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	handoff := NewResponseHandoff(beads.NewMemStore(), func(context.Context, HandoffRequest, Response) (OriginDestination, error) {
		return OriginDestination{}, errors.New("authorization failed for https://secret.invalid/?token=redacted")
	}, OriginHandoffPort{
		Publish: func(_ context.Context, _ OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			t.Fatal("publisher called after destination rejection")
			return nil, nil
		},
	})
	queued, err := handoff.Enqueue(context.Background(), request, testHandoffResponse(request, "answer"))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := handoff.Deliver(context.Background(), queued.ID, "handoff-worker", now.Add(time.Minute))
	if err == nil || failed.State != HandoffFailed {
		t.Fatalf("destination failure = record %+v, error %v; want failed record", failed, err)
	}
	if strings.Contains(failed.Error, "secret.invalid") || strings.Contains(failed.Error, "token") {
		t.Fatalf("durable failure leaked resolver details: %q", failed.Error)
	}
}

func TestResponseHandoffEnqueueIsAtomicAcrossInstances(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	response := testHandoffResponse(request, "one durable logical answer")
	store := beads.NewMemStore()
	newHandoff := func() *ResponseHandoff {
		return NewResponseHandoff(store, testOriginResolver, OriginHandoffPort{})
	}

	results := make(chan ResponseHandoffRecord, 2)
	errorsCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			record, err := newHandoff().Enqueue(context.Background(), request, response)
			results <- record
			errorsCh <- err
		}()
	}
	var ids []string
	for i := 0; i < 2; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, (<-results).ID)
	}
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("concurrent enqueue IDs = %q and %q, want one durable record", ids[0], ids[1])
	}
	items, err := store.List(beads.ListQuery{Label: responseHandoffLabel, IncludeClosed: true, AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("durable handoff count = %d, want 1", len(items))
	}
}

func TestResponseHandoffRecoveryFencesExpiredClaim(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	request := testHandoffRequest(now)
	handoff := NewResponseHandoff(beads.NewMemStore(), testOriginResolver, OriginHandoffPort{
		Publish: func(_ context.Context, _ OriginHandoffRequest) (*extmsg.PublishReceipt, error) {
			return &extmsg.PublishReceipt{Accepted: true, Delivered: true}, nil
		},
	})
	queued, err := handoff.Enqueue(context.Background(), request, testHandoffResponse(request, "answer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handoff.Claim(context.Background(), queued.ID, "crashed-worker", now); err != nil {
		t.Fatal(err)
	}
	recovered, err := handoff.Recover(context.Background(), now.Add(responseHandoffClaimLease+time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].State != HandoffUncertain {
		t.Fatalf("recovered = %+v, want one uncertain handoff", recovered)
	}
	if _, err := handoff.Deliver(context.Background(), queued.ID, "new-worker", now.Add(responseHandoffClaimLease+2*time.Second)); !errors.Is(err, ErrHandoffUncertain) {
		t.Fatalf("deliver after recovery error = %v, want ErrHandoffUncertain", err)
	}
}
