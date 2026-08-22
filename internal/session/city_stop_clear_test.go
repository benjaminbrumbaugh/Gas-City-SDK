package session

import (
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestStoreClearCityStopValueCAS(t *testing.T) {
	store := beads.NewMemStore()
	created, err := store.Create(beads.Bead{
		Type: BeadType, Labels: []string{LabelSession},
		Metadata: map[string]string{
			"state": "suspended", "sleep_reason": "city-stop",
			"held_until": "2126-07-29T03:21:45Z", "session_key": "", "pending_create_claim": "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	front := NewStore(beads.SessionStore{Store: store})
	req := ClearCityStopRequest{ExpectedHeldUntil: "2126-07-29T03:21:45Z", Now: time.Date(2026, 8, 22, 3, 25, 0, 0, time.UTC)}
	if err := front.CheckClearCityStop(created.ID, req); err != nil {
		t.Fatalf("check: %v", err)
	}
	before, _ := store.Get(created.ID)
	if before.Metadata["sleep_reason"] != "city-stop" {
		t.Fatal("check mutated sleep_reason")
	}
	if err := front.ClearCityStop(created.ID, req); err != nil {
		t.Fatalf("clear: %v", err)
	}
	after, _ := store.Get(created.ID)
	if after.Metadata["sleep_reason"] != "" {
		t.Fatalf("sleep_reason=%q", after.Metadata["sleep_reason"])
	}
	if after.Metadata["held_until"] != req.ExpectedHeldUntil {
		t.Fatal("hold changed")
	}
}

func TestStoreClearCityStopRefusesDrift(t *testing.T) {
	base := func() (*beads.MemStore, beads.Bead, *Store, ClearCityStopRequest) {
		store := beads.NewMemStore()
		b, _ := store.Create(beads.Bead{Type: BeadType, Labels: []string{LabelSession}, Metadata: map[string]string{
			"state": "asleep", "sleep_reason": "city-stop", "held_until": "2126-07-29T03:21:45Z",
		}})
		return store, b, NewStore(beads.SessionStore{Store: store}), ClearCityStopRequest{ExpectedHeldUntil: "2126-07-29T03:21:45Z", Now: time.Date(2026, 8, 22, 3, 25, 0, 0, time.UTC)}
	}
	t.Run("lost value CAS", func(t *testing.T) {
		store, b, front, req := base()
		if err := store.SetMetadata(b.ID, "sleep_reason", "other"); err != nil {
			t.Fatal(err)
		}
		err := front.ClearCityStop(b.ID, req)
		if err == nil || !strings.Contains(err.Error(), "sleep_reason") {
			t.Fatalf("err=%v", err)
		}
		after, _ := store.Get(b.ID)
		if after.Metadata["sleep_reason"] != "other" {
			t.Fatal("drift overwritten")
		}
	})
	t.Run("expired hold", func(t *testing.T) {
		_, b, front, req := base()
		req.Now = time.Date(2126, 7, 29, 3, 21, 46, 0, time.UTC)
		err := front.CheckClearCityStop(b.ID, req)
		if err == nil || !strings.Contains(err.Error(), "not in the future") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("live runtime", func(t *testing.T) {
		_, b, front, req := base()
		req.RuntimeRunning = true
		err := front.CheckClearCityStop(b.ID, req)
		if err == nil || !strings.Contains(err.Error(), "live runtime") {
			t.Fatalf("err=%v", err)
		}
	})
}
