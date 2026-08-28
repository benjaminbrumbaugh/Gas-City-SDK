package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

type routingDecisionRecoveryAuthorizer struct {
	cr       *CityRuntime
	store    *routingdecision.Store
	verifier *routingdecision.Verifier
	now      time.Time
}

func (cr *CityRuntime) newRoutingDecisionRecoveryAuthorizer(now time.Time) routingDecisionRecoveryAuthorizer {
	authorizer := routingDecisionRecoveryAuthorizer{cr: cr, now: now.UTC()}
	if cr == nil || cr.routingDecisionVerifier == nil || strings.TrimSpace(cr.cityPath) == "" {
		return authorizer
	}
	authorizer.verifier = cr.routingDecisionVerifier
	store, err := cr.decisionStoreForPass()
	if err != nil {
		return authorizer
	}
	authorizer.store = store
	return authorizer
}

// Allows preserves legacy unmarked recovery and admits a marked carried route
// only from the exact authentic, unexpired admitted record.
func (authorizer routingDecisionRecoveryAuthorizer) Allows(rig string, bead beads.Bead) bool {
	decisionID := strings.TrimSpace(bead.Metadata[beadmeta.RoutingDecisionIDMetadataKey])
	if decisionID == "" {
		return true
	}
	if authorizer.cr == nil || authorizer.store == nil || authorizer.verifier == nil {
		return false
	}
	record, err := authorizer.store.Get(decisionID)
	if err != nil || record.State != routingdecision.StateAdmitted || record.Approval == nil || record.Signature == nil || !record.Payload.IsActiveAt(authorizer.cr.routingDecisionNow()) {
		return false
	}
	if authorizer.verifier.Verify(record.Payload, *record.Approval, *record.Signature) != nil {
		return false
	}
	payload := record.Payload
	if payload.City != authorizer.cr.cityName || payload.Rig != rig || payload.WorkBeadID != bead.ID {
		return false
	}
	_, digest, ok := authorizer.cr.resolveRoutingDecisionTarget(payload.Target, rig)
	if !ok || digest != payload.TargetConfigDigest {
		return false
	}
	return routingDecisionAdmittedCarrierEligible(bead, payload)
}

func routingDecisionAdmittedCarrierEligible(bead beads.Bead, payload routingdecision.DecisionPayload) bool {
	if bead.Status != "open" || strings.TrimSpace(bead.Assignee) != "" || bead.ClaimFence != payload.ClaimFence || routingDecisionHasExistingControlMetadata(bead.Metadata) {
		return false
	}
	fence, err := strconv.ParseInt(strings.TrimSpace(bead.Metadata[beadmeta.RoutingDecisionClaimFenceMetadataKey]), 10, 64)
	if err != nil || fence != payload.ClaimFence {
		return false
	}
	state := routingdecision.WorkStateFrom(bead.ID, bead.Status, bead.Assignee, bead.Metadata, bead.ClaimFence)
	return state.RoutedTo == "" && state.RunTarget == payload.Target && state.DecisionID == payload.DecisionID && state.ExecutionRoutedTo == "" && state.DeferredExecutionRoutedTo == "" && state.ExecutionRigContext == "" && state.DeferredAssignee == "" && state.DeferredRoutedTo == "" && state.Kind == "" && state.SessionID == "" && state.SessionName == "" && state.Continuation == "" && state.CurrentRunID == ""
}
