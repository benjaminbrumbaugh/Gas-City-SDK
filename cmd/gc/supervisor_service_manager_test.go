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

func TestSupervisorServiceManagerBypassedOnlyForExactOptOut(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "none", want: true},
		{value: "NONE", want: true},
		{value: "  none  ", want: true},
		{value: "", want: false},
		{value: "launchd", want: false},
		{value: "none,launchd", want: false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(supervisorServiceManagerEnv, tc.value)
			if got := supervisorServiceManagerBypassed(); got != tc.want {
				t.Fatalf("%s=%q: bypassed = %v, want %v", supervisorServiceManagerEnv, tc.value, got, tc.want)
			}
		})
	}
}

func TestEnsureSupervisorRunningBypassesInstallAndBareStarts(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	oldGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = oldGOOS })

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
		t.Fatalf("supervisor install ran %d time(s), want 0", installs)
	}
	if starts != 1 {
		t.Fatalf("bare supervisor start ran %d time(s), want 1", starts)
	}
}

func TestEnsureSupervisorRunningBypassKeepsLiveSupervisor(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	oldGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = oldGOOS })

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
		t.Fatalf("bare supervisor start ran %d time(s) with a live supervisor, want 0", starts)
	}
}

func TestDoSupervisorStartBypassSkipsLaunchdRegistrationRequirement(t *testing.T) {
	t.Setenv(supervisorServiceManagerEnv, "none")
	t.Setenv("GC_HOME", t.TempDir())
	oldGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = oldGOOS })

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

	stub := filepath.Join(t.TempDir(), "gc-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldExecutable := supervisorExecutable
	supervisorExecutable = func() (string, error) { return stub, nil }
	t.Cleanup(func() { supervisorExecutable = oldExecutable })

	oldTimeout, oldPoll := supervisorReadyTimeout, supervisorReadyPollInterval
	supervisorReadyTimeout, supervisorReadyPollInterval = 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		supervisorReadyTimeout = oldTimeout
		supervisorReadyPollInterval = oldPoll
	})

	var stdout, stderr bytes.Buffer
	_ = doSupervisorStartJSON(&stdout, &stderr, false)
	if strings.Contains(stderr.String(), "launchd service is not registered") {
		t.Fatalf("bypassed start still demanded launchd registration: %q", stderr.String())
	}
	if launchdStarts != 0 {
		t.Fatalf("bypassed start delegated to launchd %d time(s), want 0", launchdStarts)
	}
}
