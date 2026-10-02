package main

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// authorizeRoutingExecutionLaunch is the controller-owned production worker
// authorizer. Durable ledger identity takes precedence over mutable markers.
func (cr *CityRuntime) authorizeRoutingExecutionLaunch(ctx context.Context, info sessionpkg.Info, final *runtime.Config) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var auth *routingdecision.ExecutionSessionAuthorization
	if cr.routingDecisionStore != nil {
		var err error
		auth, err = cr.routingDecisionStore.ExecutionSession(info.ID)
		if err != nil {
			return nil, errors.New("routing session authority unavailable")
		}
		if auth != nil {
			if info.TriggerBeadID != auth.WorkID || info.Generation != auth.Generation || info.InstanceToken != auth.InstanceToken {
				return nil, errors.New("routing session authority changed")
			}
			info.RoutingExecutionDecisionID = auth.DecisionID
		}
	}
	agent, ok := resolveAgentIdentity(cr.cfg, info.Template, "")
	if !ok {
		if info.RequiresRoutingExecution() {
			return nil, errors.New("routing target unavailable")
		}
		return nil, nil
	}
	target, rig := info.Template, agent.Dir
	if err := cr.checkRoutingExecutionLaunch(target, rig, info, *final); err != nil {
		return nil, err
	}
	if info.TriggerBeadID == "" {
		return nil, nil
	}
	var workStore beads.Store
	for _, scope := range cr.routingDecisionScopes() {
		if scope.rig == rig {
			workStore = scope.store
			break
		}
	}
	if workStore == nil {
		return nil, errors.New("routing work unavailable")
	}
	work, err := beads.HandlesFor(workStore).Live.Get(info.TriggerBeadID)
	if err != nil {
		return nil, err
	}
	id := work.Metadata[beadmeta.RoutingDecisionIDMetadataKey]
	if id == "" {
		return nil, nil
	}
	record, err := cr.routingDecisionStore.Get(id)
	if err != nil {
		return nil, err
	}
	p := record.Payload
	if p.Schema != routingdecision.ExecutionSchemaVersion {
		return nil, nil
	}
	if auth == nil {
		bound := routingdecision.ExecutionSessionAuthorization{DecisionID: p.DecisionID, BindingID: p.BindingID, SessionID: info.ID, Generation: info.Generation, InstanceToken: info.InstanceToken, WorkID: p.WorkBeadID, ClaimFence: p.ClaimFence, Execution: *p.Execution}
		if err := cr.routingDecisionStore.BindExecutionSession(bound); err != nil {
			return nil, err
		}
		auth = &bound
	}
	// Persist a fail-closed session classification as well as the independent
	// immutable ledger record. An interrupted second write cannot launch.
	if _, err := sessionpkg.NewStore(cr.sessionsBeadStore()).BindRoutingExecution(info.ID, p.DecisionID); err != nil {
		return nil, err
	}
	final.IsolatedLocalExecution = true
	attemptID := sessionpkg.NewInstanceToken()
	saved := *auth
	return func() error {
		_, err := cr.routingDecisionStore.RecordExecutionLaunchAttempt(saved, attemptID)
		return err
	}, nil
}
