package session

import (
	"context"
	"errors"
	"slices"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
)

// RoutingExecutionLabel is a durable fail-closed session classification. It is
// independent of mutable trigger/work metadata and survives ordinary wakes.
const RoutingExecutionLabel = "gc:routing-execution:v3"

// RoutingExecutionDecisionMetadataKey binds a session to its original decision.
const RoutingExecutionDecisionMetadataKey = "routing_execution_decision_id"

// LaunchAuthorization checks the persisted session and final runtime bytes.
// Its returned commit writes the launch receipt only after Start succeeds.
// A nil authorization never permits a routing-execution-marked session.
type LaunchAuthorization func(context.Context, Info, *runtime.Config) (func() error, error)

// WithLaunchAuthorization installs caller-owned execution authority at every
// session runtime start, including direct create and stale-key recovery.
func WithLaunchAuthorization(authorize LaunchAuthorization) ManagerOption {
	return func(m *Manager) { m.launchAuthorization = authorize }
}

// RequiresRoutingExecution reports durable evidence that forbids legacy launch.
func (i Info) RequiresRoutingExecution() bool {
	return i.RoutingExecutionDecisionID != "" || slices.Contains(i.Labels, RoutingExecutionLabel)
}

// BindRoutingExecution durably classifies a concrete session before launch. A
// conflicting decision is never substituted, including after work is claimed.
func (s *Store) BindRoutingExecution(id, decisionID string) (Info, error) {
	if decisionID == "" {
		return Info{}, errors.New("routing decision required")
	}
	b, err := s.validatedBead(id)
	if err != nil {
		return Info{}, err
	}
	prior := b.Metadata[RoutingExecutionDecisionMetadataKey]
	if prior != "" && prior != decisionID {
		return Info{}, errors.New("routing session migration refused")
	}
	if prior == decisionID {
		return infoFromPersistedBead(b), nil
	}
	writer, ok := beads.ConditionalWriterFor(s.store.Store)
	if !ok {
		return Info{}, beads.ErrConditionalWriteUnsupported
	}
	// Existing conditional writers support metadata CAS, not label mutation.
	// The independent routing ledger remains the primary immutable authority.
	if err := writer.UpdateIfMatch(id, b.Revision, beads.UpdateOpts{Metadata: map[string]string{RoutingExecutionDecisionMetadataKey: decisionID}}); err != nil {
		return Info{}, err
	}
	return s.Get(id)
}

func (m *Manager) authorizeRuntimeStart(ctx context.Context, id string, cfg *runtime.Config) (func() error, error) {
	b, err := m.store.Get(id)
	if err != nil {
		return nil, err
	}
	info := infoFromPersistedBead(b)
	if m.launchAuthorization == nil {
		bound, probeErr := routingdecision.ExecutionSessionBound(m.cityPath, id)
		if probeErr != nil || bound {
			return nil, errors.New("routing execution authorization unavailable")
		}
		if info.RequiresRoutingExecution() {
			return nil, errors.New("routing execution authorization unavailable")
		}
		return nil, nil
	}
	return m.launchAuthorization(ctx, info, cfg)
}
