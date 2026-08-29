package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// GuardedStaleCloseRequest binds an operator-authorized stale-session close to
// an exact persisted snapshot plus a fresh runtime observation.
type GuardedStaleCloseRequest struct {
	ExpectedRevision  int64
	ExpectedState     State
	ExpectedHeldUntil string
	Reason            string
	RuntimeRunning    bool
	RuntimeAlive      bool
	Now               time.Time
}

// GuardedStaleCloseFacts is the pure decision input gathered by the command
// adapter and session store.
type GuardedStaleCloseFacts struct {
	Now               time.Time
	Revision          int64
	Status            string
	State             State
	HeldUntil         string
	Closed            bool
	SessionKey        string
	PendingCreate     string
	RuntimeRunning    bool
	RuntimeAlive      bool
	ExpectedRevision  int64
	ExpectedState     State
	ExpectedHeldUntil string
	Reason            string
}

// DecideGuardedStaleClose rejects any drift from the exact stale snapshot. The
// backend revision fence rechecks the row atomically at mutation time; this
// decision keeps operator intent and error vocabulary session-owned.
func DecideGuardedStaleClose(f GuardedStaleCloseFacts) error {
	if f.ExpectedRevision == 0 {
		return errors.New("guarded stale close requires a non-zero expected revision")
	}
	if f.Revision != f.ExpectedRevision {
		return fmt.Errorf("guarded stale close revision changed: got %d want %d", f.Revision, f.ExpectedRevision)
	}
	if f.Status != "open" || f.Closed {
		return fmt.Errorf("guarded stale close status changed: got %q want %q", f.Status, "open")
	}
	if f.ExpectedState == "" || f.State != f.ExpectedState {
		return fmt.Errorf("guarded stale close state changed: got %q want %q", f.State, f.ExpectedState)
	}
	if f.ExpectedHeldUntil == "" || f.HeldUntil != f.ExpectedHeldUntil {
		return fmt.Errorf("guarded stale close hold changed: got %q want %q", f.HeldUntil, f.ExpectedHeldUntil)
	}
	heldUntil, err := time.Parse(time.RFC3339, f.HeldUntil)
	if err != nil {
		return fmt.Errorf("guarded stale close hold is not RFC3339: %w", err)
	}
	if f.Now.IsZero() || !heldUntil.After(f.Now.UTC()) {
		return errors.New("guarded stale close hold is not in the future")
	}
	if strings.TrimSpace(f.SessionKey) != "" {
		return errors.New("guarded stale close target has a session key")
	}
	if strings.TrimSpace(f.PendingCreate) != "" {
		return errors.New("guarded stale close target has a pending create claim")
	}
	if f.RuntimeRunning || f.RuntimeAlive {
		return errors.New("guarded stale close target has a live runtime")
	}
	if strings.TrimSpace(f.Reason) == "" {
		return errors.New("guarded stale close requires an operator reason")
	}
	return nil
}

// CheckGuardedStaleSession validates the exact stale snapshot and confirms the
// routed store has an atomic close capability without writing.
func (s *Store) CheckGuardedStaleSession(id string, req GuardedStaleCloseRequest) error {
	_, err := s.guardedStaleCloseWriter(id, req)
	return err
}

// CloseGuardedStaleSession applies an exact revision-fenced terminal close with
// cleanup metadata in one backend transaction. It never falls back to an
// unconditional write.
func (s *Store) CloseGuardedStaleSession(id string, req GuardedStaleCloseRequest) error {
	writer, err := s.guardedStaleCloseWriter(id, req)
	if err != nil {
		return err
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	metadata := map[string]string(ClosePatch(now, "operator-stale"))
	for _, key := range []string{
		"held_until", "quarantined_until", "wait_hold", "sleep_intent", "sleep_reason",
		"wake_request", "wake_requested_at", "pending_create_claim", "pending_create_started_at",
		"session_key",
	} {
		metadata[key] = ""
	}
	metadata["state_reason"] = "operator-stale"
	metadata["operator_close_reason"] = strings.TrimSpace(req.Reason)
	_, err = writer.CloseWithMetadataIfMatch(id, req.ExpectedRevision, metadata)
	return err
}

func (s *Store) guardedStaleCloseWriter(id string, req GuardedStaleCloseRequest) (beads.AtomicConditionalCloser, error) {
	if s == nil || !s.Backed() {
		return nil, errors.New("guarded stale close session store unavailable")
	}
	bead, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !IsSessionBeadOrRepairable(bead) {
		return nil, fmt.Errorf("guarded stale close target %q is not a session bead", id)
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	facts := GuardedStaleCloseFacts{
		Now:               now,
		Revision:          bead.Revision,
		Status:            bead.Status,
		State:             State(bead.Metadata["state"]),
		HeldUntil:         bead.Metadata["held_until"],
		Closed:            bead.Status == "closed",
		SessionKey:        bead.Metadata["session_key"],
		PendingCreate:     bead.Metadata["pending_create_claim"],
		RuntimeRunning:    req.RuntimeRunning,
		RuntimeAlive:      req.RuntimeAlive,
		ExpectedRevision:  req.ExpectedRevision,
		ExpectedState:     req.ExpectedState,
		ExpectedHeldUntil: req.ExpectedHeldUntil,
		Reason:            req.Reason,
	}
	if err := DecideGuardedStaleClose(facts); err != nil {
		return nil, err
	}
	writer, ok := beads.AtomicConditionalCloserFor(s.store.Store)
	if !ok {
		return nil, beads.ErrConditionalWriteUnsupported
	}
	return writer, nil
}
