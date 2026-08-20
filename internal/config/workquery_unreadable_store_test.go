package config

import (
	"strings"
	"testing"
)

// fakeBDServesReady answers `bd ready` with one routed bead and everything else
// with an empty array, so a healthy store has work to find.
const fakeBDServesReady = `#!/bin/sh
case "$1" in
  ready) printf '[{"id":"sk-1","status":"open"}]' ;;
  *) printf '[]' ;;
esac
`

// TestEffectiveWorkQueryRefusesEmptyAnswerWhenStoreUnreadable is the gc-mece
// regression. Every single-store probe tier suppresses its own stderr and
// ignores its exit status, and the script used to end in an unconditional
// `printf "[]"`. So a store that fails EVERY read — the live trigger was a bd
// binary 4 schema migrations behind its store — produced `[]` and exit 0. That
// is byte-identical to a genuinely empty hook, so `gc hook` told every agent it
// had no work, with nothing on stderr to say otherwise.
//
// A wholly unreadable store must not be laundered into a confident negative.
// fakeBDFails is the shared "every subcommand fails" stand-in, the same one the
// fall-through case used before that case was narrowed to what it actually
// protects.
func TestEffectiveWorkQueryRefusesEmptyAnswerWhenStoreUnreadable(t *testing.T) {
	a := &Agent{Name: "worker"}
	res := runGeneratedQueryWithBD(t, a.EffectiveWorkQueryFor(singleStoreTopology()), map[string]string{
		"GC_SESSION_ORIGIN": "ephemeral",
	}, fakeGCReadyFails, fakeBDFails)

	if res.exit == 0 {
		t.Errorf("exit = 0, want non-zero — an unreadable store must not answer successfully")
	}
	if strings.TrimSpace(res.stdout) == "[]" {
		t.Errorf("stdout = %q, want no empty-array answer — that is indistinguishable from a genuinely empty hook", res.stdout)
	}
	if !strings.Contains(res.stderr, "store unreadable") {
		t.Errorf("stderr = %q, want a diagnostic naming the unreadable store", res.stderr)
	}
	if !strings.Contains(res.stderr, "bd: store unreadable") {
		t.Errorf("stderr = %q, want the reader's own error carried through so the cause is visible", res.stderr)
	}
}

// TestEffectiveWorkQueryStillReportsEmptyWhenStoreIsHealthy is the guard on the
// fix above: a readable store with no matching work must keep answering `[]`
// successfully. Turning a normal idle hook into an error would be far worse than
// the bug — every agent in the city would fail its boot check every tick.
func TestEffectiveWorkQueryStillReportsEmptyWhenStoreIsHealthy(t *testing.T) {
	a := &Agent{Name: "worker"}
	res := runGeneratedQueryWithBD(t, a.EffectiveWorkQueryFor(singleStoreTopology()), map[string]string{
		"GC_SESSION_ORIGIN": "ephemeral",
	}, fakeGCReadyFails, fakeBDEmpty)

	if res.exit != 0 {
		t.Errorf("exit = %d, want 0 — a healthy store with no work must still succeed (stderr=%q)", res.exit, res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "[]" {
		t.Errorf("stdout = %q, want []", res.stdout)
	}
}

// TestEffectiveWorkQueryStillReturnsWorkWhenStoreIsHealthy pins that the guard
// is only reached on the no-work path: when a probe tier finds work the script
// prints it and exits 0 before the guard's extra round-trip can run.
func TestEffectiveWorkQueryStillReturnsWorkWhenStoreIsHealthy(t *testing.T) {
	a := &Agent{Name: "worker"}
	res := runGeneratedQueryWithBD(t, a.EffectiveWorkQueryFor(singleStoreTopology()), map[string]string{
		"GC_SESSION_ORIGIN": "ephemeral",
	}, fakeGCReadyFails, fakeBDServesReady)

	if res.exit != 0 {
		t.Errorf("exit = %d, want 0 (stderr=%q)", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "sk-1") {
		t.Errorf("stdout = %q, want the routed bead", res.stdout)
	}
}
