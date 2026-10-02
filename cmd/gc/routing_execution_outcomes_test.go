package main

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/routingdecision"
)

func TestProducerExecutionOutcomeServiceUnavailableDoesNotFabricatePage(t *testing.T) {
	s := &cityRoutingDecisionService{}
	if _, err := s.ExecutionOutcomes(context.Background(), routingdecision.OutcomeListOptions{}); err == nil {
		t.Fatal("unavailable ledger fabricated empty v3 page")
	}
}
