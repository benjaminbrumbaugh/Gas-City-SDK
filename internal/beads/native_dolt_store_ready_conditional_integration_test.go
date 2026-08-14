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
	before, err := store.Get(candidate.ID)
	if err != nil {
		t.Fatalf("Get candidate before dependency: %v", err)
	}
	if err := store.DepAdd(candidate.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	afterDependency, err := store.Get(candidate.ID)
	if err != nil {
		t.Fatalf("Get candidate after dependency: %v", err)
	}
	if afterDependency.Revision == before.Revision {
		t.Fatalf("dependency projection left readiness revision unchanged: %d", afterDependency.Revision)
	}
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
