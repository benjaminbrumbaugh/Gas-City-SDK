package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// gc-b9rev: a wedged ASLEEP session holding a canonical singleton pool's
// runtime name livelocked the city-worker pool for ~4h, starving 47 ready
// beads (24 P1) behind the single city writer.
//
// The sealed state, from the Mayor's live diagnosis:
//   - reuse skips it            (reusablePoolSessionInfo rejects state=asleep)
//   - create cannot proceed     (openSessionNameTaken sees the open bead still
//     advertising session_name=city-worker, so
//     derivePoolSessionName returns
//     errPoolSessionNameUnavailable every tick)
//   - wake/reset were no-ops, kill/undrain refused
//
// Only `gc session close` cleared it. The comment at
// build_desired_state.go ("The reconciler closes orphaned asleep beads")
// asserts a reaper covers this. These cases pin down whether it does.
//
// Shape is taken from the real bead gc-88xo1: session_origin=ephemeral,
// pool_managed=true, NO pool_slot (canonical singleton), a valid
// session_name equal to the canonical name, and a runtime that is NOT
// running (`gc runtime undrain` reported `session "city-worker" is not
// running`).
func gcB9revAsleepCanonicalBead(t *testing.T, store beads.Store) beads.Bead {
	t.Helper()
	bead, err := store.Create(beads.Bead{
		Title:  "city-worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "agent:city-worker"},
		Metadata: map[string]string{
			"session_name":         "city-worker",
			"template":             "city-worker",
			"agent_name":           "city-worker",
			"session_origin":       "ephemeral",
			poolManagedMetadataKey: boolMetadata(true),
			"state":                "asleep",
			"drain_at":             "2026-09-06T05:10:07Z",
			"continuation_epoch":   "11",
			"generation":           "11",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return bead
}

// canonical singleton: no namepool, max_active_sessions == 1.
func gcB9revCity() *config.City {
	return &config.City{Agents: []config.Agent{{
		Name:              "city-worker",
		MinActiveSessions: intPtr(0),
		MaxActiveSessions: intPtr(1),
	}}}
}

// Case A: the pool could NOT create (name unavailable), so buildDesiredState
// produced no entry for this session name. Nothing else can free the name, so
// the sweeper is the only actor left that can break the livelock.
func TestGCB9rev_AsleepCanonicalHolder_SweptWhenNotDesired(t *testing.T) {
	store := beads.NewMemStore()
	bead := gcB9revAsleepCanonicalBead(t, store)

	closed := sweepUndesiredPoolSessionBeads(
		"",
		beads.SessionStore{Store: store},
		nil,
		newSessionBeadSnapshot([]beads.Bead{bead}),
		nil, // desiredState: the create was refused, so the name is not desired
		gcB9revCity(),
		runtime.NewFake(), // not running
		false,
	)
	if closed != 1 {
		t.Fatalf("closed = %d, want 1: an asleep canonical pool bead that is not in desired state and whose runtime is not running must be reaped, or it holds its pool's only runtime name forever (gc-b9rev)", closed)
	}
}

// Case B: the same bead while the pool DOES desire that session name. The
// desired name for a canonical singleton pool is exactly the name the wedged
// bead is holding, but desired-state membership must not protect an asleep
// holder that cannot be reused and prevents its replacement from being named.
func TestGCB9rev_AsleepCanonicalHolder_WhenNameIsDesired(t *testing.T) {
	store := beads.NewMemStore()
	bead := gcB9revAsleepCanonicalBead(t, store)

	closed := sweepUndesiredPoolSessionBeads(
		"",
		beads.SessionStore{Store: store},
		nil,
		newSessionBeadSnapshot([]beads.Bead{bead}),
		map[string]TemplateParams{"city-worker": {}},
		gcB9revCity(),
		runtime.NewFake(),
		false,
	)
	if closed != 1 {
		t.Fatalf("closed = %d, want 1 — an asleep canonical pool holder must be reaped even when desired state still contains its blocked name (gc-b9rev)", closed)
	}
}

func TestGCB9rev_ActiveCanonicalHolder_WhenNameIsDesiredIsProtected(t *testing.T) {
	store := beads.NewMemStore()
	bead := gcB9revAsleepCanonicalBead(t, store)
	if err := store.Update(bead.ID, beads.UpdateOpts{Metadata: map[string]string{"state": "active"}}); err != nil {
		t.Fatalf("Update active holder: %v", err)
	}
	active, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("Get active holder: %v", err)
	}
	closed := sweepUndesiredPoolSessionBeads(
		"",
		beads.SessionStore{Store: store},
		nil,
		newSessionBeadSnapshot([]beads.Bead{active}),
		map[string]TemplateParams{"city-worker": {}},
		gcB9revCity(),
		runtime.NewFake(),
		false,
	)
	if closed != 0 {
		t.Fatalf("closed = %d, want 0 — an active desired canonical holder remains protected", closed)
	}
}
