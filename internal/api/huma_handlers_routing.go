package api

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

func (s *Server) routingDecisionProvider() (RoutingDecisionProvider, error) {
	provider, ok := s.state.(RoutingDecisionProvider)
	if !ok || provider == nil {
		return nil, apierr.RoutingUnavailable.Msg("routing decision service unavailable")
	}
	return provider, nil
}

func (s *Server) routingDeliveryProvider() (RoutingDeliveryProvider, error) {
	provider, ok := s.state.(RoutingDeliveryProvider)
	if !ok || provider == nil {
		return nil, apierr.RoutingUnavailable.Msg("routing delivery service unavailable")
	}
	return provider, nil
}

func (s *Server) humaHandleRoutingDecisionStatus(_ context.Context, _ *RoutingDecisionStatusInput) (*RoutingDecisionStatusOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	return &RoutingDecisionStatusOutput{Body: provider.RoutingDecisionStatus()}, nil
}

func (s *Server) humaHandleRoutingDecisionTargets(ctx context.Context, _ *RoutingDecisionTargetsInput) (*RoutingDecisionTargetsOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	items, err := provider.RoutingDecisionTargets(ctx)
	if err != nil {
		return nil, apierr.RoutingUnavailable.Msg("routing target snapshot unavailable")
	}
	return &RoutingDecisionTargetsOutput{Body: RoutingDecisionTargetsBody{Items: items}}, nil
}

func (s *Server) humaHandleRoutingDecisionEligible(ctx context.Context, _ *RoutingDecisionEligibleInput) (*RoutingDecisionEligibleOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	selection, err := provider.RoutingDecisionEligible(ctx)
	if err != nil {
		return nil, apierr.RoutingUnavailable.Msg("routing eligible-work snapshot unavailable")
	}
	return &RoutingDecisionEligibleOutput{Body: selection}, nil
}

func (s *Server) humaHandleRoutingDecisionList(ctx context.Context, input *RoutingDecisionListInput) (*RoutingDecisionListOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	page, err := provider.RoutingDecisionList(ctx, routingdecision.ListOptions{
		State: routingdecision.State(input.State), Limit: input.Limit, Cursor: input.Cursor,
	})
	if err != nil {
		if input.Cursor != "" && errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.InvalidCursor.Msg("cursor is not a valid routing-decision pagination token; re-fetch the first page")
		}
		if errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.RoutingDecisionInvalid.Msg("routing decision list request is invalid")
		}
		return nil, apierr.RoutingUnavailable.Msg("routing decision ledger unavailable")
	}
	items := make([]RoutingDecisionWithAudits, len(page.Items))
	for index := range page.Items {
		items[index] = RoutingDecisionWithAudits{
			Record: RoutingDecisionRecord(page.Items[index].Record), Audits: page.Items[index].Audits,
		}
	}
	return &RoutingDecisionListOutput{Body: RoutingDecisionListBody{
		Items: items, Total: page.Total, NextCursor: page.NextCursor,
	}}, nil
}

func (s *Server) humaHandleRoutingOutcomeList(ctx context.Context, input *RoutingOutcomeListInput) (*RoutingOutcomeListOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	page, err := provider.RoutingDecisionOutcomes(ctx, routingdecision.OutcomeListOptions{Limit: input.Limit, Cursor: input.Cursor})
	if err != nil {
		if input.Cursor != "" && errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.InvalidCursor.Msg("cursor is not a valid routing-outcome pagination token; re-fetch the first page")
		}
		if errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.RoutingDecisionInvalid.Msg("routing outcome list request is invalid")
		}
		return nil, apierr.RoutingUnavailable.Msg("routing outcome projection unavailable")
	}
	return &RoutingOutcomeListOutput{Body: page}, nil
}

func (s *Server) humaHandleRoutingDeliveryPending(ctx context.Context, input *RoutingDeliveryPendingInput) (*RoutingDeliveryPendingOutput, error) {
	provider, err := s.routingDeliveryProvider()
	if err != nil {
		return nil, err
	}
	page, err := provider.RoutingDeliveryPending(ctx, routingdecision.DeliveryListOptions{Limit: input.Limit, Cursor: input.Cursor})
	if err != nil {
		if input.Cursor != "" && errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.InvalidCursor.Msg("cursor is not a valid routing-delivery pagination token; re-fetch the first page")
		}
		if errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.RoutingDecisionInvalid.Msg("routing delivery list request is invalid")
		}
		return nil, apierr.RoutingUnavailable.Msg("routing delivery ledger unavailable")
	}
	return &RoutingDeliveryPendingOutput{Body: page}, nil
}

func (s *Server) humaHandleRoutingDeliveryAck(ctx context.Context, input *RoutingDeliveryAckInput) (*RoutingDeliveryAckOutput, error) {
	provider, err := s.routingDeliveryProvider()
	if err != nil {
		return nil, err
	}
	result, err := provider.RoutingDeliveryAck(ctx, input.Body)
	if err != nil {
		switch {
		case errors.Is(err, routingdecision.ErrDeliveryInvalid):
			return nil, apierr.InvalidRequest.Msg("routing delivery acknowledgement is malformed")
		case errors.Is(err, routingdecision.ErrDeliveryNotFound):
			return nil, apierr.RoutingDeliveryNotFound.Msg("routing delivery item not found")
		case errors.Is(err, routingdecision.ErrDeliveryAckConflict), errors.Is(err, routingdecision.ErrDeliveryConflict):
			return nil, apierr.RoutingIdempotencyConflict.Msg("routing delivery acknowledgement conflicts with the durable item")
		default:
			return nil, apierr.RoutingUnavailable.Msg("routing delivery acknowledgement unavailable")
		}
	}
	return &RoutingDeliveryAckOutput{Body: result}, nil
}

func (s *Server) humaHandleRoutingDecisionIngest(ctx context.Context, input *RoutingDecisionIngestInput) (*RoutingDecisionIngestOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	result, err := provider.RoutingDecisionIngest(ctx, routingdecision.IngestApprovedRequest{
		Payload: input.Body.Payload, Approval: input.Body.Approval, Signature: input.Body.Signature,
		IdempotencyToken: input.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, routingdecision.ErrAuthorizationRequired),
			errors.Is(err, routingdecision.ErrUnknownAuthority),
			errors.Is(err, routingdecision.ErrInvalidSignature):
			return nil, apierr.RoutingSignatureRefused.Msg("routing decision signature refused")
		case errors.Is(err, routingdecision.ErrIdempotencyConflict), errors.Is(err, routingdecision.ErrDecisionExists):
			return nil, apierr.RoutingIdempotencyConflict.Msg("routing decision idempotency conflict")
		case errors.Is(err, routingdecision.ErrInvalidDecision):
			return nil, apierr.RoutingDecisionInvalid.Msg("routing decision envelope is malformed or stale")
		default:
			return nil, apierr.RoutingUnavailable.Msg("routing decision ledger unavailable")
		}
	}
	return &RoutingDecisionIngestOutput{Body: RoutingDecisionIngestResult{
		Record: RoutingDecisionRecord(result.Record), Receipt: result.Receipt,
	}}, nil
}

func (s *Server) humaHandleRoutingDecisionLocalAdmission(ctx context.Context, input *RoutingDecisionLocalAdmissionInput) (*RoutingDecisionLocalAdmissionOutput, error) {
	provider, err := s.routingDecisionProvider()
	if err != nil {
		return nil, err
	}
	result, err := provider.RoutingDecisionAdmitLocal(ctx, input.Body, input.IdempotencyKey)
	if err != nil {
		switch {
		case errors.Is(err, routingdecision.ErrIdempotencyConflict), errors.Is(err, routingdecision.ErrDecisionExists):
			return nil, apierr.RoutingIdempotencyConflict.Msg("local routing admission idempotency conflict")
		case errors.Is(err, routingdecision.ErrInvalidDecision):
			return nil, apierr.RoutingDecisionInvalid.Msg("local routing admission is malformed or stale")
		case errors.Is(err, routingdecision.ErrAdmissionCallback), errors.Is(err, routingdecision.ErrAdmissionCommit):
			return nil, apierr.RoutingUnavailable.Msg("local routing admission could not be committed")
		default:
			return nil, apierr.RoutingUnavailable.Msg("local routing admission unavailable")
		}
	}
	return &RoutingDecisionLocalAdmissionOutput{Body: RoutingDecisionLocalAdmissionResult{
		Record: RoutingDecisionRecord(result.Record), Receipt: result.Receipt,
	}}, nil
}
