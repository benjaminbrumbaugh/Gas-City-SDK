package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// gc-hqz7e: a molecule root and its child steps could be owned by two
// different live sessions at the same time, and nothing detected or prevented
// it. Observed live on 2026-08-31 in Gas-City-Dashboard:
//
//	19:07:28Z  slit (gc-hywwv) claims child step gcd-tgu "Implement the solution"
//	19:39:49Z  the supervisor restarts after an unrelated binary install
//	19:41:16Z  furiosa (gc-k2gh9) claims ROOT gcd-8oq
//	           -> slit's step claim from 19:07 is never revoked
//
// Both sessions then ran unittest processes against the same worktree and wrote
// tests into the same file. The claim lease did not help: gcd-tgu's claim sat
// untouched for 61 minutes. Expiry is not a detection signal if nothing acts on
// it.
//
// The gate refuses rather than revokes. Revoking slit's bead claim would not
// have stopped slit's live process from editing the worktree — it would only
// have made the ledger stop recording the collision, which is strictly worse
// than the split it replaces.

type moleculeSplitRelease struct {
	calls    int
	beadID   string
	assignee string
	reasons  []string
}

// moleculeClaimOps builds the seam for a session claiming a molecule ROOT while
// steps is what the store reports as that molecule's in_progress steps.
func moleculeClaimOps(runner string, steps []beads.Bead, rel *moleculeSplitRelease) hookClaimOps {
	return hookClaimOps{
		Runner: func(string, string) (string, error) { return runner, nil },
		Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{
				ID: id, Status: "in_progress", Assignee: assignee,
				Metadata: map[string]string{"gc.routed_to": "worker"},
			}, true, nil
		},
		ListMoleculeSteps: func(_ context.Context, _ string, _ []string, rootID string) ([]beads.Bead, error) {
			out := make([]beads.Bead, 0, len(steps))
			for _, step := range steps {
				if strings.TrimSpace(step.Metadata[beadmeta.RootBeadIDMetadataKey]) == rootID {
					out = append(out, step)
				}
			}
			return out, nil
		},
		ResolveWorkBranch: func(string) string { return "" },
		StampWorkMeta: func(context.Context, string, []string, string, string, map[string]string) error {
			return nil
		},
		ReadWorkMeta: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, error) {
			return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee}, nil
		},
		PublishRunMap:     noopPublishRunMap,
		StampSessionClaim: noopStampSessionClaim,
		Release: func(_ context.Context, _ string, _ []string, beadID, assignee string) (bool, error) {
			rel.calls++
			rel.beadID = beadID
			rel.assignee = assignee
			return true, nil
		},
		EmitClaimReleased: func(rec hookClaimReleaseRecord) {
			rel.reasons = append(rel.reasons, rec.Reason)
		},
	}
}

func moleculeClaimOpts() hookClaimOptions {
	return hookClaimOptions{
		Assignee:           "gc__furiosa-gc-k2gh9",
		SessionID:          "gc-k2gh9",
		IdentityCandidates: []string{"gc__furiosa-gc-k2gh9"},
		RouteTargets:       []string{"worker"},
		Env:                []string{"GC_SESSION_ID=gc-k2gh9", "GC_SESSION_NAME=gc__furiosa-gc-k2gh9"},
		JSON:               true,
	}
}

const moleculeRootRunner = `[{"id":"gcd-8oq","status":"open","metadata":{"gc.routed_to":"worker"}}]`

// The required case. Session slit holds an in_progress step; session furiosa
// claims the root. The root claim must not be left standing alongside it.
func TestHookClaimRefusesRootWhenAnotherSessionHoldsAStep(t *testing.T) {
	rel := &moleculeSplitRelease{}
	ops := moleculeClaimOps(moleculeRootRunner, []beads.Bead{{
		ID:       "gcd-tgu",
		Status:   "in_progress",
		Assignee: "gc__slit-gc-hywwv",
		Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey:  "gcd-8oq",
			beadmeta.StepIDMetadataKey:      "implement",
			beadmeta.SessionIDMetadataKey:   "gc-hywwv",
			beadmeta.SessionNameMetadataKey: "gc__slit-gc-hywwv",
		},
	}}, rel)

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", moleculeClaimOpts(), ops, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("doHookClaim = 0: the root claim was admitted while another session holds a step.\n"+
			"stdout=%s", stdout.String())
	}
	if rel.calls != 1 {
		t.Fatalf("Release calls = %d, want 1; a refused root claim must be given back, not parked", rel.calls)
	}
	if rel.beadID != "gcd-8oq" {
		t.Fatalf("released bead = %q, want gcd-8oq", rel.beadID)
	}
	if len(rel.reasons) != 1 || rel.reasons[0] != hookClaimReleaseReasonSplitMolecule {
		t.Fatalf("release reasons = %v, want [%s]", rel.reasons, hookClaimReleaseReasonSplitMolecule)
	}
	// The acceptance criterion is explicit that the refusal names the
	// conflicting step and session: the live incident was diagnosable only by
	// running lsof against a worktree.
	msg := stderr.String()
	for _, want := range []string{"gcd-8oq", "gcd-tgu", "gc-hywwv"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q, so it cannot be acted on:\n  %s", want, msg)
		}
	}
	if strings.Contains(stdout.String(), "gcd-8oq") {
		t.Errorf("a refused claim still reported work on stdout:\n  %s", stdout.String())
	}
}

// The gate must not refuse the ordinary case, or it "fixes" the bug by stopping
// every molecule. A session that already owns the step owns the molecule.
func TestHookClaimAdmitsRootWhenThisSessionHoldsTheStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta map[string]string
		assn string
	}{
		{
			name: "step stamped with our session id",
			meta: map[string]string{
				beadmeta.RootBeadIDMetadataKey: "gcd-8oq",
				beadmeta.SessionIDMetadataKey:  "gc-k2gh9",
			},
			assn: "gc__furiosa-gc-k2gh9",
		},
		{
			name: "step assigned to our pool label, no session stamp yet",
			meta: map[string]string{beadmeta.RootBeadIDMetadataKey: "gcd-8oq"},
			assn: "gc__furiosa-gc-k2gh9",
		},
		{
			name: "step unowned, which is the continuation preassign case",
			meta: map[string]string{beadmeta.RootBeadIDMetadataKey: "gcd-8oq"},
			assn: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := &moleculeSplitRelease{}
			ops := moleculeClaimOps(moleculeRootRunner, []beads.Bead{{
				ID: "gcd-tgu", Status: "in_progress", Assignee: tc.assn, Metadata: tc.meta,
			}}, rel)

			var stdout, stderr bytes.Buffer
			if code := doHookClaim("bd ready --json", "/tmp/work", moleculeClaimOpts(), ops, &stdout, &stderr); code != 0 {
				t.Fatalf("doHookClaim = %d, want 0; the gate refused a claim this session was entitled to.\nstderr=%s",
					code, stderr.String())
			}
			if rel.calls != 0 {
				t.Fatalf("Release calls = %d, want 0", rel.calls)
			}
		})
	}
}

// A step is not responsible for its siblings. Only a root claim runs the check,
// or claiming step 5 of a molecule whose step 4 is held by the session that
// produced the branch would refuse itself.
func TestHookClaimDoesNotRunTheSplitCheckForAStepClaim(t *testing.T) {
	rel := &moleculeSplitRelease{}
	probed := false
	ops := moleculeClaimOps(
		`[{"id":"gcd-zdd","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"gcd-8oq"}}]`,
		nil, rel)
	ops.Claim = func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
		return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee, Metadata: map[string]string{
			"gc.routed_to":                 "worker",
			beadmeta.RootBeadIDMetadataKey: "gcd-8oq",
		}}, true, nil
	}
	ops.ListMoleculeSteps = func(context.Context, string, []string, string) ([]beads.Bead, error) {
		probed = true
		return nil, nil
	}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", moleculeClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0; stderr=%s", code, stderr.String())
	}
	if probed {
		t.Fatal("the split check ran for a bead that is itself a step; a step's siblings are not its to reconcile")
	}
	if rel.calls != 0 {
		t.Fatalf("Release calls = %d, want 0", rel.calls)
	}
}

// A probe failure means we know nothing: there is no conflicting step to name
// and no refusal that could be acted on. Failing closed would turn any bd
// hiccup into "no molecule root can be claimed". So the claim is admitted — but
// never silently, because a silent admission is the defect this bead reports.
func TestHookClaimAdmitsButWarnsWhenTheSplitProbeFails(t *testing.T) {
	rel := &moleculeSplitRelease{}
	ops := moleculeClaimOps(moleculeRootRunner, nil, rel)
	ops.ListMoleculeSteps = func(context.Context, string, []string, string) ([]beads.Bead, error) {
		return nil, context.DeadlineExceeded
	}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", moleculeClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0; a probe failure must not block the claim hot path.\nstderr=%s",
			code, stderr.String())
	}
	if rel.calls != 0 {
		t.Fatalf("Release calls = %d, want 0", rel.calls)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "molecule step ownership") || !strings.Contains(msg, "gcd-8oq") {
		t.Fatalf("probe failure was not reported on stderr:\n  %s", msg)
	}
}
