package api

import (
	"context"
	"testing"
)

func TestRoutingExecutionOutcomeHTTPRequiresAuthority(t *testing.T) {
	s := &Server{state: newFakeState(t)}
	if _, err := s.humaHandleRoutingExecutionOutcomes(context.Background(), &RoutingOutcomeListInput{Limit: 100}); err == nil {
		t.Fatal("missing authority fabricated v3 output")
	}
}
