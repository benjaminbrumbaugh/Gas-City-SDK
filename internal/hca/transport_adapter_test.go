package hca

import (
	"context"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/extmsg"
)

type fakeTransport struct {
	published extmsg.PublishRequest
}

func (f *fakeTransport) Name() string { return "fake-hca-transport" }
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
	return &extmsg.PublishReceipt{Delivered: true, MessageID: "message-1"}, nil
}

func TestTransportAdapterPublishesCausallyLinkedRequest(t *testing.T) {
	transport := &fakeTransport{}
	adapter := NewTransportAdapter(transport, "city-a")
	request := Request{
		RequestID:      "request-1",
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
	if transport.published.Conversation.Provider != "hermes" || transport.published.Conversation.AccountID != "desktop" || transport.published.Conversation.ConversationID != "conversation-1" {
		t.Fatalf("conversation = %+v", transport.published.Conversation)
	}
	if transport.published.IdempotencyKey != "idem-1" || transport.published.Metadata["work_ref"] != "gc-123" || transport.published.Metadata["correlation_id"] != "corr-1" {
		t.Fatalf("causal metadata = %+v", transport.published.Metadata)
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
		t.Fatalf("state = %q, want running until HCA response", stored.State)
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
