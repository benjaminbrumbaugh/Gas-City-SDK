package externalcoordination

import (
	"context"
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
