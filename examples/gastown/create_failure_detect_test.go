package gastown_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/session"
)

// Canonical close reasons stamped by session.CanonicalCloseReason. The
// detector buckets on the create-failed one; everything else counts as a
// normal closure.
const (
	failedCreateCloseReason = "session create failed: aborted before creation_complete"
	drainedCloseReason      = "session drained: pool slot retired by reconciler"
)

// sessionCloseFixture is the subset of a closed session bead that
// create-failure-detect reads: the close_reason it buckets on and the
// template it attributes the failure to.
type sessionCloseFixture struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Type     string            `json:"issue_type"`
	Metadata map[string]string `json:"metadata"`
}

// createFailureFixture builds a `gc bd list --json` payload with failed
// create-rollbacks against a template and drained (healthy) closures.
func createFailureFixture(t *testing.T, path string, failed int, failedTemplate string, drained int, drainedTemplate string) {
	t.Helper()
	beads := make([]sessionCloseFixture, 0, failed+drained)
	for i := 0; i < failed; i++ {
		beads = append(beads, sessionCloseFixture{
			ID:     fmt.Sprintf("gc-fail%02d", i),
			Status: "closed",
			Type:   "session",
			Metadata: map[string]string{
				"close_reason": failedCreateCloseReason,
				"template":     failedTemplate,
				"state":        "failed-create",
			},
		})
	}
	for i := 0; i < drained; i++ {
		beads = append(beads, sessionCloseFixture{
			ID:     fmt.Sprintf("gc-ok%02d", i),
			Status: "closed",
			Type:   "session",
			Metadata: map[string]string{
				"close_reason": drainedCloseReason,
				"template":     drainedTemplate,
				"state":        "drained",
			},
		})
	}
	data, err := json.Marshal(beads)
	if err != nil {
		t.Fatalf("Marshal(session fixture): %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// createFailureDetectEnv wires a stubbed gc + escalate hook around the
// detector and returns the env plus the two logs the assertions read.
func createFailureDetectEnv(t *testing.T, fixture string, overrides map[string]string) (env map[string]string, gcLog, escalationLog string) {
	t.Helper()
	cityDir := t.TempDir()
	binDir := t.TempDir()
	stateDir := t.TempDir()
	logDir := t.TempDir()
	gcLog = filepath.Join(logDir, "gc.log")
	escalationLog = filepath.Join(logDir, "escalation.log")
	escalateStub := filepath.Join(binDir, "escalate-stub.sh")

	writeExecutable(t, filepath.Join(binDir, "gc"), `#!/bin/sh
printf '%s\n' "$*" >> "$GC_CALL_LOG"
if [ "${1:-}" = "bd" ] && [ "${2:-}" = "list" ]; then
  cat "$GC_SESSION_FIXTURE"
fi
exit 0
`)
	writeExecutable(t, escalateStub, `#!/bin/sh
printf '%s\n' "$*" >> "$GC_ESCALATION_LOG"
exit 0
`)

	env = map[string]string{
		"GC_CALL_LOG":         gcLog,
		"GC_ESCALATION_LOG":   escalationLog,
		"GC_SESSION_FIXTURE":  fixture,
		"GC_ESCALATE_SCRIPT":  escalateStub,
		"GC_CITY":             cityDir,
		"GC_CITY_PATH":        cityDir,
		"GC_PACK_STATE_DIR":   stateDir,
		"PATH":                binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL":   filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	for k, v := range overrides {
		env[k] = v
	}
	return env, gcLog, escalationLog
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

// countEscalations counts escalate invocations. The message body spans many
// lines, so invocations are counted by the once-per-call --subject flag
// rather than by log lines.
func countEscalations(log string) int {
	return strings.Count(log, "--subject")
}

// TestCreateFailureDetectEscalatesWhenShareExceedsThreshold pins the core
// contract: a create-failure regime like 2026-08-05 (97.5% of all session
// closures were failed creates) must escalate. That outage ran ~19 hours
// entirely unnoticed because nothing watched this ratio.
func TestCreateFailureDetectEscalatesWhenShareExceedsThreshold(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 97, "bd.dog", 3, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	got := readLog(t, escalationLog)
	if got == "" {
		t.Fatal("no escalation for a 97% create-failure share; the 2026-08-05 outage would still be silent")
	}
	if !strings.Contains(got, "97") {
		t.Errorf("escalation = %q, want the failure share reported", got)
	}
	if !strings.Contains(got, "bd.dog") {
		t.Errorf("escalation = %q, want the worst-offending template named", got)
	}
}

// TestCreateFailureDetectStaysQuietBelowThreshold guards against alert
// fatigue: a low background rate of failed creates is normal and must not
// page. The fleet runs a few percent on ordinary days.
func TestCreateFailureDetectStaysQuietBelowThreshold(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 5, "factory-router", 95, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := readLog(t, escalationLog); got != "" {
		t.Fatalf("escalated on a 5%% background failure rate: %q", got)
	}
}

// TestCreateFailureDetectIgnoresSmallSamples pins the absolute floor. A
// quiet city that closes four sessions in the window, three of them failed
// creates, is 75% — a percentage over small N is noise, not an outage.
func TestCreateFailureDetectIgnoresSmallSamples(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 3, "factory-router", 1, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := readLog(t, escalationLog); got != "" {
		t.Fatalf("escalated on a 4-closure sample: %q", got)
	}
}

// TestCreateFailureDetectBoundsQueryToWindow keeps the detector cheap. The
// city retains closed session beads for 720h (reaper's purge age); an
// unbounded list would pull tens of thousands of beads every 5 minutes.
func TestCreateFailureDetectBoundsQueryToWindow(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 1, "factory-router", 99, "Gas-City-SDK/gastown.polecat")

	env, gcLog, _ := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	got := readLog(t, gcLog)
	if !strings.Contains(got, "--closed-after") {
		t.Errorf("gc calls = %q, want the list bounded with --closed-after", got)
	}
	if !strings.Contains(got, "--type=session") {
		t.Errorf("gc calls = %q, want the list restricted to session beads", got)
	}
	if !strings.Contains(got, "--status=closed") {
		t.Errorf("gc calls = %q, want the list restricted to closed beads", got)
	}
}

// TestCreateFailureDetectSuppressesRepeatAlertsWithinCooldown keeps a long
// outage from flooding mail. The 2026-08-05 regime ran ~19 hours; at the
// order's 5-minute cadence an uncooled detector would have sent ~228 mails,
// and every mail is a bead plus a Dolt commit.
func TestCreateFailureDetectSuppressesRepeatAlertsWithinCooldown(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 97, "bd.dog", 3, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := countEscalations(readLog(t, escalationLog)); got != 1 {
		t.Fatalf("escalated %d times within the cooldown, want 1:\n%s", got, readLog(t, escalationLog))
	}
}

// TestCreateFailureDetectRealertsAfterCooldown pins the other half of the
// cooldown: an outage that outlives the window must page again rather than
// going quiet forever after one alert.
func TestCreateFailureDetectRealertsAfterCooldown(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 97, "bd.dog", 3, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, map[string]string{
		"GC_CREATE_FAILURE_ALERT_COOLDOWN": "0",
	})
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := countEscalations(readLog(t, escalationLog)); got != 2 {
		t.Fatalf("escalated %d times with the cooldown disabled, want 2:\n%s", got, readLog(t, escalationLog))
	}
}

// TestCreateFailureDetectHandlesEmptyWindow guards the quiet path: a city
// with no closed sessions in the window must exit clean, not divide by zero
// or escalate.
func TestCreateFailureDetectHandlesEmptyWindow(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 0, "", 0, "")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := readLog(t, escalationLog); got != "" {
		t.Fatalf("escalated on an empty window: %q", got)
	}
}

// TestCreateFailureDetectThresholdIsConfigurable lets an operator tighten
// the ratio without editing the pack.
func TestCreateFailureDetectThresholdIsConfigurable(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 20, "factory-router", 80, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, map[string]string{
		"GC_CREATE_FAILURE_THRESHOLD": "15",
	})
	runScript(t, coreScriptPath("create-failure-detect.sh"), env)

	if got := readLog(t, escalationLog); got == "" {
		t.Fatal("no escalation at a 20% share with the threshold lowered to 15%")
	}
}

// TestCreateFailureDetectMatchesCanonicalCloseReason binds the detector's
// close_reason prefix to the Go constant that produces it. The prefix is the
// detector's only input signal: if CanonicalCloseReason("failed-create") is
// ever reworded past the prefix, the ratio silently reads 0% during a total
// spawn outage — the same blindness that let the 2026-08-05 outage run ~19
// hours unnoticed. This test fails the build instead.
func TestCreateFailureDetectMatchesCanonicalCloseReason(t *testing.T) {
	canonical := session.CanonicalCloseReason("failed-create")
	if canonical != failedCreateCloseReason {
		t.Fatalf("CanonicalCloseReason(%q) = %q, test fixture uses %q", "failed-create", canonical, failedCreateCloseReason)
	}

	data, err := os.ReadFile(coreScriptPath("create-failure-detect.sh"))
	if err != nil {
		t.Fatalf("reading create-failure-detect.sh: %v", err)
	}
	prefix := scriptShellValue(t, string(data), "FAILED_CREATE_PREFIX")
	if prefix == "" {
		t.Fatal("create-failure-detect.sh does not define FAILED_CREATE_PREFIX")
	}
	if !strings.HasPrefix(canonical, prefix) {
		t.Fatalf("detector matches prefix %q, which no longer prefixes the canonical close reason %q", prefix, canonical)
	}
}

// scriptShellValue extracts a double-quoted top-level assignment (NAME="…")
// from a shell script body.
func scriptShellValue(t *testing.T, body, name string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		want := name + `="`
		if !strings.HasPrefix(trimmed, want) {
			continue
		}
		rest := trimmed[len(want):]
		if end := strings.Index(rest, `"`); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

// TestCreateFailureDetectLoudFailsWhenEscalationFails pins the loud-fail
// contract. The controller logs an exec order's output only on a non-zero
// exit, so an undeliverable alert must exit non-zero — a detector that
// swallows its own delivery failure reproduces the silence it exists to
// remove. The cooldown must also stay unwritten so the next run retries.
func TestCreateFailureDetectLoudFailsWhenEscalationFails(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sessions.json")
	createFailureFixture(t, fixture, 97, "bd.dog", 3, "Gas-City-SDK/gastown.polecat")

	env, _, escalationLog := createFailureDetectEnv(t, fixture, nil)
	writeExecutable(t, env["GC_ESCALATE_SCRIPT"], `#!/bin/sh
printf '%s\n' "$*" >> "$GC_ESCALATION_LOG"
exit 1
`)

	out, err := runScriptResult(t, coreScriptPath("create-failure-detect.sh"), env)
	if err == nil {
		t.Fatalf("exited 0 after a failed escalation; the controller only logs exec output on a non-zero exit\n%s", out)
	}
	if !strings.Contains(string(out), "escalation failed") {
		t.Errorf("output = %q, want the undeliverable alert surfaced", out)
	}

	// Cooldown unwritten: a second run must attempt the alert again.
	if _, err := runScriptResult(t, coreScriptPath("create-failure-detect.sh"), env); err == nil {
		t.Fatal("second run exited 0; a failed escalation must not arm the cooldown")
	}
	if got := countEscalations(readLog(t, escalationLog)); got != 2 {
		t.Errorf("escalate attempted %d times, want 2 — a failed alert must be retried", got)
	}
}
