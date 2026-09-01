package beads_test

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// conditionalReleaser is the capability under test. OpenSQLiteStore returns the
// broad Store interface, which does not carry it.
type conditionalReleaser interface {
	ReleaseIfCurrent(id, expectedAssignee string) (bool, error)
}

func releaserFor(t *testing.T, store beads.Store) conditionalReleaser {
	t.Helper()
	r, ok := store.(conditionalReleaser)
	if !ok {
		t.Fatalf("store %T does not implement ReleaseIfCurrent", store)
	}
	return r
}

// gc-sjy6f: releasing a bead cleared the assignee and left the metadata still
// naming the released owner. Twelve beads were in that state in the live city
// store when this was found, and the count grew by three during a single
// session's ordinary claim/release cycles.
//
// It is not cosmetic. Orphan and liveness checks resolve ownership from these
// keys, so a released bead that still carries one reads as FREE to anything
// keying on the assignee and HELD to anything keying on the session metadata --
// the same split that produced gc-nb5h9, arriving from the release side.
//
// The camelCase variants are cleared too because routingdecision resolves
// identity as firstWorkStateValue(snake, camel): clearing only the snake keys
// would promote a stale camel value to being the answer.
func TestReleaseIfCurrentClearsTheSessionIdentity(t *testing.T) {
	store, err := beads.OpenSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}

	const owner = "Gas-City-Dashboard--gastown__furiosa"
	created, err := store.Create(beads.Bead{
		Title:    "held work",
		Type:     "task",
		Status:   "in_progress",
		Assignee: owner,
		Metadata: map[string]string{
			"gc.session_id":   "gc-k2gh9",
			"gc.session_name": owner,
			"gc.sessionId":    "gc-k2gh9",
			"gc.sessionName":  owner,
			"gc.routed_to":    "polecat",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	released, err := releaserFor(t, store).ReleaseIfCurrent(created.ID, owner)
	if err != nil {
		t.Fatalf("ReleaseIfCurrent: %v", err)
	}
	if !released {
		t.Fatal("ReleaseIfCurrent released = false for the current owner")
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Assignee != "" {
		t.Fatalf("assignee = %q, want cleared", got.Assignee)
	}
	for _, key := range []string{"gc.session_id", "gc.session_name", "gc.sessionId", "gc.sessionName"} {
		if v := got.Metadata[key]; v != "" {
			t.Errorf("%s = %q after release; the bead still names its previous owner, "+
				"so orphan and liveness checks read it as held while the assignee says it is free", key, v)
		}
	}
	// Routing must survive: the bead is still routed work, it simply has no owner.
	if got.Metadata["gc.routed_to"] != "polecat" {
		t.Errorf("gc.routed_to = %q, want it preserved; a release must not un-route the work",
			got.Metadata["gc.routed_to"])
	}
}

// A release that does NOT fire must change nothing at all — the guard is the
// whole point of the conditional form, and clearing identity on a refused
// release would hand another session's work away.
func TestReleaseIfCurrentLeavesIdentityAloneWhenTheGuardFails(t *testing.T) {
	store, err := beads.OpenSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}

	const owner = "gastown.slit"
	created, err := store.Create(beads.Bead{
		Title:    "someone else's work",
		Type:     "task",
		Status:   "in_progress",
		Assignee: owner,
		Metadata: map[string]string{
			"gc.session_id":   "gc-hywwv",
			"gc.session_name": owner,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	released, err := releaserFor(t, store).ReleaseIfCurrent(created.ID, "gastown.furiosa")
	if err != nil {
		t.Fatalf("ReleaseIfCurrent: %v", err)
	}
	if released {
		t.Fatal("ReleaseIfCurrent released a bead held by a different assignee")
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Assignee != owner {
		t.Fatalf("assignee = %q, want %q untouched", got.Assignee, owner)
	}
	if got.Metadata["gc.session_id"] != "gc-hywwv" || got.Metadata["gc.session_name"] != owner {
		t.Fatalf("a refused release cleared the owner's identity: %v", got.Metadata)
	}
}
