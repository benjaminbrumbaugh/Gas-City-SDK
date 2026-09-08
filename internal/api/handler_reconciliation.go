package api

import (
	"context"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/reconcileobservation"
)

// ReconciliationInput is the request for GET /v0/city/{cityName}/reconciliation.
// It takes no parameters: the resource is the whole latest observation, and a
// filter would only be a second way to ask the same question.
type ReconciliationInput struct {
	CityScope
}

// ReconciliationOutput carries the observation verbatim.
//
// The body IS the domain model rather than a parallel wire struct. A second
// type would have to be kept in step by hand, and the first field that drifted
// would be one the controller reports and the API silently drops.
type ReconciliationOutput struct {
	Body reconcileobservation.Observation
}

// humaHandleReconciliation serves the controller's latest reconciliation
// observation.
//
// The handler does nothing but load an already-published immutable value and
// hand it back: no store query, no runtime probe, no subprocess, no file. That
// is the contract, not an implementation detail — it is what makes the resource
// safe to poll and what keeps its cost independent of how many beads, tasks or
// sessions the city holds. /status cannot make that promise, because it
// assembles independently sampled config, sessions, runtime probes and store
// counts per request.
//
// 404 is not raised here. City-scoped operations are registered through
// cityGet, which wraps every handler in bindCity; an unregistered or
// not-running city is refused there with the stable CityNotFoundOrNotRunning
// detail before this function is reached. Writing a second 404 would fork a
// settled contract that callers already match on.
func (s *Server) humaHandleReconciliation(_ context.Context, _ *ReconciliationInput) (*ReconciliationOutput, error) {
	p, ok := s.state.(ReconciliationObservationProvider)
	if !ok {
		return nil, apierr.ServiceUnavailable.Msg(
			"no reconciliation observation is available: this city has no controller runtime attached")
	}
	obs := p.ReconciliationObservation()
	if obs == nil {
		// Deliberately not a zero-valued 200. A city whose controller has not
		// finished its first cycle has published nothing, and reporting that as
		// an observation of an empty city is the false-green this resource
		// exists to avoid.
		return nil, apierr.ServiceUnavailable.Msg(
			"no reconciliation observation is available: the controller has not completed a cycle yet")
	}
	return &ReconciliationOutput{Body: *obs}, nil
}
