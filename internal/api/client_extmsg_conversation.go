package api

import (
	"context"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/extmsg"
)

// ExtMsgOutboundSpec describes an API-backed external-conversation reply.
// SessionID is always carried explicitly so the server can authorize the
// publish against the binding or group membership for that session.
type ExtMsgOutboundSpec struct {
	Conversation     extmsg.ConversationRef
	SessionID        string
	Text             string
	ReplyToMessageID string
	IdempotencyKey   string
}

// ExtMsgTranscriptSpec describes a scoped transcript read.
type ExtMsgTranscriptSpec struct {
	Conversation  extmsg.ConversationRef
	AfterSequence int64
	Limit         int
	Order         extmsg.TranscriptOrder
}

// ExtMsgTranscriptAckSpec describes a scoped transcript acknowledgement.
type ExtMsgTranscriptAckSpec struct {
	Conversation extmsg.ConversationRef
	SessionID    string
	Sequence     int64
}

// SendExtMsg publishes one message through the city API and returns the
// server's receipt, delivery context, and transcript entry. It deliberately
// does not collapse queued and delivered into one status: callers must retain
// the distinction exposed by the API.
func (c *Client) SendExtMsg(spec ExtMsgOutboundSpec) (extmsg.OutboundResult, error) {
	if err := c.requireCityScope(); err != nil {
		return extmsg.OutboundResult{}, err
	}
	conversation := genclientConversationRef(spec.Conversation)
	body := genclient.PostV0CityByCityNameExtmsgOutboundJSONRequestBody{
		Conversation: &conversation,
		SessionId:    spec.SessionID,
	}
	body.Text = &spec.Text
	if spec.ReplyToMessageID != "" {
		body.ReplyToMessageId = &spec.ReplyToMessageID
	}
	if spec.IdempotencyKey != "" {
		body.IdempotencyKey = &spec.IdempotencyKey
	}
	resp, err := c.cw.PostV0CityByCityNameExtmsgOutboundWithResponse(
		context.Background(), c.cityName,
		&genclient.PostV0CityByCityNameExtmsgOutboundParams{XGCRequest: "true"},
		body,
	)
	if err != nil {
		return extmsg.OutboundResult{}, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if resp == nil {
		return extmsg.OutboundResult{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return extmsg.OutboundResult{}, err
	}
	if resp.JSON200 == nil {
		return extmsg.OutboundResult{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	return extmsgOutboundResultFromWire(*resp.JSON200), nil
}

// ListExtMsgTranscript reads only the requested conversation and cursor. The
// server remains responsible for membership/authority checks at this boundary.
func (c *Client) ListExtMsgTranscript(spec ExtMsgTranscriptSpec) ([]extmsg.ConversationTranscriptRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return nil, err
	}
	if spec.Order != "" && spec.Order != extmsg.TranscriptOrderAsc && spec.Order != extmsg.TranscriptOrderDesc {
		return nil, fmt.Errorf("invalid transcript order %q", spec.Order)
	}
	params := &genclient.GetV0CityByCityNameExtmsgTranscriptParams{
		ScopeId:              stringPtr(spec.Conversation.ScopeID),
		Provider:             stringPtr(spec.Conversation.Provider),
		AccountId:            stringPtr(spec.Conversation.AccountID),
		ConversationId:       stringPtr(spec.Conversation.ConversationID),
		ParentConversationId: stringPtr(spec.Conversation.ParentConversationID),
		Kind:                 stringPtr(string(spec.Conversation.Kind)),
	}
	if spec.AfterSequence != 0 {
		params.AfterSequence = &spec.AfterSequence
	}
	if spec.Limit != 0 {
		limit := int64(spec.Limit)
		params.Limit = &limit
	}
	if spec.Order != "" {
		order := genclient.GetV0CityByCityNameExtmsgTranscriptParamsOrder(spec.Order)
		params.Order = &order
	}
	resp, err := c.cw.GetV0CityByCityNameExtmsgTranscriptWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return nil, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if resp == nil {
		return nil, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Items == nil {
		return []extmsg.ConversationTranscriptRecord{}, nil
	}
	items := *resp.JSON200.Items
	out := make([]extmsg.ConversationTranscriptRecord, 0, len(items))
	for _, item := range items {
		out = append(out, extmsgTranscriptRecordFromWire(item))
	}
	return out, nil
}

// AckExtMsgTranscript acknowledges entries up to Sequence for one session and
// conversation. The API applies the membership/authority policy.
func (c *Client) AckExtMsgTranscript(spec ExtMsgTranscriptAckSpec) error {
	if err := c.requireCityScope(); err != nil {
		return err
	}
	conversation := genclientConversationRef(spec.Conversation)
	sequence := spec.Sequence
	body := genclient.PostV0CityByCityNameExtmsgTranscriptAckJSONRequestBody{
		Conversation: &conversation,
		SessionId:    spec.SessionID,
		Sequence:     &sequence,
	}
	resp, err := c.cw.PostV0CityByCityNameExtmsgTranscriptAckWithResponse(
		context.Background(), c.cityName,
		&genclient.PostV0CityByCityNameExtmsgTranscriptAckParams{XGCRequest: "true"},
		body,
	)
	if err != nil {
		return &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if resp == nil {
		return &connError{err: fmt.Errorf("nil response")}
	}
	return apiErrorFromResponse(resp.StatusCode(), pdOf(resp))
}

// ListExtMsgBindings lists active bindings for one session selector. The
// caller uses the result to reject ambiguous implicit conversation selection.
func (c *Client) ListExtMsgBindings(sessionID string) ([]extmsg.SessionBindingRecord, error) {
	if err := c.requireCityScope(); err != nil {
		return nil, err
	}
	params := &genclient.GetV0CityByCityNameExtmsgBindingsParams{}
	if sessionID != "" {
		params.SessionId = &sessionID
	}
	resp, err := c.cw.GetV0CityByCityNameExtmsgBindingsWithResponse(context.Background(), c.cityName, params)
	if err != nil {
		return nil, &connError{err: fmt.Errorf("request failed: %w", err)}
	}
	if resp == nil {
		return nil, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Items == nil {
		return []extmsg.SessionBindingRecord{}, nil
	}
	items := *resp.JSON200.Items
	out := make([]extmsg.SessionBindingRecord, 0, len(items))
	for _, item := range items {
		out = append(out, extmsgBindingRecordFromWire(item))
	}
	return out, nil
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func extmsgOutboundResultFromWire(result genclient.OutboundResult) extmsg.OutboundResult {
	return extmsg.OutboundResult{
		Receipt:         extmsgPublishReceiptFromWire(result.Receipt),
		DeliveryContext: extmsgDeliveryContextFromWire(result.DeliveryContext),
		TranscriptEntry: extmsgTranscriptRecordPtrFromWire(result.TranscriptEntry),
	}
}

func extmsgPublishReceiptFromWire(receipt genclient.PublishReceipt) extmsg.PublishReceipt {
	return extmsg.PublishReceipt{
		MessageID:    receipt.MessageID,
		Conversation: extmsgConversationRefFromWire(receipt.Conversation),
		Accepted:     receipt.Accepted,
		Queued:       receipt.Queued,
		Delivered:    receipt.Delivered,
		FailureKind:  extmsg.PublishFailureKind(receipt.FailureKind),
		RetryAfter:   time.Duration(receipt.RetryAfter),
		Metadata:     receipt.Metadata,
	}
}

func extmsgDeliveryContextFromWire(record genclient.DeliveryContextRecord) *extmsg.DeliveryContextRecord {
	if record.ID == "" && record.SessionID == "" && record.LastMessageID == "" && record.SourceSessionID == "" && record.BindingGeneration == 0 && record.SchemaVersion == 0 && record.LastPublishedAt.IsZero() {
		return nil
	}
	return &extmsg.DeliveryContextRecord{
		ID:                record.ID,
		SchemaVersion:     int(record.SchemaVersion),
		SessionID:         record.SessionID,
		Conversation:      extmsgConversationRefFromWire(record.Conversation),
		BindingGeneration: record.BindingGeneration,
		LastPublishedAt:   record.LastPublishedAt,
		LastMessageID:     record.LastMessageID,
		SourceSessionID:   record.SourceSessionID,
		Metadata:          record.Metadata,
	}
}

func extmsgTranscriptRecordPtrFromWire(record genclient.ConversationTranscriptRecord) *extmsg.ConversationTranscriptRecord {
	if record.ID == "" && record.Sequence == 0 && record.Text == "" && record.CreatedAt.IsZero() {
		return nil
	}
	converted := extmsgTranscriptRecordFromWire(record)
	return &converted
}

func extmsgTranscriptRecordFromWire(record genclient.ConversationTranscriptRecord) extmsg.ConversationTranscriptRecord {
	out := extmsg.ConversationTranscriptRecord{
		ID:                record.ID,
		SchemaVersion:     int(record.SchemaVersion),
		Conversation:      extmsgConversationRefFromWire(record.Conversation),
		Sequence:          record.Sequence,
		Kind:              extmsg.TranscriptMessageKind(record.Kind),
		Provenance:        extmsg.TranscriptProvenance(record.Provenance),
		ProviderMessageID: record.ProviderMessageID,
		Actor: extmsg.ExternalActor{
			ID:          record.Actor.Id,
			DisplayName: record.Actor.DisplayName,
			IsBot:       record.Actor.IsBot,
		},
		Text:             record.Text,
		ExplicitTarget:   record.ExplicitTarget,
		ReplyToMessageID: record.ReplyToMessageID,
		SourceSessionID:  record.SourceSessionID,
		CreatedAt:        record.CreatedAt,
		Metadata:         record.Metadata,
	}
	if record.Attachments != nil {
		out.Attachments = make([]extmsg.ExternalAttachment, 0, len(*record.Attachments))
		for _, attachment := range *record.Attachments {
			out.Attachments = append(out.Attachments, extmsg.ExternalAttachment{
				ProviderID: attachment.ProviderId,
				URL:        attachment.Url,
				MIMEType:   attachment.MimeType,
			})
		}
	}
	return out
}

func extmsgConversationRefFromWire(ref genclient.ConversationRef) extmsg.ConversationRef {
	return extmsg.ConversationRef{
		ScopeID:              ref.ScopeId,
		Provider:             ref.Provider,
		AccountID:            ref.AccountId,
		ConversationID:       ref.ConversationId,
		ParentConversationID: derefStr(ref.ParentConversationId),
		Kind:                 extmsg.ConversationKind(ref.Kind),
	}
}
