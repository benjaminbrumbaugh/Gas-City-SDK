package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

func TestV3AdmissionDefaultsDenyWithoutExecutionAdapter(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "legacy-fixture")
	p := fixture.payload
	p.Schema = routingdecision.ExecutionSchemaVersion
	p.DecisionID = "v3-admission"
	p.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	p.ServeAs = "literal-model"
	p.Provider = "test-provider"
	p.Execution = &routingdecision.ExecutionBinding{Schema: 1, CanonicalModel: p.Model, ServeAs: p.ServeAs, ReasoningEffort: "high", Account: p.Account, Provider: p.Provider, Target: p.Target, ConfigDigest: p.TargetConfigDigest, AdapterID: "test", AdapterDigest: strings.Repeat("f", 64), InvocationDigest: strings.Repeat("a", 64)}
	p.BindingID = routingdecision.BindingID(p)
	record := routingdecision.Record{Payload: p}
	result, err := fixture.cr.routeDecisionAtAdmissionBoundary(routingDecisionScope{rig: p.Rig, store: requireRoutingDecisionWorkStore(t, fixture.base)}, record, nil)
	if err != nil || result.State != routingdecision.StateRefusedAfterRace {
		t.Fatalf("unattested tuple admitted: %+v %v", result, err)
	}
	fixture.cr.routingExecutionAdapter = func(_ config.Agent, _ *config.City, final *runtime.Config) (routingdecision.ExecutionBinding, error) {
		if final != nil {
			t.Fatal("admission passed unresolved runtime as final")
		}
		return *p.Execution, nil
	}
	result, err = fixture.cr.routeDecisionAtAdmissionBoundary(routingDecisionScope{rig: p.Rig, store: requireRoutingDecisionWorkStore(t, fixture.base)}, record, nil)
	if err != nil || result.State != routingdecision.StateAdmitted {
		t.Fatalf("locally attested tuple refused: %+v %v", result, err)
	}
}

func TestControllerRefreshDesiredStateRetainsLaunchFence(t *testing.T) {
	cr := &CityRuntime{cfg: &config.City{}}
	result := DesiredStateResult{State: map[string]TemplateParams{"live": {TemplateName: "demo/reviewer", RigName: "demo"}}, BaseState: map[string]TemplateParams{"base": {TemplateName: "demo/reviewer", RigName: "demo"}}}
	refreshed := cr.refreshDesiredState(result, nil)
	if refreshed.State["live"].RoutingLaunchCheck == nil {
		t.Fatal("refresh lost execution fence")
	}
}

func TestControllerDesiredStateCarriesLaunchFence(t *testing.T) {
	cr := &CityRuntime{buildFn: func(_ *config.City, _ runtime.Provider, _ beads.Store) DesiredStateResult {
		return DesiredStateResult{State: map[string]TemplateParams{"test": {TemplateName: "demo/reviewer", RigName: "demo"}}}
	}}
	result := cr.buildDesiredState(nil, nil)
	if result.State["test"].RoutingLaunchCheck == nil {
		t.Fatal("controller output lacks final launch fence")
	}
}

func TestControllerLaunchRechecksSignedSelectionAndLocalTuple(t *testing.T) {
	fixture := newApprovedRoutingDecisionFixture(t, "launch-template")
	p := fixture.payload
	p.Schema = routingdecision.ExecutionSchemaVersion
	p.DecisionID = "v3-launch"
	p.RecommendationID = "routing/v3:" + strings.Repeat("e", 64)
	p.ServeAs = "literal-model"
	p.Provider = "test-provider"
	p.Execution = &routingdecision.ExecutionBinding{Schema: 1, CanonicalModel: p.Model, ServeAs: p.ServeAs, ReasoningEffort: "high", Account: p.Account, Provider: p.Provider, Target: p.Target, ConfigDigest: p.TargetConfigDigest, AdapterID: "test", AdapterDigest: strings.Repeat("f", 64), InvocationDigest: strings.Repeat("a", 64)}
	p.BindingID = routingdecision.BindingID(p)
	fixture.cr.routingDecisionVerifier = approveRoutingDecision(t, fixture.ledger, p, fixture.cr.routingDecisionNow())
	fixture.cr.routingExecutionAdapter = func(_ config.Agent, _ *config.City, final *runtime.Config) (routingdecision.ExecutionBinding, error) {
		if final != nil && final.Command != "account-wrapper --model literal-model" {
			return routingdecision.ExecutionBinding{}, errors.New("wrong invocation")
		}
		return *p.Execution, nil
	}
	// Revoke the unrelated fixture so there is one ready-work authorization.
	old, _ := fixture.ledger.Get(fixture.payload.DecisionID)
	if _, err := fixture.ledger.Transition(routingdecision.TransitionRequest{DecisionID: old.Payload.DecisionID, ExpectedRevision: old.RecordRevision, From: old.State, To: routingdecision.StateRevoked, IdempotencyToken: "revoke-old", Reason: "test"}, routingdecision.Verifier{}); err != nil {
		t.Fatal(err)
	}
	if n, err := fixture.cr.applyApprovedRoutingDecisions(); err != nil || n != 1 {
		t.Fatalf("admission: %d %v", n, err)
	}
	info := sessionpkg.Info{TriggerBeadID: p.WorkBeadID}
	final := runtime.Config{Command: "account-wrapper --model literal-model"}
	if err := fixture.cr.checkRoutingExecutionLaunch(p.Target, p.Rig, info, final); err != nil {
		t.Fatalf("exact launch refused: %v", err)
	}
	final.Command = "different-account --model literal-model"
	if err := fixture.cr.checkRoutingExecutionLaunch(p.Target, p.Rig, info, final); err == nil {
		t.Fatal("overridden final command accepted")
	}
	final.Command = "account-wrapper --model literal-model"
	fixture.cr.routingDecisionNowFn = func() time.Time { return p.ExpiresAt }
	if err := fixture.cr.checkRoutingExecutionLaunch(p.Target, p.Rig, info, final); err == nil {
		t.Fatal("expired selection accepted")
	}
}

func TestPreparedLaunchChecksFinalExecutableBeforeRuntimeEffects(t *testing.T) {
	sp := runtime.NewFake()
	called := false
	cfg := runtime.Config{Command: "account-wrapper --model literal --reasoning high", Env: map[string]string{"HOME": "isolated"}}
	candidate := preparedStart{candidate: startCandidate{info: sessionpkg.Info{SessionNameMetadata: "signed-test"}, tp: TemplateParams{TemplateName: "worker", RoutingLaunchCheck: func(_ sessionpkg.Info, actual runtime.Config) error {
		called = true
		if actual.Command != cfg.Command || actual.Env["HOME"] != "isolated" {
			t.Fatal("guard did not receive final tuple")
		}
		return errors.New("tuple refused")
	}}}, cfg: cfg}
	_, err := startPreparedStartCandidate(context.Background(), candidate, "", nil, sp, nil, nil, nil, nil)
	if err == nil || !called {
		t.Fatalf("launch guard bypassed: called=%v err=%v", called, err)
	}
	if len(sp.Calls) != 0 {
		t.Fatalf("runtime effects before guard: %+v", sp.Calls)
	}
	candidate.candidate.tp.RoutingLaunchCheck = func(_ sessionpkg.Info, _ runtime.Config) error { return nil }
	_, err = startPreparedStartCandidate(context.Background(), candidate, "", nil, sp, nil, nil, nil, nil)
	if err != nil || !sp.IsRunning("signed-test") {
		t.Fatalf("allowed launch failed: %v", err)
	}
}
