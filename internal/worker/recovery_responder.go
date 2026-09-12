package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/google/uuid"
)

// RecoveryWorkLabel marks ordinary work created for a recovery incident.
const RecoveryWorkLabel = "gc:recovery-responder"

const (
	maxRecoveryHold            = 24 * time.Hour
	maxRecoveryAdvisoryTimeout = 5 * time.Second
	maxRecoveryCooldown        = 24 * time.Hour
	recoveryCreationLease      = 30 * time.Second
)

// RecoveryAdvisor may recommend one of the candidates supplied in the request.
// It is advisory only: the responder validates its answer and owns all writes.
type RecoveryAdvisor interface {
	Recommend(context.Context, RecoveryRequest) (string, error)
}

// RecoveryRequest identifies the remaining configured targets. The Wayfinder
// advisor applies these caller-owned fields to an operator-authored packet.
type RecoveryRequest struct {
	CorrelationID string
	Now           time.Time
	Targets       []string
}

// RecoveryResponderOptions activates the responder only when Targets is non-empty.
type RecoveryResponderOptions struct {
	Targets         []string
	HoldDuration    time.Duration
	AdvisoryTimeout time.Duration
	Cooldown        time.Duration
	MaxAttempts     int
	Advisor         RecoveryAdvisor
}

// RecoveryReport summarizes one reconciliation pass.
type RecoveryReport struct {
	Created     int
	Active      int
	CoolingDown int
	Exhausted   int
}

// RecoveryResponder turns durable impairments into serialized ordinary work.
type RecoveryResponder struct {
	sessions *session.Store
	work     beads.Store
	options  RecoveryResponderOptions
}

// NewRecoveryResponder builds a controller-safe responder with bounded options.
func NewRecoveryResponder(sessions *session.Store, work beads.Store, options RecoveryResponderOptions) *RecoveryResponder {
	options.Targets = normalizedRecoveryTargets(options.Targets)
	options.HoldDuration = boundedPositive(options.HoldDuration, 30*time.Minute, maxRecoveryHold)
	options.AdvisoryTimeout = boundedPositive(options.AdvisoryTimeout, 2*time.Second, maxRecoveryAdvisoryTimeout)
	options.Cooldown = boundedPositive(options.Cooldown, 5*time.Minute, maxRecoveryCooldown)
	if options.MaxAttempts <= 0 || options.MaxAttempts > len(options.Targets) {
		options.MaxAttempts = len(options.Targets)
	}
	return &RecoveryResponder{sessions: sessions, work: work, options: options}
}

// Options returns the normalized bounded options used by this responder.
func (r *RecoveryResponder) Options() RecoveryResponderOptions { return r.options }

// HighConfidenceRecoveryImpairment accepts only exact durable classifications
// emitted by the provider-terminal and frozen usage-limit modal detectors.
func HighConfidenceRecoveryImpairment(info session.Info) string {
	if strings.TrimSpace(info.HealthState) != "unhealthy" {
		return ""
	}
	terminalError := strings.TrimSpace(info.ProviderTerminalError)
	switch terminalError {
	case "model_not_found", "quota_exceeded":
		if info.Drainable && strings.TrimSpace(info.HealthReason) == terminalError {
			return terminalError
		}
	}
	if strings.TrimSpace(info.HealthReason) == "usage_limit_modal" && strings.TrimSpace(info.QuarantinedUntil) != "" {
		return "usage_limit_modal"
	}
	return ""
}

func recoveryVerifiedByReconciledState(info session.Info) bool {
	return strings.TrimSpace(info.RecoveryIncidentID) != "" &&
		strings.TrimSpace(info.RecoveryOutcome) != "verified" &&
		info.State == session.StateActive &&
		strings.TrimSpace(info.HealthState) == "healthy"
}

// Reconcile adopts or advances at most one ordinary work item per incident.
func (r *RecoveryResponder) Reconcile(ctx context.Context, now time.Time) (RecoveryReport, error) {
	var report RecoveryReport
	if r == nil || len(r.options.Targets) == 0 {
		return report, nil
	}
	if r.sessions == nil || r.work == nil {
		return report, fmt.Errorf("recovery responder: missing session or work store")
	}
	if !beads.SupportsDeterministicCreate(r.work) {
		return report, beads.ErrDeterministicCreateUnsupported
	}
	workWriter, err := beads.PreflightConditionalWriter(r.work)
	if err != nil {
		return report, err
	}
	infos, err := r.sessions.List("", "")
	if err != nil {
		return report, err
	}
	advisoryAvailable := true
	for _, info := range infos {
		if recoveryVerifiedByReconciledState(info) {
			if err := r.verifyRecovery(info); err != nil {
				return report, fmt.Errorf("verify recovery for session %s: %w", info.ID, err)
			}
			continue
		}
		impairment := HighConfidenceRecoveryImpairment(info)
		if impairment == "" {
			continue
		}
		result, advised, err := r.reconcileIncident(ctx, info, impairment, now.UTC(), advisoryAvailable, workWriter)
		if err != nil {
			return report, fmt.Errorf("recovery responder session %s: %w", info.ID, err)
		}
		if advised {
			advisoryAvailable = false
		}
		report.Created += result.Created
		report.Active += result.Active
		report.CoolingDown += result.CoolingDown
		report.Exhausted += result.Exhausted
	}
	return report, nil
}

func (r *RecoveryResponder) verifyRecovery(info session.Info) (returnErr error) {
	leaseNow := time.Now().UTC()
	snapshot, acquired, err := r.sessions.AcquireRecoveryResponderLease(
		info.ID, uuid.NewString(), leaseNow, leaseNow.Add(recoveryCreationLease),
	)
	if err != nil {
		return fmt.Errorf("acquire verification lease: %w", err)
	}
	if !acquired {
		return nil
	}
	defer func() {
		if err := r.sessions.ReleaseRecoveryResponderLease(snapshot); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("release verification lease: %w", err)
		}
	}()

	fresh := snapshot.Info
	if !recoveryVerifiedByReconciledState(fresh) {
		return nil
	}
	state := recoveryStateFromInfo(fresh)
	state.Outcome = "verified"
	state.WorkID = ""
	state.HoldUntil = time.Time{}
	state.CooldownUntil = time.Time{}
	snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
	if recoveryFenceLost(err) {
		return nil
	}
	return err
}

func (r *RecoveryResponder) reconcileIncident(ctx context.Context, info session.Info, impairment string, now time.Time, allowAdvice bool, workWriter beads.ConditionalWriter) (report RecoveryReport, advised bool, returnErr error) {
	// Keep the cheap pre-lease census, but never mutate from its stale session
	// snapshot. Adoption and creation both serialize through the durable lease.
	if _, err := r.openRecoveryWorkForSource(info.ID); err != nil {
		return report, false, err
	}

	leaseNow := time.Now().UTC()
	snapshot, acquired, err := r.sessions.AcquireRecoveryResponderLease(
		info.ID, uuid.NewString(), leaseNow, leaseNow.Add(recoveryCreationLease),
	)
	if err != nil {
		return report, false, fmt.Errorf("acquire creation lease: %w", err)
	}
	if !acquired {
		// The winner owns every recovery-state mutation. A loser may report the
		// already-created work, but must not adopt it from a stale snapshot.
		if active, listErr := r.openRecoveryWorkForSource(info.ID); listErr != nil {
			return report, false, listErr
		} else if active != nil {
			return RecoveryReport{Active: 1}, false, nil
		}
		return report, false, nil
	}
	defer func() {
		if err := r.sessions.ReleaseRecoveryResponderLease(snapshot); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("release creation lease: %w", err)
		}
	}()

	// Lease acquisition returns the only snapshot authorized for writes. Every
	// branch below advances and retains that snapshot after a successful CAS.
	info = snapshot.Info
	if HighConfidenceRecoveryImpairment(info) != impairment {
		return report, false, nil
	}
	if active, err := r.openRecoveryWorkForSource(info.ID); err != nil {
		return report, false, err
	} else if active != nil {
		adopted, nextSnapshot, err := r.adoptRecoveryWork(snapshot, *active, impairment, now, workWriter)
		snapshot = nextSnapshot
		return adopted, false, err
	}

	state := recoveryStateFromInfo(info)
	if state.IncidentID == "" || state.Impairment != impairment || state.Outcome == "verified" {
		state = session.RecoveryState{
			IncidentID: deterministicRecoveryIncidentID(info, impairment),
			Impairment: impairment,
			DetectedAt: now,
			HoldUntil:  now.Add(r.options.HoldDuration),
			Outcome:    "detected",
		}
		snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
		if err != nil {
			if recoveryFenceLost(err) {
				return report, false, nil
			}
			return report, false, err
		}
		info = snapshot.Info
	}
	if HighConfidenceRecoveryImpairment(info) != impairment {
		return report, false, nil
	}
	state = recoveryStateFromInfo(info)

	works, err := r.work.ListByMetadata(map[string]string{beadmeta.RecoveryIncidentMetadataKey: state.IncidentID}, 0, beads.IncludeClosed)
	if err != nil {
		return report, false, fmt.Errorf("list incident work: %w", err)
	}
	works, err = r.validRecoveryWorks(works, info.ID, state.IncidentID, impairment)
	if err != nil {
		return report, false, err
	}
	attempt, attemptedTargets, active := recoveryAttempts(works)
	planned := state.Outcome == "planned"
	if planned && active != nil {
		adopted, nextSnapshot, err := r.adoptRecoveryWork(snapshot, *active, impairment, now, workWriter)
		snapshot = nextSnapshot
		return adopted, false, err
	}
	if planned {
		if attempt == state.Attempt && slices.Equal(state.AttemptedTargets, attemptedTargets) {
			// Creation committed but active-state writeback was lost, and the
			// exact deterministic attempt is no longer active. Advance from the
			// bound tuple instead of trying to recreate an open row over it.
			state.Attempt = attempt
			state.AttemptedTargets = append([]string(nil), attemptedTargets...)
			state.WorkID = ""
			state.Outcome = "attempt_closed"
			state.CooldownUntil = now.Add(r.options.Cooldown)
			snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
			if err != nil {
				if recoveryFenceLost(err) {
					return report, false, nil
				}
				return report, false, err
			}
			report.CoolingDown++
			return report, true, nil
		}
		if state.Attempt != attempt+1 || state.Attempt > r.options.MaxAttempts || len(state.AttemptedTargets) != len(attemptedTargets)+1 ||
			!slices.Equal(state.AttemptedTargets[:len(attemptedTargets)], attemptedTargets) ||
			!containsRecoveryTarget(r.options.Targets, state.AttemptedTargets[len(state.AttemptedTargets)-1]) ||
			containsRecoveryTarget(attemptedTargets, state.AttemptedTargets[len(state.AttemptedTargets)-1]) {
			return report, false, fmt.Errorf("invalid persisted recovery plan for incident %q", state.IncidentID)
		}
		attempt = state.Attempt
		attemptedTargets = append([]string(nil), state.AttemptedTargets...)
	} else {
		state.Attempt = attempt
		state.AttemptedTargets = attemptedTargets
	}
	if active != nil {
		adopted, nextSnapshot, err := r.adoptRecoveryWork(snapshot, *active, impairment, now, workWriter)
		snapshot = nextSnapshot
		return adopted, false, err
	}

	if !planned && attempt > 0 && state.Outcome != "attempt_closed" && state.Outcome != "exhausted" {
		if attempt >= r.options.MaxAttempts || len(attemptedTargets) >= len(r.options.Targets) {
			state.Outcome = "exhausted"
			state.CooldownUntil = time.Time{}
			snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
			if err != nil {
				if recoveryFenceLost(err) {
					return report, false, nil
				}
				return report, false, err
			}
			report.Exhausted = 1
			return report, false, nil
		}
		state.Outcome = "attempt_closed"
		state.WorkID = ""
		state.CooldownUntil = now.Add(r.options.Cooldown)
		snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
		if err != nil {
			if recoveryFenceLost(err) {
				return report, false, nil
			}
			return report, false, err
		}
		report.CoolingDown = 1
		return report, false, nil
	}
	if state.Outcome == "attempt_closed" && now.Before(state.CooldownUntil) {
		report.CoolingDown = 1
		return report, false, nil
	}
	if !planned && (attempt >= r.options.MaxAttempts || len(attemptedTargets) >= len(r.options.Targets)) {
		state.Outcome = "exhausted"
		state.CooldownUntil = time.Time{}
		snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
		if err != nil {
			if recoveryFenceLost(err) {
				return report, false, nil
			}
			return report, false, err
		}
		report.Exhausted = 1
		return report, false, nil
	}

	var nextAttempt int
	var attemptIdentity, target string
	if planned {
		nextAttempt = state.Attempt
		target = state.AttemptedTargets[len(state.AttemptedTargets)-1]
		attemptIdentity = deterministicRecoveryAttemptID(state.IncidentID, nextAttempt)
	} else {
		candidates := remainingRecoveryTargets(r.options.Targets, attemptedTargets)
		if r.options.Advisor != nil && !allowAdvice {
			// One potentially blocking advisory decision is permitted per controller
			// tick. Leave this incident durable for the next tick rather than silently
			// bypassing the configured advisor.
			return report, false, nil
		}
		nextAttempt = attempt + 1
		attemptIdentity = deterministicRecoveryAttemptID(state.IncidentID, nextAttempt)
		target = r.recommend(ctx, RecoveryRequest{
			CorrelationID: attemptIdentity,
			Now:           now,
			Targets:       append([]string(nil), candidates...),
		})
		advised = r.options.Advisor != nil
		if !containsRecoveryTarget(candidates, target) {
			target = candidates[0]
		}
		// Persist the immutable attempt tuple before the cross-store create. A
		// successor that fences this lease must reuse this exact attempt/target,
		// so concurrent creators converge on one deterministic work row.
		state.Attempt = nextAttempt
		state.AttemptedTargets = append(append([]string(nil), attemptedTargets...), target)
		state.Outcome = "planned"
		state.WorkID = ""
		snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
		if err != nil {
			if recoveryFenceLost(err) {
				return report, advised, nil
			}
			return report, advised, err
		}
	}
	// Revalidate and extend the lease immediately before the external work-store
	// mutation. An expired or superseded holder cannot create a new tuple.
	renewNow := time.Now().UTC()
	snapshot, err = r.sessions.RenewRecoveryResponderLease(snapshot, renewNow, renewNow.Add(recoveryCreationLease))
	if err != nil {
		if recoveryFenceLost(err) {
			return report, advised, nil
		}
		return report, advised, err
	}
	candidate := beads.Bead{
		Title:       fmt.Sprintf("Recover impaired session %s", info.ID),
		Type:        "task",
		Description: recoveryWorkDescription(info.ID, state.IncidentID, impairment, target),
		Labels:      []string{RecoveryWorkLabel},
		Metadata: beads.StringMap{
			beadmeta.RoutedToMetadataKey:              target,
			beadmeta.RecoveryAttemptIDMetadataKey:     attemptIdentity,
			beadmeta.RecoveryAttemptMetadataKey:       strconv.Itoa(nextAttempt),
			beadmeta.RecoveryImpairmentMetadataKey:    impairment,
			beadmeta.RecoveryIncidentMetadataKey:      state.IncidentID,
			beadmeta.RecoverySourceSessionMetadataKey: info.ID,
			beadmeta.RecoveryTargetMetadataKey:        target,
		},
	}
	work, created, err := beads.CreateDeterministically(r.work, attemptIdentity, candidate)
	if err != nil {
		return report, advised, fmt.Errorf("create ordinary recovery work: %w", err)
	}
	if !created {
		reservedWork, won, reserveErr := r.reserveRecoveryWork(workWriter, work)
		if reserveErr != nil {
			return report, advised, fmt.Errorf("reserve deterministic recovery work %s for adoption: %w", work.ID, reserveErr)
		}
		if !won {
			return report, advised, nil
		}
		work = reservedWork
	}
	state.Attempt = nextAttempt
	state.AttemptedTargets = append([]string(nil), state.AttemptedTargets...)
	state.CooldownUntil = time.Time{}
	state.Outcome = "active"
	state.WorkID = work.ID
	snapshot, err = r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
	if err != nil {
		if created {
			closeErr := r.closeUnadoptedRecoveryWork(workWriter, info.ID, state.IncidentID, work)
			if closeErr != nil {
				return report, advised, errors.Join(err, fmt.Errorf("close unbound recovery work %s: %w", work.ID, closeErr))
			}
		} else {
			adopted, closeErr := r.compensateRecoveryWorkReservation(workWriter, info.ID, state.IncidentID, work)
			if closeErr != nil {
				return report, advised, errors.Join(err, fmt.Errorf("close failed deterministic recovery work adoption %s: %w", work.ID, closeErr))
			}
			if adopted {
				return RecoveryReport{Active: 1}, advised, nil
			}
		}
		if recoveryFenceLost(err) {
			return report, advised, nil
		}
		return report, advised, err
	}
	if created {
		report.Created = 1
	} else if work.Status != "closed" {
		report.Active = 1
	}
	return report, advised, nil
}

func (r *RecoveryResponder) recoveryWorkWasAdopted(sourceSessionID, incidentID, workID string) bool {
	current, err := r.sessions.Get(sourceSessionID)
	return err == nil && !current.Closed && current.RecoveryOutcome == "active" &&
		current.RecoveryIncidentID == incidentID && current.RecoveryWorkID == workID
}

func (r *RecoveryResponder) closeUnadoptedRecoveryWork(writer beads.ConditionalWriter, sourceSessionID, incidentID string, work beads.Bead) error {
	if r.recoveryWorkWasAdopted(sourceSessionID, incidentID, work.ID) {
		return nil
	}
	if err := writer.CloseIfMatch(work.ID, work.Revision); err != nil {
		if !beads.IsPreconditionFailed(err) {
			return err
		}
		if r.recoveryWorkWasAdopted(sourceSessionID, incidentID, work.ID) {
			return nil
		}
		current, getErr := r.work.Get(work.ID)
		if getErr != nil {
			return errors.Join(err, fmt.Errorf("read recovery work after close fence loss: %w", getErr))
		}
		if current.Status == "closed" || strings.TrimSpace(current.Metadata[beadmeta.RecoveryAdoptionFenceMetadataKey]) != "" {
			return nil
		}
		return err
	}
	return nil
}

func recoveryWorkMatchesReservation(before, current beads.Bead, token string) bool {
	expected := before
	expected.Metadata = maps.Clone(before.Metadata)
	if expected.Metadata == nil {
		expected.Metadata = beads.StringMap{}
	}
	expected.Metadata[beadmeta.RecoveryAdoptionFenceMetadataKey] = token
	// Revision and UpdatedAt are store-owned effects of the reservation; the
	// ready projection may also change independently as dependencies settle.
	expected.Revision = current.Revision
	expected.UpdatedAt = current.UpdatedAt
	expected.IsBlocked = current.IsBlocked
	return reflect.DeepEqual(expected, current)
}

func (r *RecoveryResponder) reserveRecoveryWork(writer beads.ConditionalWriter, work beads.Bead) (beads.Bead, bool, error) {
	token := uuid.NewString()
	err := writer.UpdateIfMatch(work.ID, work.Revision, beads.UpdateOpts{Metadata: map[string]string{
		beadmeta.RecoveryAdoptionFenceMetadataKey: token,
	}})
	if beads.IsPreconditionFailed(err) {
		return beads.Bead{}, false, nil
	}
	current, getErr := r.work.Get(work.ID)
	if getErr != nil {
		if err != nil {
			return beads.Bead{}, false, errors.Join(err, fmt.Errorf("read recovery work reservation: %w", getErr))
		}
		return beads.Bead{}, false, fmt.Errorf("read recovery work reservation: %w", getErr)
	}
	if current.Status == "closed" || current.Metadata[beadmeta.RecoveryAdoptionFenceMetadataKey] != token ||
		!recoveryWorkMatchesReservation(work, current, token) {
		if err != nil {
			return beads.Bead{}, false, err
		}
		return beads.Bead{}, false, nil
	}
	// An exact token readback resolves an ambiguous transport error in our favor.
	return current, true, nil
}

func (r *RecoveryResponder) compensateRecoveryWorkReservation(writer beads.ConditionalWriter, sourceSessionID, incidentID string, work beads.Bead) (bool, error) {
	if r.recoveryWorkWasAdopted(sourceSessionID, incidentID, work.ID) {
		return true, nil
	}
	if err := writer.CloseIfMatch(work.ID, work.Revision); err != nil {
		if !beads.IsPreconditionFailed(err) {
			return false, err
		}
		if r.recoveryWorkWasAdopted(sourceSessionID, incidentID, work.ID) {
			return true, nil
		}
		current, getErr := r.work.Get(work.ID)
		if getErr != nil {
			return false, errors.Join(err, fmt.Errorf("read reserved recovery work after close fence loss: %w", getErr))
		}
		if current.Status == "closed" {
			return false, nil
		}
		return false, err
	}
	return false, nil
}

func recoveryFenceLost(err error) bool {
	return beads.IsPreconditionFailed(err) || errors.Is(err, session.ErrRecoveryResponderLeaseLost)
}

func (r *RecoveryResponder) recommend(ctx context.Context, request RecoveryRequest) string {
	if r.options.Advisor == nil {
		return ""
	}
	adviceCtx, cancel := context.WithTimeout(ctx, r.options.AdvisoryTimeout)
	defer cancel()
	type result struct {
		target string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		target, err := r.options.Advisor.Recommend(adviceCtx, request)
		resultCh <- result{target: strings.TrimSpace(target), err: err}
	}()
	select {
	case <-adviceCtx.Done():
		return ""
	case result := <-resultCh:
		// If completion and timeout become ready together, the deadline owns the
		// result deterministically rather than scheduler select order.
		if adviceCtx.Err() != nil || result.err != nil {
			return ""
		}
		return result.target
	}
}

func (r *RecoveryResponder) openRecoveryWorkForSource(sourceSessionID string) (*beads.Bead, error) {
	works, err := r.work.ListByMetadata(map[string]string{
		beadmeta.RecoverySourceSessionMetadataKey: sourceSessionID,
	}, 0, beads.IncludeClosed)
	if err != nil {
		return nil, fmt.Errorf("list source recovery work: %w", err)
	}
	var active *beads.Bead
	for _, work := range works {
		if work.Status != "closed" && hasRecoveryWorkLabel(work.Labels) {
			incidentID := strings.TrimSpace(work.Metadata[beadmeta.RecoveryIncidentMetadataKey])
			impairment := strings.TrimSpace(work.Metadata[beadmeta.RecoveryImpairmentMetadataKey])
			if err := r.validateRecoveryWork(work, sourceSessionID, incidentID, impairment); err != nil {
				return nil, err
			}
			if active != nil {
				return nil, fmt.Errorf("multiple active recovery attempts for source session %q", sourceSessionID)
			}
			candidate := work
			active = &candidate
		}
	}
	return active, nil
}

func (r *RecoveryResponder) adoptRecoveryWork(snapshot session.RecoverySnapshot, active beads.Bead, impairment string, now time.Time, workWriter beads.ConditionalWriter) (RecoveryReport, session.RecoverySnapshot, error) {
	info := snapshot.Info
	incidentID := strings.TrimSpace(active.Metadata[beadmeta.RecoveryIncidentMetadataKey])
	if err := r.validateRecoveryWork(active, info.ID, incidentID, impairment); err != nil {
		return RecoveryReport{}, snapshot, err
	}
	if info.RecoveryOutcome == "planned" {
		plan := recoveryStateFromInfo(info)
		activeAttempt := parseRecoveryAttempt(active.Metadata[beadmeta.RecoveryAttemptMetadataKey])
		activeTarget := strings.TrimSpace(active.Metadata[beadmeta.RecoveryTargetMetadataKey])
		if incidentID != plan.IncidentID || activeAttempt != plan.Attempt || len(plan.AttemptedTargets) == 0 || activeTarget != plan.AttemptedTargets[len(plan.AttemptedTargets)-1] {
			return RecoveryReport{}, snapshot, fmt.Errorf("active recovery work %q conflicts with persisted recovery plan", active.ID)
		}
	}
	works, err := r.work.List(beads.ListQuery{
		Metadata:      map[string]string{beadmeta.RecoveryIncidentMetadataKey: incidentID},
		IncludeClosed: true,
		Live:          true,
	})
	if err != nil {
		return RecoveryReport{}, snapshot, fmt.Errorf("list adopted incident work: %w", err)
	}
	works, err = r.validRecoveryWorks(works, info.ID, incidentID, impairment)
	if err != nil {
		return RecoveryReport{}, snapshot, err
	}
	attempt, attemptedTargets, authoritativeActive := recoveryAttempts(works)
	if info.RecoveryOutcome == "planned" {
		plan := recoveryStateFromInfo(info)
		if authoritativeActive == nil {
			if attempt != plan.Attempt || !slices.Equal(attemptedTargets, plan.AttemptedTargets) {
				return RecoveryReport{}, snapshot, fmt.Errorf("closed recovery work history conflicts with persisted recovery plan")
			}
			plan.WorkID = ""
			plan.Outcome = "attempt_closed"
			plan.CooldownUntil = now.Add(r.options.Cooldown)
			nextSnapshot, recordErr := r.sessions.RecordRecoveryStateIfCurrent(snapshot, plan)
			if recordErr != nil {
				if recoveryFenceLost(recordErr) {
					return RecoveryReport{}, snapshot, nil
				}
				return RecoveryReport{}, snapshot, recordErr
			}
			return RecoveryReport{CoolingDown: 1}, nextSnapshot, nil
		}
		active = *authoritativeActive
		activeAttempt := parseRecoveryAttempt(active.Metadata[beadmeta.RecoveryAttemptMetadataKey])
		activeTarget := strings.TrimSpace(active.Metadata[beadmeta.RecoveryTargetMetadataKey])
		if strings.TrimSpace(active.Metadata[beadmeta.RecoveryIncidentMetadataKey]) != plan.IncidentID || activeAttempt != plan.Attempt || len(plan.AttemptedTargets) == 0 || activeTarget != plan.AttemptedTargets[len(plan.AttemptedTargets)-1] {
			return RecoveryReport{}, snapshot, fmt.Errorf("active recovery work %q conflicts with persisted recovery plan", active.ID)
		}
	} else if authoritativeActive != nil {
		active = *authoritativeActive
	}
	if attempt == 0 {
		attempt = parseRecoveryAttempt(active.Metadata[beadmeta.RecoveryAttemptMetadataKey])
		if target := strings.TrimSpace(active.Metadata[beadmeta.RecoveryTargetMetadataKey]); target != "" {
			attemptedTargets = []string{target}
		}
	}
	state := recoveryStateFromInfo(info)
	if state.IncidentID != incidentID {
		state = session.RecoveryState{
			IncidentID: incidentID,
			Impairment: strings.TrimSpace(active.Metadata[beadmeta.RecoveryImpairmentMetadataKey]),
			DetectedAt: now,
			HoldUntil:  now.Add(r.options.HoldDuration),
		}
	} else if state.Outcome == "verified" {
		// A verified incident releases its hold. If the same still-open work is
		// adopted after the impairment recurs, bound the renewed recovery window
		// from this recurrence rather than leaving the session unheld.
		state.HoldUntil = now.Add(r.options.HoldDuration)
	}
	if state.Impairment == "" {
		state.Impairment = impairment
	}
	state.Attempt = attempt
	state.AttemptedTargets = attemptedTargets
	state.CooldownUntil = time.Time{}
	state.Outcome = "active"
	state.WorkID = active.ID
	reserved, won, err := r.reserveRecoveryWork(workWriter, active)
	if err != nil {
		return RecoveryReport{}, snapshot, fmt.Errorf("reserve recovery work %s for adoption: %w", active.ID, err)
	}
	if !won {
		return RecoveryReport{}, snapshot, nil
	}
	active = reserved
	nextSnapshot, err := r.sessions.RecordRecoveryStateIfCurrent(snapshot, state)
	if err != nil {
		adopted, closeErr := r.compensateRecoveryWorkReservation(workWriter, info.ID, incidentID, active)
		if closeErr != nil {
			return RecoveryReport{}, snapshot, errors.Join(err, fmt.Errorf("close failed recovery work adoption %s: %w", active.ID, closeErr))
		}
		if adopted {
			return RecoveryReport{Active: 1}, snapshot, nil
		}
		if recoveryFenceLost(err) {
			return RecoveryReport{}, snapshot, nil
		}
		return RecoveryReport{}, snapshot, err
	}
	return RecoveryReport{Active: 1}, nextSnapshot, nil
}

func (r *RecoveryResponder) validRecoveryWorks(works []beads.Bead, sourceSessionID, incidentID, impairment string) ([]beads.Bead, error) {
	valid := make([]beads.Bead, 0, len(works))
	for _, work := range works {
		if !hasRecoveryWorkLabel(work.Labels) || strings.TrimSpace(work.Metadata[beadmeta.RecoverySourceSessionMetadataKey]) != sourceSessionID {
			continue
		}
		if err := r.validateRecoveryWork(work, sourceSessionID, incidentID, impairment); err != nil {
			return nil, err
		}
		valid = append(valid, work)
	}
	return valid, nil
}

func (r *RecoveryResponder) validateRecoveryWork(work beads.Bead, sourceSessionID, incidentID, impairment string) error {
	attempt := parseRecoveryAttempt(work.Metadata[beadmeta.RecoveryAttemptMetadataKey])
	target := strings.TrimSpace(work.Metadata[beadmeta.RecoveryTargetMetadataKey])
	if strings.TrimSpace(incidentID) == "" || strings.TrimSpace(impairment) == "" || attempt <= 0 || attempt > r.options.MaxAttempts ||
		!containsRecoveryTarget(r.options.Targets, target) || work.Type != "task" ||
		work.Title != fmt.Sprintf("Recover impaired session %s", sourceSessionID) ||
		work.Description != recoveryWorkDescription(sourceSessionID, incidentID, impairment, target) ||
		len(work.Labels) != 1 || work.Labels[0] != RecoveryWorkLabel ||
		strings.TrimSpace(work.Metadata[beadmeta.RecoverySourceSessionMetadataKey]) != sourceSessionID ||
		strings.TrimSpace(work.Metadata[beadmeta.RecoveryIncidentMetadataKey]) != incidentID ||
		strings.TrimSpace(work.Metadata[beadmeta.RecoveryImpairmentMetadataKey]) != impairment ||
		strings.TrimSpace(work.Metadata[beadmeta.RecoveryAttemptIDMetadataKey]) != deterministicRecoveryAttemptID(incidentID, attempt) ||
		strings.TrimSpace(work.Metadata[beadmeta.RoutedToMetadataKey]) != target {
		return fmt.Errorf("recovery work %q conflicts with its deterministic attempt tuple", work.ID)
	}
	return nil
}

func hasRecoveryWorkLabel(labels []string) bool {
	for _, label := range labels {
		if label == RecoveryWorkLabel {
			return true
		}
	}
	return false
}

func deterministicRecoveryIncidentID(info session.Info, impairment string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		info.ID,
		strings.TrimSpace(impairment),
		strings.TrimSpace(info.RecoveryIncidentID),
		strings.TrimSpace(info.RecoveryOutcome),
	}, "\x00")))
	return "recovery-" + hex.EncodeToString(sum[:])
}

func deterministicRecoveryAttemptID(incidentID string, attempt int) string {
	sum := sha256.Sum256([]byte(incidentID + "\x00" + strconv.Itoa(attempt)))
	return "recovery-attempt-" + hex.EncodeToString(sum[:])
}

func recoveryStateFromInfo(info session.Info) session.RecoveryState {
	return session.RecoveryState{
		IncidentID:       info.RecoveryIncidentID,
		Impairment:       info.RecoveryImpairment,
		DetectedAt:       parseRecoveryTime(info.RecoveryDetectedAt),
		HoldUntil:        parseRecoveryTime(info.RecoveryHoldUntil),
		Attempt:          parseRecoveryAttempt(info.RecoveryAttempt),
		AttemptedTargets: splitRecoveryTargets(info.RecoveryAttemptedTargets),
		CooldownUntil:    parseRecoveryTime(info.RecoveryCooldownUntil),
		Outcome:          info.RecoveryOutcome,
		WorkID:           info.RecoveryWorkID,
	}
}

func recoveryAttempts(works []beads.Bead) (int, []string, *beads.Bead) {
	attempts := make(map[int]string)
	var active *beads.Bead
	maxAttempt := 0
	for i := range works {
		attempt := parseRecoveryAttempt(works[i].Metadata[beadmeta.RecoveryAttemptMetadataKey])
		if attempt <= 0 {
			continue
		}
		if attempt > maxAttempt {
			maxAttempt = attempt
		}
		attempts[attempt] = strings.TrimSpace(works[i].Metadata[beadmeta.RecoveryTargetMetadataKey])
		if works[i].Status != "closed" && (active == nil || attempt > parseRecoveryAttempt(active.Metadata[beadmeta.RecoveryAttemptMetadataKey])) {
			active = &works[i]
		}
	}
	ordered := make([]string, 0, maxAttempt)
	for attempt := 1; attempt <= maxAttempt; attempt++ {
		if target := attempts[attempt]; target != "" {
			ordered = append(ordered, target)
		}
	}
	return maxAttempt, ordered, active
}

func recoveryWorkDescription(sourceSessionID, incidentID, impairment, target string) string {
	return fmt.Sprintf("Investigate and remediate recovery incident %s for source session %s. The deterministic impairment is %s and the bound recovery target is %s. Use normal Gas City work/session APIs; report evidence and close this task when the attempt is complete.", incidentID, sourceSessionID, impairment, target)
}

func boundedPositive(value, fallback, maximum time.Duration) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func normalizedRecoveryTargets(targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out
}

func remainingRecoveryTargets(configured, attempted []string) []string {
	used := make(map[string]struct{}, len(attempted))
	for _, target := range attempted {
		used[target] = struct{}{}
	}
	out := make([]string, 0, len(configured))
	for _, target := range configured {
		if _, ok := used[target]; !ok {
			out = append(out, target)
		}
	}
	return out
}

func containsRecoveryTarget(targets []string, target string) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}

func parseRecoveryTime(raw string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	return parsed
}

func parseRecoveryAttempt(raw string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(raw))
	return parsed
}

func splitRecoveryTargets(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
