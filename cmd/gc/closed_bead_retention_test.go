package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// closedBeadRetentionFixture seeds one store with the cases the sweep must
// classify: an eligible aged closed task, a too-recent closed task, an aged
// closed bead linked (either direction) to open work, an aged closed bead
// whose parent is open, an ephemeral aged closed bead (wisp GC's domain), and
// an open bead. bd's pinned protection is a status ("pinned" is in bd's
// not-done set), so a pinned bead never matches the status=closed candidate
// query and needs no separate case here.
func closedBeadRetentionFixture(now time.Time) []beads.Bead {
	old := now.Add(-20 * 24 * time.Hour)
	return []beads.Bead{
		{ID: "aged-1", Title: "old session", Type: "session", Status: "closed", CreatedAt: old, UpdatedAt: old},
		{ID: "aged-2", Title: "old task", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old},
		{ID: "recent-1", Title: "just closed", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: now.Add(-time.Hour)},
		{
			ID: "open-1", Title: "still open", Type: "task", Status: "open", CreatedAt: old, UpdatedAt: old,
			Dependencies: []beads.Dep{{IssueID: "open-1", DependsOnID: "cited-by-open", Type: "blocks"}},
		},
		{ID: "cited-by-open", Title: "open work depends on me", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old},
		{
			ID: "depends-on-open", Title: "I depend on open work", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old,
			Dependencies: []beads.Dep{{IssueID: "depends-on-open", DependsOnID: "open-1", Type: "discovered-from"}},
		},
		{ID: "child-of-open", Title: "child of open parent", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old, ParentID: "open-1"},
		{ID: "closed-parent", Title: "closed parent", Type: "epic", Status: "closed", CreatedAt: old, UpdatedAt: old},
		{ID: "child-of-closed", Title: "child of closed parent", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old, ParentID: "closed-parent"},
		{ID: "pinned-1", Title: "pinned", Type: "task", Status: "pinned", CreatedAt: old, UpdatedAt: old},
		{ID: "wisp-1", Title: "ephemeral", Type: "task", Status: "closed", CreatedAt: old, UpdatedAt: old, Ephemeral: true},
	}
}

func TestSweepClosedBeadRetention_DeletesOnlyUnlinkedAgedClosedBeads(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)

	result, err := sweepClosedBeadRetention(store, now, 14*24*time.Hour, 100, true)
	if err != nil {
		t.Fatalf("sweepClosedBeadRetention: %v", err)
	}
	wantDeleted := []string{"aged-1", "aged-2", "closed-parent", "child-of-closed"}
	if result.deleted != len(wantDeleted) {
		t.Fatalf("deleted = %d, want %d (%v); result=%+v", result.deleted, len(wantDeleted), wantDeleted, result)
	}
	for _, id := range wantDeleted {
		if _, err := store.Get(id); !errors.Is(err, beads.ErrNotFound) {
			t.Errorf("Get(%s) err = %v, want ErrNotFound", id, err)
		}
	}
	for _, id := range []string{"recent-1", "open-1", "cited-by-open", "depends-on-open", "child-of-open", "pinned-1", "wisp-1"} {
		if _, err := store.Get(id); err != nil {
			t.Errorf("%s must be preserved: %v", id, err)
		}
	}
	if result.protected != 3 {
		t.Errorf("protected (linked to open work) = %d, want 3", result.protected)
	}
}

func TestSweepClosedBeadRetention_DryRunCountsWithoutDeleting(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	fixture := closedBeadRetentionFixture(now)
	store := beads.NewMemStoreFrom(100, fixture, nil)

	result, err := sweepClosedBeadRetention(store, now, 14*24*time.Hour, 100, false)
	if err != nil {
		t.Fatalf("sweepClosedBeadRetention: %v", err)
	}
	if result.deleted != 0 || result.eligible != 4 {
		t.Fatalf("dry run: deleted=%d eligible=%d, want 0/4", result.deleted, result.eligible)
	}
	for _, b := range fixture {
		if _, err := store.Get(b.ID); err != nil {
			t.Errorf("dry run must not delete %s: %v", b.ID, err)
		}
	}
}

func TestSweepClosedBeadRetention_RespectsBudget(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)

	result, err := sweepClosedBeadRetention(store, now, 14*24*time.Hour, 2, true)
	if err != nil {
		t.Fatalf("sweepClosedBeadRetention: %v", err)
	}
	if result.deleted != 2 {
		t.Fatalf("deleted = %d, want budget 2", result.deleted)
	}
}

func TestSweepClosedBeadRetention_DisabledWhenNoTTL(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)
	result, err := sweepClosedBeadRetention(store, now, 0, 100, true)
	if err != nil || result.eligible != 0 || result.deleted != 0 {
		t.Fatalf("zero ttl must be a no-op: result=%+v err=%v", result, err)
	}
}

func TestClosedBeadRetentionPolicyForConfig(t *testing.T) {
	if got := closedBeadRetentionTTLForConfig(nil); got != 0 {
		t.Fatalf("nil config ttl = %s, want 0 (disabled)", got)
	}
	if got := closedBeadRetentionTTLForConfig(&config.City{}); got != 0 {
		t.Fatalf("unset policy ttl = %s, want 0 (disabled — opt-in only)", got)
	}
	cfg := &config.City{Beads: config.BeadsConfig{Policies: map[string]config.BeadPolicyConfig{
		closedBeadRetentionPolicyName: {DeleteAfterClose: "14d"},
	}}}
	if got := closedBeadRetentionTTLForConfig(cfg); got != 14*24*time.Hour {
		t.Fatalf("ttl = %s, want 14d", got)
	}
}

func TestClosedBeadRetentionWatchdog_DryRunByDefaultAndLogs(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)
	var stderr strings.Builder
	cr := &CityRuntime{
		cityName: "test-city",
		cfg: &config.City{Workspace: config.Workspace{Name: "test-city"}, Beads: config.BeadsConfig{Policies: map[string]config.BeadPolicyConfig{
			closedBeadRetentionPolicyName: {DeleteAfterClose: "14d"},
		}}},
		standaloneCityStore: store,
		stdout:              io.Discard,
		stderr:              &stderr,
		logPrefix:           "gc test",
	}
	prev := closedBeadRetentionEnforced
	closedBeadRetentionEnforced = func() bool { return false }
	t.Cleanup(func() { closedBeadRetentionEnforced = prev })

	cr.runClosedBeadRetentionWatchdog(now)
	if _, err := store.Get("aged-1"); err != nil {
		t.Fatalf("dry-run watchdog must not delete: %v", err)
	}
	if !strings.Contains(stderr.String(), "dry-run") || !strings.Contains(stderr.String(), "4 closed bead(s)") {
		t.Fatalf("expected dry-run advisory naming 4 eligible beads, got %q", stderr.String())
	}

	// Enforced: deletes, and honors the interval on the next call.
	closedBeadRetentionEnforced = func() bool { return true }
	cr.closedBeadRetentionWatchdogLast = time.Time{}
	cr.runClosedBeadRetentionWatchdog(now)
	if _, err := store.Get("aged-1"); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("enforced watchdog should delete aged-1: %v", err)
	}
	if _, err := store.Get("cited-by-open"); err != nil {
		t.Fatalf("enforced watchdog must still protect linked bead: %v", err)
	}
	store2 := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)
	cr.standaloneCityStore = store2
	cr.runClosedBeadRetentionWatchdog(now.Add(time.Minute))
	if _, err := store2.Get("aged-1"); err != nil {
		t.Fatalf("watchdog ran again inside its interval: %v", err)
	}
}

func TestClosedBeadRetentionWatchdog_NoPolicyIsSilentNoop(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store := beads.NewMemStoreFrom(100, closedBeadRetentionFixture(now), nil)
	var stderr strings.Builder
	cr := &CityRuntime{
		cityName:            "test-city",
		cfg:                 &config.City{Workspace: config.Workspace{Name: "test-city"}},
		standaloneCityStore: store,
		stdout:              io.Discard,
		stderr:              &stderr,
		logPrefix:           "gc test",
	}
	cr.runClosedBeadRetentionWatchdog(now)
	if _, err := store.Get("aged-1"); err != nil {
		t.Fatalf("no policy must not delete: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("no policy must be silent, got %q", stderr.String())
	}
}
