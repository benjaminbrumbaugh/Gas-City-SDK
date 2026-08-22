package main

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/agentutil"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

type cityRoutingDecisionService struct {
	mu              sync.RWMutex
	store           *routingdecision.Store
	verifier        *routingdecision.Verifier
	status          string
	reason          string
	authorityReady  bool
	closed          bool
	purgeCursor     string
	lifecycleCursor string
	targets         func() ([]routingdecision.TargetSnapshot, error)
	eligible        func() (routingdecision.SelectionSnapshot, error)
	now             func() time.Time
	outcomeWork     func(context.Context, routingdecision.DecisionPayload) (routingdecision.OutcomeWorkSnapshot, error)
}

func initializeRoutingDecisionService(cr *CityRuntime) {
	service := &cityRoutingDecisionService{
		status: routingdecision.AvailabilityDenied, reason: routingdecision.ReasonAuthorityUnavailable,
		targets: cr.routingDecisionTargetSnapshots, eligible: cr.routingDecisionEligibleSnapshot,
		now: cr.routingDecisionNow, outcomeWork: cr.routingDecisionOutcomeWork,
	}
	cr.routingDecisionService = service
	verifier, err := routingdecision.LoadAuthorityFile(cr.cityPath)
	if err != nil {
		if !errors.Is(err, routingdecision.ErrAuthorizationRequired) {
			service.reason = routingdecision.ReasonAuthorityInvalid
		}
		return
	}
	service.authorityReady = true
	store, err := routingdecision.OpenStore(cr.cityPath, routingdecision.StoreOptions{Now: cr.routingDecisionNow})
	if err != nil {
		service.reason = routingdecision.ReasonLedgerUnavailable
		return
	}
	if _, err := store.Verify(verifier); err != nil {
		_ = store.Close()
		service.reason = routingdecision.ReasonLedgerInvalid
		return
	}
	service.store = store
	service.verifier = &verifier
	service.status = routingdecision.AvailabilityReady
	service.reason = routingdecision.ReasonReady
	cr.routingDecisionStore = store
	cr.routingDecisionVerifier = &verifier
}

func (service *cityRoutingDecisionService) Status() routingdecision.LiveStatus {
	service.mu.RLock()
	defer service.mu.RUnlock()
	status := routingdecision.LiveStatus{
		Schema: routingdecision.SchemaVersion, Status: service.status, Reason: service.reason,
		AuthorityReady: service.authorityReady, RetentionMonths: routingdecision.TerminalRetentionMonths,
		TerminalStateBasis: "latest_terminal_transition_at",
	}
	if service.closed {
		status.Status = routingdecision.AvailabilityDenied
		status.Reason = routingdecision.ReasonServiceClosed
		return status
	}
	if service.store != nil {
		storeStatus, err := service.store.Status()
		if err != nil {
			status.Status = routingdecision.AvailabilityDenied
			status.Reason = routingdecision.ReasonLedgerInvalid
			return status
		}
		status.Store = storeStatus
	}
	return status
}

func (service *cityRoutingDecisionService) Close() {
	if service == nil {
		return
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.closed = true
	store := service.store
	service.store = nil
	service.verifier = nil
	service.mu.Unlock()
	if store != nil {
		_ = store.Close()
	}
}

func (service *cityRoutingDecisionService) Targets(ctx context.Context) ([]routingdecision.TargetSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed {
		return nil, errors.New("routing decision service unavailable")
	}
	return service.targets()
}

func (service *cityRoutingDecisionService) Eligible(ctx context.Context) (routingdecision.SelectionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.SelectionSnapshot{}, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed {
		return routingdecision.SelectionSnapshot{}, errors.New("routing decision service unavailable")
	}
	return service.eligible()
}

func (service *cityRoutingDecisionService) List(ctx context.Context, opts routingdecision.ListOptions) (routingdecision.DecisionPage, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.DecisionPage{}, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed || service.status != routingdecision.AvailabilityReady || service.store == nil {
		return routingdecision.DecisionPage{}, errors.New("routing decision service unavailable")
	}
	return service.store.ListDecisions(opts)
}

func (service *cityRoutingDecisionService) Outcomes(ctx context.Context, opts routingdecision.OutcomeListOptions) (routingdecision.OutcomePage, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.OutcomePage{}, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed || service.status != routingdecision.AvailabilityReady || service.store == nil || service.outcomeWork == nil {
		return routingdecision.OutcomePage{}, errors.New("routing decision service unavailable")
	}
	decisions, err := service.store.ListOutcomeDecisions(opts)
	if err != nil {
		return routingdecision.OutcomePage{}, err
	}
	observedAt := time.Now().UTC()
	if service.now != nil {
		observedAt = service.now().UTC()
	}
	page := routingdecision.OutcomePage{
		SchemaVersion: routingdecision.OutcomeSchemaVersion,
		Items:         make([]routingdecision.OutcomeRecord, 0, len(decisions.Items)),
		NextCursor:    decisions.NextCursor,
	}
	for _, decision := range decisions.Items {
		work, workErr := service.outcomeWork(ctx, decision.Record.Payload)
		if workErr != nil {
			work = routingdecision.OutcomeWorkSnapshot{}
		}
		outcome := routingdecision.ProjectOutcome(decision, work, observedAt)
		if err := outcome.Validate(); err != nil {
			return routingdecision.OutcomePage{}, errors.New("routing outcome projection invalid")
		}
		if outcome.Coverage != routingdecision.OutcomeCoverageAvailable {
			page.Partial = true
		}
		page.Items = append(page.Items, outcome)
	}
	return page, nil
}

func (cr *CityRuntime) routingDecisionOutcomeWork(ctx context.Context, payload routingdecision.DecisionPayload) (routingdecision.OutcomeWorkSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.OutcomeWorkSnapshot{}, err
	}
	var store beads.Store
	for _, scope := range cr.routingDecisionScopes() {
		if scope.rig == payload.Rig {
			store = scope.store
			break
		}
	}
	if store == nil {
		return routingdecision.OutcomeWorkSnapshot{}, errors.New("routing outcome work scope unavailable")
	}
	work, err := beads.HandlesFor(store).Live.Get(payload.WorkBeadID)
	if err != nil {
		return routingdecision.OutcomeWorkSnapshot{}, errors.New("routing outcome work unavailable")
	}
	metadata := make(map[string]string, 9)
	for _, key := range []string{
		beadmeta.RoutingDecisionIDMetadataKey, beadmeta.RoutingDecisionClaimFenceMetadataKey,
		beadmeta.RunTargetMetadataKey, beadmeta.RoutedToMetadataKey,
		beadmeta.WorkOutcomeMetadataKey, beadmeta.FailureClassMetadataKey,
		beadmeta.SessionIDMetadataKey, beadmeta.SessionIDCamelMetadataKey,
		beadmeta.CurrentRunIDMetadataKey,
	} {
		if value := work.Metadata[key]; value != "" {
			metadata[key] = value
		}
	}
	return routingdecision.OutcomeWorkSnapshot{
		Found: true, WorkID: work.ID, Status: work.Status, Assignee: work.Assignee,
		ClaimFence: work.ClaimFence, Metadata: metadata,
	}, nil
}

func (service *cityRoutingDecisionService) Ingest(ctx context.Context, request routingdecision.IngestApprovedRequest) (routingdecision.IngestApprovedResult, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed || service.status != routingdecision.AvailabilityReady || service.store == nil || service.verifier == nil {
		return routingdecision.IngestApprovedResult{}, errors.New("routing decision service unavailable")
	}
	return service.store.IngestApproved(request, *service.verifier)
}

func (cr *CityRuntime) routingDecisionTargetSnapshots() ([]routingdecision.TargetSnapshot, error) {
	cr.serviceStateMu.RLock()
	defer cr.serviceStateMu.RUnlock()
	if cr.cfg == nil {
		return []routingdecision.TargetSnapshot{}, nil
	}
	items := make([]routingdecision.TargetSnapshot, 0, len(cr.cfg.Agents))
	seen := make(map[string]struct{}, len(cr.cfg.Agents))
	for index := range cr.cfg.Agents {
		configured := &cr.cfg.Agents[index]
		target := agentutil.RoutedToIdentity(configured)
		agent, digest, ok := cr.resolveRoutingDecisionTarget(target, strings.TrimSpace(configured.Dir))
		if !ok {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		resolved, err := config.ResolveProvider(&agent, &cr.cfg.Workspace, cr.cfg.Providers, func(file string) (string, error) {
			if strings.TrimSpace(file) == "" {
				return "", errors.New("empty provider executable")
			}
			return file, nil
		})
		if err != nil {
			continue
		}
		seen[target] = struct{}{}
		items = append(items, routingdecision.TargetSnapshot{
			Target: target, Rig: strings.TrimSpace(agent.Dir), Description: agent.Description,
			ResolvedProvider: resolved.Name, ConfigDigest: digest,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rig != items[j].Rig {
			return items[i].Rig < items[j].Rig
		}
		return items[i].Target < items[j].Target
	})
	return items, nil
}

func (cr *CityRuntime) routingDecisionEligibleSnapshot() (routingdecision.SelectionSnapshot, error) {
	observedAt := cr.routingDecisionNow()
	targets, err := cr.routingDecisionTargetSnapshots()
	if err != nil {
		return routingdecision.SelectionSnapshot{}, err
	}
	result := routingdecision.SelectionSnapshot{
		ObservedAt: observedAt, Work: []routingdecision.EligibleWorkSnapshot{}, Targets: targets,
	}
	for _, scope := range cr.routingDecisionScopes() {
		if scope.store == nil || len(result.Work) >= routingDecisionAdmissionLimit {
			continue
		}
		ready, err := beads.HandlesFor(scope.store).Live.Ready(beads.ReadyQuery{
			Limit: routingDecisionAdmissionLimit - len(result.Work), TierMode: beads.TierBoth,
		})
		if err != nil {
			return routingdecision.SelectionSnapshot{}, errors.New("routing decision eligible work unavailable")
		}
		for _, candidate := range ready {
			if len(result.Work) >= routingDecisionAdmissionLimit {
				break
			}
			live, err := beads.HandlesFor(scope.store).Live.Get(candidate.ID)
			if err != nil {
				return routingdecision.SelectionSnapshot{}, errors.New("routing decision eligible work unavailable")
			}
			state, ok := routingDecisionSelectionState(live, observedAt)
			if !ok {
				continue
			}
			scopeName := "rig"
			if scope.rig == "" {
				scopeName = "city"
			}
			result.Work = append(result.Work, routingdecision.EligibleWorkSnapshot{
				Rig: scope.rig, Scope: scopeName, WorkBeadID: live.ID, WorkRevision: live.Revision,
				ClaimFence: live.ClaimFence, WorkStateDigest: routingdecision.WorkStateDigest(state),
			})
		}
	}
	sort.Slice(result.Work, func(i, j int) bool {
		if result.Work[i].Rig != result.Work[j].Rig {
			return result.Work[i].Rig < result.Work[j].Rig
		}
		return result.Work[i].WorkBeadID < result.Work[j].WorkBeadID
	})
	return result, nil
}

func routingDecisionSelectionState(bead beads.Bead, now time.Time) (routingdecision.WorkState, bool) {
	if bead.Status != "open" || strings.TrimSpace(bead.Assignee) != "" ||
		bead.DeferUntil != nil && now.Before(*bead.DeferUntil) ||
		bead.IsBlocked != nil && *bead.IsBlocked || routingDecisionHasExistingControlMetadata(bead.Metadata) {
		return routingdecision.WorkState{}, false
	}
	state := routingdecision.WorkStateFrom(bead.ID, bead.Status, bead.Assignee, bead.Metadata, bead.ClaimFence)
	if state.RoutedTo != "" || state.RunTarget != "" || state.DecisionID != "" || state.ExecutionRoutedTo != "" ||
		state.DeferredExecutionRoutedTo != "" || state.ExecutionRigContext != "" || state.DeferredAssignee != "" ||
		state.DeferredRoutedTo != "" || state.Kind != "" || state.SessionID != "" || state.SessionName != "" ||
		state.Continuation != "" || state.CurrentRunID != "" {
		return routingdecision.WorkState{}, false
	}
	return state, true
}

func (cr *CityRuntime) reconcileRoutingDecisionLifecycle() (int, error) {
	if cr.routingDecisionStore == nil {
		return 0, nil
	}
	cursor := ""
	if cr.routingDecisionService != nil {
		cr.routingDecisionService.mu.RLock()
		cursor = cr.routingDecisionService.lifecycleCursor
		cr.routingDecisionService.mu.RUnlock()
	}
	page, err := cr.routingDecisionStore.ListDecisions(routingdecision.ListOptions{Limit: routingDecisionAdmissionLimit, Cursor: cursor})
	if err != nil {
		return 0, errors.New("routing decision lifecycle query refused")
	}
	if cr.routingDecisionService != nil {
		cr.routingDecisionService.mu.Lock()
		cr.routingDecisionService.lifecycleCursor = page.NextCursor
		cr.routingDecisionService.mu.Unlock()
	}
	scopes := cr.routingDecisionScopes()
	byRig := make(map[string]routingDecisionScope, len(scopes))
	for _, scope := range scopes {
		if scope.store != nil {
			byRig[scope.rig] = scope
		}
	}
	transitioned := 0
	for _, item := range page.Items {
		record := item.Record
		if record.State != routingdecision.StateAdmitted && record.State != routingdecision.StateClaimed {
			continue
		}
		scope, ok := byRig[record.Payload.Rig]
		if !ok || record.Payload.City != cr.cityName {
			continue
		}
		live, err := beads.HandlesFor(scope.store).Live.Get(record.Payload.WorkBeadID)
		if err != nil {
			continue
		}
		to, reason, ok := observedRoutingDecisionTransition(record, live)
		if !ok {
			continue
		}
		_, err = cr.routingDecisionStore.Transition(routingdecision.TransitionRequest{
			DecisionID: record.Payload.DecisionID, ExpectedRevision: record.RecordRevision,
			From: record.State, To: to,
			IdempotencyToken: "controller-lifecycle:" + record.Payload.DecisionID + ":" + strconv.FormatUint(record.RecordRevision, 10) + ":" + string(to),
			Reason:           reason,
		}, routingdecision.Verifier{})
		if errors.Is(err, routingdecision.ErrStaleRevision) || errors.Is(err, routingdecision.ErrInvalidTransition) {
			continue
		}
		if err != nil {
			return transitioned, errors.New("routing decision lifecycle transition refused")
		}
		transitioned++
	}
	return transitioned, nil
}

func observedRoutingDecisionTransition(record routingdecision.Record, bead beads.Bead) (routingdecision.State, string, bool) {
	payload := record.Payload
	if bead.ID != payload.WorkBeadID || bead.Metadata[beadmeta.RoutingDecisionIDMetadataKey] != payload.DecisionID ||
		bead.Metadata[beadmeta.RunTargetMetadataKey] != payload.Target {
		return "", "", false
	}
	stampedFence, err := strconv.ParseInt(strings.TrimSpace(bead.Metadata[beadmeta.RoutingDecisionClaimFenceMetadataKey]), 10, 64)
	if err != nil || stampedFence != payload.ClaimFence {
		return "", "", false
	}
	switch record.State {
	case routingdecision.StateAdmitted:
		if bead.Status == "in_progress" && strings.TrimSpace(bead.Assignee) != "" && bead.ClaimFence > payload.ClaimFence && strings.TrimSpace(bead.Metadata[beadmeta.RoutedToMetadataKey]) == "" {
			return routingdecision.StateClaimed, "exact carrier claim observed", true
		}
		if bead.Status == "closed" && strings.TrimSpace(bead.Assignee) == "" && bead.ClaimFence == payload.ClaimFence {
			return routingdecision.StateOutcomeRecorded, "exact unclaimed carrier closure observed", true
		}
	case routingdecision.StateClaimed:
		if bead.Status == "closed" && strings.TrimSpace(bead.Assignee) != "" && bead.ClaimFence > payload.ClaimFence && strings.TrimSpace(bead.Metadata[beadmeta.RoutedToMetadataKey]) == "" {
			return routingdecision.StateOutcomeRecorded, "exact claimed carrier closure observed", true
		}
	}
	return "", "", false
}

func (cr *CityRuntime) advanceRoutingDecisionPurge() error {
	if cr.routingDecisionService == nil || cr.routingDecisionStore == nil {
		return nil
	}
	cr.routingDecisionService.mu.RLock()
	cursor := cr.routingDecisionService.purgeCursor
	cr.routingDecisionService.mu.RUnlock()
	result, err := cr.routingDecisionStore.PurgeTerminal(routingdecision.PurgeOptions{
		Now: cr.routingDecisionNow(), Limit: routingDecisionAdmissionLimit, Cursor: cursor,
	})
	if err != nil {
		return errors.New("routing decision retention refused")
	}
	cr.routingDecisionService.mu.Lock()
	cr.routingDecisionService.purgeCursor = result.NextCursor
	cr.routingDecisionService.mu.Unlock()
	return nil
}
