package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

const routingDecisionAdmissionLimit = 256

type routingDecisionScope struct {
	label string
	rig   string
	store beads.Store
}

type routingDecisionAdmissionAttempt struct {
	postStampRevision int64
	stamped           bool
}

func (cr *CityRuntime) routingDecisionNow() time.Time {
	if cr.routingDecisionNowFn != nil {
		return cr.routingDecisionNowFn().UTC()
	}
	return time.Now().UTC()
}

func (cr *CityRuntime) routingDecisionScopes() []routingDecisionScope {
	scopes := []routingDecisionScope{{label: "city", store: cr.cityBeadStore()}}
	rigStores := cr.rigBeadStores()
	rigNames := make([]string, 0, len(rigStores))
	for rig := range rigStores {
		rigNames = append(rigNames, rig)
	}
	sort.Strings(rigNames)
	for _, rig := range rigNames {
		scopes = append(scopes, routingDecisionScope{label: "rig " + rig, rig: rig, store: rigStores[rig]})
	}
	return scopes
}

func (cr *CityRuntime) decisionStoreForPass() (*routingdecision.Store, error) {
	if cr.routingDecisionStore != nil {
		return cr.routingDecisionStore, nil
	}
	return nil, errors.New("routing decision ledger unavailable")
}

// applyApprovedRoutingDecisions coordinates the signed local ledger with one
// readiness+revision bead CAS. It only writes route metadata; normal demand
// reconciliation remains the sole session-launch path.
func (cr *CityRuntime) applyApprovedRoutingDecisions() (int, error) {
	if strings.TrimSpace(cr.cityPath) == "" || strings.TrimSpace(cr.cityName) == "" {
		return 0, nil
	}
	if cr.routingDecisionVerifier == nil {
		return 0, routingdecision.ErrAuthorizationRequired
	}
	decisionStore, err := cr.decisionStoreForPass()
	if err != nil {
		return 0, err
	}
	now := cr.routingDecisionNow()
	if _, err := decisionStore.ExpireDue(now, routingDecisionAdmissionLimit, func(decisionID string) string {
		return "controller-expire:" + decisionID
	}); err != nil {
		return 0, errors.New("routing decision ledger expiry refused")
	}
	records, err := decisionStore.ActiveApproved(now, routingDecisionAdmissionLimit, *cr.routingDecisionVerifier)
	if err != nil {
		return 0, errors.New("routing decision ledger query refused")
	}
	scopes := cr.routingDecisionScopes()
	byRig := make(map[string]routingDecisionScope, len(scopes))
	for _, scope := range scopes {
		if scope.store != nil {
			byRig[scope.rig] = scope
		}
	}
	applied := 0
	var safeErrors []error
	for _, record := range records {
		payload := record.Payload
		if payload.City != cr.cityName {
			safeErrors = append(safeErrors, fmt.Errorf("decision %q: scope refused", payload.DecisionID))
			continue
		}
		scope, ok := byRig[payload.Rig]
		if !ok {
			safeErrors = append(safeErrors, fmt.Errorf("decision %q: work scope unavailable", payload.DecisionID))
			continue
		}
		_, targetDigest, ok := cr.resolveRoutingDecisionTarget(payload.Target, payload.Rig)
		if !ok || targetDigest != payload.TargetConfigDigest {
			safeErrors = append(safeErrors, fmt.Errorf("decision %q: target binding refused", payload.DecisionID))
			continue
		}
		if _, ok := beads.ReadyConditionalWriterFor(scope.store); !ok {
			safeErrors = append(safeErrors, fmt.Errorf("decision %q: atomic readiness unavailable", payload.DecisionID))
			continue
		}
		attempt := routingDecisionAdmissionAttempt{}
		receipt, admissionErr := decisionStore.FinalAdmission(routingdecision.FinalAdmissionRequest{
			DecisionID: payload.DecisionID, ExpectedRevision: record.RecordRevision,
			IdempotencyToken: fmt.Sprintf("controller-admit:%s:%d", payload.DecisionID, record.RecordRevision),
		}, *cr.routingDecisionVerifier, func(gated routingdecision.Record) (routingdecision.AdmissionCallbackResult, error) {
			return cr.routeDecisionAtAdmissionBoundary(scope, gated, &attempt)
		})
		if errors.Is(admissionErr, routingdecision.ErrStaleRevision) || errors.Is(admissionErr, routingdecision.ErrInvalidTransition) {
			continue
		}
		if admissionErr != nil {
			if attempt.stamped {
				_ = cr.compensateRoutingDecisionStamp(scope, payload, attempt.postStampRevision)
			}
			safeErrors = append(safeErrors, fmt.Errorf("decision %q: admission refused", payload.DecisionID))
			continue
		}
		if receipt.State == routingdecision.StateAdmitted {
			applied++
		}
	}
	return applied, errors.Join(safeErrors...)
}

func (cr *CityRuntime) routeDecisionAtAdmissionBoundary(scope routingDecisionScope, record routingdecision.Record, attempt *routingDecisionAdmissionAttempt) (routingdecision.AdmissionCallbackResult, error) {
	payload := record.Payload
	_, targetDigest, ok := cr.resolveRoutingDecisionTarget(payload.Target, payload.Rig)
	if !ok || targetDigest != payload.TargetConfigDigest || payload.City != cr.cityName || payload.Rig != scope.rig {
		return routingdecision.AdmissionCallbackResult{State: routingdecision.StateRefusedAfterRace, Reason: "live target binding changed"}, nil
	}
	live, err := beads.HandlesFor(scope.store).Live.Get(payload.WorkBeadID)
	if err != nil {
		return routingdecision.AdmissionCallbackResult{}, errors.New("live work unavailable")
	}
	if routingDecisionExactStampedCarrier(live, payload) {
		ready, err := routingDecisionWorkReadyNow(scope.store, live.ID)
		if err != nil {
			return routingdecision.AdmissionCallbackResult{}, errors.New("live readiness unavailable")
		}
		if !ready {
			return routingdecision.AdmissionCallbackResult{State: routingdecision.StateRefusedAfterRace, Reason: "live work readiness changed"}, nil
		}
		return routingdecision.AdmissionCallbackResult{State: routingdecision.StateAdmitted, Reason: "reconciled exact approved route marker"}, nil
	}
	if !routingDecisionEligibleWork(live, payload, cr.routingDecisionNow()) {
		return routingdecision.AdmissionCallbackResult{State: routingdecision.StateRefusedAfterRace, Reason: "live work binding changed"}, nil
	}
	writer, ok := beads.ReadyConditionalWriterFor(scope.store)
	if !ok {
		return routingdecision.AdmissionCallbackResult{}, errors.New("atomic readiness unavailable")
	}
	updateErr := writer.UpdateIfReadyAndMatch(live.ID, live.Revision, beads.UpdateOpts{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey:                  payload.Target,
		beadmeta.RunTargetMetadataKey:                 payload.Target,
		beadmeta.RoutingDecisionClaimFenceMetadataKey: strconv.FormatInt(payload.ClaimFence, 10),
		beadmeta.RoutingDecisionIDMetadataKey:         payload.DecisionID,
	}})
	if beads.IsPreconditionFailed(updateErr) || errors.Is(updateErr, beads.ErrNotReadyForConditionalUpdate) {
		return routingdecision.AdmissionCallbackResult{State: routingdecision.StateRefusedAfterRace, Reason: "ready work CAS lost"}, nil
	}
	if updateErr != nil {
		return routingdecision.AdmissionCallbackResult{}, errors.New("ready work mutation unavailable")
	}
	stamped, err := beads.HandlesFor(scope.store).Live.Get(live.ID)
	if err != nil || !routingDecisionExactStampedCarrier(stamped, payload) {
		return routingdecision.AdmissionCallbackResult{}, errors.New("stamped work state unavailable")
	}
	if attempt != nil {
		attempt.postStampRevision = stamped.Revision
		attempt.stamped = true
	}
	return routingdecision.AdmissionCallbackResult{State: routingdecision.StateAdmitted, Reason: "exact ready work CAS committed"}, nil
}

func routingDecisionWorkReadyNow(store beads.Store, workID string) (bool, error) {
	ready, err := beads.HandlesFor(store).Live.Ready(beads.ReadyQuery{TierMode: beads.TierBoth})
	if err != nil {
		return false, err
	}
	for _, item := range ready {
		if item.ID == workID {
			return true, nil
		}
	}
	return false, nil
}

func (cr *CityRuntime) compensateRoutingDecisionStamp(scope routingDecisionScope, payload routingdecision.DecisionPayload, expectedRevision int64) error {
	live, err := beads.HandlesFor(scope.store).Live.Get(payload.WorkBeadID)
	if err != nil || live.Revision != expectedRevision || !routingDecisionExactStampedCarrier(live, payload) {
		return errors.New("exact compensation state unavailable")
	}
	writer, ok := beads.ReadyConditionalWriterFor(scope.store)
	if !ok {
		return errors.New("atomic compensation unavailable")
	}
	err = writer.UpdateIfReadyAndMatch(live.ID, expectedRevision, beads.UpdateOpts{Metadata: map[string]string{
		beadmeta.RoutedToMetadataKey:                  "",
		beadmeta.RunTargetMetadataKey:                 "",
		beadmeta.RoutingDecisionClaimFenceMetadataKey: "",
		beadmeta.RoutingDecisionIDMetadataKey:         "",
	}})
	if beads.IsPreconditionFailed(err) || errors.Is(err, beads.ErrNotReadyForConditionalUpdate) {
		return nil
	}
	if err != nil {
		return errors.New("atomic compensation unavailable")
	}
	return nil
}

func routingDecisionHasExistingControlMetadata(metadata map[string]string) bool {
	for _, key := range []string{
		beadmeta.SessionAffinityMetadataKey, beadmeta.BrainParentSIDMetadataKey,
		beadmeta.ControlForMetadataKey, beadmeta.ControlEpochMetadataKey,
		beadmeta.ControlQuarantinedMetadataKey, beadmeta.ControlQuarantinedAtMetadataKey,
		beadmeta.ControlQuarantineReasonMetadataKey, beadmeta.ControlDispatcherFallbackMetadataKey,
		beadmeta.AttachFencePendingMetadataKey, beadmeta.InstantiatingMetadataKey, beadmeta.DetachedMetadataKey,
	} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}

func routingDecisionEligibleWork(bead beads.Bead, payload routingdecision.DecisionPayload, now time.Time) bool {
	if bead.ID != payload.WorkBeadID || bead.Revision != payload.WorkRevision || bead.ClaimFence != payload.ClaimFence || bead.Status != "open" || strings.TrimSpace(bead.Assignee) != "" {
		return false
	}
	if bead.DeferUntil != nil && now.Before(*bead.DeferUntil) || bead.IsBlocked != nil && *bead.IsBlocked || routingDecisionHasExistingControlMetadata(bead.Metadata) {
		return false
	}
	state := routingdecision.WorkStateFrom(bead.ID, bead.Status, bead.Assignee, bead.Metadata, bead.ClaimFence)
	if state.RoutedTo != "" || state.RunTarget != "" || state.DecisionID != "" || state.ExecutionRoutedTo != "" || state.DeferredExecutionRoutedTo != "" || state.ExecutionRigContext != "" || state.DeferredAssignee != "" || state.DeferredRoutedTo != "" || state.Kind != "" || state.SessionID != "" || state.SessionName != "" || state.Continuation != "" || state.CurrentRunID != "" {
		return false
	}
	return routingdecision.WorkStateDigest(state) == payload.WorkStateDigest
}

func routingDecisionExactStampedCarrier(bead beads.Bead, payload routingdecision.DecisionPayload) bool {
	if bead.ID != payload.WorkBeadID || bead.Status != "open" || strings.TrimSpace(bead.Assignee) != "" || bead.ClaimFence != payload.ClaimFence || routingDecisionHasExistingControlMetadata(bead.Metadata) {
		return false
	}
	fence, err := strconv.ParseInt(strings.TrimSpace(bead.Metadata[beadmeta.RoutingDecisionClaimFenceMetadataKey]), 10, 64)
	if err != nil || fence != payload.ClaimFence {
		return false
	}
	state := routingdecision.WorkStateFrom(bead.ID, bead.Status, bead.Assignee, bead.Metadata, bead.ClaimFence)
	return state.RoutedTo == payload.Target && state.RunTarget == payload.Target && state.DecisionID == payload.DecisionID && state.ExecutionRoutedTo == "" && state.DeferredExecutionRoutedTo == "" && state.ExecutionRigContext == "" && state.DeferredAssignee == "" && state.DeferredRoutedTo == "" && state.Kind == "" && state.SessionID == "" && state.SessionName == "" && state.Continuation == "" && state.CurrentRunID == ""
}

func (cr *CityRuntime) applyApprovedRoutingDecisionsAndLog() {
	applied, err := cr.applyApprovedRoutingDecisions()
	if err != nil {
		fmt.Fprintf(cr.stderr, "%s: routing decision admission refused\n", cr.logPrefix) //nolint:errcheck // best-effort sanitized diagnostics
	}
	if applied > 0 {
		fmt.Fprintf(cr.stderr, "%s: routing decision admission routed %d newly ready work bead(s)\n", cr.logPrefix, applied) //nolint:errcheck // best-effort diagnostics
	}
}

func (cr *CityRuntime) reconcileRoutingDecisionsAndLog() {
	cr.applyApprovedRoutingDecisionsAndLog()
	if _, err := cr.reconcileRoutingDecisionLifecycle(); err != nil {
		fmt.Fprintf(cr.stderr, "%s: routing decision lifecycle refused\n", cr.logPrefix) //nolint:errcheck // best-effort sanitized diagnostics
	}
	if err := cr.advanceRoutingDecisionPurge(); err != nil {
		fmt.Fprintf(cr.stderr, "%s: routing decision retention refused\n", cr.logPrefix) //nolint:errcheck // best-effort sanitized diagnostics
	}
}
