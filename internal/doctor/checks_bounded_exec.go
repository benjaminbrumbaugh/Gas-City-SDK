package doctor

import (
	"fmt"
	"os/exec"
)

// boundedExecPreferred are the external bounded-execution helpers, in the
// order assets/scripts/bounded.sh resolves them. Keep this list and its
// order in sync with that script: a doctor check that disagrees with the
// runtime about which mechanism is in play is worse than no check at all.
var boundedExecPreferred = []string{"gtimeout", "timeout"}

// BoundedExecCheck reports which bounded-execution mechanism the Dolt
// maintenance scripts will use on this host.
//
// It exists because the absence of coreutils' timeout(1) was, for a long
// time, invisible: doctor verified tmux, git, jq, pgrep and lsof but not
// timeout, so a stock macOS host looked fully provisioned while every
// `timeout N gc dolt ...` in a patrol runbook died with "command not
// found". That failure is indistinguishable from a wedged data plane, and
// it routed patrolling agents toward restarting Dolt — destroying the
// evidence the runbook exists to preserve.
//
// This is deliberately not a BinaryCheck. A missing timeout is not a fault
// on macOS, it is the normal state; bounded.sh falls back to python3 and
// then to a portable shell watchdog, both of which bound the child for
// real. Reporting StatusError for the common case would fabricate exactly
// the kind of false alarm this check was added to prevent. So the result
// names the mechanism that will actually be used, and only warns —
// advisory, never gating — when the preferred helper is absent.
type BoundedExecCheck struct {
	lookPath LookPathFunc
}

// NewBoundedExecCheck creates a bounded-execution capability check.
func NewBoundedExecCheck(lp LookPathFunc) *BoundedExecCheck {
	if lp == nil {
		lp = exec.LookPath
	}
	return &BoundedExecCheck{lookPath: lp}
}

// Name returns the check identifier.
func (c *BoundedExecCheck) Name() string { return "bounded-exec" }

// WarmupEligible returns false: this is host provisioning, not a
// steady-state condition worth re-scanning on every `gc start`.
func (c *BoundedExecCheck) WarmupEligible() bool { return false }

// CanFix returns false; installing coreutils is an operator decision.
func (c *BoundedExecCheck) CanFix() bool { return false }

// Fix is a no-op.
func (c *BoundedExecCheck) Fix(_ *CheckContext) error { return nil }

// Run reports the bounded-execution mechanism available on this host.
func (c *BoundedExecCheck) Run(_ *CheckContext) *CheckResult {
	r := &CheckResult{Name: c.Name(), Severity: SeverityAdvisory}

	for _, bin := range boundedExecPreferred {
		if path, err := c.lookPath(bin); err == nil {
			r.Status = StatusOK
			r.Message = fmt.Sprintf("bounded via %s (%s)", bin, path)
			return r
		}
	}

	// No coreutils. The fallbacks bound the child correctly, but they do
	// not accept suffixed durations ("30s", "5m") the way timeout(1)
	// does, so callers must pass bare seconds. Say so rather than
	// reporting a bare OK.
	const suffixNote = "fallbacks take bare seconds, not suffixed durations like \"30s\""
	r.Status = StatusWarning
	r.FixHint = "install coreutils for gtimeout (brew install coreutils), or ignore — the fallback bound is real"

	if path, err := c.lookPath("python3"); err == nil {
		r.Message = fmt.Sprintf("no timeout/gtimeout on PATH — bounded via python3 (%s); %s", path, suffixNote)
		return r
	}

	r.Message = "no timeout, gtimeout, or python3 on PATH — bounded via the portable shell watchdog; " + suffixNote
	return r
}
