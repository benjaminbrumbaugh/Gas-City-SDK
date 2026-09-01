package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The defect (gc-2rglt): on macOS a failed `gc supervisor install` was
// terminal, so `gc start`/`gc init` could not bring a supervisor up in an
// environment that must not touch the host's launchd. Mac acceptance failed
// on every run from the day the workflow landed because its whole isolation
// strategy — shim launchctl to exit 1 and let gc fall back to a bare fork —
// depends on a fallback that macOS does not have.

func TestSupervisorServiceManagerBypassedOnlyForExactOptOut(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"none", true},
		{"NONE", true},
		{"  none  ", true},
		{"", false},
		{"launchd", false},
		{"systemd", false},
		// A typo must not silently strip lifecycle ownership on a real
		// machine, so anything that is not the opt-out keeps the manager.
		{"non", false},
		{"none,launchd", false},
		{"0", false},
		{"false", false},
	} {
		t.Setenv(supervisorServiceManagerEnv, tc.value)
		if got := supervisorServiceManagerBypassed(); got != tc.want {
			t.Errorf("%s=%q: bypassed = %v, want %v", supervisorServiceManagerEnv, tc.value, got, tc.want)
		}
	}
	unsetServiceManagerEnv(t)
	if supervisorServiceManagerBypassed() {
		t.Fatalf("unset %s must not bypass the service manager", supervisorServiceManagerEnv)
	}
}

// The reported case, at the exact seam that failed: macOS, service manager
// bypassed, no supervisor running. Install must never be reached and the
// bare start must be.
func TestEnsureSupervisorRunningBypassesInstallAndBareStarts(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	restoreGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = restoreGOOS })

	installs := 0
	oldInstall := supervisorInstallHook
	supervisorInstallHook = func(_, _ io.Writer) int {
		installs++
		return 1
	}
	t.Cleanup(func() { supervisorInstallHook = oldInstall })

	oldAlive := supervisorAliveHook
	supervisorAliveHook = func() int { return 0 }
	t.Cleanup(func() { supervisorAliveHook = oldAlive })

	starts := 0
	oldStart := doSupervisorStartHook
	doSupervisorStartHook = func(_, _ io.Writer) int {
		starts++
		return 0
	}
	t.Cleanup(func() { doSupervisorStartHook = oldStart })

	var stdout, stderr bytes.Buffer
	if code := ensureSupervisorRunning(&stdout, &stderr); code != 0 {
		t.Fatalf("ensureSupervisorRunning = %d, want 0; stderr=%q", code, stderr.String())
	}
	if installs != 0 {
		t.Fatalf("supervisor install ran %d time(s); a bypassed service manager must never write or load a service file", installs)
	}
	if starts != 1 {
		t.Fatalf("bare supervisor start ran %d time(s), want 1", starts)
	}
}

// An already-running supervisor is not restarted, so a bypassed manager
// does not turn every `gc start` into a second supervisor.
func TestEnsureSupervisorRunningBypassKeepsLiveSupervisor(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	restoreGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = restoreGOOS })

	oldAlive := supervisorAliveHook
	supervisorAliveHook = func() int { return 4242 }
	t.Cleanup(func() { supervisorAliveHook = oldAlive })

	starts := 0
	oldStart := doSupervisorStartHook
	doSupervisorStartHook = func(_, _ io.Writer) int {
		starts++
		return 0
	}
	t.Cleanup(func() { doSupervisorStartHook = oldStart })

	var stdout, stderr bytes.Buffer
	if code := ensureSupervisorRunning(&stdout, &stderr); code != 0 {
		t.Fatalf("ensureSupervisorRunning = %d, want 0; stderr=%q", code, stderr.String())
	}
	if starts != 0 {
		t.Fatalf("bare start ran %d time(s) with a live supervisor, want 0", starts)
	}
}

// Without the opt-out, macOS keeps launchd as the sole lifecycle owner: a
// failed install stays terminal and no detached child is forked. A fix that
// simply made darwin fall back would reintroduce the split ownership this
// guard exists to prevent, so it is asserted, not assumed.
func TestEnsureSupervisorRunningWithoutBypassStaysTerminalOnDarwin(t *testing.T) {
	unsetServiceManagerEnv(t)
	restoreGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = restoreGOOS })

	oldInstall := supervisorInstallHook
	supervisorInstallHook = func(_, _ io.Writer) int { return 1 }
	t.Cleanup(func() { supervisorInstallHook = oldInstall })

	oldAlive := supervisorAliveHook
	supervisorAliveHook = func() int { return 0 }
	t.Cleanup(func() { supervisorAliveHook = oldAlive })

	starts := 0
	oldStart := doSupervisorStartHook
	doSupervisorStartHook = func(_, _ io.Writer) int {
		starts++
		return 0
	}
	t.Cleanup(func() { doSupervisorStartHook = oldStart })

	var stdout, stderr bytes.Buffer
	if code := ensureSupervisorRunning(&stdout, &stderr); code != 1 {
		t.Fatalf("ensureSupervisorRunning = %d, want 1 (launchd owns the lifecycle)", code)
	}
	if starts != 0 {
		t.Fatalf("bare start ran %d time(s) without the opt-out, want 0", starts)
	}
}

// `gc supervisor start` refuses to fork on macOS unless launchd already
// owns the label. Under the opt-out there is no launchd job to own it, so
// the refusal must not fire — otherwise the bypass would dead-end one call
// later.
func TestDoSupervisorStartBypassSkipsLaunchdRegistrationRequirement(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	t.Setenv("GC_HOME", t.TempDir())
	restoreGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = restoreGOOS })

	oldRegistered := supervisorLaunchdRegistered
	supervisorLaunchdRegistered = func(string) bool { return false }
	t.Cleanup(func() { supervisorLaunchdRegistered = oldRegistered })

	launchdStarts := 0
	oldLaunchdStart := supervisorLaunchdStartHook
	supervisorLaunchdStartHook = func(_, _ io.Writer, _ bool) int {
		launchdStarts++
		return 0
	}
	t.Cleanup(func() { supervisorLaunchdStartHook = oldLaunchdStart })

	// Fork a stub, not the test binary: re-executing gc.test would run the
	// whole suite again as a detached child.
	stub := filepath.Join(t.TempDir(), "gc-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldExe := supervisorExecutable
	supervisorExecutable = func() (string, error) { return stub, nil }
	t.Cleanup(func() { supervisorExecutable = oldExe })

	oldTimeout, oldPoll := supervisorReadyTimeout, supervisorReadyPollInterval
	supervisorReadyTimeout, supervisorReadyPollInterval = 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { supervisorReadyTimeout, supervisorReadyPollInterval = oldTimeout, oldPoll })

	var stdout, stderr bytes.Buffer
	// The stub exits immediately, so the start still reports failure. What
	// matters is which failure: the launchd refusal must not fire, because
	// under the opt-out there is no launchd job to be registered.
	_ = doSupervisorStartJSON(&stdout, &stderr, false)
	if strings.Contains(stderr.String(), "launchd service is not registered") {
		t.Fatalf("bypassed start still demanded a registered launchd service: %q", stderr.String())
	}
	if launchdStarts != 0 {
		t.Fatalf("bypassed start delegated to launchd %d time(s), want 0", launchdStarts)
	}
}

// The mirror image: without the opt-out the refusal is exactly what should
// happen, so the test above is measuring the bypass and not a message that
// never appears.
func TestDoSupervisorStartWithoutBypassStillRequiresLaunchdRegistration(t *testing.T) {
	unsetServiceManagerEnv(t)
	t.Setenv("GC_HOME", t.TempDir())
	restoreGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = restoreGOOS })

	oldRegistered := supervisorLaunchdRegistered
	supervisorLaunchdRegistered = func(string) bool { return false }
	t.Cleanup(func() { supervisorLaunchdRegistered = oldRegistered })

	oldExe := supervisorExecutable
	supervisorExecutable = func() (string, error) {
		t.Fatal("unbypassed darwin start must refuse before forking")
		return "", nil
	}
	t.Cleanup(func() { supervisorExecutable = oldExe })

	var stdout, stderr bytes.Buffer
	if code := doSupervisorStartJSON(&stdout, &stderr, false); code != 1 {
		t.Fatalf("doSupervisorStartJSON = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "launchd service is not registered") {
		t.Fatalf("stderr = %q, want the launchd ownership refusal", stderr.String())
	}
}

// The original diagnosis of gc-2rglt was wrong because the only symptom was
// "exit status 1". launchctl's own message must reach the error.
func TestRunSupervisorLaunchctlIncludesStderr(t *testing.T) {
	err := runSupervisorLaunchctl("this-subcommand-does-not-exist-gc2rglt")
	if err == nil {
		t.Skip("launchctl accepted an unknown subcommand; nothing to assert")
	}
	msg := err.Error()
	if msg == "exit status 1" || !strings.Contains(msg, ":") {
		t.Fatalf("launchctl error = %q, want the exit status plus launchctl's own diagnostic", msg)
	}
}

func TestFirstDiagnosticLine(t *testing.T) {
	if got := firstDiagnosticLine("Load failed: 5: Input/output error\ntrailing noise"); got != "Load failed: 5: Input/output error" {
		t.Fatalf("firstDiagnosticLine = %q", got)
	}
	if got := firstDiagnosticLine("single line"); got != "single line" {
		t.Fatalf("firstDiagnosticLine = %q", got)
	}
}

// unsetServiceManagerEnv removes the opt-out for the duration of the test.
// t.Setenv first so the original value is restored on cleanup; the unset
// itself is what the assertions need, since an empty value and an absent
// one must both leave the platform service manager in charge.
func unsetServiceManagerEnv(t *testing.T) {
	t.Helper()
	t.Setenv(supervisorServiceManagerEnv, "")
	if err := os.Unsetenv(supervisorServiceManagerEnv); err != nil {
		t.Fatalf("unsetting %s: %v", supervisorServiceManagerEnv, err)
	}
}
