//go:build integration

package beads

import (
	"strings"
	"sync"
	"testing"
)

func TestNativeDoltStoreCreateDeterministicAgainstRealOpenBestAvailable(t *testing.T) {
	store := openRealNativeDoltStoreForCAS(t, "deterministic-create-real")
	request := deterministicCreateTestBead("recovery")
	created, inserted, err := store.CreateDeterministic("recovery-attempt-real", request)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || !strings.HasPrefix(created.ID, "gc-") {
		t.Fatalf("real create = %#v, inserted=%v", created, inserted)
	}
	adopted, inserted, err := store.CreateDeterministic("recovery-attempt-real", request)
	if err != nil || inserted || adopted.ID != created.ID {
		t.Fatalf("real retry = %#v, inserted=%v, err=%v", adopted, inserted, err)
	}
}

// TestNativeDoltStoreCreateDeterministicConcurrentAgainstRealDolt proves that
// the upstream create-only coordination survives real transaction contention,
// not only the causal unit fixture. Every caller asks for the same tuple; one
// commits it and every loser adopts the committed snapshot.
func TestNativeDoltStoreCreateDeterministicConcurrentAgainstRealDolt(t *testing.T) {
	store := openRealNativeDoltStoreForCAS(t, "deterministic-create-contention")
	const callers = 16
	start := make(chan struct{})
	type result struct {
		bead     Bead
		inserted bool
		err      error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			bead, inserted, err := store.CreateDeterministic("recovery-attempt-contention", deterministicCreateTestBead("recovery"))
			results <- result{bead: bead, inserted: inserted, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	insertions := 0
	var first Bead
	for got := range results {
		if got.err != nil {
			t.Errorf("CreateDeterministic: %v", got.err)
			continue
		}
		if got.inserted {
			insertions++
		}
		if first.ID == "" {
			first = got.bead
		} else if got.bead.ID != first.ID || !got.bead.CreatedAt.Equal(first.CreatedAt) {
			t.Errorf("concurrent result diverged: first=%+v got=%+v", first, got.bead)
		}
	}
	if insertions != 1 {
		t.Fatalf("reported %d insertions, want exactly one", insertions)
	}
	all, err := store.List(ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != first.ID {
		t.Fatalf("stored rows = %+v, want only %q", all, first.ID)
	}
}
