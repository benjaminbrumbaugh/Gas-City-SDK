package externalcoordination

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/extmsg"
)

const maxBridgeMetadataValueBytes = 512

// TransportAdapter adapts the SDK's existing external-messaging transport
// registry to the external coordination callback contract. This is useful for adapters that can
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
	metadata, err := bridgeMetadata(request)
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: err.Error()}, err
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
		Metadata:       metadata,
	})
	if err != nil {
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: err.Error()}, err
	}
	if result == nil || (!result.Delivered && (!result.Accepted || !result.Queued)) {
		errText := "transport rejected external coordination request"
		if result != nil && result.FailureKind != "" {
			errText = string(result.FailureKind)
		}
		// The transport already classified this. A transient or rate-limited
		// publish says nothing about the request -- the adapter is registered
		// but momentarily unreachable -- so surface it as ErrUnavailable and
		// let the dispatcher requeue rather than burning the record. Dropping
		// the classification here is what turned a restarting bridge into
		// destroyed requests.
		if result != nil && (result.FailureKind == extmsg.PublishFailureTransient ||
			result.FailureKind == extmsg.PublishFailureRateLimited) {
			return DeliveryReceipt{
				RequestID:  request.RequestID,
				State:      StateQueued,
				Error:      errText,
				RetryAfter: result.RetryAfter,
			}, fmt.Errorf("%w: %s", ErrUnavailable, errText)
		}
		return DeliveryReceipt{RequestID: request.RequestID, State: StateFailed, Error: errText}, nil
	}
	return DeliveryReceipt{
		RequestID:       request.RequestID,
		Attempt:         request.Attempt,
		CorrelationID:   request.CorrelationID,
		State:           StateRunning,
		Accepted:        true,
		TargetSessionID: request.Target.ConversationID,
	}, nil
}

func bridgeMetadata(request Request) (map[string]string, error) {
	if len(request.RequestID) > maxBridgeMetadataValueBytes {
		return nil, fmt.Errorf("%w: request_id exceeds bridge metadata limit", ErrInvalidInput)
	}
	if len(request.CorrelationID) > maxBridgeMetadataValueBytes {
		return nil, fmt.Errorf("%w: correlation_id exceeds bridge metadata limit", ErrInvalidInput)
	}
	// These keys are the bridge-facing wire contract. They were named hca_*
	// while this package was called the human coordinator adapter; the name no
	// longer describes anything the package does, and no released bridge reads
	// the old spelling, so they carry the package's own vocabulary instead.
	return map[string]string{
		"coordination_request_id": request.RequestID,
		"coordination_attempt":    strconv.Itoa(request.Attempt),
		"source_agent":            boundBridgeMetadataValue(request.SourceAgent),
		"reason":                  boundBridgeMetadataValue(string(request.Reason)),
		"work_ref":                boundBridgeMetadataValue(request.WorkRef),
		"correlation_id":          request.CorrelationID,
		"content_retention":       string(request.ContentRetention),
	}, nil
}

func boundBridgeMetadataValue(value string) string {
	if len(value) <= maxBridgeMetadataValueBytes {
		return value
	}
	limit := maxBridgeMetadataValueBytes
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
