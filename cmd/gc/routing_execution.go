package main

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// checkRoutingExecutionLaunch re-reads the exact carrier and immutable signed
// approval; the final invocation is attested independently by a local adapter.
// Legacy decisions retain their prior launch behavior. No adapter means deny,
// never copying the signed tuple back as an alleged local observation.
func (cr *CityRuntime) checkRoutingExecutionLaunch(target, rig string, info sessionpkg.Info, final runtime.Config) error {
	var bound *routingdecision.ExecutionSessionAuthorization
	if cr.routingDecisionStore != nil && info.ID != "" {
		var err error
		bound, err = cr.routingDecisionStore.ExecutionSession(info.ID)
		if err != nil {
			return errors.New("routing session authority unavailable")
		}
		if bound != nil {
			if info.TriggerBeadID != bound.WorkID || info.Generation != bound.Generation || info.InstanceToken != bound.InstanceToken {
				return errors.New("routing session authority changed")
			}
			info.RoutingExecutionDecisionID = bound.DecisionID
		}
	}
	if info.TriggerBeadID == "" {
		if info.RequiresRoutingExecution() {
			return errors.New("routing trigger missing")
		}
		return nil
	}
	var workStore beads.Store
	for _, scope := range cr.routingDecisionScopes() {
		if scope.rig == rig {
			workStore = scope.store
			break
		}
	}
	if workStore == nil {
		return errors.New("routing work unavailable")
	}
	work, err := beads.HandlesFor(workStore).Live.Get(info.TriggerBeadID)
	if err != nil {
		return errors.New("routing work unavailable")
	}
	decisionID := strings.TrimSpace(work.Metadata[beadmeta.RoutingDecisionIDMetadataKey])
	if decisionID == "" {
		if info.RequiresRoutingExecution() {
			return errors.New("routing carrier marker missing")
		}
		return nil
	}
	if info.RoutingExecutionDecisionID != "" && info.RoutingExecutionDecisionID != decisionID {
		return errors.New("routing session migration refused")
	}
	if cr.routingDecisionStore == nil || cr.routingDecisionVerifier == nil {
		return routingdecision.ErrAuthorizationRequired
	}
	record, err := cr.routingDecisionStore.Get(decisionID)
	if err != nil {
		return errors.New("routing decision unavailable")
	}
	p := record.Payload
	if p.Schema == routingdecision.SchemaVersion {
		if info.RequiresRoutingExecution() {
			return errors.New("routing schema downgrade refused")
		}
		return nil
	}
	continuing := bound != nil && bound.DecisionID == p.DecisionID && work.Status == "in_progress" &&
		work.ClaimFence == p.ClaimFence+1 && slices.Contains(sessionpkg.AssigneeIdentities(info), strings.TrimSpace(work.Assignee)) &&
		strings.TrimSpace(work.Metadata[beadmeta.RoutedToMetadataKey]) == ""
	if (record.State != routingdecision.StateAdmitted && (!continuing || record.State != routingdecision.StateClaimed)) || record.Approval == nil || record.Signature == nil || (!continuing && !p.IsActiveAt(cr.routingDecisionNow())) {
		return errors.New("routing selection unavailable")
	}
	if err := cr.routingDecisionVerifier.Verify(p, *record.Approval, *record.Signature); err != nil {
		return err
	}
	if p.City != cr.cityName || p.Rig != rig || p.Target != target || p.WorkBeadID != work.ID ||
		(!continuing && (work.Status != "open" || strings.TrimSpace(work.Assignee) != "" || work.ClaimFence != p.ClaimFence)) ||
		work.Metadata[beadmeta.RunTargetMetadataKey] != p.Target ||
		work.Metadata[beadmeta.RoutingDecisionClaimFenceMetadataKey] != strconv.FormatInt(p.ClaimFence, 10) {
		return errors.New("routing carrier changed")
	}
	agent, digest, ok := cr.resolveRoutingDecisionTarget(target, rig)
	if !ok || digest != p.TargetConfigDigest || cr.routingExecutionAdapter == nil {
		return errors.New("routing execution adapter unavailable")
	}
	actual, err := cr.routingExecutionAdapter(agent, cr.cfg, &final)
	if err != nil {
		return errors.New("routing invocation unresolved")
	}
	return p.MatchesExecution(actual)
}
