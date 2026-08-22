package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// ClearCityStopRequest binds a city-stop latch clear to an exact future hold and
// fresh no-runtime observation. The value-CAS itself fences the only mutated key.
type ClearCityStopRequest struct {
	ExpectedHeldUntil string
	RuntimeRunning    bool
	RuntimeAlive      bool
	Now               time.Time
}

// CheckClearCityStop validates every read-side precondition and confirms the
// routed session store exposes metadata value-CAS, without writing.
func (s *Store) CheckClearCityStop(id string, req ClearCityStopRequest) error {
	_, err := s.cityStopCASWriter(id, req)
	return err
}

// ClearCityStop atomically swaps sleep_reason from city-stop to empty. It never
// falls back to an unconditional update.
func (s *Store) ClearCityStop(id string, req ClearCityStopRequest) error {
	writer, err := s.cityStopCASWriter(id, req)
	if err != nil {
		return err
	}
	swapped, err := writer.CompareAndSetMetadataKey(id, "sleep_reason", string(SleepReasonCityStop), "")
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("clear city-stop lost metadata precondition")
	}
	return nil
}

func (s *Store) cityStopCASWriter(id string, req ClearCityStopRequest) (beads.MetadataCASWriter, error) {
	if s == nil || !s.Backed() {
		return nil, errors.New("clear city-stop session store unavailable")
	}
	bead, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !IsSessionBeadOrRepairable(bead) {
		return nil, fmt.Errorf("clear city-stop target %q is not a session bead", id)
	}
	if bead.Status != "open" {
		return nil, fmt.Errorf("clear city-stop status = %q, want open", bead.Status)
	}
	state := State(strings.TrimSpace(bead.Metadata["state"]))
	if state != StateAsleep && state != StateSuspended {
		return nil, fmt.Errorf("clear city-stop state = %q, want asleep or suspended", state)
	}
	if strings.TrimSpace(bead.Metadata["sleep_reason"]) != string(SleepReasonCityStop) {
		return nil, fmt.Errorf("clear city-stop sleep_reason = %q, want city-stop", bead.Metadata["sleep_reason"])
	}
	if strings.TrimSpace(bead.Metadata["session_key"]) != "" {
		return nil, errors.New("clear city-stop target has a session key")
	}
	if strings.TrimSpace(bead.Metadata["pending_create_claim"]) != "" {
		return nil, errors.New("clear city-stop target has a pending create claim")
	}
	if req.RuntimeRunning || req.RuntimeAlive {
		return nil, errors.New("clear city-stop target has a live runtime")
	}
	held := strings.TrimSpace(bead.Metadata["held_until"])
	if req.ExpectedHeldUntil == "" || held != strings.TrimSpace(req.ExpectedHeldUntil) {
		return nil, fmt.Errorf("clear city-stop hold = %q, want %q", held, req.ExpectedHeldUntil)
	}
	heldUntil, err := time.Parse(time.RFC3339, held)
	if err != nil {
		return nil, fmt.Errorf("clear city-stop hold is not RFC3339: %w", err)
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !heldUntil.After(now) {
		return nil, errors.New("clear city-stop hold is not in the future")
	}
	writer, ok := beads.MetadataCASWriterFor(s.store.Store)
	if !ok {
		return nil, beads.ErrConditionalWriteUnsupported
	}
	return writer, nil
}
