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

func TestProviderFenceStoreFailsClosedWhenUnavailable(t *testing.T) {
	var store *Store
	if _, err := store.ActiveProviderFences(time.Now()); err == nil {
		t.Fatal("nil provider fence store returned an empty fence set")
	}
}

func TestProviderFenceStoreFailsClosedOnMalformedActiveRecord(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
	if _, err := mem.Create(beads.Bead{
		Title:  "provider usage fence",
		Status: "open",
		Type:   WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel},
		Metadata: map[string]string{
			"kind":                    providerFenceBeadKind,
			"provider_fence_identity": "account:a",
			"fenced_until":            "not-a-time",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveProviderFences(time.Now()); err == nil {
		t.Fatal("malformed durable provider fence was treated as absent")
	}
}
