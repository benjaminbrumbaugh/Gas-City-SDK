package session

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestRecoveryStatePersistsQuotaObservationEvidence(t *testing.T) {
	backend := recoverySessionMemStore(nil)
	store := NewStore(beads.SessionStore{Store: backend})
	now := time.Now().UTC().Truncate(time.Second)
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	const evidence = `{"provider":"codex","session_id":"session-1","incarnation":"incarnation-a","hard_exhausted":true}`
	if _, err := store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID:       "incident-1",
		Impairment:       "quota_exceeded",
		DetectedAt:       now,
		QuotaObservation: evidence,
		Outcome:          "detected",
	}); err != nil {
		t.Fatal(err)
	}
	info, err := store.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.RecoveryQuotaObservation != evidence {
		t.Fatalf("RecoveryQuotaObservation = %q, want persisted evidence", info.RecoveryQuotaObservation)
	}
}
