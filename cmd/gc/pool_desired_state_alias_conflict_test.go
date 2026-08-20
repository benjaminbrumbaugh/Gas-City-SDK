package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// aliasConflictPoolTemplate is the pool template every fixture in this file
// uses; the cases vary the ALIAS, which is what the resolution turns on.
const aliasConflictPoolTemplate = "rig/polecat"

// conflictedPoolSessionBead builds a LIVE pool session bead that lost the
// namepool alias slot race: the namepool handed its intended canonical alias to
// another session, so `alias` was never stamped and the deferred value survives
// only in pool_alias_conflict. This is the exact on-disk shape observed on
// session bead gc-vlg4 during the gc-3040 incident.
func conflictedPoolSessionBead(id, deferredAlias string) beads.Bead {
	const template = aliasConflictPoolTemplate
	return beads.Bead{
		ID:     id,
		Status: "open",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "template:" + template},
		Metadata: map[string]string{
			"template":                        template,
			"session_name":                    PoolSessionName(template, id),
			"state":                           "active",
			poolManagedMetadataKey:            boolMetadata(true),
			poolAliasConflictMetadataKey:      deferredAlias,
			poolAliasConflictCountMetadataKey: "1",
			poolAliasConflictAtMetadataKey:    "2026-08-17T14:48:33Z",
		},
	}
}

// aliasedPoolSessionBead builds a live pool session that DID win the alias
// slot, so the alias is stamped and no conflict marker is present.
func aliasedPoolSessionBead(id, alias string) beads.Bead {
	const template = aliasConflictPoolTemplate
	return beads.Bead{
		ID:     id,
		Status: "open",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "template:" + template},
		Metadata: map[string]string{
			"template":             template,
			"session_name":         PoolSessionName(template, id),
			"state":                "active",
			"alias":                alias,
			poolManagedMetadataKey: boolMetadata(true),
		},
	}
}

func resumeRequestForSession(states []PoolDesiredState, sessionBeadID string) (SessionRequest, bool) {
	for _, ds := range states {
		for _, req := range ds.Requests {
			if req.SessionBeadID == sessionBeadID {
				return req, true
			}
		}
	}
	return SessionRequest{}, false
}

// TestComputePoolDesiredStates_DeferredAliasConflictResolvesToItsSession is the
// gc-3040 regression. A live polecat whose alias slot collided still claims work
// under the alias it believes it owns. Because that alias was never stamped on
// its session bead, the resume-tier reverse lookup resolved nothing, control
// fell through to the "orphaned work, not our job to respawn" branch, pool
// demand for the template dropped to zero, and the reconciler drained the very
// session that was doing the work ("no-wake-reason") — then the reclaim reopened
// its bead and the whole cycle repeated.
//
// The deferred alias in pool_alias_conflict must therefore resolve back to its
// own session so the live worker keeps a wake reason.
func TestComputePoolDesiredStates_DeferredAliasConflictResolvesToItsSession(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	const deferredAlias = "rig/gastown.furiosa"
	work := []beads.Bead{
		workBead("w1", "rig/polecat", deferredAlias, "in_progress", 1),
	}
	sessions := []beads.Bead{conflictedPoolSessionBead("sess-loser", deferredAlias)}

	result := ComputePoolDesiredStates(cfg, work, sessionInfosFromBeads(sessions), nil)

	req, ok := resumeRequestForSession(result, "sess-loser")
	if !ok {
		t.Fatalf("no request preserves sess-loser; states=%#v — a live session whose alias slot collided must still be resolvable as the owner of work assigned under its deferred alias, or the reconciler drains it mid-work", result)
	}
	if req.Tier != "resume" {
		t.Errorf("tier = %q, want resume", req.Tier)
	}
	if req.WorkBeadID != "w1" {
		t.Errorf("WorkBeadID = %q, want w1", req.WorkBeadID)
	}
}

// TestComputePoolDesiredStates_DeferredAliasNeverOverridesStampedAlias is the
// safety guard on the fix above. The deferred alias is a FALLBACK only: when
// another live session actually holds that alias, that session is the owner and
// the conflicted session must not steal the resolution. Otherwise the repair
// would hand a stamped owner's work to whichever session happened to lose the
// slot race.
func TestComputePoolDesiredStates_DeferredAliasNeverOverridesStampedAlias(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	const alias = "rig/gastown.furiosa"
	work := []beads.Bead{
		workBead("w1", "rig/polecat", alias, "in_progress", 1),
	}
	sessions := []beads.Bead{
		aliasedPoolSessionBead("sess-holder", alias),
		conflictedPoolSessionBead("sess-loser", alias),
	}

	result := ComputePoolDesiredStates(cfg, work, sessionInfosFromBeads(sessions), nil)

	if _, ok := resumeRequestForSession(result, "sess-loser"); ok {
		t.Errorf("deferred alias resolved to sess-loser while sess-holder has it stamped; states=%#v", result)
	}
	if _, ok := resumeRequestForSession(result, "sess-holder"); !ok {
		t.Errorf("stamped alias no longer resolves to sess-holder; states=%#v", result)
	}
}

// TestComputePoolDesiredStates_AmbiguousDeferredAliasResolvesToNoSession pins
// that two sessions deferring the SAME alias produce no resolution rather than
// an arbitrary one. Map iteration order is unspecified, so picking a winner here
// would make demand nondeterministic across ticks — the failure mode this bead
// is about. Ambiguity keeps the pre-fix behavior, which the orphan trace now
// makes visible.
func TestComputePoolDesiredStates_AmbiguousDeferredAliasResolvesToNoSession(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	const alias = "rig/gastown.furiosa"
	work := []beads.Bead{
		workBead("w1", "rig/polecat", alias, "in_progress", 1),
	}
	sessions := []beads.Bead{
		conflictedPoolSessionBead("sess-a", alias),
		conflictedPoolSessionBead("sess-b", alias),
	}

	result := ComputePoolDesiredStates(cfg, work, sessionInfosFromBeads(sessions), nil)

	for _, id := range []string{"sess-a", "sess-b"} {
		if _, ok := resumeRequestForSession(result, id); ok {
			t.Errorf("ambiguous deferred alias resolved to %s; states=%#v", id, result)
		}
	}
}

// TestComputePoolDesiredStates_ClosedConflictedSessionIsNotResurrected pins that
// the deferred-alias fallback inherits the same liveness filter as every other
// identity: a closed session bead never resolves. Without this the fallback
// would manufacture resume demand for a session that is already gone.
func TestComputePoolDesiredStates_ClosedConflictedSessionIsNotResurrected(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	const alias = "rig/gastown.furiosa"
	work := []beads.Bead{
		workBead("w1", "rig/polecat", alias, "in_progress", 1),
	}
	closed := conflictedPoolSessionBead("sess-dead", alias)
	closed.Status = "closed"

	result := ComputePoolDesiredStates(cfg, work, sessionInfosFromBeads([]beads.Bead{closed}), nil)

	if _, ok := resumeRequestForSession(result, "sess-dead"); ok {
		t.Errorf("closed conflicted session resolved as owner; states=%#v", result)
	}
}

// TestComputePoolDesiredStates_OrphanedWorkIsTraced is the observability half of
// gc-3040. The "orphaned work, not our job to respawn" branch was a silent
// `continue`: demand for a template vanished with nothing in the reconciler
// trace naming the work bead or the assignee that failed to resolve. That is why
// the live loop had to be diagnosed from correlated log counts instead of read
// off a trace. The branch must record a decision.
func TestComputePoolDesiredStates_OrphanedWorkIsTraced(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	work := []beads.Bead{
		workBead("w1", "rig/polecat", "rig/gastown.nobody", "in_progress", 1),
	}
	trace := newPoolDesiredStateTestTrace("rig/polecat")

	result := ComputePoolDesiredStatesTraced(cfg, work, nil, nil, trace)

	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 0 {
		t.Fatalf("total requests = %d, want 0 — unresolvable assignee must not generate demand", total)
	}

	rec := poolTraceDecision(t, trace, TraceSitePoolOrphanedWork)
	if rec.ReasonCode != TraceReasonOrphaned {
		t.Errorf("reason = %q, want %q", rec.ReasonCode, TraceReasonOrphaned)
	}
	if rec.OutcomeCode != TraceOutcomeSkipped {
		t.Errorf("outcome = %q, want %q", rec.OutcomeCode, TraceOutcomeSkipped)
	}
	if got, _ := rec.Fields["work_bead"].(string); got != "w1" {
		t.Errorf("trace field work_bead = %#v, want w1", rec.Fields["work_bead"])
	}
	if got, _ := rec.Fields["assignee"].(string); got != "rig/gastown.nobody" {
		t.Errorf("trace field assignee = %#v, want rig/gastown.nobody", rec.Fields["assignee"])
	}
}

// TestComputePoolDesiredStates_NamedSessionDeferredAliasStillWakesTemplate pins
// the boundary of the deferred-alias fallback. A configured named session's
// demand is materialized by the named-session loop, and a resolved session bead
// ID hits the pool path's namedSessionBeadIDs skip — so admitting a named
// session into the fallback would silently convert the wake-known-identity
// request the pool path used to emit into no request at all. The fallback is
// confined to pool instances, leaving this case exactly as it was.
func TestComputePoolDesiredStates_NamedSessionDeferredAliasStillWakesTemplate(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("polecat", "rig", intPtr(4), 0)},
	}
	// The canonical alias of a singleton pool agent is the template name
	// itself, which is also what isKnownPoolTemplate accepts — so this assignee
	// reaches the wake tier before the fallback exists.
	const deferredAlias = "rig/polecat"
	work := []beads.Bead{
		workBead("w1", "rig/polecat", deferredAlias, "in_progress", 1),
	}
	named := conflictedPoolSessionBead("sess-named", deferredAlias)
	named.Metadata["configured_named_session"] = boolMetadata(true)

	result := ComputePoolDesiredStates(cfg, work, sessionInfosFromBeads([]beads.Bead{named}), nil)

	wake := 0
	for _, ds := range result {
		for _, req := range ds.Requests {
			if req.Tier == "wake-known-identity" {
				wake++
			}
		}
	}
	if wake != 1 {
		t.Errorf("wake-known-identity count = %d, want 1 — the deferred-alias fallback must not absorb a named session and drop its wake request; states=%#v", wake, result)
	}
}
