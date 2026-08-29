package dolt_test

// Push-failure diagnostics (gc-lnf79). A failed `gc dolt sync` push is not one
// undifferentiated event to an unattended patrol, and reporting every one as a
// bare "ERROR: push failed (exit 1)" cost this city a full day of backups.
//
// Two distinctions have to survive into the output, and into the trailing
// summary line that the OrderFailed tail keeps:
//
//   1. A FIRST push failing means the store has no off-box copy at all, not a
//      stale one. That is a different severity from a missed incremental delta
//      and must not read the same.
//   2. "connection was closed" / "row read wait bigger than connection
//      timeout" is the Dolt sql-server tearing the client connection down
//      mid-push at its listener read_timeout. Raising
//      GC_DOLT_SYNC_PUSH_TIMEOUT_SECS cannot help, so the message must not
//      leave an operator tuning the client bound.

import (
	"path/filepath"
	"strings"
	"testing"
)

// connClosedStderr is the verbatim diagnostic Dolt 2.1.0 returns when the
// sql-server closes the client connection while DOLT_PUSH is still
// transferring — captured from this city's supervisor.log on 2026-08-29.
const connClosedStderr = "error on line 1 for query CALL DOLT_PUSH('origin', 'main'): " +
	"Error 1105 (HY000): unknown push error; connection was closed"

// writeFFFakeDoltPushFailing installs a fake dolt whose pre-push DOLT_FETCH
// takes fetchArm (a `case` body deciding first-push vs classified) and whose
// DOLT_PUSH fails exit 1 with the given stderr — the shape every real
// connection-closed and authentication failure arrives in.
func writeFFFakeDoltPushFailing(t *testing.T, dir, fetchArm, stderr string) {
	t.Helper()
	escaped := strings.ReplaceAll(stderr, "'", `'\''`)
	body := fakeDoltHeader(filepath.Join(dir, "dolt.log"), "main") +
		fetchArm +
		"  *DOLT_PUSH*)\n" +
		"    printf '%s\\n' '" + escaped + "' >&2\n" +
		"    exit 1 ;;\n" +
		"esac\nexit 0\n"
	installFFFakeDolt(t, dir, body)
}

// firstPushFetchArm models an empty remote: DOLT_FETCH reports no branches, so
// sync classifies the push as a first push.
const firstPushFetchArm = "  *\"CALL DOLT_FETCH(\"*) printf 'fetch failed: no branches found in remote\\n' >&2 ; exit 1 ;;\n"

// aheadFetchArm models an established remote ref with the local branch two
// commits ahead — an ordinary incremental push, not a first push.
const aheadFetchArm = "  *\"CALL DOLT_FETCH(\"*) exit 0 ;;\n" +
	"  *\"dolt_log('remotes/origin/main..main')\"*) printf 'n\\n2\\n' ; exit 0 ;;\n" +
	"  *\"dolt_log('main..remotes/origin/main')\"*) printf 'n\\n0\\n' ; exit 0 ;;\n"

// TestSyncFirstPushFailureNamesNoOffBoxCopy pins the severity distinction: a
// failed first push says the store has NO off-box copy, in the per-db output
// and in the trailing summary the OrderFailed tail preserves.
func TestSyncFirstPushFailureNamesNoOffBoxCopy(t *testing.T) {
	binDir := t.TempDir()
	writeFFFakeDoltPushFailing(t, binDir, firstPushFetchArm, connClosedStderr)
	out := runFFSync(t, binDir, "--db", "app")

	if !strings.Contains(out, "FIRST PUSH FAILED") {
		t.Fatalf("a failed first push must be reported distinctly, got:\n%s", out)
	}
	if !strings.Contains(out, "NO off-box copy") {
		t.Fatalf("a failed first push must say the store has no off-box copy, got:\n%s", out)
	}
	// The summary line is the one guaranteed to survive OrderFailed truncation.
	summary := lastLineContaining(out, "database(s) failed")
	if summary == "" {
		t.Fatalf("expected a trailing failure summary line, got:\n%s", out)
	}
	if !strings.Contains(summary, "FIRST PUSH FAILED") {
		t.Fatalf("summary must carry the first-push severity, got:\n%s", summary)
	}
}

// TestSyncPushCutByServerNamesReadTimeout pins that the connection-closed
// signature is attributed to the server's listener read_timeout and that the
// message steers away from the client bound, which cannot fix it.
func TestSyncPushCutByServerNamesReadTimeout(t *testing.T) {
	binDir := t.TempDir()
	writeFFFakeDoltPushFailing(t, binDir, aheadFetchArm, connClosedStderr)
	out := runFFSync(t, binDir, "--db", "app")

	if !strings.Contains(out, "read_timeout") {
		t.Fatalf("connection-closed push failure must name read_timeout, got:\n%s", out)
	}
	if !strings.Contains(out, "read_timeout_millis") {
		t.Fatalf("expected the actionable city.toml knob, got:\n%s", out)
	}
	if !strings.Contains(out, "cut by the Dolt server") {
		t.Fatalf("expected the failure attributed to the server, got:\n%s", out)
	}
	// An ordinary incremental push must not be mislabelled a first push.
	if strings.Contains(out, "FIRST PUSH FAILED") {
		t.Fatalf("ahead-only push must not be reported as a first push, got:\n%s", out)
	}
}

// TestSyncFirstPushCutByServerReportsBoth is the exact incident shape: the city
// store had no remote ref AND the push was cut by the server. Both facts must
// appear; suppressing either loses half the diagnosis.
func TestSyncFirstPushCutByServerReportsBoth(t *testing.T) {
	binDir := t.TempDir()
	writeFFFakeDoltPushFailing(t, binDir, firstPushFetchArm, connClosedStderr)
	out := runFFSync(t, binDir, "--db", "app")

	if !strings.Contains(out, "FIRST PUSH FAILED") || !strings.Contains(out, "cut by the Dolt server") {
		t.Fatalf("first-push + server-cut must report both facts, got:\n%s", out)
	}
	if !strings.Contains(out, connClosedStderr) {
		t.Fatalf("underlying dolt stderr must still be replayed, got:\n%s", out)
	}
}

// TestSyncUnrelatedPushFailureStaysGeneric guards the other direction: an
// ordinary push failure with an unrelated diagnostic must not acquire either
// banner, or the loud signals stop meaning anything.
func TestSyncUnrelatedPushFailureStaysGeneric(t *testing.T) {
	binDir := t.TempDir()
	writeFFFakeDoltPushFailing(t, binDir, aheadFetchArm, "fatal: authentication required")
	out := runFFSync(t, binDir, "--db", "app")

	if !strings.Contains(out, "ERROR: push failed (exit 1)") {
		t.Fatalf("expected the generic failure line, got:\n%s", out)
	}
	if strings.Contains(out, "FIRST PUSH FAILED") {
		t.Fatalf("unrelated failure must not claim a first push, got:\n%s", out)
	}
	if strings.Contains(out, "cut by the Dolt server") {
		t.Fatalf("unrelated failure must not claim a server cut, got:\n%s", out)
	}
	if !strings.Contains(out, "fatal: authentication required") {
		t.Fatalf("underlying stderr must still be replayed, got:\n%s", out)
	}
}

// TestSyncSucceedingFirstPushEmitsNoBanner keeps the diagnostics on the failure
// path only: a first push that works is an ordinary success.
func TestSyncSucceedingFirstPushEmitsNoBanner(t *testing.T) {
	binDir := t.TempDir()
	logPath := installFFFakeDolt(t, binDir, fakeDoltHeader(filepath.Join(binDir, "dolt.log"), "main")+
		firstPushFetchArm+"esac\nexit 0\n")
	out := runFFSync(t, binDir, "--db", "app")

	if !pushed(readLog(t, logPath)) {
		t.Fatalf("first push should still push, out:\n%s", out)
	}
	if strings.Contains(out, "FIRST PUSH FAILED") || strings.Contains(out, "cut by the Dolt server") {
		t.Fatalf("a successful first push must emit no failure banner, got:\n%s", out)
	}
}

// lastLineContaining returns the last line of out containing needle, or "".
func lastLineContaining(out, needle string) string {
	found := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			found = line
		}
	}
	return found
}

// TestSyncPushCutByServerQuotesConfiguredReadTimeout pins the @@net_read_timeout
// probe and its CSV parse. Quoting the real number is what tells an operator the
// push died at 15s and not at the 1800s client bound they were about to raise;
// a silent parse regression would leave only the vaguer wording.
func TestSyncPushCutByServerQuotesConfiguredReadTimeout(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "dolt.log")
	body := fakeDoltHeader(logPath, "main") +
		aheadFetchArm +
		"  *\"SELECT @@net_read_timeout\"*) printf '@@net_read_timeout\\n15\\n' ; exit 0 ;;\n" +
		"  *DOLT_PUSH*)\n" +
		"    printf '%s\\n' 'Error 1105 (HY000): unknown push error; connection was closed' >&2\n" +
		"    exit 1 ;;\n" +
		"esac\nexit 0\n"
	installFFFakeDolt(t, binDir, body)

	out := runFFSync(t, binDir, "--db", "app")
	if !strings.Contains(out, "listener read_timeout (15s)") {
		t.Fatalf("expected the configured read_timeout quoted in seconds, got:\n%s", out)
	}
}

// TestSyncPushCutByServerDegradesWhenReadTimeoutUnreadable keeps the diagnostic
// best-effort: the probe runs against a server that just tore a connection down,
// so an unanswerable probe must soften the wording, never fail the run or drop
// the attribution.
func TestSyncPushCutByServerDegradesWhenReadTimeoutUnreadable(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "dolt.log")
	body := fakeDoltHeader(logPath, "main") +
		aheadFetchArm +
		"  *\"SELECT @@net_read_timeout\"*) exit 1 ;;\n" +
		"  *DOLT_PUSH*)\n" +
		"    printf '%s\\n' 'Error 1105 (HY000): unknown push error; connection was closed' >&2\n" +
		"    exit 1 ;;\n" +
		"esac\nexit 0\n"
	installFFFakeDolt(t, binDir, body)

	out := runFFSync(t, binDir, "--db", "app")
	if !strings.Contains(out, "cut by the Dolt server at its listener read_timeout,") {
		t.Fatalf("expected the attribution to survive an unreadable probe, got:\n%s", out)
	}
	if strings.Contains(out, "read_timeout ()") || strings.Contains(out, "read_timeout (s)") {
		t.Fatalf("unreadable probe must not emit an empty seconds value, got:\n%s", out)
	}
}
