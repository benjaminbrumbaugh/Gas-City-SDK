package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/worktree"
)

// Tests for gc-ipeiw: the claim hook stamped the branch of whatever directory
// the claimant happened to be standing in onto gc.work_branch, compare-and-
// overwrite. Three subsystems read that key as a fact about the WORK:
//
//	pool_desired_state.go   worktreeSpecForBead requires it as one of nine
//	                        worktree-ownership keys and uses it as
//	                        worktree.Spec.Branch
//	work_record_gate.go     validateWorkRecordOnClose asserts gc.work_commit
//	                        is reachable on it
//	pool_detached_orphan_sweep.go
//	                        keys off its NON-EMPTINESS to recognize a
//	                        completed-work handoff bead
//
// A claim-time directory cannot satisfy the first two, and for a polecat
// molecule the per-bead worktree does not exist yet at claim time — so the
// value routinely read "main". The fix splits the two facts: the claim
// directory gets its own key, and gc.work_branch became write-once here.
//
// The third reader is why the write-when-absent path is retained rather than
// removed outright, and it is asserted below.

func claimBranchOps(branch string) hookClaimOps {
	return hookClaimOps{ResolveWorkBranch: func(string) string { return branch }}
}

// provisionedWorkBead is the shape gc worktree ensure publishes: a work bead
// carrying all nine ownership keys, whose gc.work_branch is real provenance.
func provisionedWorkBead() beads.Bead {
	return beads.Bead{ID: "gc-test", Status: "open", Metadata: map[string]string{
		beadmeta.WorkDirMetadataKey:            "/worktrees/gc-test",
		beadmeta.WorkBranchMetadataKey:         "polecat/gc-test",
		beadmeta.WorktreeRootMetadataKey:       "/worktrees",
		beadmeta.WorktreeRepoMetadataKey:       "/repos/gascity",
		beadmeta.WorktreeBaseRefMetadataKey:    "main",
		beadmeta.WorktreeBaseSHAMetadataKey:    strings.Repeat("a", 40),
		beadmeta.WorktreeCreatorMetadataKey:    "gc-sling",
		beadmeta.WorktreeOwnerMetadataKey:      "gc-sling",
		beadmeta.WorktreeGenerationMetadataKey: "7",
		beadmeta.WorktreeLifecycleMetadataKey:  worktree.LifecycleActive,
	}}
}

// TestHookClaimIdentityPatchDoesNotOverwriteWorkBranch is the core assertion:
// a bead that already records the branch its work lives on keeps it, even when
// the claimant is standing somewhere else entirely.
func TestHookClaimIdentityPatchDoesNotOverwriteWorkBranch(t *testing.T) {
	bead := beads.Bead{ID: "hw-1", Status: "open", Metadata: map[string]string{
		beadmeta.WorkBranchMetadataKey: "polecat/hw-1",
	}}

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps("main"), "/tmp/agent-home")

	if got, ok := patch[beadmeta.WorkBranchMetadataKey]; ok {
		t.Fatalf("patch rewrote %s to %q; a claim from an unrelated checkout must not touch it",
			beadmeta.WorkBranchMetadataKey, got)
	}
	if got := patch[beadmeta.ClaimBranchMetadataKey]; got != "main" {
		t.Fatalf("patch[%s] = %q, want %q (the claimant's own directory, under its own key)",
			beadmeta.ClaimBranchMetadataKey, got, "main")
	}
}

// TestWorktreeProvenanceSurvivesClaimFromUnrelatedCheckout is the same defect
// measured where it did damage: apply the claim patch to a fully provisioned
// work bead and ask worktreeSpecForBead what branch the session would get.
//
// Before the fix the patch replaced gc.work_branch with "main", so the spec
// named a branch the work does not live on — while every other ownership key
// still described the per-bead worktree.
func TestWorktreeProvenanceSurvivesClaimFromUnrelatedCheckout(t *testing.T) {
	bead := provisionedWorkBead()
	want := bead.Metadata[beadmeta.WorkBranchMetadataKey]

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps("main"), "/tmp/agent-home")
	for k, v := range patch { // what the store would then hold
		bead.Metadata[k] = v
	}

	spec, err := worktreeSpecForBead(bead, "rig:gascity")
	if err != nil {
		t.Fatalf("worktreeSpecForBead after claim: %v", err)
	}
	if spec == nil {
		t.Fatal("worktreeSpecForBead returned no spec for a fully provisioned bead")
	}
	if spec.Branch != want {
		t.Fatalf("spec.Branch = %q after a claim from a checkout on main, want %q", spec.Branch, want)
	}
}

// TestHookClaimIdentityPatchFillsWorkBranchWhenAbsent pins the deliberately
// retained write: isDetachedHandoffOrphanCandidate treats a non-empty
// gc.work_branch as the marker of a completed-work handoff bead, so a claim
// path that stopped writing the key altogether would silently stop
// detached-orphan recovery rather than fail visibly.
func TestHookClaimIdentityPatchFillsWorkBranchWhenAbsent(t *testing.T) {
	bead := beads.Bead{ID: "hw-2", Status: "open", Metadata: map[string]string{}}

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps("polecat/hw-2"), "/tmp/wt")

	if got := patch[beadmeta.WorkBranchMetadataKey]; got != "polecat/hw-2" {
		t.Fatalf("patch[%s] = %q, want %q (fill when absent)", beadmeta.WorkBranchMetadataKey, got, "polecat/hw-2")
	}
	if got := patch[beadmeta.ClaimBranchMetadataKey]; got != "polecat/hw-2" {
		t.Fatalf("patch[%s] = %q, want %q", beadmeta.ClaimBranchMetadataKey, got, "polecat/hw-2")
	}

	for k, v := range patch {
		bead.Metadata[k] = v
	}
	// The equality is itself the documented signal that this work-branch value
	// is claim-derived and must not be trusted as an outcome fact.
	if bead.Metadata[beadmeta.ClaimBranchMetadataKey] != bead.Metadata[beadmeta.WorkBranchMetadataKey] {
		t.Fatal("a filled work_branch must equal claim_branch, which is how a reader detects a claim-derived value")
	}
}

// TestHookClaimIdentityPatchClaimBranchTracksLatestClaimant keeps the new key
// compare-and-overwrite. It answers "where did the claimant claim from", so a
// stale predecessor value would be strictly less useful than none.
func TestHookClaimIdentityPatchClaimBranchTracksLatestClaimant(t *testing.T) {
	bead := beads.Bead{ID: "hw-3", Status: "open", Metadata: map[string]string{
		beadmeta.ClaimBranchMetadataKey: "gc-gastown.slit-old",
		beadmeta.WorkBranchMetadataKey:  "polecat/hw-3",
	}}

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps("gc-gastown.nux-new"), "/tmp/home")

	if got := patch[beadmeta.ClaimBranchMetadataKey]; got != "gc-gastown.nux-new" {
		t.Fatalf("patch[%s] = %q, want the newest claimant's branch", beadmeta.ClaimBranchMetadataKey, got)
	}
	if _, ok := patch[beadmeta.WorkBranchMetadataKey]; ok {
		t.Fatalf("patch touched %s; it is write-once", beadmeta.WorkBranchMetadataKey)
	}
}

// TestHookClaimIdentityPatchOmitsBranchKeysWhenUnresolvable preserves the
// best-effort contract: no repo or a detached HEAD omits the branch keys
// entirely. An omitted key is safer than a wrong one, and a claim must never
// fail because a branch could not be resolved.
func TestHookClaimIdentityPatchOmitsBranchKeysWhenUnresolvable(t *testing.T) {
	bead := beads.Bead{ID: "hw-4", Status: "open", Metadata: map[string]string{}}

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps(""), "/tmp/detached")

	if _, ok := patch[beadmeta.WorkBranchMetadataKey]; ok {
		t.Fatalf("patch = %v, want %s absent when no branch resolves", patch, beadmeta.WorkBranchMetadataKey)
	}
	if _, ok := patch[beadmeta.ClaimBranchMetadataKey]; ok {
		t.Fatalf("patch = %v, want %s absent when no branch resolves", patch, beadmeta.ClaimBranchMetadataKey)
	}
}

// TestDetachedHandoffOrphanStillRecognisedAfterClaimFill closes the loop on the
// retained write: the bead a claim-filled work_branch produces must still be
// eligible for the detached-orphan sweep, which is the reader that write exists
// to serve.
func TestDetachedHandoffOrphanStillRecognisedAfterClaimFill(t *testing.T) {
	bead := beads.Bead{ID: "hw-5", Status: "open", Metadata: map[string]string{
		beadmeta.SessionIDMetadataKey: "mc-sess1",
	}}

	patch := hookClaimIdentityPatch(bead, hookClaimOptions{}, claimBranchOps("polecat/hw-5"), "/tmp/wt")
	for k, v := range patch {
		bead.Metadata[k] = v
	}

	if !isDetachedHandoffOrphanCandidate(bead) {
		t.Fatalf("a claim-stamped handoff bead is no longer sweep-eligible: %+v", bead.Metadata)
	}
}
