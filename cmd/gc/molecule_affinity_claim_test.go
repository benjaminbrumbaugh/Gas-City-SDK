package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestDoHookClaimRejectsForeignContinuationClaim(t *testing.T) {
	claimedMeta := map[string]string{
		beadmeta.RoutedToMetadataKey:          "worker",
		beadmeta.RootBeadIDMetadataKey:        "root-1",
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.SessionAffinityMetadataKey:   "require",
	}
	var released bool
	var stamped bool
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"step-a","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"pool-workflow","gc.session_affinity":"require"}}]`, nil
		},
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee, Metadata: claimedMeta}, true, nil
		},
		ListMoleculeSteps: func(context.Context, string, []string, string, string) ([]beads.Bead, error) {
			return []beads.Bead{
				{ID: "step-a", Status: "in_progress", Assignee: "session-current", Metadata: claimedMeta},
				{ID: "step-b", Status: "in_progress", Assignee: "session-foreign", Metadata: claimedMeta},
			}, nil
		},
		StampWorkMeta: func(context.Context, string, []string, string, string, map[string]string) error {
			stamped = true
			return nil
		},
		Release: func(context.Context, string, []string, string, string) (bool, error) {
			released = true
			return true, nil
		},
		EmitClaimReleased: func(hookClaimReleaseRecord) {},
		DrainAck:          func(io.Writer) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/worktrees/current", hookClaimOptions{
		Assignee:           "session-current",
		IdentityCandidates: []string{"session-current"},
		RouteTargets:       []string{"worker"},
		Env:                []string{"GC_SESSION_ID=session-current", "GC_SESSION_NAME=current"},
		JSON:               true,
	}, ops, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHookClaim = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !released {
		t.Fatal("foreign continuation claim was not released")
	}
	if stamped {
		t.Fatal("foreign continuation claim stamped execution identity before refusal")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no executable result", stdout.String())
	}
	if !strings.Contains(stderr.String(), "session-foreign") {
		t.Fatalf("stderr = %q, want conflicting session diagnostic", stderr.String())
	}
}

func TestHookContinuationClaimConflictsAllowsCurrentSession(t *testing.T) {
	meta := map[string]string{
		beadmeta.RootBeadIDMetadataKey:        "root-1",
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.SessionAffinityMetadataKey:   "require",
	}
	ops := hookClaimOps{
		ListMoleculeSteps: func(context.Context, string, []string, string, string) ([]beads.Bead, error) {
			return []beads.Bead{
				{ID: "step-a", Status: "in_progress", Assignee: "session-current", Metadata: meta},
				{ID: "step-b", Status: "in_progress", Assignee: "current", Metadata: meta},
			}, nil
		},
	}
	conflicts, err := hookContinuationClaimConflicts(beads.Bead{Metadata: meta}, hookClaimOptions{
		Assignee: "session-current",
		Env:      []string{"GC_SESSION_ID=session-current", "GC_SESSION_NAME=current"},
	}, ops, "/worktrees/current")
	if err != nil {
		t.Fatalf("hookContinuationClaimConflicts error = %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("current session's continuation steps reported as conflicts: %+v", conflicts)
	}
}

func TestDefaultScaleCheckCountsDeduplicatesPoolMoleculeSteps(t *testing.T) {
	backing := beads.NewMemStore()
	for _, item := range []struct{ id, root string }{
		{id: "step-a", root: "root-1"},
		{id: "step-b", root: "root-1"},
		{id: "step-c", root: "root-2"},
	} {
		if _, err := backing.Create(beads.Bead{
			ID:     item.id,
			Type:   "task",
			Status: "open",
			Metadata: map[string]string{
				beadmeta.RoutedToMetadataKey:          "worker",
				beadmeta.RootBeadIDMetadataKey:        item.root,
				beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
				beadmeta.SessionAffinityMetadataKey:   "require",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	cache := beads.NewCachingStoreForTest(backing, nil)
	if err := cache.PrimeActive(); err != nil {
		t.Fatal(err)
	}

	counts, demand, _, errs := defaultScaleCheckCountsAndDemand(nil, []defaultScaleCheckTarget{{
		template: "worker",
		storeKey: "city",
		store:    cache,
	}})
	if len(errs) != 0 {
		t.Fatalf("defaultScaleCheckCountsAndDemand errs = %v", errs)
	}
	if got := counts["worker"]; got != 2 {
		t.Fatalf("worker demand = %d, want 2 independent claimable molecule units", got)
	}
	if got := demand["worker"].WorkBeadIDs; len(got) != 2 {
		t.Fatalf("worker demand bead ids = %v, want one representative per root", got)
	}
}

func TestDoHookClaimOwnershipProbeFailureDoesNotAdmitContinuation(t *testing.T) {
	claimedMeta := map[string]string{
		beadmeta.RoutedToMetadataKey:          "worker",
		beadmeta.RootBeadIDMetadataKey:        "root-1",
		beadmeta.ContinuationGroupMetadataKey: "pool-workflow",
		beadmeta.SessionAffinityMetadataKey:   "require",
	}
	claimed := false
	released := false
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"step-a","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"pool-workflow","gc.session_affinity":"require"}}]`, nil
		},
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			claimed = true
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee, Metadata: claimedMeta}, true, nil
		},
		ListMoleculeSteps: func(context.Context, string, []string, string, string) ([]beads.Bead, error) {
			return nil, errors.New("ownership probe unavailable")
		},
		Release: func(context.Context, string, []string, string, string) (bool, error) {
			released = true
			return true, nil
		},
		DrainAck: func(io.Writer) error { return nil },
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/worktrees/current", hookClaimOptions{
		Assignee:     "session-current",
		RouteTargets: []string{"worker"},
		Env:          []string{"GC_SESSION_ID=session-current", "GC_SESSION_NAME=current"},
		JSON:         true,
	}, ops, &stdout, &stderr)
	if code != 1 || !claimed || !released {
		t.Fatalf("probe failure result = code %d claimed=%t released=%t stdout=%q stderr=%q; want refusal before executable handoff", code, claimed, released, stdout.String(), stderr.String())
	}
}
