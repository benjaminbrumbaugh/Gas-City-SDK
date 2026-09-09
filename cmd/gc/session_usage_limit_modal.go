package main

import (
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

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
// Probes run lazily and in cost order. lastActivity/lastActivityErr are the
// caller's own activity read — the reconciler's progress-stall gate already pays
// for it, and an unconditional read of its own would put a tmux subprocess per
// alive session on every tick, the cost the desired-session fast path forbids.
// A stale timestamp is what gates the pane capture, and a pane match gates the
// attachment probe. Every failure fails closed — a session whose state cannot be
// read is left alone rather than quarantined on a guess — and the two probes this
// function owns report through onProbeError. An activity read that failed is the
// caller's to log, so it is declined silently here rather than reported twice.
// recorded suppresses re-reporting a wedge that is already carrying its
// quarantine.
func detectUsageLimitModalWedge(
	alive bool,
	recorded bool,
	now time.Time,
	lastActivity time.Time,
	lastActivityErr error,
	capturePane func() (string, error),
	attached func() (bool, error),
	onProbeError func(stage string, err error),
) bool {
	if !alive || recorded || lastActivityErr != nil {
		return false
	}
	if lastActivity.IsZero() || now.Sub(lastActivity) < usageLimitModalFrozenPane {
		return false
	}
	pane, err := capturePane()
	if err != nil {
		onProbeError("pane capture", err)
		return false
	}
	if !runtime.ContainsUsageLimitChoiceModal(pane) {
		return false
	}
	isAttached, err := attached()
	if err != nil {
		onProbeError("attachment", err)
		return false
	}
	// An attached human can answer the dialog themselves, and choosing between
	// waiting and buying more usage is theirs to make.
	return !isAttached
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
func usageLimitModalWedgePatch(now time.Time) sessionpkg.MetadataPatch {
	return sessionpkg.MetadataPatch{
		sessionHealthStateMetadataKey:  "unhealthy",
		sessionHealthReasonMetadataKey: sessionHealthReasonUsageLimitModal,
		"quarantined_until":            now.UTC().Add(defaultRateLimitQuarantineDuration).Format(time.RFC3339),
	}
}

// usageLimitModalWedgeRecorded reports whether a session already carries an
// unexpired usage-limit-modal quarantine. It bounds reporting to once per
// quarantine window for a wedge that outlives its restart request — a pinned
// named session declines the abrupt kill — so the condition stays visible
// without re-announcing itself every reconciler tick.
func usageLimitModalWedgeRecorded(info sessionpkg.Info, now time.Time) bool {
	if strings.TrimSpace(info.HealthReason) != sessionHealthReasonUsageLimitModal {
		return false
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(info.QuarantinedUntil))
	if err != nil {
		return false
	}
	return now.Before(until)
}
