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

func (cr *CityRuntime) checkUnavailableRoutingExecutionAuthority(info sessionpkg.Info) error {
	if cr.routingDecisionStore != nil {
		return nil
	}
	bound, err := routingdecision.ExecutionSessionBound(cr.cityPath, info.ID)
	if err != nil || bound || info.RequiresRoutingExecution() {
		return errors.New("routing execution authorization unavailable")
	}
	return nil
}

func (cr *CityRuntime) authorizeRoutingExecutionWake(info sessionpkg.Info, generation, token string) error {
	if err := cr.checkUnavailableRoutingExecutionAuthority(info); err != nil {
		return err
	}
	if cr.routingDecisionStore == nil {
		return nil
	}
	auth, err := cr.routingDecisionStore.ExecutionSessionIncarnation(info.ID, info.Generation, info.InstanceToken)
	if err != nil {
		return err
	}
	if auth == nil {
		return nil
	}
	agent, ok := resolveAgentIdentity(cr.cfg, info.Template, "")
	if !ok {
		return errors.New("routing target unavailable")
	}
	if cr.cfg == nil || cr.cfg.RoutingExecution == nil || !cr.cfg.RoutingExecution.Enabled {
		return errors.New("routing execution disabled")
	}
	binding := cr.cfg.RoutingExecution.Bindings[info.Template]
	command, err := routingExecutionCommand(binding)
	if err != nil {
		return err
	}
	if err := cr.checkRoutingExecutionLaunch(info.Template, agent.Dir, info, runtime.Config{Command: command, WorkDir: binding.WorkDir}); err != nil {
		return err
	}
	return cr.routingDecisionStore.AuthorizeExecutionSuccessor(*auth, generation, token)
}

// authorizeRoutingExecutionLaunch is the controller-owned production worker
// authorizer. Durable ledger identity takes precedence over mutable markers.
func (cr *CityRuntime) authorizeRoutingExecutionLaunch(ctx context.Context, info sessionpkg.Info, final *runtime.Config) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cr.checkUnavailableRoutingExecutionAuthority(info); err != nil {
		return nil, err
	}
	var auth *routingdecision.ExecutionSessionAuthorization
	if cr.routingDecisionStore != nil {
		var err error
		auth, err = cr.routingDecisionStore.ExecutionSessionIncarnation(info.ID, info.Generation, info.InstanceToken)
		if err != nil {
			return nil, errors.New("routing session authority unavailable")
		}
		if auth != nil {
			if (info.RoutingExecutionDecisionID != "" && info.RoutingExecutionDecisionID != auth.DecisionID) || info.TriggerBeadID != auth.WorkID || info.Generation != auth.Generation || info.InstanceToken != auth.InstanceToken {
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
	local := cr.cfg.RoutingExecution.Bindings[target]
	final.IsolatedLocalExecution = true
	final.BoundExecutable = local.Executable
	final.BoundArgs = append([]string(nil), local.Args...)
	final.BoundEnvironment = make(map[string]string, len(local.Environment))
	for key, value := range local.Environment {
		final.BoundEnvironment[key] = value
	}
	attemptID := sessionpkg.NewInstanceToken()
	saved := *auth
	return func() error {
		_, err := cr.routingDecisionStore.RecordExecutionLaunchAttempt(saved, attemptID)
		return err
	}, nil
}
