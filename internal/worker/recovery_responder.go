package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	mu       sync.Mutex
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
	switch strings.TrimSpace(info.ProviderTerminalError) {
	case "model_not_found", "quota_exceeded":
		return strings.TrimSpace(info.ProviderTerminalError)
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
	r.mu.Lock()
	defer r.mu.Unlock()
	infos, err := r.sessions.List("", "")
	if err != nil {
		return report, err
	}
	for _, info := range infos {
		if recoveryVerifiedByReconciledState(info) {
			state := recoveryStateFromInfo(info)
			state.Outcome = "verified"
			state.WorkID = ""
			state.HoldUntil = time.Time{}
			state.CooldownUntil = time.Time{}
			if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
				return report, fmt.Errorf("verify recovery for session %s: %w", info.ID, err)
			}
			continue
		}
		impairment := HighConfidenceRecoveryImpairment(info)
		if impairment == "" {
			continue
		}
		result, err := r.reconcileIncident(ctx, info, impairment, now.UTC())
		if err != nil {
			return report, fmt.Errorf("recovery responder session %s: %w", info.ID, err)
		}
		report.Created += result.Created
		report.Active += result.Active
		report.CoolingDown += result.CoolingDown
		report.Exhausted += result.Exhausted
	}
	return report, nil
}

func (r *RecoveryResponder) reconcileIncident(ctx context.Context, info session.Info, impairment string, now time.Time) (RecoveryReport, error) {
	var report RecoveryReport
	state := recoveryStateFromInfo(info)
	if state.IncidentID == "" || state.Impairment != impairment || state.Outcome == "verified" {
		state = session.RecoveryState{
			IncidentID: "recovery-" + uuid.NewString(),
			Impairment: impairment,
			DetectedAt: now,
			HoldUntil:  now.Add(r.options.HoldDuration),
			Outcome:    "detected",
		}
		var err error
		info, err = r.sessions.RecordRecoveryState(info, state)
		if err != nil {
			return report, err
		}
	}

	works, err := r.work.ListByMetadata(map[string]string{beadmeta.RecoveryIncidentMetadataKey: state.IncidentID}, 0, beads.IncludeClosed)
	if err != nil {
		return report, fmt.Errorf("list incident work: %w", err)
	}
	attempt, attemptedTargets, active := recoveryAttempts(works)
	state.Attempt = attempt
	state.AttemptedTargets = attemptedTargets
	if active != nil {
		state.WorkID = active.ID
		state.Outcome = "active"
		if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
			return report, err
		}
		report.Active = 1
		return report, nil
	}

	if attempt > 0 && state.Outcome != "attempt_closed" && state.Outcome != "exhausted" {
		if attempt >= r.options.MaxAttempts || len(attemptedTargets) >= len(r.options.Targets) {
			state.Outcome = "exhausted"
			state.CooldownUntil = time.Time{}
			if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
				return report, err
			}
			report.Exhausted = 1
			return report, nil
		}
		state.Outcome = "attempt_closed"
		state.WorkID = ""
		state.CooldownUntil = now.Add(r.options.Cooldown)
		if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
			return report, err
		}
		report.CoolingDown = 1
		return report, nil
	}
	if state.Outcome == "attempt_closed" && now.Before(state.CooldownUntil) {
		report.CoolingDown = 1
		return report, nil
	}
	if attempt >= r.options.MaxAttempts || len(attemptedTargets) >= len(r.options.Targets) {
		state.Outcome = "exhausted"
		state.CooldownUntil = time.Time{}
		if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
			return report, err
		}
		report.Exhausted = 1
		return report, nil
	}

	candidates := remainingRecoveryTargets(r.options.Targets, attemptedTargets)
	target := r.recommend(ctx, RecoveryRequest{
		CorrelationID: state.IncidentID,
		Now:           now,
		Targets:       append([]string(nil), candidates...),
	})
	if !containsRecoveryTarget(candidates, target) {
		target = candidates[0]
	}
	nextAttempt := attempt + 1
	work, err := r.work.Create(beads.Bead{
		Title:       fmt.Sprintf("Recover impaired session %s", info.ID),
		Type:        "task",
		Description: recoveryWorkDescription(info, state.IncidentID, impairment),
		Labels:      []string{RecoveryWorkLabel},
		Metadata: beads.StringMap{
			beadmeta.RoutedToMetadataKey:              target,
			beadmeta.RecoveryAttemptMetadataKey:       strconv.Itoa(nextAttempt),
			beadmeta.RecoveryImpairmentMetadataKey:    impairment,
			beadmeta.RecoveryIncidentMetadataKey:      state.IncidentID,
			beadmeta.RecoverySourceSessionMetadataKey: info.ID,
			beadmeta.RecoveryTargetMetadataKey:        target,
		},
	})
	if err != nil {
		return report, fmt.Errorf("create ordinary recovery work: %w", err)
	}
	state.Attempt = nextAttempt
	state.AttemptedTargets = attemptedTargets
	state.AttemptedTargets = append(state.AttemptedTargets, target)
	state.CooldownUntil = time.Time{}
	state.Outcome = "active"
	state.WorkID = work.ID
	if _, err := r.sessions.RecordRecoveryState(info, state); err != nil {
		return report, err
	}
	report.Created = 1
	return report, nil
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
		if result.err != nil {
			return ""
		}
		return result.target
	}
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

func recoveryWorkDescription(info session.Info, incidentID, impairment string) string {
	return fmt.Sprintf("Investigate and remediate recovery incident %s for session %s (runtime %s, provider %s). The deterministic impairment is %s. Use normal Gas City work/session APIs; report evidence and close this task when the attempt is complete.", incidentID, info.ID, strings.TrimSpace(info.SessionName), strings.TrimSpace(info.Provider), impairment)
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
