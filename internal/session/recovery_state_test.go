package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type recoveryAtomicStore struct {
	*beads.MemStore
	updates int
	failAt  int
}

func (s *recoveryAtomicStore) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	s.updates++
	if s.failAt == s.updates {
		return errors.New("injected full transition failure")
	}
	return s.MemStore.UpdateIfMatch(id, revision, opts)
}

func (s *recoveryAtomicStore) CompareAndSetMetadataKey(string, string, string, string) (bool, error) {
	return false, errors.New("recovery must not use split metadata CAS")
}

func TestRecoveryTransitionAtomicallyOwnsMovesAndReleasesHold(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := recoverySessionMemStore(nil)
	backend := &recoveryAtomicStore{MemStore: base}
	store := NewStore(beads.SessionStore{Store: backend})

	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	firstHold := now.Add(time.Hour)
	snapshot, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now,
		HoldUntil: firstHold, Outcome: "detected",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveryHoldPair(t, base, firstHold.Format(time.RFC3339))

	secondHold := now.Add(2 * time.Hour)
	snapshot, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now,
		HoldUntil: secondHold, Attempt: 1, Outcome: "active", WorkID: "work-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveryHoldPair(t, base, secondHold.Format(time.RFC3339))

	snapshot, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now,
		Attempt: 1, Outcome: "verified",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveryHoldPair(t, base, "")
	if err := store.ReleaseRecoveryResponderLease(snapshot); err != nil {
		t.Fatal(err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata[recoveryResponderLeaseMetadataKey] != "" {
		t.Fatalf("lease not released: %+v", persisted.Metadata)
	}
	if backend.updates != 5 { // acquire + three transitions + release
		t.Fatalf("full conditional updates = %d, want 5", backend.updates)
	}
}

func TestRecoveryTransitionFailureLeavesLifecycleAndHoldUnchanged(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := recoverySessionMemStore(nil)
	backend := &recoveryAtomicStore{MemStore: base, failAt: 2}
	store := NewStore(beads.SessionStore{Store: backend})
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	_, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now,
		HoldUntil: now.Add(time.Hour), Outcome: "detected",
	})
	if err == nil {
		t.Fatal("atomic transition failure returned nil")
	}
	persisted, getErr := base.Get("session-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	for _, key := range []string{"recovery_incident_id", "recovery_hold_until", "held_until", recoveryHoldOwnedMetadataKey} {
		if got := persisted.Metadata[key]; got != "" {
			t.Fatalf("failed transition partially wrote %s=%q", key, got)
		}
	}
}

func TestRecoveryTransitionPreservesOperatorHoldAndRetiresStaleOwnership(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	operatorHold := now.Add(6 * time.Hour).Format(time.RFC3339)
	base := recoverySessionMemStore(map[string]string{
		"held_until": operatorHold, "recovery_hold_until": now.Add(time.Hour).Format(time.RFC3339),
		recoveryHoldOwnedMetadataKey: now.Add(time.Hour).Format(time.RFC3339),
	})
	store := NewStore(beads.SessionStore{Store: &recoveryAtomicStore{MemStore: base}})
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	snapshot, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now,
		HoldUntil: now.Add(2 * time.Hour), Outcome: "active", WorkID: "work-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata["held_until"] != operatorHold {
		t.Fatalf("operator hold = %q, want %q", persisted.Metadata["held_until"], operatorHold)
	}
	if persisted.Metadata[recoveryHoldOwnedMetadataKey] != "" {
		t.Fatalf("stale ownership survived: %+v", persisted.Metadata)
	}
	if err := store.ReleaseRecoveryResponderLease(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryOperatorClearedOwnedHoldIsNotReasserted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ownedUntil := now.Add(time.Hour).Format(time.RFC3339)
	base := recoverySessionMemStore(map[string]string{
		"held_until": ownedUntil, "recovery_hold_until": ownedUntil, recoveryHoldOwnedMetadataKey: ownedUntil,
	})
	if err := base.SetMetadata("session-1", "held_until", ""); err != nil {
		t.Fatal(err)
	}
	store := NewStore(beads.SessionStore{Store: base})
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	state := RecoveryState{HoldUntil: now.Add(2 * time.Hour), Outcome: "detected"}
	if _, err := store.RecordRecoveryStateIfCurrent(snapshot, state); err != nil {
		t.Fatal(err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata["held_until"] != "" || persisted.Metadata[recoveryHoldOwnedMetadataKey] != "" {
		t.Fatalf("operator-cleared hold was reasserted: %#v", persisted.Metadata)
	}
}

func TestRecoveryLeaseTakeoverFencesEveryOldSnapshotMutationAndRelease(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := recoverySessionMemStore(nil)
	store := NewStore(beads.SessionStore{Store: base})
	old, acquired, err := store.AcquireRecoveryResponderLease("session-1", "old-owner", now, now.Add(time.Second))
	if err != nil || !acquired {
		t.Fatalf("old acquire = %v, %v", acquired, err)
	}
	current, acquired, err := store.AcquireRecoveryResponderLease("session-1", "new-owner", now.Add(2*time.Second), now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("takeover acquire = %v, %v", acquired, err)
	}
	state := RecoveryState{IncidentID: "stale", Impairment: "quota_exceeded", DetectedAt: now, HoldUntil: now.Add(time.Hour), Outcome: "exhausted"}
	if _, err := store.RecordRecoveryStateIfCurrent(old, state); !beads.IsPreconditionFailed(err) {
		t.Fatalf("stale transition = %v, want precondition failure", err)
	}
	if err := store.ReleaseRecoveryResponderLease(old); err != nil {
		t.Fatalf("stale release = %v, want harmless fence", err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Metadata[recoveryResponderLeaseMetadataKey]; !strings.HasPrefix(got, "new-owner\n") {
		t.Fatalf("stale release cleared successor lease: %q", got)
	}
	current, err = store.RecordRecoveryStateIfCurrent(current, RecoveryState{
		IncidentID: "current", Impairment: "quota_exceeded", DetectedAt: now,
		HoldUntil: now.Add(time.Hour), Outcome: "exhausted",
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Info.RecoveryIncidentID != "current" {
		t.Fatalf("successor did not remain authoritative: %+v", current.Info)
	}
}

func TestRecoveryVerificationCleanupIsInSameFencedTransition(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	hold := now.Add(time.Hour).Format(time.RFC3339)
	base := recoverySessionMemStore(map[string]string{
		"state": "active", "session_health": "healthy", "session_health_reason": "quota_exceeded",
		"provider_terminal_error": "quota_exceeded", "provider_terminal_error_at": now.Format(time.RFC3339),
		"session_drainable": "true", "recovery_incident_id": "incident-1",
		"recovery_impairment": "quota_exceeded", "recovery_hold_until": hold,
		"held_until": hold, recoveryHoldOwnedMetadataKey: hold,
	})
	backend := &recoveryAtomicStore{MemStore: base}
	store := NewStore(beads.SessionStore{Store: backend})
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner-1", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, %v", acquired, err)
	}
	snapshot, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{
		IncidentID: "incident-1", Impairment: "quota_exceeded", DetectedAt: now, Outcome: "verified",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Info.RecoveryOutcome != "verified" || snapshot.Info.HeldUntil != "" || snapshot.Info.ProviderTerminalError != "" || snapshot.Info.Drainable || snapshot.Info.HealthReason != "" {
		t.Fatalf("verification cleanup incomplete: %+v", snapshot.Info)
	}
	if backend.updates != 2 { // acquire + one complete verification transition
		t.Fatalf("verification used %d updates, want 2", backend.updates)
	}
}

func TestRecoveryExpiredLeaseCannotMutateWithoutTakeover(t *testing.T) {
	base := recoverySessionMemStore(nil)
	store := NewStore(beads.SessionStore{Store: base})
	logicalNow := time.Now().UTC().Add(-time.Hour)
	snapshot, acquired, err := store.AcquireRecoveryResponderLease("session-1", "expired-owner", logicalNow, logicalNow.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("AcquireRecoveryResponderLease = acquired %v, err %v", acquired, err)
	}
	_, err = store.RecordRecoveryStateIfCurrent(snapshot, RecoveryState{IncidentID: "must-not-land", Outcome: "detected"})
	if !errors.Is(err, ErrRecoveryResponderLeaseLost) {
		t.Fatalf("expired transition error = %v, want lease lost", err)
	}
	persisted, err := base.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata["recovery_incident_id"] != "" {
		t.Fatalf("expired lease mutated row: %#v", persisted.Metadata)
	}
}

func TestRecoveryLeaseRenewalAdvancesFenceBeforeExternalMutation(t *testing.T) {
	base := recoverySessionMemStore(nil)
	store := NewStore(beads.SessionStore{Store: base})
	now := time.Now().UTC()
	old, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner", now, now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("AcquireRecoveryResponderLease = acquired %v, err %v", acquired, err)
	}
	current, err := store.RenewRecoveryResponderLease(old, now.Add(time.Second), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordRecoveryStateIfCurrent(old, RecoveryState{IncidentID: "stale"}); !beads.IsPreconditionFailed(err) {
		t.Fatalf("pre-renewal snapshot error = %v, want precondition failure", err)
	}
	if _, err := store.RecordRecoveryStateIfCurrent(current, RecoveryState{IncidentID: "current", Outcome: "planned"}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryLeaseRejectsClosedSessionBeforeCAS(t *testing.T) {
	base := recoverySessionMemStore(nil)
	status := "closed"
	if err := base.Update("session-1", beads.UpdateOpts{Status: &status}); err != nil {
		t.Fatal(err)
	}
	backend := &recoveryAtomicStore{MemStore: base}
	store := NewStore(beads.SessionStore{Store: backend})
	now := time.Now().UTC()
	_, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner", now, now.Add(time.Minute))
	if err != nil || acquired {
		t.Fatalf("closed acquire = %v, %v; want clean rejection", acquired, err)
	}
	if backend.updates != 0 {
		t.Fatalf("closed session received %d lease CAS updates", backend.updates)
	}
}

type closeAfterRecoveryLeaseStore struct {
	*beads.MemStore
	updates int
}

func (s *closeAfterRecoveryLeaseStore) UpdateIfMatch(id string, revision int64, opts beads.UpdateOpts) error {
	s.updates++
	if err := s.MemStore.UpdateIfMatch(id, revision, opts); err != nil {
		return err
	}
	if opts.Metadata[recoveryResponderLeaseMetadataKey] != "" {
		status := "closed"
		return s.Update(id, beads.UpdateOpts{Status: &status})
	}
	return nil
}

func TestRecoveryLeaseRejectsSessionClosedBeforeAuthoritativeReadback(t *testing.T) {
	backend := &closeAfterRecoveryLeaseStore{MemStore: recoverySessionMemStore(nil)}
	store := NewStore(beads.SessionStore{Store: backend})
	now := time.Now().UTC()
	_, acquired, err := store.AcquireRecoveryResponderLease("session-1", "owner", now, now.Add(time.Minute))
	if err != nil || acquired {
		t.Fatalf("post-CAS closed acquire = %v, %v; want clean rejection", acquired, err)
	}
	persisted, err := backend.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "closed" || backend.updates != 1 {
		t.Fatalf("authoritative row = %+v, updates=%d", persisted, backend.updates)
	}
}

func TestRecoveryLeaseRequiresConditionalWriter(t *testing.T) {
	base := recoverySessionMemStore(nil)
	base.DisableConditionalWrites = true
	store := NewStore(beads.SessionStore{Store: base})
	now := time.Now().UTC().Truncate(time.Second)
	_, _, err := store.AcquireRecoveryResponderLease("session-1", "owner", now, now.Add(time.Minute))
	if !errors.Is(err, beads.ErrConditionalWriteUnsupported) {
		t.Fatalf("acquire = %v, want unsupported", err)
	}
}

func recoverySessionMemStore(extra map[string]string) *beads.MemStore {
	metadata := map[string]string{"state": "asleep"}
	for key, value := range extra {
		metadata[key] = value
	}
	return beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: "session-1", Type: BeadType, Status: "open", Labels: []string{LabelSession}, Metadata: metadata,
	}}, nil)
}

func assertRecoveryHoldPair(t *testing.T, store beads.Store, want string) {
	t.Helper()
	persisted, err := store.Get("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata["held_until"] != want || persisted.Metadata["recovery_hold_until"] != want || persisted.Metadata[recoveryHoldOwnedMetadataKey] != want {
		t.Fatalf("hold pair = held %q recovery %q owner %q, want %q", persisted.Metadata["held_until"], persisted.Metadata["recovery_hold_until"], persisted.Metadata[recoveryHoldOwnedMetadataKey], want)
	}
}
