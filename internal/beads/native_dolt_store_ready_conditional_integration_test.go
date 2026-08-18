//go:build integration

package beads

import (
	"errors"
	"testing"
)

func TestNativeDoltStoreReadyConditionalUpdateRejectsDependencyRaceAgainstRealDolt(t *testing.T) {
	store := openRealNativeDoltStoreForCAS(t, "ready-dependency-race")
	blocker, err := store.Create(Bead{Title: "open blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	candidate, err := store.Create(Bead{Title: "routing candidate"})
	if err != nil {
		t.Fatalf("Create candidate: %v", err)
	}
	if err := store.DepAdd(candidate.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	afterDependency, err := store.Get(candidate.ID)
	if err != nil {
		t.Fatalf("Get candidate after dependency: %v", err)
	}
	// Dependency/is_blocked projection writes are derived state and are allowed
	// not to change RowVersion. This is the important case for the ready fence:
	// readiness must be checked independently of the caller's revision token.
	if err := store.UpdateIfReadyAndMatch(candidate.ID, afterDependency.Revision, UpdateOpts{Metadata: map[string]string{"route": "must-not-land"}}); !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("blocked admission update error = %v, want ErrNotReadyForConditionalUpdate", err)
	}
	final, err := store.Get(candidate.ID)
	if err != nil {
		t.Fatalf("Get candidate after refusal: %v", err)
	}
	if final.Metadata["route"] != "" {
		t.Fatalf("blocked admission wrote route metadata = %q", final.Metadata["route"])
	}
}

func TestNativeDoltStoreReadyConditionalUpdateAgainstRealDolt(t *testing.T) {
	store := openRealNativeDoltStoreForCAS(t, "ready-admission")
	staleCandidate, err := store.Create(Bead{Title: "stale revision candidate"})
	if err != nil {
		t.Fatalf("Create stale candidate: %v", err)
	}
	staleSnapshot, err := store.Get(staleCandidate.ID)
	if err != nil {
		t.Fatalf("Get stale candidate: %v", err)
	}
	changedTitle := "changed before conditional admission"
	if err := store.Update(staleCandidate.ID, UpdateOpts{Title: &changedTitle}); err != nil {
		t.Fatalf("update stale candidate: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(staleCandidate.ID, staleSnapshot.Revision, UpdateOpts{Metadata: map[string]string{"route": "must-not-land"}}); err == nil {
		t.Fatal("stale revision admission succeeded")
	} else {
		var pfe *PreconditionFailedError
		if !errors.As(err, &pfe) {
			t.Fatalf("stale revision admission error = %v, want *PreconditionFailedError", err)
		}
	}

	created, err := store.Create(Bead{Title: "ready routing candidate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get ready candidate: %v", err)
	}
	if current.Revision == 0 {
		t.Fatal("native issue did not expose a row revision")
	}
	if err := store.UpdateIfReadyAndMatch(current.ID, current.Revision, UpdateOpts{Metadata: map[string]string{"route": "alpha/or-fast"}}); err != nil {
		t.Fatalf("ready admission update: %v", err)
	}

	afterRoute, err := store.Get(current.ID)
	if err != nil {
		t.Fatalf("Get routed candidate: %v", err)
	}
	if afterRoute.Metadata["route"] != "alpha/or-fast" {
		t.Fatalf("route metadata = %q, want alpha/or-fast", afterRoute.Metadata["route"])
	}
	assignee := "other"
	if err := store.Update(current.ID, UpdateOpts{Assignee: &assignee}); err != nil {
		t.Fatalf("assign candidate: %v", err)
	}
	assigned, err := store.Get(current.ID)
	if err != nil {
		t.Fatalf("Get assigned candidate: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(current.ID, assigned.Revision, UpdateOpts{Metadata: map[string]string{"route": "must-not-land"}}); !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("assigned admission update error = %v, want ErrNotReadyForConditionalUpdate", err)
	}
	final, err := store.Get(current.ID)
	if err != nil {
		t.Fatalf("Get refused candidate: %v", err)
	}
	if final.Metadata["route"] != "alpha/or-fast" {
		t.Fatalf("refused admission changed route metadata = %q", final.Metadata["route"])
	}
}
