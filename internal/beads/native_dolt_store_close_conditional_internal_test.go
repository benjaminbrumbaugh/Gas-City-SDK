package beads

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/rollout/gate"
)

func TestNativeDoltStoreAtomicConditionalCloseIsRevisionFenced(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	store.stampConditionalWritesMode(gate.Require, false)
	closer, ok := AtomicConditionalCloserFor(store)
	if !ok {
		t.Fatal("NativeDoltStore does not resolve AtomicConditionalCloser")
	}
	if _, ok := ConditionalWriterFor(store); !ok {
		t.Fatal("NativeDoltStore does not resolve ConditionalWriter")
	}

	created, err := store.Create(Bead{
		Title: "stale session",
		Metadata: map[string]string{
			"state":        "asleep",
			"held_until":   "2126-07-28T22:59:29Z",
			"close_reason": "session closed: operator-stale",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := closer.CloseWithMetadataIfMatch(current.ID, current.Revision, map[string]string{
		"state":      "operator-stale",
		"held_until": "",
	}); err != nil {
		t.Fatalf("conditional close: %v", err)
	}
	closed, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after close: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("status = %q, want closed", closed.Status)
	}
	if closed.Metadata["state"] != "operator-stale" || closed.Metadata["held_until"] != "" {
		t.Fatalf("metadata after close = %#v", closed.Metadata)
	}
}

func TestNativeDoltStoreConditionalCloseRejectsStaleOrBlocked(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	store.stampConditionalWritesMode(gate.Require, false)
	closer, ok := AtomicConditionalCloserFor(store)
	if !ok {
		t.Fatal("NativeDoltStore does not resolve AtomicConditionalCloser")
	}

	stale, err := store.Create(Bead{Title: "stale revision", Metadata: map[string]string{"state": "asleep"}})
	if err != nil {
		t.Fatalf("Create stale: %v", err)
	}
	snapshot, err := store.Get(stale.ID)
	if err != nil {
		t.Fatalf("Get stale: %v", err)
	}
	changed := "changed"
	if err := store.Update(stale.ID, UpdateOpts{Title: &changed}); err != nil {
		t.Fatalf("Update stale: %v", err)
	}
	if _, err := closer.CloseWithMetadataIfMatch(stale.ID, snapshot.Revision, map[string]string{"state": "operator-stale"}); err == nil {
		t.Fatal("stale revision close succeeded")
	} else {
		var pfe *PreconditionFailedError
		if !errors.As(err, &pfe) {
			t.Fatalf("stale close error = %v, want *PreconditionFailedError", err)
		}
	}
	stillOpen, err := store.Get(stale.ID)
	if err != nil {
		t.Fatalf("Get after stale close: %v", err)
	}
	if stillOpen.Status == "closed" || stillOpen.Metadata["state"] == "operator-stale" {
		t.Fatalf("stale close mutated issue: %#v", stillOpen)
	}

	blocker, err := store.Create(Bead{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	blocked, err := store.Create(Bead{Title: "blocked stale session", Metadata: map[string]string{"state": "asleep"}})
	if err != nil {
		t.Fatalf("Create blocked: %v", err)
	}
	if err := store.DepAdd(blocked.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	blockedCurrent, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get blocked: %v", err)
	}
	if _, err := closer.CloseWithMetadataIfMatch(blocked.ID, blockedCurrent.Revision, map[string]string{"state": "operator-stale"}); !errors.Is(err, ErrNotClosableForConditionalClose) {
		t.Fatalf("blocked close error = %v, want ErrNotClosableForConditionalClose", err)
	}
	blockedFinal, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get blocked final: %v", err)
	}
	if blockedFinal.Status == "closed" || blockedFinal.Metadata["state"] == "operator-stale" {
		t.Fatalf("blocked close mutated issue: %#v", blockedFinal)
	}

	nonOpen, err := store.Create(Bead{Title: "non-open stale session", Metadata: map[string]string{"state": "asleep"}})
	if err != nil {
		t.Fatalf("Create non-open: %v", err)
	}
	inProgress := "in_progress"
	if err := store.Update(nonOpen.ID, UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("mark non-open: %v", err)
	}
	nonOpenCurrent, err := store.Get(nonOpen.ID)
	if err != nil {
		t.Fatalf("Get non-open: %v", err)
	}
	if _, err := closer.CloseWithMetadataIfMatch(nonOpen.ID, nonOpenCurrent.Revision, map[string]string{"state": "operator-stale"}); !errors.Is(err, ErrNotClosableForConditionalClose) {
		t.Fatalf("non-open close error = %v, want ErrNotClosableForConditionalClose", err)
	}
	nonOpenFinal, err := store.Get(nonOpen.ID)
	if err != nil {
		t.Fatalf("Get non-open final: %v", err)
	}
	if nonOpenFinal.Status == "closed" || nonOpenFinal.Metadata["state"] == "operator-stale" {
		t.Fatalf("non-open close mutated issue: %#v", nonOpenFinal)
	}
}
