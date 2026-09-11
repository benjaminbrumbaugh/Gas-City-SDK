package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/agentutil"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

type cityRoutingDecisionService struct {
	mu               sync.RWMutex
	ingestWG         sync.WaitGroup
	store            *routingdecision.Store
	verifier         *routingdecision.Verifier
	status           string
	reason           string
	authorityReady   bool
	closed           bool
	purgeCursor      string
	lifecycleCursor  string
	targets          func() ([]routingdecision.TargetSnapshot, error)
	eligible         func() (routingdecision.SelectionSnapshot, error)
	now              func() time.Time
	outcomeWork      func(context.Context, routingdecision.DecisionPayload) (routingdecision.OutcomeWorkSnapshot, error)
	outcomeAuthority func(context.Context, routingdecision.DecisionPayload) routingdecision.OutcomeAuthoritySnapshot
	reconcile        func()
}

func initializeRoutingDecisionService(cr *CityRuntime) {
	service := &cityRoutingDecisionService{
		status: routingdecision.AvailabilityDenied, reason: routingdecision.ReasonAuthorityUnavailable,
		targets: cr.routingDecisionTargetSnapshots, eligible: cr.routingDecisionEligibleSnapshot,
		now: cr.routingDecisionNow, outcomeWork: cr.routingDecisionOutcomeWork,
		outcomeAuthority: cr.routingDecisionOutcomeAuthority,
		reconcile:        cr.reconcileRoutingDecisionsAndLog,
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
	service.ingestWG.Wait()
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
		var authority routingdecision.OutcomeAuthoritySnapshot
		if service.outcomeAuthority != nil {
			authority = service.outcomeAuthority(ctx, decision.Record.Payload)
		}
		outcome := routingdecision.ProjectOutcome(decision, work, authority, observedAt)
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
	// Carrier metadata is restricted to the binding allowlist. Causal keys
	// (gc.work_outcome, gc.failure_class, gc.session_id, gc.current_run_id)
	// are deliberately NOT collected: they are caller-authored open-world
	// values that must never reach the projector as evidence.
	metadata := make(map[string]string, 4)
	for _, key := range []string{
		beadmeta.RoutingDecisionIDMetadataKey, beadmeta.RoutingDecisionClaimFenceMetadataKey,
		beadmeta.RunTargetMetadataKey, beadmeta.RoutedToMetadataKey,
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

// routingOutcomeAuthorityLimits bound every journal read in the causal-authority
// resolver to a tail window. The event journal has no RunID index seam, so the
// reads are exact-subject/type filtered and tail-capped instead of open-world
// scans; evidence outside the window stays unavailable (fail closed), never
// guessed.
const (
	routingOutcomeAssociationTailLimit = 16
	routingOutcomeCompletedTailLimit   = 64
	routingOutcomeMaxScanBytes         = 1 << 20
)

// routingDecisionOutcomeAuthority resolves the typed causal-authority snapshot
// for one decision using ONLY real SDK-owned records:
//
//  1. Authoritative work→run association: execution.work_associated facts are
//     minted solely by executionevent.EmitCurrent from the live convoy tracks
//     graph; a fact whose Subject equals this decision's exact WorkBeadID names
//     its workflow run in RunID.
//  2. Terminal execution record: an execution.step_completed fact minted by
//     executionevent.EmitLifecycle — which loads the workflow root from the
//     graph store and rejects non-graph.v2 parents — carries RunID plus the
//     SessionID it validated against the step bead.
//  3. Session record: session.Store.Get over the session-class store proves the
//     referenced id IS a session bead, so identity binds to a real record.
//
// Any missing layer leaves the corresponding snapshot fields nil; the
// projector fails closed. No terminal-disposition authority exists yet
// (gc.work_outcome is caller-authored), so TerminalDispositionKnown stays
// false here by construction — succeeded/failed-known outcomes are
// unreachable from production input until a real disposition record class
// lands. That gap is recorded on OutcomeAuthoritySnapshot, not papered over.
func (cr *CityRuntime) routingDecisionOutcomeAuthority(ctx context.Context, payload routingdecision.DecisionPayload) routingdecision.OutcomeAuthoritySnapshot {
	if err := ctx.Err(); err != nil || strings.TrimSpace(cr.cityPath) == "" {
		return routingdecision.OutcomeAuthoritySnapshot{}
	}
	eventsPath := filepath.Join(cr.cityPath, citylayout.RuntimeRoot, "events.jsonl")
	associations, err := events.ReadFilteredTail(eventsPath, events.Filter{
		Type: events.ExecutionWorkAssociated, Subject: payload.WorkBeadID,
		MaxScanBytes: routingOutcomeMaxScanBytes,
	}, routingOutcomeAssociationTailLimit)
	if err != nil || len(associations) == 0 {
		return routingdecision.OutcomeAuthoritySnapshot{}
	}
	runIDs := make(map[string]struct{}, len(associations))
	for _, association := range associations {
		if association.RunID != "" {
			runIDs[association.RunID] = struct{}{}
		}
	}
	completions, err := events.ReadFilteredTail(eventsPath, events.Filter{
		Type: events.ExecutionStepCompleted, MaxScanBytes: routingOutcomeMaxScanBytes,
	}, routingOutcomeCompletedTailLimit)
	if err != nil {
		return routingdecision.OutcomeAuthoritySnapshot{}
	}
	for _, completion := range completions {
		if _, associated := runIDs[completion.RunID]; !associated {
			continue
		}
		sessionID := strings.TrimSpace(completion.SessionID)
		if sessionID == "" {
			continue
		}
		var store beads.Store
		for _, scope := range cr.routingDecisionScopes() {
			if scope.rig == payload.Rig && scope.store != nil {
				store = scope.store
				break
			}
		}
		if store == nil {
			return routingdecision.OutcomeAuthoritySnapshot{}
		}
		if _, err := sessionFrontDoor(store).Get(sessionID); err != nil {
			// The execution fact named a session record that does not exist:
			// ambiguous evidence fails closed rather than partially reporting.
			continue
		}
		executionID := strings.TrimSpace(completion.RunID)
		return routingdecision.OutcomeAuthoritySnapshot{
			Session:                  &routingdecision.OutcomeSessionRecord{SessionID: sessionID},
			Execution:                &routingdecision.OutcomeExecutionRecord{ExecutionID: executionID, SessionID: sessionID, Completed: true},
			TerminalDispositionKnown: false,
		}
	}
	return routingdecision.OutcomeAuthoritySnapshot{}
}

func (service *cityRoutingDecisionService) Ingest(ctx context.Context, request routingdecision.IngestApprovedRequest) (routingdecision.IngestApprovedResult, error) {
	if err := ctx.Err(); err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	service.mu.RLock()
	if service.closed || service.status != routingdecision.AvailabilityReady || service.store == nil || service.verifier == nil {
		service.mu.RUnlock()
		return routingdecision.IngestApprovedResult{}, errors.New("routing decision service unavailable")
	}
	store := service.store
	verifier := *service.verifier
	reconcile := service.reconcile
	service.ingestWG.Add(1)
	service.mu.RUnlock()
	defer service.ingestWG.Done()
	result, err := store.IngestApproved(request, verifier)
	if err != nil {
		return routingdecision.IngestApprovedResult{}, err
	}
	if reconcile != nil {
		reconcile()
		current, getErr := store.Get(request.Payload.DecisionID)
		if getErr != nil {
			return routingdecision.IngestApprovedResult{}, getErr
		}
		result.Record = current
		result.Receipt = routingdecision.TransitionReceipt{
			DecisionID: current.Payload.DecisionID, State: current.State,
			RecordRevision: current.RecordRevision, StoreRevision: current.StoreRevision,
		}
	}
	return result, nil
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

func (cr *CityRuntime) reconcileRoutingDecisionLifecycle() (int, error) { //nolint:unparam // count is retained for focused lifecycle diagnostics.
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
