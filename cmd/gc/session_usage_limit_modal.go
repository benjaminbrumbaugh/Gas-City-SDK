package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// providerUsageFenceIdentity returns the opaque identity resolved at config
// time. Runtime reconciliation must never derive identity from commands,
// provider names, or raw environment maps.
func providerUsageFenceIdentity(tp TemplateParams) string {
	return strings.TrimSpace(tp.ProviderFenceIdentity)
}

const (
	legacyProviderUsageFenceIdentity   = sessionpkg.LegacyGlobalProviderFenceIdentity
	unscopedProviderUsageFenceIdentity = "unscoped:no-provider-account"
)

func activeProviderUsageFence(fences map[string]time.Time, currentIdentity string, now time.Time) (string, time.Time, bool) {
	var matchedIdentity string
	var matchedUntil time.Time
	for identity, until := range fences {
		if sessionpkg.ProviderFenceIdentityMatches(identity, currentIdentity) && now.Before(until) && until.After(matchedUntil) {
			matchedIdentity = identity
			matchedUntil = until
		}
	}
	return matchedIdentity, matchedUntil, matchedIdentity != ""
}

// providerUsageFenceIdentityForRuntime attributes a live observation to the
// account that actually launched the runtime. Desired configuration is the
// fallback for a dead or not-yet-started session; it must not overwrite the
// durable launched identity while a runtime is alive.
func providerUsageFenceIdentityForRuntime(info sessionpkg.Info, tp TemplateParams, alive bool) string {
	if alive {
		if identity := strings.TrimSpace(info.LaunchProviderFenceIdentity); identity != "" {
			return identity
		}
		if identity := strings.TrimSpace(info.StartedProviderFenceIdentity); identity != "" {
			return identity
		}
	}
	return providerUsageFenceIdentity(tp)
}

// recordedProviderUsageFenceIdentity keeps an existing fence attached to the
// account that produced it even if configuration changes while quarantined.
// Identity-less legacy usage fences deliberately fall back to a global,
// fail-closed identity instead of being silently narrowed to current config.
func recordedProviderUsageFenceIdentity(info sessionpkg.Info, current string) string {
	if identity := strings.TrimSpace(info.ProviderFenceIdentity); identity != "" {
		if identity == unscopedProviderUsageFenceIdentity {
			return ""
		}
		return identity
	}
	if strings.TrimSpace(info.HealthReason) == sessionHealthReasonUsageLimitModal {
		return legacyProviderUsageFenceIdentity
	}
	return current
}

const (
	// sessionHealthReasonUsageLimitModal is the health reason recorded for a
	// session parked on the provider's usage-limit choice modal.
	sessionHealthReasonUsageLimitModal = "usage_limit_modal"

	// usageLimitModalFrozenPane is how long a session must have produced no
	// provider-reported output before a usage-limit-modal text match is treated
	// as a wedge rather than as a pane that merely mentions the modal.
	//
	// It is the corroborating half of the detection. The modal's text is
	// reproduced by anything that discusses it — a bug report rendered in a pane,
	// a sweep grepping every pane for the option phrases — and such a session is
	// by definition drawing output, so its activity timestamp is seconds old. A
	// genuinely wedged pane has not changed since the dialog appeared, because
	// the dialog is the last thing the provider will ever draw. One window is
	// therefore enough to separate the two, and it keeps the check honest without
	// depending on which pane the observer happens to be looking at.
	usageLimitModalFrozenPane = 2 * time.Minute
	// A usage subscription can legitimately reset several days out, but a
	// stale dated line must never roll into a year-long automatic fence.
	maxUsageLimitQuarantineDuration = 8 * 24 * time.Hour
)

// detectUsageLimitModalWedge reports whether one alive session is permanently
// parked on the provider's usage-limit choice modal.
//
// The modal blocks on a keypress no managed session sends, and it does not clear
// when the limit window closes, so every liveness signal the reconciler reads —
// the tmux session, the provider process, the session bead's own heartbeat —
// keeps reporting a healthy session that is in fact dead. Recovering it needs a
// fresh session; answering the dialog is not an option, because its second arm
// requests more usage from the account owner and that is paid spend.
//
// Probes run lazily and in cost order: the free activity read gates the pane
// capture, and a pane match gates the attachment probe. Every probe failure is
// reported through onProbeError and fails closed — a session whose state cannot
// be read is left alone rather than quarantined on a guess. recorded suppresses
// re-reporting a wedge that is already carrying its quarantine.
func detectUsageLimitModalWedge(
	alive bool,
	recorded bool,
	now time.Time,
	lastActivity func() (time.Time, error),
	capturePane func() (string, error),
	attached func() (bool, error),
	onProbeError func(stage string, err error),
) (time.Time, bool) {
	if !alive || recorded {
		return time.Time{}, false
	}
	activity, err := lastActivity()
	if err != nil {
		onProbeError("last activity", err)
		return time.Time{}, false
	}
	if activity.IsZero() || now.Sub(activity) < usageLimitModalFrozenPane {
		return time.Time{}, false
	}
	pane, err := capturePane()
	if err != nil {
		onProbeError("pane capture", err)
		return time.Time{}, false
	}
	if !runtime.ContainsUsageLimitChoiceModal(pane) {
		return time.Time{}, false
	}
	// The strict modal matcher above establishes that this is the current
	// interactive frame. Its confirm footer is necessarily the final line, so
	// the provider's optional reset text appears inside the modal rather than in
	// the final-line shape required by ProviderUsageLimitResetAt.
	resetAt, _ := runtime.ProviderRateLimitResetAt(pane, now)
	isAttached, err := attached()
	if err != nil {
		onProbeError("attachment", err)
		return time.Time{}, false
	}
	// An attached human can answer the dialog themselves, and choosing between
	// waiting and buying more usage is theirs to make.
	return resetAt, !isAttached
}

// usageLimitModalWedgePatch is the session metadata a detected usage-limit-modal
// wedge records: the session is scored unhealthy with an attributable reason,
// and its slot is quarantined for the same window a rate-limit exit gets.
//
// The quarantine is what makes the repair safe to automate. A wedged session is
// evidence the account was limited recently, so restarting it can land straight
// back on the same modal; the quarantine the awake set already honors holds the
// slot until the limit window has had time to close, then lapses on its own so
// the session recovers without an operator.
//
// Deliberately absent is the drainable flag. It is what distinguishes a terminal
// provider error — a misconfiguration only an operator can repair — from this
// wedge, which a fresh session clears (see sessionHasProviderTerminalErrorInfo).
func usageLimitModalWedgePatch(now, resetAt time.Time) sessionpkg.MetadataPatch {
	until := usageLimitModalQuarantineUntil(now, resetAt)
	return sessionpkg.ProviderFencePatch(until, sessionHealthReasonUsageLimitModal)
}

func usageLimitModalQuarantineUntil(now, resetAt time.Time) time.Time {
	until := now.Add(defaultRateLimitQuarantineDuration)
	if resetAt.After(now) && resetAt.Sub(now) <= maxUsageLimitQuarantineDuration {
		until = resetAt
	}
	return until
}

// usageLimitModalWedgeRecorded reports whether a session already carries an
// unexpired usage-limit-modal quarantine. It bounds reporting to once per
// quarantine window for a wedge that outlives its restart request — a pinned
// named session declines the abrupt kill — so the condition stays visible
// without re-announcing itself every reconciler tick.
func usageLimitModalWedgeRecorded(info sessionpkg.Info, now time.Time) bool {
	_, recorded := usageLimitModalFenceUntil(info, now)
	return recorded
}

func usageLimitModalFenceUntil(info sessionpkg.Info, now time.Time) (time.Time, bool) {
	if strings.TrimSpace(info.HealthReason) != sessionHealthReasonUsageLimitModal {
		return time.Time{}, false
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(info.QuarantinedUntil))
	if err != nil {
		return time.Time{}, false
	}
	if until.Sub(now) > maxUsageLimitQuarantineDuration {
		return time.Time{}, false
	}
	return until, now.Before(until)
}

// usageLimitModalRuntimeMatchesInstance binds destructive provider-fence work
// to the runtime incarnation whose pane was observed. Missing or unreadable
// tokens fail closed: a stale controller observation must never stop a healthy
// replacement that now occupies the same session name.
func usageLimitModalRuntimeMatchesInstance(sp runtime.Provider, name string, info sessionpkg.Info) error {
	expected := strings.TrimSpace(info.InstanceToken)
	if expected == "" {
		return fmt.Errorf("session bead has no instance token")
	}
	actual, err := sp.GetMeta(name, "GC_INSTANCE_TOKEN")
	if err != nil {
		return fmt.Errorf("read live instance token: %w", err)
	}
	if strings.TrimSpace(actual) != expected {
		return fmt.Errorf("%w: expected %q, got %q", errTokenMismatch, expected, strings.TrimSpace(actual))
	}
	return nil
}

// usageLimitRestartHandoffRollback restores the fields RestartRequestPatch
// changes destructively when the atomic runtime guard refuses the stop. The
// restart request itself deliberately remains armed for a later safe retry.
// convergecompare:recorded-by-caller — this pure builder has no bead ID; the
// reconciler records the returned patch immediately before persisting it.
func usageLimitRestartHandoffRollback(info sessionpkg.Info, handoff sessionpkg.MetadataPatch) sessionpkg.MetadataPatch {
	rollback := sessionpkg.MetadataPatch{
		"started_config_hash":                    info.StartedConfigHash,
		"continuation_reset_pending":             info.ContinuationResetPending,
		sessionpkg.ResetCommittedAtKey:           info.ResetCommittedAt,
		"last_woke_at":                           info.LastWokeAt,
		"pending_create_claim":                   info.PendingCreateClaimMetadata,
		"pending_create_started_at":              info.PendingCreateStartedAt,
		sessionpkg.PrimedAtMetadataKey:           info.PrimedAtMetadata,
		sessionpkg.PrimingAttemptedAtMetadataKey: info.PrimingAttemptedAtMetadata,
		sessionpkg.PromptHashMetadataKey:         info.PromptHashMetadata,
	}
	if _, changed := handoff["session_key"]; changed {
		rollback["session_key"] = info.SessionKey
	}
	return rollback
}
