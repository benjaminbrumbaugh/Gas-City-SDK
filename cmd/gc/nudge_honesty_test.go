package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/worker"
)

// The nudge-honesty rows. Every one of these is a place the nudge path told an
// operator something that was not true: a store that would not open reported no
// cause, and a queued item reported plain success whether or not the live leg
// had even been attempted.

// TestOpenNudgeBeadStoreReportsWhyItCouldNotOpen: the seam used by the poll and
// drain helpers stays nil-tolerant (their contract is "no store means do
// nothing"), but the error form the operator-facing call sites use must carry
// the cause.
func TestOpenNudgeBeadStoreReportsWhyItCouldNotOpen(t *testing.T) {
	// A regular file where the city directory should be: the open genuinely
	// fails, which is the case the swallowed error used to render as an
	// unexplained "opening city store for X".
	notACity := filepath.Join(t.TempDir(), "city-is-a-file")
	if err := os.WriteFile(notACity, []byte("not a city"), 0o600); err != nil {
		t.Fatalf("seeding the fixture: %v", err)
	}

	store, err := openNudgeBeadStoreErr(notACity)
	if err == nil {
		t.Skipf("openNudgeBeadStoreErr(%q) opened a store over a plain file; this row needs a fixture the store layer actually refuses", notACity)
	}
	if store.Store != nil {
		t.Fatal("a failed open returned a usable store")
	}
	if !strings.Contains(err.Error(), notACity) {
		t.Fatalf("error = %q, want the offending city path named", err)
	}
}

// TestQueuedNudgeResultNamesTheQueueAndTheDowngrade: "Queued nudge for X" was
// printed identically whether the nudge had been queued by request or silently
// downgraded from a live delivery the provider cannot take — and it never said
// WHERE the item went. The queue's authority is the flock'd state.json (the
// shadow bead is a projection of it), so that path is what an operator needs.
func TestQueuedNudgeResultNamesTheQueueAndTheDowngrade(t *testing.T) {
	cityPath := t.TempDir()
	target := nudgeTarget{
		cityPath: cityPath,
		alias:    "worker-1",
		agent:    config.Agent{Name: "worker"},
		resolved: &config.ResolvedProvider{Name: "codex"},
	}

	var stdout, stderr bytes.Buffer
	if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryWaitIdle, false,
		worker.NudgeUndeliveredProviderUnsupported, nudgeQueueProspectReachable, &stdout, &stderr); code != 0 {
		t.Fatalf("writeQueuedSessionNudgeResult = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, nudgequeue.StatePath(cityPath)) {
		t.Fatalf("queued message = %q, want the state.json queue path named", out)
	}
	if !strings.Contains(out, "live delivery is unsupported") || !strings.Contains(out, "codex") {
		t.Fatalf("queued message = %q, want the skipped live leg and its provider named", out)
	}

	// Control: a nudge queued BY REQUEST carries no downgrade note — the note
	// must describe something that happened, not decorate every queue write.
	stdout.Reset()
	if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryQueue, false, "", nudgeQueueProspectReachable, &stdout, &stderr); code != 0 {
		t.Fatalf("writeQueuedSessionNudgeResult = %d, want 0", code)
	}
	out = stdout.String()
	if strings.Contains(out, "unsupported") || strings.Contains(out, "idle boundary") {
		t.Fatalf("queued-by-request message = %q, want no downgrade note", out)
	}
	if !strings.Contains(out, nudgequeue.StatePath(cityPath)) {
		t.Fatalf("queued-by-request message = %q, want the queue path named", out)
	}
}

// TestQueuedNudgeDowngradeNoteDistinguishesItsCauses keeps the two downgrades
// distinguishable: an unsupported transport is a permanent property of the
// runtime, while a missed idle boundary is a transient state of the session, and
// an operator acts differently on each.
func TestQueuedNudgeDowngradeNoteDistinguishesItsCauses(t *testing.T) {
	target := nudgeTarget{resolved: &config.ResolvedProvider{Name: "codex"}}
	unsupported := queuedNudgeDowngradeNote(target, worker.NudgeUndeliveredProviderUnsupported)
	noIdle := queuedNudgeDowngradeNote(target, worker.NudgeUndeliveredNoIdleBoundary)
	if unsupported == "" || noIdle == "" || unsupported == noIdle {
		t.Fatalf("downgrade notes must differ and be non-empty; unsupported=%q no-idle=%q", unsupported, noIdle)
	}
	if got := queuedNudgeDowngradeNote(target, ""); got != "" {
		t.Fatalf("note for a non-downgrade = %q, want empty", got)
	}
}

// TestManagedNudgeWakeReportsASkippedWake: the enqueue succeeded and the wake did
// not. Returning nil for both made a queued-but-unwoken nudge indistinguishable
// from a delivered one.
func TestManagedNudgeWakeReportsASkippedWake(t *testing.T) {
	var warnings bytes.Buffer
	prev := nudgeWarningWriter
	nudgeWarningWriter = &warnings
	t.Cleanup(func() { nudgeWarningWriter = prev })

	target := nudgeTarget{cityPath: t.TempDir(), alias: "worker-1", agent: config.Agent{Name: "worker"}}
	if err := requestManagedNudgeWake(target, nil); err != nil {
		t.Fatalf("requestManagedNudgeWake = %v, want nil (the enqueue still stands)", err)
	}
	if !strings.Contains(warnings.String(), "no managed wake was requested") {
		t.Fatalf("warnings = %q, want the skipped wake reported", warnings.String())
	}
	if !strings.Contains(warnings.String(), "no session store") {
		t.Fatalf("warnings = %q, want the missing precondition named", warnings.String())
	}
}

// TestQueuedNudgeSaysWhenNothingWillAttemptIt is gc-py7pc's sender-side row, and
// it is the largest of them: the sender was told the enqueue worked and nothing
// else, for an item no dispatch tick would ever enumerate.
//
// dispatchAllQueuedNudges walks OPEN SESSIONS asking each "is anything queued
// for you?". Nothing walks the queue asking "can this be delivered?", so a nudge
// addressed to an agent with no open session is never enumerated at all — not
// delivered, not skipped, not counted — and it expires at attempts=0. The cost
// on the record: a "MAYOR STAND-DOWN — STOP EDITING NOW" nudge to gastown.slit,
// sent while two sessions were writing one worktree, sat unattempted for sixteen
// hours because slit's session had exited. `gc session nudge` had reported
// success.
//
// The warning goes to STDERR on purpose. The queue path's stdout line is
// consumed by callers that want the state.json path, and a warning folded into
// it would be captured and dropped.
func TestQueuedNudgeSaysWhenNothingWillAttemptIt(t *testing.T) {
	cityPath := t.TempDir()
	target := nudgeTarget{
		cityPath: cityPath,
		alias:    "gastown.slit",
		agent:    config.Agent{Name: "slit"},
		resolved: &config.ResolvedProvider{Name: "codex"},
	}

	var stdout, stderr bytes.Buffer
	if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryQueue, false, "",
		nudgeQueueProspectNoOpenSession, &stdout, &stderr); code != 0 {
		t.Fatalf("writeQueuedSessionNudgeResult = %d, want 0 (the enqueue did succeed)", code)
	}
	// The enqueue succeeded and stdout still says so: this row is about adding
	// the second fact, not about retracting the first.
	if !strings.Contains(stdout.String(), nudgequeue.StatePath(cityPath)) {
		t.Fatalf("stdout = %q, want the queue path still named", stdout.String())
	}
	warn := stderr.String()
	if !strings.Contains(warn, "no live session") {
		t.Fatalf("stderr = %q, want the absent session named as the reason", warn)
	}
	if !strings.Contains(warn, "will NOT be attempted") {
		t.Fatalf("stderr = %q, want the consequence stated, not merely the state", warn)
	}
	if !strings.Contains(warn, "gastown.slit") {
		t.Fatalf("stderr = %q, want the addressee named", warn)
	}
	// The remedy is not obvious from the warning alone, and it is the whole
	// operating rule the city adopted while this was broken.
	if !strings.Contains(warn, "durable bead state") {
		t.Fatalf("stderr = %q, want the remedy named", warn)
	}

	// Control: a reachable addressee must produce NO warning, or the warning
	// becomes noise on every queue write and stops being read.
	stdout.Reset()
	stderr.Reset()
	if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryQueue, false, "",
		nudgeQueueProspectReachable, &stdout, &stderr); code != 0 {
		t.Fatalf("writeQueuedSessionNudgeResult = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silence for a reachable addressee", stderr.String())
	}

	// A failed probe must say "unknown" rather than pick an answer. Asserting
	// either way here would be a fresh falsehood in the file that exists to
	// stop them.
	stdout.Reset()
	stderr.Reset()
	if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryQueue, false, "",
		nudgeQueueProspectUnknown, &stdout, &stderr); code != 0 {
		t.Fatalf("writeQueuedSessionNudgeResult = %d, want 0", code)
	}
	if unknown := stderr.String(); !strings.Contains(unknown, "could not determine") {
		t.Fatalf("stderr = %q, want the unknown case reported as unknown", unknown)
	}
}

// TestQueuedNudgeJSONCarriesTheAttemptProspect: the human warning above is not
// enough on its own. Agents call this with --json and branch on fields, and both
// `ok` and `queued` stayed true for an item nothing would attempt, so a scripted
// sender had nothing to test. `ok` deliberately remains true — the enqueue did
// succeed, and callers already branch on it — so the second fact needs a field
// of its own.
func TestQueuedNudgeJSONCarriesTheAttemptProspect(t *testing.T) {
	cityPath := t.TempDir()
	target := nudgeTarget{
		cityPath: cityPath,
		alias:    "gastown.slit",
		agent:    config.Agent{Name: "slit"},
		resolved: &config.ResolvedProvider{Name: "codex"},
	}

	for _, tc := range []struct {
		name     string
		prospect nudgeQueueProspect
		want     string
	}{
		{"stranded", nudgeQueueProspectNoOpenSession, "no-open-session"},
		{"reachable", nudgeQueueProspectReachable, "reachable"},
		{"unknown", nudgeQueueProspectUnknown, "unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := writeQueuedSessionNudgeResult(target, nudgeDeliveryQueue, true, "",
			tc.prospect, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: writeQueuedSessionNudgeResult = %d, want 0; stderr=%s", tc.name, code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, `"attempt_prospect":"`+tc.want+`"`) {
			t.Fatalf("%s: json = %q, want attempt_prospect %q", tc.name, out, tc.want)
		}
		// The enqueue succeeded in every one of these cases.
		if !strings.Contains(out, `"ok":true`) {
			t.Fatalf("%s: json = %q, want ok:true — the enqueue succeeded", tc.name, out)
		}
	}
}

// TestNudgeAttemptSummaryDistinguishesNeverAttempted: `gc nudge status` rendered
// every pending item identically, so "no tick has ever looked at this" and
// "delivery is being tried and failing" were the same line. They have opposite
// remedies — the first is an absent session (gc-py7pc), the second a transport
// fault (gc-syqor's territory) — and the stand-down order that rotted for
// sixteen hours read exactly like a nudge being retried.
//
// This cannot be left to the JSON: `omitempty` does not omit a zero struct, so
// last_attempt_at renders as the 0001-01-01T00:00:00Z sentinel, which reads as
// data rather than as absence.
func TestNudgeAttemptSummaryDistinguishesNeverAttempted(t *testing.T) {
	never := newQueuedNudge("gastown.slit", "MAYOR STAND-DOWN — STOP EDITING NOW", time.Now())
	if got := nudgeAttemptSummary(never); !strings.Contains(got, "never-attempted") {
		t.Fatalf("nudgeAttemptSummary(fresh) = %q, want it marked never-attempted", got)
	}

	tried := never
	tried.Attempts = 5
	tried.LastAttemptAt = time.Date(2026, 9, 1, 10, 35, 25, 0, time.UTC)
	got := nudgeAttemptSummary(tried)
	if strings.Contains(got, "never-attempted") {
		t.Fatalf("nudgeAttemptSummary(tried) = %q, must not claim never-attempted", got)
	}
	if !strings.Contains(got, "attempts=5") || !strings.Contains(got, "2026-09-01T10:35:25Z") {
		t.Fatalf("nudgeAttemptSummary(tried) = %q, want the count and the last attempt instant", got)
	}

	// A nonzero count with a zero timestamp is inconsistent state; it must read
	// as never-attempted rather than print the 0001-01-01 sentinel at an
	// operator.
	inconsistent := never
	inconsistent.Attempts = 2
	if got := nudgeAttemptSummary(inconsistent); strings.Contains(got, "0001-01-01") {
		t.Fatalf("nudgeAttemptSummary(inconsistent) = %q, must not surface the zero-time sentinel", got)
	}
}
