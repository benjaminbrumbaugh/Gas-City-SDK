// Package dolt_test pins the boundary between "the probe ran and
// exceeded its bound" (exit 124) and "the probe could not be run at
// all" (exit RUN_BOUNDED_UNAVAILABLE_RC). Conflating the two is what
// let a stock macOS host — no coreutils `timeout`, no `gtimeout` —
// report a healthy Dolt server as unreachable, which the deacon
// patrol's escalation table reads as CRITICAL and answers with a
// server restart that destroys the very evidence it wants (sdk-4is).
package dolt_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// boundedMechanismNames are the PATH entries that give run_bounded an
// external way to bound a command. A host missing all of them is the
// stock-macOS case this file exercises: `timeout` and `gtimeout` ship
// with GNU coreutils (absent by default on macOS) and python3 is
// absent whenever the Xcode command line tools are not installed.
var boundedMechanismNames = map[string]bool{
	"timeout":  true,
	"gtimeout": true,
}

// pathWithoutBoundedMechanisms builds a bin directory that mirrors the
// host's system utilities but omits every external bounded-execution
// mechanism, so run_bounded is forced onto its portable shell
// fallback. Symlinking the real system directories entry-by-entry
// (rather than hand-listing the handful of tools health.sh happens to
// call today) keeps the fixture from silently losing coverage the
// first time the script reaches for one more utility.
func pathWithoutBoundedMechanisms(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	linked := 0
	for _, sysDir := range []string{"/bin", "/usr/bin", "/usr/sbin", "/sbin"} {
		entries, err := os.ReadDir(sysDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if boundedMechanismNames[name] || strings.HasPrefix(name, "python") {
				continue
			}
			dst := filepath.Join(binDir, name)
			if _, err := os.Lstat(dst); err == nil {
				continue // first directory on the search order wins
			}
			if err := os.Symlink(filepath.Join(sysDir, name), dst); err != nil {
				continue
			}
			linked++
		}
	}
	if linked == 0 {
		t.Skip("no system utilities found to build a sanitized PATH")
	}
	for _, required := range []string{"sh", "sleep", "kill"} {
		if _, err := os.Lstat(filepath.Join(binDir, required)); err != nil && required != "kill" {
			t.Skipf("sanitized PATH is missing %q; cannot exercise the shell fallback", required)
		}
	}
	// Guard the fixture itself: if a bounded mechanism leaked through,
	// the test below would pass for the wrong reason.
	for name := range boundedMechanismNames {
		if _, err := os.Lstat(filepath.Join(binDir, name)); err == nil {
			t.Fatalf("sanitized PATH still exposes %q", name)
		}
	}
	return binDir
}

// runRunBoundedOnSanitizedPATH sources runtime.sh with a PATH that has
// no timeout/gtimeout/python3 and runs `run_bounded <bound> <args...>`,
// returning the exit code and combined output.
func runRunBoundedOnSanitizedPATH(t *testing.T, bound string, args ...string) (int, string) {
	t.Helper()

	binDir := pathWithoutBoundedMechanisms(t)
	root := repoRoot(t)
	cityPath := t.TempDir()

	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	script := `. "$GC_PACK_DIR/assets/scripts/runtime.sh"; run_bounded ` +
		shellQuote(bound) + " " + strings.Join(quoted, " ")

	cmd := exec.Command(filepath.Join(binDir, "sh"), "-c", script)
	cmd.Env = append(filteredEnv("GC_CITY_PATH", "GC_PACK_DIR", "GC_DOLT_PORT", "PATH"),
		"GC_CITY_PATH="+cityPath,
		"GC_PACK_DIR="+root,
		"GC_DOLT_PORT=4406",
		"PATH="+binDir,
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running run_bounded: %v\noutput:\n%s", err, out)
	return 0, ""
}

// runBoundedUnavailableRC reads the exit code runtime.sh reserves for
// "the probe could not be executed". Reading it from the script keeps
// this file honest if the constant ever moves.
func runBoundedUnavailableRC(t *testing.T) int {
	t.Helper()

	root := repoRoot(t)
	cityPath := t.TempDir()
	cmd := exec.Command("sh", "-c",
		`. "$GC_PACK_DIR/assets/scripts/runtime.sh"; printf '%s' "$RUN_BOUNDED_UNAVAILABLE_RC"`)
	cmd.Env = append(filteredEnv("GC_CITY_PATH", "GC_PACK_DIR", "GC_DOLT_PORT"),
		"GC_CITY_PATH="+cityPath,
		"GC_PACK_DIR="+root,
		"GC_DOLT_PORT=4406",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading RUN_BOUNDED_UNAVAILABLE_RC: %v\noutput:\n%s", err, out)
	}
	rc, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("RUN_BOUNDED_UNAVAILABLE_RC is not numeric (%q): %v", out, err)
	}
	return rc
}

// TestRunBoundedUnavailableRCIsNotTheTimeoutRC is the contract at the
// heart of sdk-4is: the two failures must not share an exit code, or
// no caller can tell them apart.
func TestRunBoundedUnavailableRCIsNotTheTimeoutRC(t *testing.T) {
	if rc := runBoundedUnavailableRC(t); rc == 124 {
		t.Fatalf("RUN_BOUNDED_UNAVAILABLE_RC = 124, the coreutils timeout code; "+
			"'could not run the probe' must not be encoded as 'the probe timed out' (got %d)", rc)
	}
}

// TestRunBoundedShellFallbackBoundsWithoutTimeoutBinary proves the
// portable fallback actually bounds a hung command on a host with no
// timeout/gtimeout/python3 — the case that previously had no mechanism
// at all and fell straight through to a fabricated exit 124.
func TestRunBoundedShellFallbackBoundsWithoutTimeoutBinary(t *testing.T) {
	// The bounded command is `sleep` itself, not a shell script that
	// wraps it: a shell blocked in wait() on a foreground child defers
	// the signal until that child returns, and the orphaned `sleep`
	// would keep the output pipe open long past the bound. That is an
	// artifact of shell signal delivery, not of run_bounded — a single
	// process mirrors the real targets (dolt, gc) faithfully.
	start := time.Now()
	exitCode, out := runRunBoundedOnSanitizedPATH(t, "1", "sleep", "30")
	elapsed := time.Since(start)

	if exitCode != 124 {
		t.Fatalf("run_bounded exit code = %d, want 124 (a real wall-clock timeout)\noutput:\n%s", exitCode, out)
	}
	if elapsed > 20*time.Second {
		t.Fatalf("run_bounded took %s; the shell fallback did not enforce the 1s bound", elapsed)
	}
}

// TestRunBoundedShellFallbackPassesThroughExitCodeAndOutput covers the
// ordinary path: on a host with no external mechanism the fallback is
// the only implementation, so a command that finishes inside its bound
// must still deliver its output and its own exit status.
func TestRunBoundedShellFallbackPassesThroughExitCodeAndOutput(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.sh")
	writeExecutable(t, child, "#!/bin/sh\necho to-stdout\necho to-stderr >&2\nexit 3\n")

	exitCode, out := runRunBoundedOnSanitizedPATH(t, "5", child)

	if exitCode != 3 {
		t.Fatalf("run_bounded exit code = %d, want 3 (the child's own status)\noutput:\n%s", exitCode, out)
	}
	if !strings.Contains(out, "to-stdout") || !strings.Contains(out, "to-stderr") {
		t.Fatalf("child output not passed through; got:\n%s", out)
	}
}

// TestRunBoundedShellFallbackSucceedsWithoutTimeoutBinary is the
// narrow proof that a healthy command is reported healthy — not
// timed out — when no external bounding mechanism exists.
func TestRunBoundedShellFallbackSucceedsWithoutTimeoutBinary(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.sh")
	writeExecutable(t, child, "#!/bin/sh\nexit 0\n")

	exitCode, out := runRunBoundedOnSanitizedPATH(t, "5", child)

	if exitCode != 0 {
		t.Fatalf("run_bounded exit code = %d, want 0; a healthy command on a host without "+
			"coreutils must not be reported as a failure\noutput:\n%s", exitCode, out)
	}
}

// TestRunBoundedUnparseableBoundReportsUnavailableNotTimeout covers the
// remaining way a probe can fail to run: a bound the mechanism cannot
// parse (e.g. an operator setting GC_DOLT_RIG_LIST_TIMEOUT_SECS=30m).
// The command never runs, so reporting a timeout would be a fabrication.
func TestRunBoundedUnparseableBoundReportsUnavailableNotTimeout(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.sh")
	writeExecutable(t, child, "#!/bin/sh\nexit 0\n")

	wantRC := runBoundedUnavailableRC(t)
	exitCode, out := runRunBoundedOnSanitizedPATH(t, "not-a-number", child)

	if exitCode == 124 {
		t.Fatalf("run_bounded reported 124 (timed out) for a bound it could not parse; "+
			"the command never ran\noutput:\n%s", out)
	}
	if exitCode != wantRC {
		t.Fatalf("run_bounded exit code = %d, want %d (unavailable)\noutput:\n%s", exitCode, wantRC, out)
	}
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "diagnostic unavailable") {
		t.Fatalf("run_bounded must say the diagnostic is unavailable; got:\n%s", out)
	}
	if strings.Contains(lower, "timed out") {
		t.Fatalf("run_bounded must never claim the probe timed out when it never ran; got:\n%s", out)
	}
}

// TestHealthReportsReachableWithoutTimeoutBinary is the acceptance
// test for sdk-4is: on a macOS-shaped host with no coreutils and no
// python3, `gc dolt health --json` must still produce a real report
// that scores a live server as reachable. Before the portable
// fallback, run_bounded had no mechanism, returned 124, and the SQL
// ping was recorded as a failure — publishing server.reachable=false
// for a perfectly healthy data plane.
func TestHealthReportsReachableWithoutTimeoutBinary(t *testing.T) {
	cityPath := t.TempDir()
	root := repoRoot(t)

	binDir := pathWithoutBoundedMechanisms(t)
	env := reachableServerEnv(t, root, cityPath)
	// reachableServerEnv prepends its fake lsof/nc/dolt to the host
	// PATH; re-point the tail at the sanitized directory so no real
	// timeout/gtimeout/python3 remains reachable.
	for i, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == "PATH" {
			fakeBin, _, _ := strings.Cut(value, string(os.PathListSeparator))
			env[i] = "PATH=" + fakeBin + string(os.PathListSeparator) + binDir
		}
	}

	out, err := newHealthScriptCmd(root, env, "--json").Output()
	if err != nil {
		t.Fatalf("health.sh failed on a host without a timeout binary: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"reachable": true`) {
		t.Fatalf("health reported an unreachable server on a host whose only defect is a "+
			"missing timeout binary; a missing mechanism must never score as unreachable:\n%s", out)
	}
}

// shippedAssetExtensions are the pack files that reach a host and are
// either executed or read as instructions: shell scripts, the
// formula/order/command descriptors, and the markdown that becomes
// agent-facing prompt text.
var shippedAssetExtensions = map[string]bool{
	".sh":   true,
	".md":   true,
	".toml": true,
}

// bareTimeoutInvocation matches `timeout`/`gtimeout` used as a command —
// at the start of a line or directly after a shell operator — while
// leaving the legitimate spellings alone: a descriptor's own
// `timeout = "1800s"` key (excluded by the trailing class rejecting
// `=`), a variable such as `$push_timeout`, and a `--timeout` flag.
var bareTimeoutInvocation = regexp.MustCompile(`(^|[|;&(]|\$\()[[:space:]]*g?timeout[[:space:]]+[^=[:space:]]`)

// TestNoBareTimeoutInShippedPackAssets is the standing guard for the
// third sdk-4is acceptance criterion. The incident began as one line of
// shipped asset text that assumed a binary the target host does not
// have: macOS ships no coreutils, so `timeout 60 gc dolt health --json`
// exits 127 and the caller's `|| echo "(timed out or failed)"` branch
// reports a healthy data plane as a timeout. Every offender inside this
// pack is gone; this test is what keeps the next one from landing.
//
// The scan is deliberately line-based and does not skip comments. A
// commented-out `timeout 5 gc ...` in a shipped asset is a copy-paste
// trap for the next reader, and in a prompt fragment a comment is
// simply instruction text.
//
// Use run_bounded (assets/scripts/bounded.sh) in scripts, or
// `gc hook run --timeout` from prompt and formula text.
func TestNoBareTimeoutInShippedPackAssets(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !shippedAssetExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			// `command -v timeout` is the guarded probe in bounded.sh:
			// it asks whether the binary exists, which is the opposite
			// of assuming that it does.
			if strings.Contains(line, "command -v") {
				continue
			}
			if bareTimeoutInvocation.MatchString(line) {
				offenders = append(offenders, fmt.Sprintf("  %s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking shipped pack assets: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("shipped pack assets invoke the external `timeout` binary, which a stock "+
			"macOS host does not have — the command exits 127 and the caller records a "+
			"fabricated timeout for a healthy server (sdk-4is).\n"+
			"Use run_bounded (assets/scripts/bounded.sh) in scripts, or `gc hook run --timeout` "+
			"in prompt/formula text:\n%s", strings.Join(offenders, "\n"))
	}
}
