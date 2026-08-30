package externalcoordination

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/extmsg"
)

type fakeTransport struct {
	published extmsg.PublishRequest
	receipt   *extmsg.PublishReceipt
}

func (f *fakeTransport) Name() string { return "fake-external-coordination-transport" }
func (f *fakeTransport) Capabilities() extmsg.AdapterCapabilities {
	return extmsg.AdapterCapabilities{}
}

func (f *fakeTransport) VerifyAndNormalizeInbound(context.Context, extmsg.InboundPayload) (*extmsg.ExternalInboundMessage, error) {
	return nil, nil
}

func (f *fakeTransport) EnsureChildConversation(context.Context, extmsg.ConversationRef, string) (*extmsg.ConversationRef, error) {
	return nil, nil
}

func (f *fakeTransport) Publish(_ context.Context, request extmsg.PublishRequest) (*extmsg.PublishReceipt, error) {
	f.published = request
	if f.receipt != nil {
		return f.receipt, nil
	}
	return &extmsg.PublishReceipt{Accepted: true, Delivered: true, MessageID: "message-1"}, nil
}

func TestTransportAdapterAcceptsQueuedReceiptWithoutCompletingRequest(t *testing.T) {
	transport := &fakeTransport{receipt: &extmsg.PublishReceipt{
		Accepted:  true,
		Queued:    true,
		Delivered: false,
		MessageID: "queue-1",
	}}
	request := Request{
		RequestID:     "request-queued",
		Attempt:       3,
		Target:        Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Prompt:        "Need authorization.",
		CorrelationID: "corr-queued",
	}

	receipt, err := NewTransportAdapter(transport, "city-a").Deliver(context.Background(), request)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !receipt.Accepted || receipt.State != StateRunning {
		t.Fatalf("receipt = %+v, want accepted running", receipt)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		t.Fatalf("receipt causal fence = %+v", receipt)
	}
}

func TestTransportAdapterPreservesLegacyDeliveredReceiptCompatibility(t *testing.T) {
	transport := &fakeTransport{receipt: &extmsg.PublishReceipt{
		Delivered: true,
		MessageID: "message-legacy",
	}}
	request := Request{
		RequestID:     "request-legacy",
		Attempt:       2,
		Target:        Target{Provider: "slack", AccountID: "default", ConversationID: "conversation-legacy"},
		Prompt:        "Need a completed delivery.",
		CorrelationID: "corr-legacy",
	}

	receipt, err := NewTransportAdapter(transport, "city-a").Deliver(context.Background(), request)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !receipt.Accepted || receipt.State != StateRunning {
		t.Fatalf("receipt = %+v, want accepted running", receipt)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		t.Fatalf("receipt causal fence = %+v", receipt)
	}
}

func TestTransportAdapterRejectsPartialQueuedAcceptanceEvidence(t *testing.T) {
	tests := map[string]*extmsg.PublishReceipt{
		"accepted without queued": {Accepted: true},
		"queued without accepted": {Queued: true},
	}
	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			request := Request{
				RequestID:     "request-partial",
				Target:        Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
				Prompt:        "Need authorization.",
				CorrelationID: "corr-partial",
			}

			receipt, err := NewTransportAdapter(&fakeTransport{receipt: result}, "city-a").Deliver(context.Background(), request)
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if receipt.Accepted || receipt.State != StateFailed {
				t.Fatalf("receipt = %+v, want rejected failed", receipt)
			}
		})
	}
}

func TestTransportAdapterPublishesCausallyLinkedRequest(t *testing.T) {
	transport := &fakeTransport{}
	adapter := NewTransportAdapter(transport, "city-a")
	request := Request{
		RequestID:      "request-1",
		Attempt:        1,
		SourceAgent:    "mayor",
		Target:         Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:         ReasonEscalation,
		Prompt:         "Need authorization.",
		WorkRef:        "gc-123",
		CorrelationID:  "corr-1",
		IdempotencyKey: "idem-1",
	}
	receipt, err := adapter.Deliver(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Accepted || receipt.State != StateRunning {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.Attempt != request.Attempt || receipt.CorrelationID != request.CorrelationID {
		t.Fatalf("receipt causal fence = %+v", receipt)
	}
	if transport.published.Conversation.Provider != "hermes" || transport.published.Conversation.AccountID != "desktop" || transport.published.Conversation.ConversationID != "conversation-1" {
		t.Fatalf("conversation = %+v", transport.published.Conversation)
	}
	if transport.published.IdempotencyKey != "idem-1" || transport.published.Metadata["work_ref"] != "gc-123" || transport.published.Metadata["correlation_id"] != "corr-1" {
		t.Fatalf("causal metadata = %+v", transport.published.Metadata)
	}
}

func TestTransportAdapterSendsBoundedBridgeMetadataWithRequestFences(t *testing.T) {
	transport := &fakeTransport{}
	longValue := strings.Repeat("x", 2048)
	request := Request{
		RequestID:        "request-7",
		Attempt:          7,
		SourceAgent:      longValue,
		Target:           Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:           ReasonEscalation,
		Prompt:           "Need authorization.",
		WorkRef:          longValue,
		CorrelationID:    "corr-7",
		ContentRetention: RetentionDurable,
	}

	if _, err := NewTransportAdapter(transport, "city-a").Deliver(context.Background(), request); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := transport.published.Metadata["coordination_attempt"]; got != "7" {
		t.Fatalf("coordination_attempt = %q, want 7", got)
	}
	if got := transport.published.Metadata["content_retention"]; got != string(RetentionDurable) {
		t.Fatalf("content_retention = %q, want %q", got, RetentionDurable)
	}
	if transport.published.Metadata["coordination_request_id"] != request.RequestID || transport.published.Metadata["correlation_id"] != request.CorrelationID {
		t.Fatalf("causal metadata = %+v", transport.published.Metadata)
	}
	for key, value := range transport.published.Metadata {
		if len(value) > 512 {
			t.Fatalf("metadata[%q] length = %d, want <= 512", key, len(value))
		}
	}
}

func TestDispatcherDeliversRegisteredTransportAndLeavesCompletionOpen(t *testing.T) {
	service := NewService(beads.NewMemStore())
	now := time.Now().UTC()
	record, err := service.Enqueue(context.Background(), RequestInput{
		SourceAgent:    "mayor",
		Target:         Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:         ReasonLargeSummary,
		Prompt:         "Summary.",
		CorrelationID:  "corr-dispatch",
		IdempotencyKey: "idem-dispatch",
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Request.ContentRetention != RetentionEphemeral {
		t.Fatalf("default content retention = %q, want ephemeral", record.Request.ContentRetention)
	}
	dispatcher := Dispatcher{Queue: service, Adapter: NewTransportAdapter(&fakeTransport{}, "city-a"), Worker: "worker-1"}
	if _, receipt, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	} else if receipt == nil || receipt.State != StateRunning {
		t.Fatalf("receipt = %+v", receipt)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateRunning {
		t.Fatalf("state = %q, want running until external coordination response", stored.State)
	}
}

func TestEphemeralContentIsScrubbedAfterDelivery(t *testing.T) {
	service := NewService(beads.NewMemStore())
	record, err := service.Enqueue(context.Background(), RequestInput{
		SourceAgent:      "mayor",
		Target:           Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:           ReasonDirectRequest,
		Prompt:           "One-off question that need not become durable knowledge.",
		ContentRetention: RetentionEphemeral,
		CorrelationID:    "corr-ephemeral",
		Now:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewTransportAdapter(&fakeTransport{}, "city-a")
	if _, _, err := (&Dispatcher{Queue: service, Adapter: adapter}).DeliverNext(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Request.Prompt != "" {
		t.Fatalf("prompt retained after ephemeral delivery: %q", stored.Request.Prompt)
	}
}

// TestDispatcherRequeuesTransientDeliveryFailureInsteadOfDestroyingRequest is
// the regression for a request being destroyed by an adapter outage. The
// adapter registration lives in controller memory and outlives the adapter
// PROCESS, so a bridge that crashes leaves a registration pointing at a dead
// callback. Delivery then fails as "transient" -- a verdict the transport
// itself makes -- and the request used to be marked terminally failed. Only
// queued records are ever dispatched again, so that was unrecoverable: the
// request was gone, and no later restart could bring it back.
func TestDispatcherRequeuesTransientDeliveryFailureInsteadOfDestroyingRequest(t *testing.T) {
	service := NewService(beads.NewMemStore())
	now := time.Now().UTC()
	record, err := service.Enqueue(context.Background(), RequestInput{
		SourceAgent:    "mayor",
		Target:         Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:         ReasonDirectRequest,
		Prompt:         "Is anyone home?",
		CorrelationID:  "corr-transient",
		IdempotencyKey: "idem-transient",
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The bridge process is gone; its registration is not. The transport
	// classifies the unreachable callback as transient.
	down := &fakeTransport{receipt: &extmsg.PublishReceipt{FailureKind: extmsg.PublishFailureTransient}}
	dispatcher := Dispatcher{Queue: service, Adapter: NewTransportAdapter(down, "city-a"), Worker: "worker-1"}
	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("DeliverNext error = %v, want ErrUnavailable", err)
	}

	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("causal record was lost after a transient failure: %v", err)
	}
	if stored.State != StateQueued {
		t.Fatalf("state after transient failure = %q, want queued so a later trigger can retry", stored.State)
	}
	if stored.Attempt != 1 {
		t.Fatalf("attempt after transient failure = %d, want 1; the try happened and must stay recorded", stored.Attempt)
	}

	// The bridge comes back. The same request must now deliver.
	up := &fakeTransport{}
	recovered := Dispatcher{Queue: service, Adapter: NewTransportAdapter(up, "city-a"), Worker: "worker-1"}
	if _, receipt, err := recovered.DeliverNext(context.Background(), now.Add(2*time.Second)); err != nil {
		t.Fatalf("DeliverNext after adapter recovery: %v", err)
	} else if receipt == nil || receipt.State != StateRunning {
		t.Fatalf("receipt after recovery = %+v, want running", receipt)
	}
	if up.published.Metadata["correlation_id"] != "corr-transient" {
		t.Fatalf("recovered delivery carried metadata %+v", up.published.Metadata)
	}
	stored, err = service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateRunning || stored.Attempt != 2 {
		t.Fatalf("after recovery state = %q attempt = %d, want running/2", stored.State, stored.Attempt)
	}
}

// TestDispatcherStillFailsPermanentDeliveryRejection keeps the other half of
// the line: a permanent rejection is the request's problem and must stay
// terminal, or a poison request would be retried forever.
func TestDispatcherStillFailsPermanentDeliveryRejection(t *testing.T) {
	service := NewService(beads.NewMemStore())
	now := time.Now().UTC()
	record, err := service.Enqueue(context.Background(), RequestInput{
		SourceAgent:    "mayor",
		Target:         Target{Provider: "hermes", AccountID: "desktop", ConversationID: "conversation-1"},
		Reason:         ReasonDirectRequest,
		Prompt:         "Refuse me.",
		CorrelationID:  "corr-permanent",
		IdempotencyKey: "idem-permanent",
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	rejecting := &fakeTransport{receipt: &extmsg.PublishReceipt{FailureKind: extmsg.PublishFailurePermanent}}
	dispatcher := Dispatcher{Queue: service, Adapter: NewTransportAdapter(rejecting, "city-a"), Worker: "worker-1"}
	if _, _, err := dispatcher.DeliverNext(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatalf("DeliverNext returned %v, want a recorded failure with no error", err)
	}
	stored, err := service.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != StateFailed {
		t.Fatalf("state after permanent rejection = %q, want failed", stored.State)
	}
}
