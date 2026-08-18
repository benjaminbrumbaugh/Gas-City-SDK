package main

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/routingdecision"
)

func (s *controllerState) routingDecisions() *cityRoutingDecisionService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.routingDecisionService
}

func (s *controllerState) RoutingDecisionStatus() routingdecision.LiveStatus {
	service := s.routingDecisions()
	if service == nil {
		return routingdecision.LiveStatus{
			Schema: routingdecision.SchemaVersion, Status: routingdecision.AvailabilityDenied,
			Reason: routingdecision.ReasonServiceClosed, RetentionMonths: routingdecision.TerminalRetentionMonths,
			TerminalStateBasis: "latest_terminal_transition_at",
		}
	}
	return service.Status()
}

func (s *controllerState) RoutingDecisionTargets(ctx context.Context) ([]routingdecision.TargetSnapshot, error) {
	service := s.routingDecisions()
	if service == nil {
		return nil, errors.New("routing decision service unavailable")
	}
	return service.Targets(ctx)
}

func (s *controllerState) RoutingDecisionEligible(ctx context.Context) (routingdecision.SelectionSnapshot, error) {
	service := s.routingDecisions()
	if service == nil {
		return routingdecision.SelectionSnapshot{}, errors.New("routing decision service unavailable")
	}
	return service.Eligible(ctx)
}

func (s *controllerState) RoutingDecisionList(ctx context.Context, opts routingdecision.ListOptions) (routingdecision.DecisionPage, error) {
	service := s.routingDecisions()
	if service == nil {
		return routingdecision.DecisionPage{}, errors.New("routing decision service unavailable")
	}
	return service.List(ctx, opts)
}

func (s *controllerState) RoutingDecisionIngest(ctx context.Context, request routingdecision.IngestApprovedRequest) (routingdecision.IngestApprovedResult, error) {
	service := s.routingDecisions()
	if service == nil {
		return routingdecision.IngestApprovedResult{}, errors.New("routing decision service unavailable")
	}
	return service.Ingest(ctx, request)
}
