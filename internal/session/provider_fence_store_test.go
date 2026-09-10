package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type providerFenceRemediationRaceStore struct {
	*beads.MemStore
	beforeUpdate func()
}

func (s *providerFenceRemediationRaceStore) UpdateIfMatch(id string, expectedRevision int64, opts beads.UpdateOpts) error {
	if s.beforeUpdate != nil {
		before := s.beforeUpdate
		s.beforeUpdate = nil
		before()
	}
	return s.MemStore.UpdateIfMatch(id, expectedRevision, opts)
}

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

func TestProviderFenceStoreBypassesStaleCachingStore(t *testing.T) {
	backing := beads.NewMemStore()
	cache := beads.NewCachingStore(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC)
	if _, err := backing.Create(beads.Bead{
		Title:  "externally-created provider fence",
		Type:   WaitBeadType,
		Labels: []string{ProviderFenceBeadLabel},
		Metadata: map[string]string{
			"kind":                    providerFenceBeadKind,
			"provider_fence_identity": "account:external",
			"fenced_until":            now.Add(time.Hour).Format(time.RFC3339),
			"observed_at":             now.Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatal(err)
	}

	fences, err := NewStore(beads.SessionStore{Store: cache}).ActiveProviderFences(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(fences) != 1 || fences[0].Identity != "account:external" {
		t.Fatalf("active fences = %#v, want externally-created fence despite stale cache", fences)
	}
}

func TestProviderFenceStoreDoesNotReturnExpiredRecords(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
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
	rows, err := mem.List(beads.ListQuery{Label: ProviderFenceBeadLabel, IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "closed" {
		t.Fatalf("expired fence rows = %#v, want one closed audit row", rows)
	}
}

func TestProviderFenceStoreBoundsExpiredCleanupPerRead(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
	now := time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC)
	for i := 0; i < maxExpiredProviderFencesClosedPerRead+1; i++ {
		if err := store.RecordProviderFence(
			fmt.Sprintf("account:%d", i),
			now.Add(-time.Minute),
			now.Add(-2*time.Minute),
			"usage_limit_modal",
		); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.ActiveProviderFences(now); err != nil {
		t.Fatal(err)
	}
	open, err := mem.List(beads.ListQuery{Label: ProviderFenceBeadLabel, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open expired rows after one read = %d, want 1", len(open))
	}
	if _, err := store.ActiveProviderFences(now); err != nil {
		t.Fatal(err)
	}
	open, err = mem.List(beads.ListQuery{Label: ProviderFenceBeadLabel, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("open expired rows after second read = %d, want 0", len(open))
	}
}

func TestProviderFenceBookkeepingCannotEnterSessionOrWaitEnumeration(t *testing.T) {
	mem := beads.NewMemStore()
	store := NewStore(beads.SessionStore{Store: mem})
	now := time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC)
	if err := store.RecordProviderFence("account:a", now.Add(time.Hour), now, "usage_limit_modal"); err != nil {
		t.Fatal(err)
	}
	sessionRow, err := mem.Create(beads.Bead{
		Title:  "managed session",
		Type:   BeadType,
		Labels: []string{LabelSession},
		Metadata: map[string]string{
			"session_name": "managed",
			"state":        string(StateActive),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := store.List("all", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != sessionRow.ID {
		t.Fatalf("session list = %#v, want only %s", listed, sessionRow.ID)
	}
	reconciled, err := store.ListAllForReconcile(ListAllOptions{IncludeClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled) != 1 || reconciled[0].Info.ID != sessionRow.ID {
		t.Fatalf("reconcile list = %#v, want only %s", reconciled, sessionRow.ID)
	}
	waits, err := store.ListWaits("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 0 {
		t.Fatalf("wait list contains provider-fence bookkeeping: %#v", waits)
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

func TestProviderFenceStoreFailsClosedOnEveryLabeledCorruption(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		kind   string
		meta   map[string]string
		needle string
	}{
		{"wrong type", "task", map[string]string{"kind": providerFenceBeadKind, "provider_fence_identity": "account:a", "fenced_until": now.Add(time.Hour).Format(time.RFC3339)}, "invalid type"},
		{"wrong kind", WaitBeadType, map[string]string{"kind": "other", "provider_fence_identity": "account:a", "fenced_until": now.Add(time.Hour).Format(time.RFC3339)}, "invalid kind"},
		{"empty identity", WaitBeadType, map[string]string{"kind": providerFenceBeadKind, "fenced_until": now.Add(time.Hour).Format(time.RFC3339)}, "empty identity"},
		{"bad deadline", WaitBeadType, map[string]string{"kind": providerFenceBeadKind, "provider_fence_identity": "account:a", "fenced_until": "broken"}, "invalid deadline"},
		{"bad observed time", WaitBeadType, map[string]string{"kind": providerFenceBeadKind, "provider_fence_identity": "account:a", "fenced_until": now.Add(time.Hour).Format(time.RFC3339), "observed_at": "broken"}, "invalid observation time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := beads.NewMemStore()
			row, err := mem.Create(beads.Bead{Title: "provider fence", Type: tc.kind, Labels: []string{ProviderFenceBeadLabel}, Metadata: tc.meta})
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewStore(beads.SessionStore{Store: mem}).ActiveProviderFences(now)
			if err == nil || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), tc.needle) || !strings.Contains(err.Error(), "explicitly") {
				t.Fatalf("corrupt fence error = %v, want row ID, corruption, and operator repair path", err)
			}
		})
	}
}

func TestProviderFenceStoreAutoClosesOldCorruptionAfterSafetyBound(t *testing.T) {
	now := time.Now().UTC().Add(maxCorruptProviderFenceFailClosedAge + time.Minute)
	mem := beads.NewMemStore()
	row, err := mem.Create(beads.Bead{
		Title:    "corrupt provider fence",
		Type:     WaitBeadType,
		Labels:   []string{ProviderFenceBeadLabel},
		Metadata: map[string]string{"kind": providerFenceBeadKind, "provider_fence_identity": "account:a", "fenced_until": "broken"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(beads.SessionStore{Store: mem})
	if _, err := store.ActiveProviderFences(now); err == nil || !strings.Contains(err.Error(), "auto-closed") {
		t.Fatalf("first read error = %v, want visible auto-remediation error", err)
	}
	repaired, err := mem.Get(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "closed" || repaired.Metadata[providerFenceRemediationMetadataKey] != providerFenceAutoClosedCorruptionReason || repaired.Metadata[providerFenceRemediationAtMetadataKey] == "" {
		t.Fatalf("repaired row = %#v, want closed with durable remediation audit", repaired)
	}
	if fences, err := store.ActiveProviderFences(now); err != nil || len(fences) != 0 {
		t.Fatalf("second read = %#v, %v; corrupt row wedged city after bounded remediation", fences, err)
	}
}

func TestProviderFenceStoreRemediationCannotCloseConcurrentlyRepairedRow(t *testing.T) {
	now := time.Now().UTC().Add(maxCorruptProviderFenceFailClosedAge + time.Minute)
	mem := beads.NewMemStore()
	row, err := mem.Create(beads.Bead{Title: "corrupt provider fence", Type: WaitBeadType, Labels: []string{ProviderFenceBeadLabel}, Metadata: map[string]string{"kind": providerFenceBeadKind, "provider_fence_identity": "account:a", "fenced_until": "broken"}})
	if err != nil {
		t.Fatal(err)
	}
	racing := &providerFenceRemediationRaceStore{MemStore: mem}
	racing.beforeUpdate = func() {
		if err := mem.Update(row.ID, beads.UpdateOpts{Metadata: map[string]string{"fenced_until": now.Add(time.Hour).Format(time.RFC3339)}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = NewStore(beads.SessionStore{Store: racing}).ActiveProviderFences(now)
	var precondition *beads.PreconditionFailedError
	if !errors.As(err, &precondition) {
		t.Fatalf("racing remediation error = %v, want revision precondition failure", err)
	}
	repaired, err := mem.Get(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status == "closed" || repaired.Metadata["fenced_until"] == "broken" {
		t.Fatalf("concurrently repaired row was closed or lost: %#v", repaired)
	}
}

func TestProviderFenceStoreBoundsCorruptionRemediationPerRead(t *testing.T) {
	now := time.Now().UTC().Add(maxCorruptProviderFenceFailClosedAge + time.Minute)
	mem := beads.NewMemStore()
	for i := 0; i < maxCorruptProviderFencesClosedPerRead+1; i++ {
		if _, err := mem.Create(beads.Bead{
			Title:  fmt.Sprintf("corrupt provider fence %d", i),
			Type:   WaitBeadType,
			Labels: []string{ProviderFenceBeadLabel},
			Metadata: map[string]string{
				"kind":                    providerFenceBeadKind,
				"provider_fence_identity": fmt.Sprintf("account:%d", i),
				"fenced_until":            "broken",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(beads.SessionStore{Store: mem})
	if _, err := store.ActiveProviderFences(now); err == nil {
		t.Fatal("first corruption-remediation pass returned no visible error")
	}
	open, err := mem.List(beads.ListQuery{Label: ProviderFenceBeadLabel, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open corrupt rows after one read = %d, want 1", len(open))
	}
	if _, err := store.ActiveProviderFences(now); err == nil {
		t.Fatal("second corruption-remediation pass returned no visible error")
	}
	if fences, err := store.ActiveProviderFences(now); err != nil || len(fences) != 0 {
		t.Fatalf("post-remediation read = %#v, %v", fences, err)
	}
}

func TestProviderFenceIdentityHistoryIncludesClosedFencesAndSessionAttribution(t *testing.T) {
	const keyed = "account:hmac-sha256:0123456789abcdef"
	for _, tc := range []struct {
		name string
		bead beads.Bead
	}{
		{"closed fence", beads.Bead{Type: WaitBeadType, Status: "closed", Labels: []string{ProviderFenceBeadLabel}, Metadata: map[string]string{"provider_fence_identity": keyed}}},
		{"started session", beads.Bead{Type: BeadType, Status: "closed", Labels: []string{LabelSession}, Metadata: map[string]string{"started_provider_fence_identity": keyed}}},
		{"launch session", beads.Bead{Type: BeadType, Labels: []string{LabelSession}, Metadata: map[string]string{"launch_provider_fence_identity": keyed}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := beads.NewMemStore()
			if _, err := mem.Create(tc.bead); err != nil {
				t.Fatal(err)
			}
			got, err := NewStore(beads.SessionStore{Store: mem}).HasKeyedProviderFenceIdentityHistory()
			if err != nil || !got {
				t.Fatalf("history = %v, %v; want true", got, err)
			}
		})
	}
}

func TestProviderFenceIdentityHistoryBypassesStaleSessionCache(t *testing.T) {
	backing := beads.NewMemStore()
	cache := beads.NewCachingStore(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := backing.Create(beads.Bead{Type: BeadType, Labels: []string{LabelSession}, Metadata: map[string]string{"started_provider_fence_identity": "account:hmac-sha256:0123456789abcdef"}}); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(beads.SessionStore{Store: cache}).HasKeyedProviderFenceIdentityHistory()
	if err != nil || !got {
		t.Fatalf("history through stale cache = %v, %v; want live true", got, err)
	}
}
