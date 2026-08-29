package beads

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/rollout/gate"
	beadslib "github.com/steveyegge/beads"
)

// TestNativeDoltStoreDeclaresReadyConditionalWriter pins the readiness-specific
// capability needed by routing admission. NativeDoltStore also implements the
// broader row-version ConditionalWriter contract now that upstream validates
// and rejects mutations that cannot be fenced atomically.
func TestNativeDoltStoreDeclaresReadyConditionalWriter(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	store.stampConditionalWritesMode(gate.Require, false)
	if _, ok := ReadyConditionalWriterFor(store); !ok {
		t.Fatal("NativeDoltStore does not resolve ReadyConditionalWriter")
	}
	if _, ok := ConditionalWriterFor(store); !ok {
		t.Fatal("NativeDoltStore does not resolve ConditionalWriter")
	}
}

func TestNativeDoltStoreReadyConditionalUpdateUsesAllReadinessGuards(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	created, err := store.Create(Bead{Title: "ready candidate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(current.ID, current.Revision, UpdateOpts{
		Metadata: map[string]string{"route": "alpha/or-fast"},
	}); err != nil {
		t.Fatalf("ready update: %v", err)
	}
	afterReady, err := store.Get(current.ID)
	if err != nil {
		t.Fatalf("Get after ready update: %v", err)
	}
	if afterReady.Revision == current.Revision {
		t.Fatalf("ready update did not advance revision: before=%d after=%d", current.Revision, afterReady.Revision)
	}

	if err := store.UpdateIfReadyAndMatch(current.ID, current.Revision, UpdateOpts{
		Metadata: map[string]string{"route": "stale"},
	}); err == nil {
		t.Fatal("stale revision update succeeded")
	} else {
		var pfe *PreconditionFailedError
		if !errors.As(err, &pfe) {
			t.Fatalf("stale revision error = %v, want *PreconditionFailedError", err)
		}
	}

	blocker, err := store.Create(Bead{Title: "open blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	blocked, err := store.Create(Bead{Title: "blocked candidate"})
	if err != nil {
		t.Fatalf("Create blocked candidate: %v", err)
	}
	if err := store.DepAdd(blocked.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	blockedCurrent, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get blocked candidate: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(blocked.ID, blockedCurrent.Revision, UpdateOpts{
		Metadata: map[string]string{"route": "blocked"},
	}); !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("blocked update error = %v, want ErrNotReadyForConditionalUpdate", err)
	}

	assignee := "other"
	if err := store.Update(blocked.ID, UpdateOpts{Assignee: &assignee}); err != nil {
		t.Fatalf("assign candidate: %v", err)
	}
	assigned, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get assigned candidate: %v", err)
	}
	if err := store.Close(blocker.ID); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(blocked.ID, assigned.Revision, UpdateOpts{
		Metadata: map[string]string{"route": "assigned"},
	}); !errors.Is(err, ErrNotReadyForConditionalUpdate) {
		t.Fatalf("assigned update error = %v, want ErrNotReadyForConditionalUpdate", err)
	}

	final, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get final candidate: %v", err)
	}
	if final.Metadata["route"] != "" {
		t.Fatalf("refused update wrote route metadata = %q", final.Metadata["route"])
	}
}

func TestNativeDoltStoreReadyConditionalUpdateFailsClosedForUnsupportedMutations(t *testing.T) {
	store := newNativeDoltStoreForTest(newNativeDoltMemStorage())
	created, err := store.Create(Bead{Title: "ready candidate"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := store.UpdateIfReadyAndMatch(current.ID, current.Revision, UpdateOpts{Labels: []string{"route"}}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("label update error = %v, want unsupported refusal", err)
	}
	if err := store.UpdateIfReadyAndMatch("missing", current.Revision, UpdateOpts{Metadata: map[string]string{"route": "x"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update error = %v, want ErrNotFound", err)
	}
}

func TestNativeIssueReadyForConditionalUpdateHonorsReadyProjection(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	ready, err := nativeIssueReadyForConditionalUpdate(&beadslib.Issue{
		ID:         "future-deferred",
		Status:     beadslib.StatusOpen,
		IssueType:  beadslib.TypeTask,
		DeferUntil: &future,
	})
	if err != nil {
		t.Fatalf("future-deferred readiness: %v", err)
	}
	if ready {
		t.Fatal("future-deferred issue reported ready")
	}

	past := future.Add(-2 * time.Hour)
	ready, err = nativeIssueReadyForConditionalUpdate(&beadslib.Issue{
		ID:         "past-deferred",
		Status:     beadslib.StatusDeferred,
		IssueType:  beadslib.TypeTask,
		DeferUntil: &past,
	})
	if err != nil {
		t.Fatalf("past-deferred readiness: %v", err)
	}
	if !ready {
		t.Fatal("past-deferred issue reported not ready")
	}

	ready, err = nativeIssueReadyForConditionalUpdate(&beadslib.Issue{
		ID:        "indefinite-deferred",
		Status:    beadslib.StatusDeferred,
		IssueType: beadslib.TypeTask,
	})
	if err != nil {
		t.Fatalf("indefinite-deferred readiness: %v", err)
	}
	if ready {
		t.Fatal("indefinite-deferred issue reported ready")
	}
}
