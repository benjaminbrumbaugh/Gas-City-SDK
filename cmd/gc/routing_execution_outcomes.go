package main

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/routingdecision"
)

func (service *cityRoutingDecisionService) ExecutionOutcomes(ctx context.Context, opts routingdecision.OutcomeListOptions) (routingdecision.ProducerExecutionOutcomePage, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.ProducerExecutionOutcomePage{}, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed || service.store == nil {
		return routingdecision.ProducerExecutionOutcomePage{}, errors.New("routing execution ledger unavailable")
	}
	decisions, err := service.store.ListExecutionOutcomeDecisions(opts)
	if err != nil {
		return routingdecision.ProducerExecutionOutcomePage{}, err
	}
	page := routingdecision.ProducerExecutionOutcomePage{SchemaVersion: "routing/outcome/v3", Items: []routingdecision.ProducerExecutionOutcome{}, Total: decisions.Total, NextCursor: decisions.NextCursor}
	for _, item := range decisions.Items {
		launches, err := service.store.ExecutionLaunches(item.Record.Payload.DecisionID)
		if err != nil {
			return page, err
		}
		row, available, err := routingdecision.ProjectProducerExecutionOutcome(item, launches)
		if err != nil {
			return page, err
		}
		if !available {
			page.Partial = true
			continue
		}
		page.Items = append(page.Items, row)
	}
	page.Total = len(page.Items)
	return page, nil
}

func (s *controllerState) RoutingExecutionOutcomes(ctx context.Context, opts routingdecision.OutcomeListOptions) (routingdecision.ProducerExecutionOutcomePage, error) {
	service := s.routingDecisions()
	if service == nil {
		return routingdecision.ProducerExecutionOutcomePage{}, errors.New("routing execution service unavailable")
	}
	return service.ExecutionOutcomes(ctx, opts)
}
