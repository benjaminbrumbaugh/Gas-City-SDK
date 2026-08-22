package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type guardedCloseTestStore struct {
	*beads.MemStore
}

func (s *guardedCloseTestStore) CloseWithMetadataIfMatch(id string, revision int64, metadata map[string]string) error {
	if err := s.UpdateIfMatch(id, revision, beads.UpdateOpts{Metadata: metadata}); err != nil {
		return err
	}
	updated, err := s.Get(id)
	if err != nil {
		return err
	}
	return s.CloseIfMatch(id, updated.Revision)
}

func TestDecideGuardedStaleCloseRequiresExactStaleSnapshot(t *testing.T) {
	base := GuardedStaleCloseFacts{
		Now:               time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC),
		Revision:          42,
		Status:            "open",
		State:             StateAsleep,
		HeldUntil:         "2126-07-28T22:59:29Z",
		ExpectedRevision:  42,
		ExpectedState:     StateAsleep,
		ExpectedHeldUntil: "2126-07-28T22:59:29Z",
		Reason:            "operator confirmed no pending work",
	}
	if err := DecideGuardedStaleClose(base); err != nil {
		t.Fatalf("valid decision: %v", err)
	}

	tests := []struct {
		name string
		mut  func(*GuardedStaleCloseFacts)
		want string
	}{
		{"zero expected revision", func(f *GuardedStaleCloseFacts) { f.ExpectedRevision = 0 }, "non-zero expected revision"},
		{"revision changed", func(f *GuardedStaleCloseFacts) { f.Revision++ }, "revision changed"},
		{"closed", func(f *GuardedStaleCloseFacts) { f.Status, f.Closed = "closed", true }, "status changed"},
		{"non-open", func(f *GuardedStaleCloseFacts) { f.Status = "in_progress" }, "status changed"},
		{"state changed", func(f *GuardedStaleCloseFacts) { f.State = StateActive }, "state changed"},
		{"hold changed", func(f *GuardedStaleCloseFacts) { f.HeldUntil = "" }, "hold changed"},
		{"malformed hold", func(f *GuardedStaleCloseFacts) { f.HeldUntil, f.ExpectedHeldUntil = "not-a-time", "not-a-time" }, "not RFC3339"},
		{"expired hold", func(f *GuardedStaleCloseFacts) {
			f.HeldUntil, f.ExpectedHeldUntil = "2026-08-22T01:59:59Z", "2026-08-22T01:59:59Z"
		}, "not in the future"},
		{"session key", func(f *GuardedStaleCloseFacts) { f.SessionKey = "runtime-key" }, "session key"},
		{"pending create", func(f *GuardedStaleCloseFacts) { f.PendingCreate = "true" }, "pending create"},
		{"running", func(f *GuardedStaleCloseFacts) { f.RuntimeRunning = true }, "live runtime"},
		{"alive", func(f *GuardedStaleCloseFacts) { f.RuntimeAlive = true }, "live runtime"},
		{"missing reason", func(f *GuardedStaleCloseFacts) { f.Reason = "" }, "operator reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base
			tt.mut(&got)
			err := DecideGuardedStaleClose(got)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestStoreCloseGuardedStaleSessionClosesExactSnapshot(t *testing.T) {
	backing := &guardedCloseTestStore{MemStore: beads.NewMemStore()}
	created, err := backing.Create(beads.Bead{
		Type:   BeadType,
		Labels: []string{LabelSession},
		Metadata: map[string]string{
			"state":                string(StateAsleep),
			"held_until":           "2126-07-28T22:59:29Z",
			"wait_hold":            "true",
			"sleep_reason":         "user-hold",
			"pending_create_claim": "",
			"session_key":          "",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := backing.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	front := NewStore(beads.SessionStore{Store: backing})
	now := time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC)
	req := GuardedStaleCloseRequest{
		ExpectedRevision:  current.Revision,
		ExpectedState:     StateAsleep,
		ExpectedHeldUntil: "2126-07-28T22:59:29Z",
		Reason:            "operator confirmed no pending work",
		Now:               now,
	}
	if err := front.CheckGuardedStaleSession(created.ID, req); err != nil {
		t.Fatalf("CheckGuardedStaleSession: %v", err)
	}
	checked, err := backing.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after check: %v", err)
	}
	if checked.Status == "closed" || checked.Revision != current.Revision {
		t.Fatalf("check mode mutated session: %#v", checked)
	}
	if err := front.CloseGuardedStaleSession(created.ID, req); err != nil {
		t.Fatalf("CloseGuardedStaleSession: %v", err)
	}
	closed, err := backing.Get(created.ID)
	if err != nil {
		t.Fatalf("Get closed: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("status = %q, want closed", closed.Status)
	}
	if closed.Metadata["state"] != "operator-stale" || closed.Metadata["held_until"] != "" || closed.Metadata["wait_hold"] != "" {
		t.Fatalf("close metadata = %#v", closed.Metadata)
	}
	if closed.Metadata["operator_close_reason"] != "operator confirmed no pending work" {
		t.Fatalf("operator_close_reason = %q", closed.Metadata["operator_close_reason"])
	}
}

func TestStoreCloseGuardedStaleSessionRefusesCapabilityOrStaleRevision(t *testing.T) {
	now := time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC)
	future := "2126-08-22T02:00:00Z"
	plain := beads.NewMemStore()
	created, err := plain.Create(beads.Bead{
		Type: BeadType, Labels: []string{LabelSession},
		Metadata: map[string]string{"state": string(StateAsleep), "held_until": future},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, _ := plain.Get(created.ID)
	front := NewStore(beads.SessionStore{Store: plain})
	err = front.CloseGuardedStaleSession(created.ID, GuardedStaleCloseRequest{
		ExpectedRevision: current.Revision, ExpectedState: StateAsleep, ExpectedHeldUntil: future, Reason: "reason", Now: now,
	})
	if !errors.Is(err, beads.ErrConditionalWriteUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}

	backing := &guardedCloseTestStore{MemStore: beads.NewMemStore()}
	created, _ = backing.Create(beads.Bead{
		Type: BeadType, Labels: []string{LabelSession},
		Metadata: map[string]string{"state": string(StateAsleep), "held_until": future},
	})
	snapshot, _ := backing.Get(created.ID)
	changed := "changed"
	if err := backing.Update(created.ID, beads.UpdateOpts{Title: &changed}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	front = NewStore(beads.SessionStore{Store: backing})
	err = front.CloseGuardedStaleSession(created.ID, GuardedStaleCloseRequest{
		ExpectedRevision: snapshot.Revision, ExpectedState: StateAsleep, ExpectedHeldUntil: future, Reason: "reason", Now: now,
	})
	if err == nil || !strings.Contains(err.Error(), "revision changed") {
		t.Fatalf("stale error = %v", err)
	}
	final, _ := backing.Get(created.ID)
	if final.Status == "closed" {
		t.Fatal("stale revision closed session")
	}
}
