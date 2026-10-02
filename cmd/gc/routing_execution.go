package main

import (
	"errors"
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
	if info.TriggerBeadID == "" {
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
		return nil
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
		return nil
	}
	if record.State != routingdecision.StateAdmitted || record.Approval == nil || record.Signature == nil || !p.IsActiveAt(cr.routingDecisionNow()) {
		return errors.New("routing selection unavailable")
	}
	if err := cr.routingDecisionVerifier.Verify(p, *record.Approval, *record.Signature); err != nil {
		return err
	}
	if p.City != cr.cityName || p.Rig != rig || p.Target != target || p.WorkBeadID != work.ID ||
		work.Status != "open" || strings.TrimSpace(work.Assignee) != "" || work.ClaimFence != p.ClaimFence ||
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
