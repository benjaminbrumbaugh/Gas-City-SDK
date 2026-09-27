package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// poolReclaimTestCity is the agent config shared by the exact-owner reclaim
// tests: one pool template ("worker") that supports generic ephemeral sessions,
// which is what gates a bead into releaseOrphanedPoolAssignments at all.
func poolReclaimTestCity() *config.City {
	return &config.City{Agents: []config.Agent{{
		Name:              "worker",
		MinActiveSessions: intPtr(0),
		MaxActiveSessions: intPtr(2),
	}}}
}

// createInProgressPoolWork seeds a claimed pool work bead. Status is set through
// a follow-up Update because MemStore.Create does not accept a non-open status,
// matching the existing orphan-release fixtures.
func createInProgressPoolWork(t *testing.T, store beads.Store, metadata map[string]string) beads.Bead {
	t.Helper()
	work, err := store.Create(beads.Bead{
		Title:    "claimed pool work",
		Assignee: "Gas-City-SDK/gastown.nux",
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("Create work bead: %v", err)
	}
	if err := store.Update(work.ID, beads.UpdateOpts{Status: stringPtr("in_progress")}); err != nil {
		t.Fatalf("Set work status: %v", err)
	}
	work, err = store.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload work bead: %v", err)
	}
	return work
}

// createOpenPoolSessionBead seeds an open, pool-managed session bead carrying
// the given session_name and instance alias.
func createOpenPoolSessionBead(t *testing.T, store beads.Store, sessionName, alias string) beads.Bead {
	t.Helper()
	sb, err := store.Create(beads.Bead{
		Title:  "polecat",
		Type:   sessionBeadType,
		Status: "open",
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":         sessionName,
			"alias":                alias,
			"template":             "worker",
			poolManagedMetadataKey: boolMetadata(true),
		},
	})
	if err != nil {
		t.Fatalf("Create session bead: %v", err)
	}
	return sb
}

// TestReleaseOrphanedPoolAssignments_ReleasesRecycledSlotNameWhenStampedOwnerGone
// is the gc-b237 regression. Namepool slot names are recycled across polecat
// incarnations: incarnation A claims a bead and dies before branch-setup, then
// incarnation B takes the same slot NAME. Resolving liveness from the assignee
// string alone sees a live session under that name and skips the release
// forever, so the bead sits in_progress with no worker while a live agent works
// something else. The bead's own gc.session_id names the exact dead owner and is
// the discriminator the reclaim must use.
func TestReleaseOrphanedPoolAssignments_ReleasesRecycledSlotNameWhenStampedOwnerGone(t *testing.T) {
	store := beads.NewMemStore()
	// Incarnation B: live, and it now occupies the recycled slot name.
	liveB := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-wzjt", "Gas-City-SDK/gastown.nux")
	// Incarnation A stamped itself as the exact owner and is gone: "gc-anrc" is
	// absent from the session snapshot AND from the store.
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                  "worker",
		beadmeta.SessionIDMetadataKey:   "gc-anrc",
		beadmeta.SessionNameMetadataKey: "gastown__polecat-gc-wzjt",
	})

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store,
		poolReclaimTestCity(),
		"",
		[]beads.Bead{liveB},
		[]beads.Bead{work},
		[]beads.Store{store},
		nil,
		nil,
	)
	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released = %v, want %s — stamped owner gc-anrc is gone, the live nux slot is a different incarnation", released, work.ID)
	}

	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Get work bead: %v", err)
	}
	if got.Status != "open" {
		t.Fatalf("status = %q, want open", got.Status)
	}
	if got.Assignee != "" {
		t.Fatalf("assignee = %q, want cleared so the pool can re-dispatch", got.Assignee)
	}
	// The dead owner's back-reference must not survive the release: a claim made
	// without GC_SESSION_ID does not rewrite it, and a surviving dead stamp would
	// make the next tick release that fresh claim too.
	if owner := beadStampedSessionOwner(got); owner != "" {
		t.Fatalf("gc.session_id = %q after release, want cleared so the release cannot loop", owner)
	}
}

// TestReleaseOrphanedPoolAssignments_SkipsStalePolecatOwnerOnLiveNamedHandoff
// covers the polecat->refinery handoff shape: the completed polecat is gone,
// so its exact owner stamp is stale, but the bead now belongs to a configured
// named refinery route. That current route must win over the historical stamp.
func TestReleaseOrphanedPoolAssignments_SkipsStalePolecatOwnerOnLiveNamedHandoff(t *testing.T) {
	store := beads.NewMemStore()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "wayfinder"},
		Agents: []config.Agent{{
			Name:              "refinery",
			BindingName:       "gastown",
			Dir:               "Wayfinder",
			MinActiveSessions: intPtr(0),
			MaxActiveSessions: intPtr(2),
		}},
		NamedSessions: []config.NamedSession{{
			Template:    "refinery",
			BindingName: "gastown",
			Dir:         "Wayfinder",
			Mode:        "on_demand",
		}},
	}
	const refineryIdentity = "Wayfinder/gastown.refinery"
	live, err := store.Create(beads.Bead{
		Title:  "live refinery",
		Type:   sessionBeadType,
		Status: "open",
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":              config.NamedSessionRuntimeName("wayfinder", cfg.Workspace, refineryIdentity),
			"template":                  refineryIdentity,
			"state":                     "active",
			"configured_named_session":  "true",
			"configured_named_identity": refineryIdentity,
			"configured_named_mode":     "on_demand",
		},
	})
	if err != nil {
		t.Fatalf("Create live refinery: %v", err)
	}
	work, err := store.Create(beads.Bead{
		Title:    "completed polecat handoff",
		Assignee: refineryIdentity,
		Metadata: map[string]string{
			"gc.routed_to":                refineryIdentity,
			beadmeta.SessionIDMetadataKey: "gc-departed-polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create handed-off work: %v", err)
	}
	inProgress := "in_progress"
	if err := store.Update(work.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("Set handed-off work status: %v", err)
	}
	work, err = store.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload handed-off work: %v", err)
	}

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store, cfg, "", []beads.Bead{live}, []beads.Bead{work}, []beads.Store{store}, nil, nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — the stale polecat stamp must not release a live refinery handoff", released)
	}

	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Get handed-off work: %v", err)
	}
	if got.Status != "in_progress" || got.Assignee != refineryIdentity {
		t.Fatalf("work = status %q assignee %q, want in_progress/%s", got.Status, got.Assignee, refineryIdentity)
	}
}

// TestReleaseOrphanedPoolAssignments_SkipsStaleOwnerOnLiveBareHandoff covers
// a completed handoff to a live bare session identity. The departed polecat's
// exact-owner stamp is historical and must not bypass current liveness.
func TestReleaseOrphanedPoolAssignments_SkipsStaleOwnerOnLiveBareHandoff(t *testing.T) {
	store := beads.NewMemStore()
	live := createOpenSessionBeadWithIdentity(t, store, "gastown.mayor", "worker")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                "worker",
		beadmeta.SessionIDMetadataKey: "gc-departed-polecat",
	})
	work.Assignee = "gastown.mayor"
	if err := store.Update(work.ID, beads.UpdateOpts{Assignee: &work.Assignee}); err != nil {
		t.Fatalf("Set handoff assignee: %v", err)
	}
	work, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload handed-off work: %v", err)
	}

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store, poolReclaimTestCity(), "", []beads.Bead{live}, []beads.Bead{work}, []beads.Store{store}, nil, nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — the live bare assignee must preserve the handoff", released)
	}
	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Get handed-off work: %v", err)
	}
	if got.Status != "in_progress" || got.Assignee != "gastown.mayor" {
		t.Fatalf("work = status %q assignee %q, want in_progress/gastown.mayor", got.Status, got.Assignee)
	}
}

func TestReleaseOrphanedPoolAssignments_SkipsStaleOwnerOnCanonicalHandoffMissingSnapshot(t *testing.T) {
	store := beads.NewMemStore()
	createOpenSessionBeadWithIdentity(t, store, "gastown.mayor", "worker")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                "worker",
		beadmeta.SessionIDMetadataKey: "gc-departed-polecat",
	})
	work.Assignee = "gastown.mayor"
	if err := store.Update(work.ID, beads.UpdateOpts{Assignee: &work.Assignee}); err != nil {
		t.Fatalf("Set handoff assignee: %v", err)
	}
	work, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload handed-off work: %v", err)
	}

	// The open-session snapshot is absent, but the session-class store still
	// contains the canonical identity. A stale exact-owner stamp must not turn
	// that completed handoff into an orphan.
	released := releaseOrphanedPoolAssignmentsFromBeads(
		store, poolReclaimTestCity(), "", nil, []beads.Bead{work}, []beads.Store{store}, nil, nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — direct canonical liveness must preserve the handoff", released)
	}
}

// TestReleaseOrphanedPoolAssignments_SkipsStaleOwnerOnLiveRigQualifiedHandoff
// covers the same handoff across a rig store, where the current assignee is a
// rig-qualified identity and the live session is recorded in the city store.
func TestReleaseOrphanedPoolAssignments_SkipsStaleOwnerOnLiveRigQualifiedHandoff(t *testing.T) {
	cityPath := t.TempDir()
	cityStore := beads.NewMemStore()
	rigStore := beads.NewMemStore()
	live := createOpenSessionBeadWithIdentity(t, cityStore, "repo/refinery", "worker")
	work, err := rigStore.Create(beads.Bead{
		Title:    "rig handoff",
		Assignee: "repo/refinery",
		Metadata: map[string]string{
			"gc.routed_to":                "repo/worker",
			beadmeta.SessionIDMetadataKey: "gc-departed-polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create rig work: %v", err)
	}
	inProgress := "in_progress"
	if err := rigStore.Update(work.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("Set rig work status: %v", err)
	}
	work, err = rigStore.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload rig work: %v", err)
	}
	cfg := &config.City{
		Rigs: []config.Rig{{Name: "repo", Path: t.TempDir()}},
		Agents: []config.Agent{{
			Name:              "worker",
			Dir:               "repo",
			MinActiveSessions: intPtr(0),
			MaxActiveSessions: intPtr(2),
		}},
	}

	released := releaseOrphanedPoolAssignmentsFromBeads(
		cityStore, cfg, cityPath, []beads.Bead{live}, []beads.Bead{work},
		[]beads.Store{rigStore}, []string{"repo"}, map[string]beads.Store{"repo": rigStore},
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — the live rig-qualified assignee must preserve the handoff", released)
	}
	got, err := rigStore.Get(work.ID)
	if err != nil {
		t.Fatalf("Get rig work: %v", err)
	}
	if got.Status != "in_progress" || got.Assignee != "repo/refinery" {
		t.Fatalf("rig work = status %q assignee %q, want in_progress/repo/refinery", got.Status, got.Assignee)
	}
}

func createOpenSessionBeadWithIdentity(t *testing.T, store beads.Store, identity, template string) beads.Bead {
	t.Helper()
	sb, err := store.Create(beads.Bead{
		Title:  "live handoff session",
		Type:   sessionBeadType,
		Status: "open",
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":         identity,
			"alias":                identity,
			"template":             template,
			poolManagedMetadataKey: boolMetadata(true),
		},
	})
	if err != nil {
		t.Fatalf("Create live handoff session: %v", err)
	}
	return sb
}

// TestReleaseOrphanedPoolAssignments_ExactOwnerReleaseIsIdempotent pins that the
// release cannot become its own loop. After the orphaned claim is released, a
// re-claim that does not stamp an owner (a `gc hook --claim` outside a
// session-template context blanks GC_SESSION_ID) must be treated by the ordinary
// name-based liveness checks and left alone.
func TestReleaseOrphanedPoolAssignments_ExactOwnerReleaseIsIdempotent(t *testing.T) {
	store := beads.NewMemStore()
	liveB := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-wzjt", "Gas-City-SDK/gastown.nux")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                "worker",
		beadmeta.SessionIDMetadataKey: "gc-anrc",
	})

	cfg := poolReclaimTestCity()
	if released := releaseOrphanedPoolAssignmentsFromBeads(
		store, cfg, "", []beads.Bead{liveB}, []beads.Bead{work}, []beads.Store{store}, nil, nil,
	); len(released) != 1 {
		t.Fatalf("first pass released = %v, want the orphaned claim", released)
	}

	// The pool re-dispatches and a non-session claim re-takes the same slot name
	// without stamping an owner.
	if err := store.Update(work.ID, beads.UpdateOpts{
		Assignee: stringPtr("Gas-City-SDK/gastown.nux"),
		Status:   stringPtr("in_progress"),
	}); err != nil {
		t.Fatalf("Re-claim work bead: %v", err)
	}
	reclaimed, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Reload re-claimed bead: %v", err)
	}

	if released := releaseOrphanedPoolAssignmentsFromBeads(
		store, cfg, "", []beads.Bead{liveB}, []beads.Bead{reclaimed}, []beads.Store{store}, nil, nil,
	); len(released) != 0 {
		t.Fatalf("second pass released = %v, want none — the stale stamp was cleared, so the live slot holder keeps its claim", released)
	}
}

// TestReleaseOrphanedPoolAssignments_SkipsWhenStampedOwnerIsLiveSession pins the
// other side of the discriminator: when gc.session_id names the live session that
// holds the slot, the claim is genuinely owned and must survive.
func TestReleaseOrphanedPoolAssignments_SkipsWhenStampedOwnerIsLiveSession(t *testing.T) {
	store := beads.NewMemStore()
	live := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-wzjt", "Gas-City-SDK/gastown.nux")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                "worker",
		beadmeta.SessionIDMetadataKey: live.ID,
	})

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store,
		poolReclaimTestCity(),
		"",
		[]beads.Bead{live},
		[]beads.Bead{work},
		[]beads.Store{store},
		nil,
		nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — the stamped owner is the live session", released)
	}
	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Get work bead: %v", err)
	}
	if got.Status != "in_progress" || got.Assignee != "Gas-City-SDK/gastown.nux" {
		t.Fatalf("work = (%q, %q), want (in_progress, Gas-City-SDK/gastown.nux)", got.Status, got.Assignee)
	}
}

// TestReleaseOrphanedPoolAssignments_SkipsStampedOwnerMissingFromSnapshotButLiveInStore
// guards the failure mode the exact-owner check could introduce. A session
// snapshot that merely MISSED the stamped owner is not evidence the owner died,
// so absence in the snapshot must be confirmed against the store before it can
// override the assignee-name liveness checks. Here owner A is open in the store
// but absent from the snapshot: releasing would strand a live worker's claim.
func TestReleaseOrphanedPoolAssignments_SkipsStampedOwnerMissingFromSnapshotButLiveInStore(t *testing.T) {
	store := beads.NewMemStore()
	// Owner A is genuinely live in the store, under its own slot name.
	ownerA := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-anrc", "Gas-City-SDK/gastown.rictus")
	// Some other live session occupies the slot name the work is assigned under,
	// so the name-based checks would skip anyway; only the snapshot omission of
	// ownerA is under test.
	liveB := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-wzjt", "Gas-City-SDK/gastown.nux")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to":                "worker",
		beadmeta.SessionIDMetadataKey: ownerA.ID,
	})

	// Snapshot deliberately omits ownerA.
	released := releaseOrphanedPoolAssignmentsFromBeads(
		store,
		poolReclaimTestCity(),
		"",
		[]beads.Bead{liveB},
		[]beads.Bead{work},
		[]beads.Store{store},
		nil,
		nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — the stamped owner is open in the store, only missing from the snapshot", released)
	}
}

// TestReleaseOrphanedPoolAssignments_UnstampedClaimKeepsNameBasedLiveness pins
// the backward-compatible fallback: a claim with no gc.session_id carries no
// exact owner, so liveness stays resolved from the assignee name exactly as
// before (the SkipsLiveSessionAssignedByAlias contract).
func TestReleaseOrphanedPoolAssignments_UnstampedClaimKeepsNameBasedLiveness(t *testing.T) {
	store := beads.NewMemStore()
	live := createOpenPoolSessionBead(t, store, "gastown__polecat-gc-wzjt", "Gas-City-SDK/gastown.nux")
	work := createInProgressPoolWork(t, store, map[string]string{
		"gc.routed_to": "worker",
	})

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store,
		poolReclaimTestCity(),
		"",
		[]beads.Bead{live},
		[]beads.Bead{work},
		[]beads.Store{store},
		nil,
		nil,
	)
	if len(released) != 0 {
		t.Fatalf("released = %v, want none — unstamped claims still resolve liveness by assignee name", released)
	}
}
