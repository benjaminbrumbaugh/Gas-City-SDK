package session

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestProviderFenceStoreFoldsMonotonicDeadlines(t *testing.T) {
	store := NewStore(beads.SessionStore{Store: beads.NewMemStore()})
	now := time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC)
	if err := store.RecordProviderFence("account:a", now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderFence("account:a", now.Add(2*time.Hour), now.Add(time.Minute), "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderFence("account:b", now.Add(30*time.Minute), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}

	got, err := store.ActiveProviderFences(now.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("active fences = %#v, want two identities", got)
	}
	for _, fence := range got {
		if fence.Identity == "account:a" && !fence.Until.Equal(now.Add(2*time.Hour)) {
			t.Fatalf("account:a deadline = %s, want %s", fence.Until, now.Add(2*time.Hour))
		}
	}
}

func TestProviderFenceStoreDoesNotReturnExpiredRecords(t *testing.T) {
	store := NewStore(beads.SessionStore{Store: beads.NewMemStore()})
	now := time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC)
	if err := store.RecordProviderFence("account:a", now.Add(time.Minute), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	got, err := store.ActiveProviderFences(now.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expired fences = %#v, want none", got)
	}
}
