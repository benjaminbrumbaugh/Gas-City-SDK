package hca

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/extmsg"
)

// TransportAdapter adapts the SDK's existing external-messaging transport
// registry to the HCA callback contract. This is useful for adapters that can
// publish into a configured coordinator conversation; provider-specific session
// creation/resumption remains the adapter's responsibility.
type TransportAdapter struct {
	transport extmsg.TransportAdapter
	scopeID   string
}

// NewTransportAdapter bridges one registered external transport.
func NewTransportAdapter(transport extmsg.TransportAdapter, scopeID string) *TransportAdapter {
	return &TransportAdapter{transport: transport, scopeID: scopeID}
}

// Name returns the bridged transport name.
func (a *TransportAdapter) Name() string {
	if a == nil || a.transport == nil {
		return ""
	}
	return a.transport.Name()
}

// Capabilities returns the conservative capabilities of the bridged transport.
func (a *TransportAdapter) Capabilities() Capability {
	if a == nil || a.transport == nil {
		return Capability{}
	}
	return Capability{CanResumeSession: true, CanSubmitPrompt: true, CanReceiveEvents: true}
}

// Deliver publishes the request to the configured coordinator conversation.
func (a *TransportAdapter) Deliver(ctx context.Context, request Request) (DeliveryReceipt, error) {
	if a == nil || a.transport == nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "transport adapter is not registered"}, ErrUnavailable
	}
	conversationID := request.Target.ConversationID
	if conversationID == "" {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: "target conversation_id is not configured"}, fmt.Errorf("%w: conversation_id is required", ErrInvalidInput)
	}
	result, err := a.transport.Publish(ctx, extmsg.PublishRequest{
		Conversation: extmsg.ConversationRef{
			ScopeID:        a.scopeID,
			Provider:       request.Target.Provider,
			AccountID:      request.Target.AccountID,
			ConversationID: conversationID,
			Kind:           extmsg.ConversationDM,
		},
		Text:           request.Prompt,
		IdempotencyKey: request.IdempotencyKey,
		Metadata: map[string]string{
			"hca_request_id": request.RequestID,
			"source_agent":   request.SourceAgent,
			"reason":         string(request.Reason),
			"work_ref":       request.WorkRef,
			"correlation_id": request.CorrelationID,
		},
	})
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: err.Error()}, err
	}
	if result == nil || !result.Delivered {
		errText := "transport rejected HCA request"
		if result != nil && result.FailureKind != "" {
			errText = string(result.FailureKind)
		}
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: errText}, nil
	}
	return DeliveryReceipt{
		RequestID:       request.RequestID,
		State:           StateRunning,
		Accepted:        true,
		TargetSessionID: request.Target.ConversationID,
	}, nil
}
