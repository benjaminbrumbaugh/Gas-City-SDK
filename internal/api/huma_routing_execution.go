package api

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// RoutingExecutionOutcomeProvider is additive: legacy State and client method
// signatures are unchanged. The ordinary city read-auth perimeter applies.
type RoutingExecutionOutcomeProvider interface {
	RoutingExecutionOutcomes(context.Context, routingdecision.OutcomeListOptions) (routingdecision.ProducerExecutionOutcomePage, error)
}

// RoutingExecutionOutcomeListOutput returns the redacted producer v3 page.
type RoutingExecutionOutcomeListOutput struct {
	Body routingdecision.ProducerExecutionOutcomePage
}

func (s *Server) humaHandleRoutingExecutionOutcomes(ctx context.Context, input *RoutingOutcomeListInput) (*RoutingExecutionOutcomeListOutput, error) {
	provider, ok := s.state.(RoutingExecutionOutcomeProvider)
	if !ok {
		return nil, apierr.RoutingUnavailable.Msg("routing execution outcomes unavailable")
	}
	page, err := provider.RoutingExecutionOutcomes(ctx, routingdecision.OutcomeListOptions{Limit: input.Limit, Cursor: input.Cursor})
	if err != nil {
		if input.Cursor != "" && errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.InvalidCursor.Msg("cursor is not a valid routing-execution pagination token; re-fetch the first page")
		}
		if errors.Is(err, routingdecision.ErrInvalidDecision) {
			return nil, apierr.RoutingDecisionInvalid.Msg("routing execution outcome request invalid")
		}
		return nil, apierr.RoutingUnavailable.Msg("routing execution outcome authority unavailable")
	}
	return &RoutingExecutionOutcomeListOutput{Body: page}, nil
}
