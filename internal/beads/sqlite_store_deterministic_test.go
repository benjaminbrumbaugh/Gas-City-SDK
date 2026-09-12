package beads

import (
	"errors"
	"sync"
	"testing"
)

func TestSQLiteStoreCreateDeterministicTwoHandlesConverge(t *testing.T) {
	dir := t.TempDir()
	openedA, err := OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeA := openedA.(*SQLiteStore)
	t.Cleanup(func() { _ = storeA.CloseStore() })
	openedB, err := OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeB := openedB.(*SQLiteStore)
	t.Cleanup(func() { _ = storeB.CloseStore() })

	request := deterministicCreateTestBead("sqlite recovery")
	type result struct {
		bead     Bead
		inserted bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, store := range []*SQLiteStore{storeA, storeB} {
		go func(store *SQLiteStore) {
			ready.Done()
			<-start
			bead, inserted, err := store.CreateDeterministic("recovery-attempt-sqlite", request)
			results <- result{bead: bead, inserted: inserted, err: err}
		}(store)
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("two-handle results: %v / %v", first.err, second.err)
	}
	if first.bead.ID != second.bead.ID || first.inserted == second.inserted {
		t.Fatalf("two-handle results = %#v / %#v, want one insert and one adoption", first, second)
	}
	if !SupportsDeterministicCreate(storeA) {
		t.Fatal("SQLite deterministic capability is hidden")
	}
}

func TestSQLiteStoreCreateDeterministicRejectsConflictingTuple(t *testing.T) {
	opened, err := OpenSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := opened.(*SQLiteStore)
	t.Cleanup(func() { _ = store.CloseStore() })
	request := deterministicCreateTestBead("sqlite recovery")
	if _, _, err := store.CreateDeterministic("recovery-attempt-sqlite", request); err != nil {
		t.Fatal(err)
	}
	request.Description = "conflicting"
	if _, _, err := store.CreateDeterministic("recovery-attempt-sqlite", request); !errors.Is(err, ErrDeterministicCreateConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}
}
