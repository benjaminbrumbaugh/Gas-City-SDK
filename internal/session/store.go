package session

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// This file extends the session-class domain wrapper (Store) with the
// WRITE half of the front door per OBJECT-MODEL-FRONT-DOOR-DESIGN sec 3.1. The
// read half (Get / List, projecting beads.Bead -> session.Info via
// InfoFromPersistedBead) already lives in info_store.go. Together they form the
// single typed seam over a session-class bead store: callers speak session.Info
// / session.State / session.MetadataPatch, and beads.Bead / SetMetadataBatch /
// Update / Close are confined inside the impl.
//
// PHASE 0 STATUS: these write methods are the skeleton front door. Their
// SIGNATURES are the contract Phase 4 routes call sites through; the bodies
// already emit byte-identical bead writes to the raw ops they replace
// (ApplyPatch == setMetaBatch == store.SetMetadataBatch with empty-skip), so a
// recording-fake store can prove parity now. No production caller is routed
// through them yet — that is Phase 4/5.

// ApplyPatch applies a MetadataPatch to the session bead identified by id. It is
// the single write chokepoint for session metadata transitions: every typed
// write method below funnels through it, and it is the byte-identical
// replacement for setMetaBatch(store, id, patch) (cmd/gc/session_beads.go) and
// the ~20 reconciler SetMetadataBatch(session.ID, patch) sites.
//
// An empty patch is a no-op (matching setMetaBatch). Empty-string values in the
// patch are written verbatim; the cross-backend contract that an empty-string
// metadata value reads back as empty (observationally "cleared") is pinned by
// TestMetadataEmptyStringClearContract.
func (s *Store) ApplyPatch(id string, patch MetadataPatch) error {
	if len(patch) == 0 {
		return nil
	}
	// Return the bare store error: this method confines the write codec, it does
	// not re-message failures. Callers (the reconciler, setMetaBatch, the circuit
	// breaker) log/wrap the error themselves, and several tests assert their exact
	// diagnostic text — wrapping here would change that caller-visible text and
	// break runtime fidelity.
	return s.store.SetMetadataBatch(id, map[string]string(patch))
}

// ApplyPatchInfo persists patch for info.ID (via ApplyPatch) and returns the
// refreshed Info as a LOCAL fold — info.ApplyPatch(patch) — never a re-Get. It
// is the write-returns-Info chokepoint the reconciler routes its direct
// write+fold two-steps through: a store Get per patch would blow the tick budget
// under Dolt (~2s/bd-op; the reconciler does ~57-61 patch writes per tick), and
// the coherent caller-held Info already carries the pre-image the fold needs, so
// no read is required.
//
// An empty patch is a no-op: it returns info unchanged with no write (matching
// ApplyPatch's len==0 short-circuit). On a persist error the INPUT info is
// returned UNCHANGED with the error — the snapshot never advances past a write
// the store rejected, so an error-ignoring caller stays consistent with the
// store and an error-checking caller can bail.
//
// The fold is byte-identical to re-projecting the patched bead
// (TestInfoApplyPatchMatchesReprojection is the equivalence oracle). It cannot
// express a status close: patches never flip Info.Closed (see info_apply_patch.go),
// so in-memory closes fold via MarkClosed instead, and the one NDI witness close
// (finalizeDrainAckStoppedSession) is the single documented Store.Get refresh.
// The handle-only ApplyPatch(id, patch) form remains for callers that hold no
// coherent Info snapshot.
func (s *Store) ApplyPatchInfo(info Info, patch MetadataPatch) (Info, error) {
	if len(patch) == 0 {
		return info, nil
	}
	if err := s.ApplyPatch(info.ID, patch); err != nil {
		return info, err
	}
	return info.ApplyPatch(patch), nil
}

// UpdateMetadataInfo persists patch for info.ID via a SINGLE
// Store.Update(id, UpdateOpts{Metadata: patch}) and folds the patch onto Info on
// success. It is the write-returns-Info chokepoint for provenance clusters that
// must commit ALL-OR-NOTHING across every supported backend.
//
// One-operation contract (why this is NOT ApplyPatchInfo): ApplyPatch routes
// through SetMetadataBatch, which some backends decompose into one op PER KEY
// (the exec: store issues one `bd` subprocess per map key, in nondeterministic
// order), so a failure on the Nth key leaves an arbitrary subset of the cluster
// committed — a mixed identity/provenance row. A single Update carries the whole
// metadata map in one backend operation: exec: emits one JSON --set-metadata
// subprocess, native Dolt keeps its read/merge/write transaction isolation, and
// the caching/DoltLite stores keep their existing single-write refresh path. The
// trigger/provenance cluster (trigger id, store ref, brain parent, pack,
// workspace, workdir) therefore commits atomically or not at all.
//
// An empty patch is a no-op: it returns info unchanged with no write. On a
// persist error the INPUT info is returned UNCHANGED with the error, so a caller
// that logs-and-continues keeps its pre-write in-memory Info (never a partially
// applied fold) and the durable row is left exactly as the backend left it. On
// success the fold is info.ApplyPatch(patch) — byte-identical to re-projecting
// the patched bead, exactly as ApplyPatchInfo folds.
func (s *Store) UpdateMetadataInfo(info Info, patch MetadataPatch) (Info, error) {
	if len(patch) == 0 {
		return info, nil
	}
	if err := s.store.Update(info.ID, beads.UpdateOpts{Metadata: map[string]string(patch)}); err != nil {
		return info, err
	}
	return info.ApplyPatch(patch), nil
}

// SetState heals a session to the given lifecycle state with a state_reason.
// It replaces the canonical state-heal SetMetadataBatch(id, {state, state_reason})
// in session_reconcile.go (healState / healStateWithRollback).
func (s *Store) SetState(id string, state State, reason string) error {
	return s.ApplyPatch(id, MetadataPatch{
		"state":        string(state),
		"state_reason": reason,
	})
}

// Sleep records a non-terminal sleep/drain result via SleepPatch. It replaces
// the max-age and idle-timeout sleep writes in session_reconciler.go.
func (s *Store) Sleep(id, reason string, now time.Time) error {
	return s.ApplyPatch(id, SleepPatch(now, reason))
}

// BeginDrainAckStopPending moves a drain-acked session into durable
// stop-pending state via DrainAckStopPendingPatch. Replaces markDrainAckStopPending.
func (s *Store) BeginDrainAckStopPending(id string, now time.Time) error {
	return s.ApplyPatch(id, DrainAckStopPendingPatch(now))
}

// RequestRestart records a controller handoff to a fresh provider conversation
// via RestartRequestPatch. Replaces the restart-request write in session_reconciler.go.
func (s *Store) RequestRestart(id, sessionKey string, now time.Time) error {
	return s.ApplyPatch(id, RestartRequestPatch(sessionKey, now))
}

const (
	recoveryHoldOwnedMetadataKey      = "recovery_hold_owned"
	recoveryResponderLeaseMetadataKey = "recovery_responder_lease"
)

// ErrRecoveryResponderLeaseLost reports a recovery transition fenced by a
// newer row revision or lease owner.
var ErrRecoveryResponderLeaseLost = errors.New("recovery responder lease lost")

// RecoverySnapshot couples a session projection to the exact persisted revision
// and recovery-responder lease that authorized it. Its fencing fields are opaque
// outside this package; callers can only advance or release the lease by passing
// the whole snapshot back.
type RecoverySnapshot struct {
	Info         Info
	revision     int64
	leaseToken   string
	leaseExpires time.Time
	metadata     beads.StringMap
}

// AcquireRecoveryResponderLease claims one source-session row with a full-row
// revision CAS. The returned snapshot is the only authority accepted by recovery
// transitions. Lease acquisition itself advances the row revision, so a
// successor takeover fences every snapshot held by the previous owner.
func (s *Store) AcquireRecoveryResponderLease(id, owner string, now, expires time.Time) (RecoverySnapshot, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || !expires.After(now) {
		return RecoverySnapshot{}, false, fmt.Errorf("invalid recovery responder lease")
	}
	writer, ok := beads.ConditionalWriterFor(s.store.Store)
	if !ok {
		return RecoverySnapshot{}, false, beads.ErrConditionalWriteUnsupported
	}
	persisted, err := s.validatedBead(id)
	if err != nil {
		return RecoverySnapshot{}, false, err
	}
	if recoverySessionClosed(persisted) {
		return RecoverySnapshot{}, false, nil
	}
	current := strings.TrimSpace(persisted.Metadata[recoveryResponderLeaseMetadataKey])
	if recoveryLeaseCurrent(current, now) {
		return RecoverySnapshot{}, false, nil
	}
	token := owner + "\n" + expires.UTC().Format(time.RFC3339Nano)
	patch := map[string]string{recoveryResponderLeaseMetadataKey: token}
	if err := writer.UpdateIfMatch(id, persisted.Revision, beads.UpdateOpts{Metadata: patch}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return RecoverySnapshot{}, false, nil
		}
		// A transport failure can be ambiguous. Adopt only an exact token readback;
		// any other value is unauthorized and must fail closed.
		if snapshot, readErr := s.recoverySnapshotForLease(id, token); readErr == nil {
			return snapshot, true, nil
		}
		return RecoverySnapshot{}, false, err
	}
	snapshot, err := s.recoverySnapshotForLease(id, token)
	if err != nil {
		if errors.Is(err, ErrRecoveryResponderLeaseLost) {
			return RecoverySnapshot{}, false, nil
		}
		return RecoverySnapshot{}, false, err
	}
	return snapshot, true, nil
}

func recoveryLeaseCurrent(token string, now time.Time) bool {
	_, until, ok := recoveryLeaseParts(token)
	return ok && until.After(now)
}

func recoveryLeaseParts(token string) (string, time.Time, bool) {
	parts := strings.SplitN(strings.TrimSpace(token), "\n", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", time.Time{}, false
	}
	until, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return "", time.Time{}, false
	}
	return strings.TrimSpace(parts[0]), until.UTC(), true
}

func recoverySessionClosed(persisted beads.Bead) bool {
	return strings.EqualFold(strings.TrimSpace(persisted.Status), "closed")
}

func (s *Store) recoverySnapshotForLease(id, token string) (RecoverySnapshot, error) {
	persisted, err := s.validatedBead(id)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if recoverySessionClosed(persisted) {
		return RecoverySnapshot{}, ErrRecoveryResponderLeaseLost
	}
	if token == "" || persisted.Metadata[recoveryResponderLeaseMetadataKey] != token {
		return RecoverySnapshot{}, ErrRecoveryResponderLeaseLost
	}
	_, leaseExpires, ok := recoveryLeaseParts(token)
	if !ok {
		return RecoverySnapshot{}, ErrRecoveryResponderLeaseLost
	}
	return RecoverySnapshot{
		Info:         InfoFromPersistedBead(persisted),
		revision:     persisted.Revision,
		leaseToken:   token,
		leaseExpires: leaseExpires,
		metadata:     maps.Clone(persisted.Metadata),
	}, nil
}

// RenewRecoveryResponderLease proves that snapshot is still the current,
// unexpired lease generation immediately before an external side effect. The
// renewal itself advances the revision, fencing the caller if a successor or
// unrelated writer won first.
func (s *Store) RenewRecoveryResponderLease(snapshot RecoverySnapshot, now, expires time.Time) (RecoverySnapshot, error) {
	owner, _, ok := recoveryLeaseParts(snapshot.leaseToken)
	if !ok || snapshot.Info.ID == "" || !now.Before(snapshot.leaseExpires) || !expires.After(now) ||
		snapshot.metadata[recoveryResponderLeaseMetadataKey] != snapshot.leaseToken {
		return RecoverySnapshot{}, ErrRecoveryResponderLeaseLost
	}
	writer, ok := beads.ConditionalWriterFor(s.store.Store)
	if !ok {
		return RecoverySnapshot{}, beads.ErrConditionalWriteUnsupported
	}
	nextToken := owner + "\n" + expires.UTC().Format(time.RFC3339Nano)
	if err := writer.UpdateIfMatch(snapshot.Info.ID, snapshot.revision, beads.UpdateOpts{
		Metadata: map[string]string{recoveryResponderLeaseMetadataKey: nextToken},
	}); err != nil {
		return RecoverySnapshot{}, err
	}
	return s.recoverySnapshotForLease(snapshot.Info.ID, nextToken)
}

// RecordRecoveryStateIfCurrent commits the complete recovery lifecycle, generic
// hold, hold-ownership marker, and any verification cleanup in one revision-CAS.
// No split metadata CAS is permitted: unsupported stores fail closed.
func (s *Store) RecordRecoveryStateIfCurrent(snapshot RecoverySnapshot, state RecoveryState) (RecoverySnapshot, error) {
	if snapshot.Info.ID == "" || snapshot.leaseToken == "" ||
		snapshot.metadata[recoveryResponderLeaseMetadataKey] != snapshot.leaseToken ||
		!time.Now().UTC().Before(snapshot.leaseExpires) {
		return RecoverySnapshot{}, ErrRecoveryResponderLeaseLost
	}
	writer, ok := beads.ConditionalWriterFor(s.store.Store)
	if !ok {
		return RecoverySnapshot{}, beads.ErrConditionalWriteUnsupported
	}
	patch := MetadataPatch{
		"recovery_incident_id":       state.IncidentID,
		"recovery_impairment":        state.Impairment,
		"recovery_detected_at":       formatRecoveryTime(state.DetectedAt),
		"recovery_hold_until":        formatRecoveryTime(state.HoldUntil),
		"recovery_attempt":           strconv.Itoa(state.Attempt),
		"recovery_attempted_targets": strings.Join(state.AttemptedTargets, "\n"),
		"recovery_cooldown_until":    formatRecoveryTime(state.CooldownUntil),
		"recovery_outcome":           state.Outcome,
		"recovery_work_id":           state.WorkID,
	}
	fresh := snapshot.Info
	if strings.TrimSpace(state.Outcome) == "verified" {
		if strings.TrimSpace(fresh.ProviderTerminalError) == strings.TrimSpace(state.Impairment) {
			patch["provider_terminal_error"] = ""
			patch["provider_terminal_error_at"] = ""
			patch["session_drainable"] = ""
		}
		if strings.TrimSpace(fresh.HealthReason) == strings.TrimSpace(state.Impairment) {
			patch["session_health_reason"] = ""
		}
	}

	previousRecoveryHold := strings.TrimSpace(fresh.RecoveryHoldUntil)
	currentHold := strings.TrimSpace(fresh.HeldUntil)
	nextRecoveryHold := strings.TrimSpace(patch["recovery_hold_until"])
	ownership := strings.TrimSpace(snapshot.metadata[recoveryHoldOwnedMetadataKey])
	owned := previousRecoveryHold != "" && currentHold == previousRecoveryHold &&
		(ownership == previousRecoveryHold || ownership == "true")

	switch {
	case owned && nextRecoveryHold == "":
		patch["held_until"] = ""
		patch[recoveryHoldOwnedMetadataKey] = ""
	case owned && nextRecoveryHold != currentHold:
		patch["held_until"] = nextRecoveryHold
		patch[recoveryHoldOwnedMetadataKey] = nextRecoveryHold
	case owned:
		// Migrate the legacy boolean marker while preserving the paired deadline.
		patch["held_until"] = currentHold
		patch[recoveryHoldOwnedMetadataKey] = currentHold
	case currentHold == "" && nextRecoveryHold != "" && previousRecoveryHold == "":
		patch["held_until"] = nextRecoveryHold
		patch[recoveryHoldOwnedMetadataKey] = nextRecoveryHold
	case currentHold == "" && previousRecoveryHold != "" && ownership != "":
		// An empty generic hold with a prior paired recovery hold is an operator
		// override. Never recreate what the operator cleared; retire only our
		// stale ownership marker in this atomic transition.
		patch[recoveryHoldOwnedMetadataKey] = ""
	case ownership != "":
		// A marker that no longer names the current generic hold is stale. Retire
		// only the marker in the same transition; the operator's hold survives.
		patch[recoveryHoldOwnedMetadataKey] = ""
	}

	if err := writer.UpdateIfMatch(snapshot.Info.ID, snapshot.revision, beads.UpdateOpts{Metadata: map[string]string(patch)}); err != nil {
		return RecoverySnapshot{}, err
	}
	return s.recoverySnapshotForLease(snapshot.Info.ID, snapshot.leaseToken)
}

// ReleaseRecoveryResponderLease clears only the exact lease generation and row
// revision represented by snapshot. A stale release is harmless: its successor
// has already changed the revision and remains authoritative.
func (s *Store) ReleaseRecoveryResponderLease(snapshot RecoverySnapshot) error {
	if snapshot.Info.ID == "" || snapshot.leaseToken == "" {
		return nil
	}
	writer, ok := beads.ConditionalWriterFor(s.store.Store)
	if !ok {
		return beads.ErrConditionalWriteUnsupported
	}
	if err := writer.UpdateIfMatch(snapshot.Info.ID, snapshot.revision, beads.UpdateOpts{
		Metadata: map[string]string{recoveryResponderLeaseMetadataKey: ""},
	}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return nil
		}
		return err
	}
	return nil
}

// RecoveryState is the durable lifecycle owned by the recovery responder.
type RecoveryState struct {
	IncidentID       string
	Impairment       string
	DetectedAt       time.Time
	HoldUntil        time.Time
	Attempt          int
	AttemptedTargets []string
	CooldownUntil    time.Time
	Outcome          string
	WorkID           string
}

func formatRecoveryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// ResetConfigDrift records an in-place named-session repair after core config
// drift via ConfigDriftResetPatch. Replaces the config-drift reset writes in
// session_reconciler.go and soft_reload.go.
func (s *Store) ResetConfigDrift(id string, next State, sessionKey string, now time.Time) error {
	return s.ApplyPatch(id, ConfigDriftResetPatch(next, sessionKey, now))
}

// SetWaitHold sets or clears the wait-hold + sleep-intent markers. Replaces the
// SetMetadataBatch(sessionID, {wait_hold, sleep_intent}) writes in cmd_wait.go.
// When on is false both keys are cleared (empty-string write).
func (s *Store) SetWaitHold(id string, on bool, reason string) error {
	if on {
		return s.ApplyPatch(id, MetadataPatch{
			"wait_hold":    reason,
			"sleep_intent": reason,
		})
	}
	return s.ApplyPatch(id, MetadataPatch{
		"wait_hold":    "",
		"sleep_intent": "",
	})
}

// setMetadataValue is the single-key write chokepoint. It is the byte-identical
// replacement for the raw store.SetMetadata(id, key, value) sites that write a
// single session-attribute key. Unlike ApplyPatch (which emits SetMetadataBatch),
// this emits SetMetadata so the bead op is identical to the raw single-key write
// it replaces.
func (s *Store) setMetadataValue(id, key, value string) error {
	// Bare store error — callers own their diagnostic text (see ApplyPatch).
	return s.store.SetMetadata(id, key, value)
}

// SetMarker writes a single session-attribute marker key. It is the front door
// for the raw store.SetMetadata(session.ID, key, value) sites: the stranded
// throttle marker (session_reconciler.go), the sleep_intent clear, and the
// city-stop sleep_reason (cmd_stop.go). It emits a single SetMetadata op,
// byte-identical to the raw write. An empty value clears the key per the
// empty-string-clear contract.
func (s *Store) SetMarker(id, key, value string) error {
	return s.setMetadataValue(id, key, value)
}

// RecordCurrentBead stamps the work bead a session is currently processing.
// Replaces recordCurrentBeadIDOnWake (session_bead_cycle.go), which uses a
// single-key SetMetadata write — so this emits SetMetadata, not a batch.
func (s *Store) RecordCurrentBead(id, beadID string) error {
	return s.setMetadataValue(id, CurrentBeadIDKey, beadID)
}

// SetCurrentClaim stamps the work bead this session claimed for itself through
// `gc hook --claim` (beadmeta.CurrentClaimBeadIDMetadataKey) — or clears the
// stamp when beadID is empty. It reports whether a write was actually issued.
//
// It is deliberately a different key from RecordCurrentBead's: that one records
// a controller-side assignment the reconciler made at wake time, this one
// records a claim the session made for itself, and a shared key would let the
// two lanes overwrite each other. Callers must clear it on every path that takes
// the work back off the session — a stale stamp names a bead the session no
// longer owns.
//
// The read is validatedBead, so the id is resolved EXACTLY (the bead store's
// Get surfaces a prefix collision as beads.ErrIDCollision) and a non-session
// bead is refused before any write: bd's fuzzy id resolver would otherwise let a
// post-claim update land on a prefix-colliding session bead, which is why the
// claim path historically refused to decorate the session bead at all
// (cmd/gc/cmd_hook_claim.go publishHookClaimRunMap). The write targets the
// canonical bead id the read returned, never the caller's raw identifier.
//
// The current value is compared first and an unchanged value writes nothing: the
// claim path re-runs on every hook tick through its adoption branches, so an
// unconditional write would emit one bead.updated per tick per in-progress bead.
// It emits a single-key SetMetadata, not a batch, matching RecordCurrentBead.
func (s *Store) SetCurrentClaim(id, beadID string) (bool, error) {
	b, err := s.validatedBead(id)
	if err != nil {
		return false, err
	}
	beadID = strings.TrimSpace(beadID)
	if strings.TrimSpace(b.Metadata[beadmeta.CurrentClaimBeadIDMetadataKey]) == beadID {
		return false, nil
	}
	if err := s.setMetadataValue(b.ID, beadmeta.CurrentClaimBeadIDMetadataKey, beadID); err != nil {
		return false, err
	}
	return true, nil
}

// CurrentClaimBeadID returns the id of the work bead this session most recently
// claimed through `gc hook --claim` ("" when unset). It is the read half of
// SetCurrentClaim and the front door for `gc hook current`.
//
// It shares Get's validation and error contract (both route through
// validatedBead): a present-but-non-session bead is ErrSessionNotFound and an
// absent id is the wrapped store not-found error, so a caller can tell "this is
// not my session" from "nothing is claimed".
func (s *Store) CurrentClaimBeadID(id string) (string, error) {
	b, err := s.validatedBead(id)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(b.Metadata[beadmeta.CurrentClaimBeadIDMetadataKey]), nil
}

// CloseWithoutReason closes the session bead identified by id without stamping
// terminal close metadata. It is the front door for the raw store.Close(id)
// call in closeBead, which stamps ClosePatch via setMetaBatch separately and
// then closes the bead. It emits a single Close op, byte-identical to the raw
// write.
func (s *Store) CloseWithoutReason(id string) error {
	// Bare store error — callers own their diagnostic text (see ApplyPatch).
	return s.store.Close(id)
}

// Backed reports whether this front door wraps a usable (non-nil) underlying
// store. It is the typed probe for the `sessFront == nil || sessFront.Store().Store == nil`
// guard at the controller/CLI roots: a front door constructed over a nil store
// (the documented typed-nil pattern, where construction yields a real nil
// *Store when the store is nil) reports false, and so does a nil receiver.
// Callers use `if !sessFront.Backed() { return }` instead of reaching for the
// raw embedded store to nil-check it.
func (s *Store) Backed() bool {
	return s != nil && s.store.Store != nil
}

// CircuitResetGeneration returns the persisted session-circuit-breaker reset
// generation metadata value for id, verbatim (the raw string; "" when unset).
//
// It is the front door for the raw store.Get(sessionID) + read
// .Metadata[SessionCircuitResetGenerationMetadataKey] pattern in
// loadPersistedSessionCircuitResetGeneration (cmd/gc/session_circuit_breaker.go).
// The bead read and the metadata-key access are confined here; the caller still
// owns parsing the value and observing it into the breaker, and owns its own
// diagnostic wrapping (the error is returned bare — see ApplyPatch). It does NOT
// validate the bead as a session bead: the raw read it replaces did not either,
// so a non-session bead carrying the key reads back identically.
func (s *Store) CircuitResetGeneration(id string) (string, error) {
	b, err := s.store.Get(id)
	if err != nil {
		return "", err
	}
	return b.Metadata[SessionCircuitResetGenerationMetadataKey], nil
}

// PersistedMarkers is a narrow typed view of the persisted session-attribute
// markers the wait paths read off a session bead: the bead Title (used to build
// the wait bead title), the tmux session_name, the continuation_epoch (stamped
// onto wait beads as registered_epoch), and the sleep_reason (consulted when
// clearing a wait-hold). It carries the raw bead fields verbatim.
type PersistedMarkers struct {
	Title             string
	SessionName       string
	ContinuationEpoch string
	SleepReason       string
}

// PersistedMarkers returns the persisted Title / session_name /
// continuation_epoch / sleep_reason markers for id, verbatim (each "" when
// unset).
//
// It is the front door for the raw store.Get(sessionID) + read .Title/.Metadata[...]
// pattern in the wait registration (cmd_wait.go session-wait creation), the
// closed-wait retry path, and the wait-hold clear path. The bead read and the
// field access are confined here; the caller still owns observing the values and
// its own diagnostic wrapping (the error is returned bare — see ApplyPatch).
// Like CircuitResetGeneration, it does NOT validate the bead as a session bead:
// the raw reads it replaces did not either.
func (s *Store) PersistedMarkers(id string) (PersistedMarkers, error) {
	b, err := s.store.Get(id)
	if err != nil {
		return PersistedMarkers{}, err
	}
	return PersistedMarkers{
		Title:             b.Title,
		SessionName:       b.Metadata["session_name"],
		ContinuationEpoch: b.Metadata["continuation_epoch"],
		SleepReason:       b.Metadata["sleep_reason"],
	}, nil
}

// GetState returns the persisted lifecycle state for id and whether the bead is
// closed. It replaces the Get(id) + read .Status/.Metadata["state"] pattern at
// the reconciler / session_beads close-path sites. Returns ErrSessionNotFound
// when no session bead exists.
func (s *Store) GetState(id string) (state State, closed bool, err error) {
	info, err := s.Get(id)
	if err != nil {
		return "", false, err
	}
	return info.State, info.Closed, nil
}

// Close closes the session bead with terminal close metadata via ClosePatch,
// then sets status closed. It is the front door for closeBead /
// closeFailedCreateBead. stateCode is the canonical short state code recorded
// before close; ClosePatch expands it to a validator-safe close_reason.
//
// Reports whether the bead was actually closed (false when it was already
// closed). PHASE 0: the work-reassignment side effect that closeBead performs
// (releaseWorkFromClosedSessionBead) is intentionally NOT part of this method —
// that is a cross-class WORK op owned by the Phase 6 work/assignment API.
func (s *Store) Close(id, stateCode string, now time.Time) (bool, error) {
	info, err := s.Get(id)
	if err != nil {
		return false, err
	}
	if info.Closed {
		return false, nil
	}
	if err := s.ApplyPatch(id, ClosePatch(now, stateCode)); err != nil {
		return false, err
	}
	if err := s.store.Close(id); err != nil {
		return false, fmt.Errorf("closing session %q: %w", id, err)
	}
	return true, nil
}

// SetStatusOpen sets the session bead status to "open". It is the front door
// for the raw store.Update(id, UpdateOpts{Status: &"open"}) writes in the
// reopen and named-session retire-archive paths (session_beads.go), which open
// the bead row after stamping archive/reopen metadata via setMetaBatch. It
// emits a single Update op with only Status set, byte-identical to the raw
// write.
func (s *Store) SetStatusOpen(id string) error {
	open := "open"
	if err := s.store.Update(id, beads.UpdateOpts{Status: &open}); err != nil {
		return err
	}
	return nil
}

// RepairType sets the session bead Type to the canonical session bead type. It
// is the front door for the empty-type repair write in session_beads.go, where
// a session-labeled bead with an empty Type (left by a schema migration or a
// partial write) is healed back to the session type. It emits a single Update
// op with only Type set, byte-identical to the raw write.
func (s *Store) RepairType(id string) error {
	t := BeadType
	if err := s.store.Update(id, beads.UpdateOpts{Type: &t}); err != nil {
		return err
	}
	return nil
}

// RepairTypeBestEffort re-issues the empty-type heal (RepairType) and logs a
// failure instead of returning it, for the read paths that heal a type-lost
// session bead as a side effect (the API/worker Get compositions and the raw
// assignee-normalize lane). It preserves the best-effort logging the retired
// RepairEmptyType emitted — the heal must never abort the current operation, but
// a silent drop would hide a failing write. The log line matches RepairEmptyType.
func (s *Store) RepairTypeBestEffort(id string) {
	if err := s.RepairType(id); err != nil {
		log.Printf("session %s: repairing empty bead type: %v", id, err)
	}
}

// Store returns the embedded strongly-typed session-class bead store. It is a
// transition-period accessor for call sites that still need raw bead access
// while their reads/writes are migrated behind the typed methods above. New
// code must prefer the typed methods; this exists so Phase 4/5 can land
// incrementally without a flag-day rewrite.
func (s *Store) Store() beads.SessionStore { return s.store }

// SetLocalString stores a clone-local session value without exposing the
// underlying Beads store through the sessions front door.
func (s *Store) SetLocalString(id, key, value string) error {
	return s.store.SetLocalString(id, key, value)
}

// GetLocalString returns a clone-local session value without exposing the
// underlying Beads store through the sessions front door.
func (s *Store) GetLocalString(id, key string) (string, error) {
	return s.store.GetLocalString(id, key)
}
