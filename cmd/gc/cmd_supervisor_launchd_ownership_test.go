package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoSupervisorStartDarwinDelegatesToRegisteredLaunchd(t *testing.T) {
	pinRealHome(t)
	t.Setenv("GC_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldAlive := supervisorAliveHook
	oldStart := supervisorLaunchdStartHook
	oldLaunchctl := supervisorLaunchctlRun
	oldPID := supervisorLaunchdPID
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorAliveHook = oldAlive
		supervisorLaunchdStartHook = oldStart
		supervisorLaunchctlRun = oldLaunchctl
		supervisorLaunchdPID = oldPID
	})

	supervisorRuntimeGOOS = "darwin"
	supervisorLaunchdRegistered = func(label string) bool {
		return label == supervisorLaunchdLabel()
	}
	supervisorAliveHook = func() int { return 4242 }
	supervisorLaunchdPID = func(string) int { return 4242 }
	supervisorLaunchdStartHook = startSupervisorViaLaunchd
	var launchctlArgs []string
	supervisorLaunchctlRun = func(args ...string) error {
		launchctlArgs = append([]string(nil), args...)
		return nil
	}

	var stdout, stderr bytes.Buffer
	if code := doSupervisorStart(&stdout, &stderr); code != 0 {
		t.Fatalf("doSupervisorStart code = %d, want 0; stderr=%q", code, stderr.String())
	}
	wantArgs := []string{"kickstart", supervisorLaunchdServiceTarget(supervisorLaunchdLabel())}
	if strings.Join(launchctlArgs, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("launchctl args = %v, want %v", launchctlArgs, wantArgs)
	}
	if !strings.Contains(stdout.String(), "managed by launchd") {
		t.Fatalf("stdout = %q, want launchd ownership confirmation", stdout.String())
	}
}

func TestStartSupervisorViaLaunchdRejectsOwnershipMismatch(t *testing.T) {
	oldLaunchctl := supervisorLaunchctlRun
	oldAlive := supervisorAliveHook
	oldPID := supervisorLaunchdPID
	oldTimeout := supervisorReadyTimeout
	t.Cleanup(func() {
		supervisorLaunchctlRun = oldLaunchctl
		supervisorAliveHook = oldAlive
		supervisorLaunchdPID = oldPID
		supervisorReadyTimeout = oldTimeout
	})

	supervisorLaunchctlRun = func(...string) error { return nil }
	supervisorAliveHook = func() int { return 4242 }
	supervisorLaunchdPID = func(string) int { return 7777 }
	supervisorReadyTimeout = 0

	var stdout, stderr bytes.Buffer
	if code := startSupervisorViaLaunchd(&stdout, &stderr, false); code != 1 {
		t.Fatalf("startSupervisorViaLaunchd code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "ownership mismatch") {
		t.Fatalf("stderr = %q, want ownership mismatch", stderr.String())
	}
}

func TestDoSupervisorStartDarwinRefusesUnmanagedFork(t *testing.T) {
	pinRealHome(t)
	t.Setenv("GC_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldAlive := supervisorAliveHook
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorAliveHook = oldAlive
	})

	supervisorRuntimeGOOS = "darwin"
	supervisorLaunchdRegistered = func(string) bool { return false }
	supervisorAliveHook = func() int { return 0 }

	var stdout, stderr bytes.Buffer
	if code := doSupervisorStart(&stdout, &stderr); code != 1 {
		t.Fatalf("doSupervisorStart code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"launchd service is not registered", "gc supervisor install"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestDoSupervisorStartDarwinReportsExistingOwnershipConflict(t *testing.T) {
	pinRealHome(t)
	gcHome := shortTempDir(t, "gc-home-")
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t, "gc-run-"))
	startTestSupervisorSocket(t, filepath.Join(gcHome, "supervisor.sock"), func(cmd string) string {
		if cmd == "ping" {
			return "4242\n"
		}
		return ""
	})

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldPID := supervisorLaunchdPID
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorLaunchdPID = oldPID
	})
	supervisorRuntimeGOOS = "darwin"
	supervisorLaunchdRegistered = func(string) bool { return true }
	supervisorLaunchdPID = func(string) int { return 7777 }

	var stdout, stderr bytes.Buffer
	if code := doSupervisorStart(&stdout, &stderr); code != 1 {
		t.Fatalf("doSupervisorStart code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "ownership conflict") {
		t.Fatalf("stderr = %q, want ownership conflict", stderr.String())
	}
}

func TestEnsureSupervisorRunningDarwinDoesNotFallbackAfterInstallFailure(t *testing.T) {
	pinRealHome(t)
	t.Setenv("GC_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	oldGOOS := supervisorRuntimeGOOS
	oldInstall := supervisorInstallHook
	oldAlive := supervisorAliveHook
	oldStart := supervisorLaunchdStartHook
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorInstallHook = oldInstall
		supervisorAliveHook = oldAlive
		supervisorLaunchdStartHook = oldStart
	})

	supervisorRuntimeGOOS = "darwin"
	supervisorInstallHook = func(io.Writer, io.Writer) int { return 1 }
	supervisorAliveHook = func() int { return 0 }
	startCalled := false
	supervisorLaunchdStartHook = func(io.Writer, io.Writer, bool) int {
		startCalled = true
		return 0
	}

	var stdout, stderr bytes.Buffer
	if code := ensureSupervisorRunning(&stdout, &stderr); code != 1 {
		t.Fatalf("ensureSupervisorRunning code = %d, want 1", code)
	}
	if startCalled {
		t.Fatal("ensureSupervisorRunning attempted a second start after launchd installation failed")
	}
}

func TestEnsureSupervisorRunningDarwinRejectsOwnershipConflictAfterInstall(t *testing.T) {
	pinRealHome(t)
	t.Setenv("GC_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	oldGOOS := supervisorRuntimeGOOS
	oldInstall := supervisorInstallHook
	oldAlive := supervisorAliveHook
	oldPID := supervisorLaunchdPID
	oldTimeout := supervisorReadyTimeout
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorInstallHook = oldInstall
		supervisorAliveHook = oldAlive
		supervisorLaunchdPID = oldPID
		supervisorReadyTimeout = oldTimeout
	})

	supervisorRuntimeGOOS = "darwin"
	supervisorInstallHook = func(io.Writer, io.Writer) int { return 0 }
	supervisorAliveHook = func() int { return 4242 }
	supervisorLaunchdPID = func(string) int { return 7777 }
	supervisorReadyTimeout = 0

	var stdout, stderr bytes.Buffer
	if code := ensureSupervisorRunning(&stdout, &stderr); code != 1 {
		t.Fatalf("ensureSupervisorRunning code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "ownership mismatch") {
		t.Fatalf("stderr = %q, want ownership mismatch", stderr.String())
	}
}

func TestLaunchdPrintPID(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want int
	}{
		{name: "running child", out: "state = running\n	pid = 4242\n", want: 4242},
		{name: "registered without child", out: "state = spawn scheduled\n	last exit code = 1\n", want: 0},
		{name: "invalid pid", out: "state = running\n	pid = unavailable\n", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := launchdPrintPID([]byte(tc.out)); got != tc.want {
				t.Fatalf("launchdPrintPID() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSupervisorStatusJSONReportsLaunchdOwnership(t *testing.T) {
	gcHome := shortTempDir(t, "gc-home-")
	runtimeDir := shortTempDir(t, "gc-run-")
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	startTestSupervisorSocket(t, filepath.Join(gcHome, "supervisor.sock"), func(cmd string) string {
		if cmd == "ping" {
			return "4242\n"
		}
		return ""
	})

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldPID := supervisorLaunchdPID
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorLaunchdPID = oldPID
	})
	supervisorRuntimeGOOS = "darwin"

	for _, tc := range []struct {
		name        string
		registered  bool
		managerPID  int
		wantManager string
		wantLabel   string
		wantStatus  string
	}{
		{name: "matched", registered: true, managerPID: 4242, wantManager: "launchd", wantLabel: supervisorLaunchdLabel(), wantStatus: "matched"},
		{name: "direct process conflicts", registered: true, managerPID: 0, wantManager: "launchd", wantLabel: supervisorLaunchdLabel(), wantStatus: "conflict"},
		{name: "unregistered direct process", registered: false, managerPID: 0, wantManager: "direct", wantStatus: "unmanaged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supervisorLaunchdRegistered = func(string) bool { return tc.registered }
			supervisorLaunchdPID = func(string) int { return tc.managerPID }
			var stdout, stderr bytes.Buffer
			if code := supervisorStatusWithOptions(&stdout, &stderr, true); code != 0 {
				t.Fatalf("status code = %d, want 0; stderr=%q", code, stderr.String())
			}
			var payload struct {
				LifecycleManager string `json:"lifecycle_manager"`
				ServiceLabel     string `json:"service_label"`
				ManagerPID       int    `json:"manager_pid"`
				OwnershipStatus  string `json:"ownership_status"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatalf("decode status JSON: %v", err)
			}
			if payload.LifecycleManager != tc.wantManager || payload.ServiceLabel != tc.wantLabel || payload.ManagerPID != tc.managerPID || payload.OwnershipStatus != tc.wantStatus {
				t.Fatalf("status ownership = %+v, want manager=%q label=%q manager_pid=%d ownership=%q", payload, tc.wantManager, tc.wantLabel, tc.managerPID, tc.wantStatus)
			}
		})
	}
}

func TestSupervisorStatusJSONReportsUniqueLegacyLaunchdOwner(t *testing.T) {
	homeDir := t.TempDir()
	gcHome := shortTempDir(t, "gc-home-")
	t.Setenv("HOME", homeDir)
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t, "gc-run-"))
	legacyPath := legacySupervisorLaunchdPlistPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := renderSupervisorTemplate(supervisorLaunchdTemplate, &supervisorServiceData{
		GCPath: "/tmp/gc-legacy", LogPath: filepath.Join(gcHome, "supervisor.log"),
		GCHome: gcHome, LaunchdLabel: defaultSupervisorLaunchdLabel, Path: "/usr/local/bin:/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	startTestSupervisorSocket(t, filepath.Join(gcHome, "supervisor.sock"), func(cmd string) string {
		if cmd == "ping" {
			return "4242\n"
		}
		return ""
	})

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldPID := supervisorLaunchdPID
	supervisorRuntimeGOOS = "darwin"
	supervisorLaunchdRegistered = func(label string) bool {
		return label == supervisorLaunchdLabel() || label == defaultSupervisorLaunchdLabel
	}
	supervisorLaunchdPID = func(label string) int {
		if label == defaultSupervisorLaunchdLabel {
			return 4242
		}
		return 0
	}
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorLaunchdPID = oldPID
	})

	var stdout, stderr bytes.Buffer
	if code := supervisorStatusWithOptions(&stdout, &stderr, true); code != 0 {
		t.Fatalf("status code = %d; stderr=%q", code, stderr.String())
	}
	var payload struct {
		LifecycleManager string `json:"lifecycle_manager"`
		ServiceLabel     string `json:"service_label"`
		ManagerPID       int    `json:"manager_pid"`
		OwnershipStatus  string `json:"ownership_status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.LifecycleManager != "launchd" || payload.ServiceLabel != defaultSupervisorLaunchdLabel || payload.ManagerPID != 4242 || payload.OwnershipStatus != "matched" {
		t.Fatalf("status ownership = %+v, want unique legacy launchd owner", payload)
	}
}

func TestSupervisorStatusJSONReportsLegacyLaunchdOwnerWithoutSocket(t *testing.T) {
	homeDir := t.TempDir()
	gcHome := shortTempDir(t, "gc-home-")
	t.Setenv("HOME", homeDir)
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t, "gc-run-"))
	legacyPath := legacySupervisorLaunchdPlistPath()
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := renderSupervisorTemplate(supervisorLaunchdTemplate, &supervisorServiceData{
		GCPath: "/tmp/gc-legacy", LogPath: filepath.Join(gcHome, "supervisor.log"),
		GCHome: gcHome, LaunchdLabel: defaultSupervisorLaunchdLabel, Path: "/usr/local/bin:/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	oldGOOS := supervisorRuntimeGOOS
	oldRegistered := supervisorLaunchdRegistered
	oldPID := supervisorLaunchdPID
	oldActive := supervisorLaunchdActive
	supervisorRuntimeGOOS = "darwin"
	supervisorLaunchdRegistered = func(label string) bool {
		return label == supervisorLaunchdLabel() || label == defaultSupervisorLaunchdLabel
	}
	supervisorLaunchdPID = func(label string) int {
		if label == defaultSupervisorLaunchdLabel {
			return 4242
		}
		return 0
	}
	supervisorLaunchdActive = func(label string) bool { return label == defaultSupervisorLaunchdLabel }
	t.Cleanup(func() {
		supervisorRuntimeGOOS = oldGOOS
		supervisorLaunchdRegistered = oldRegistered
		supervisorLaunchdPID = oldPID
		supervisorLaunchdActive = oldActive
	})

	var stdout, stderr bytes.Buffer
	if code := supervisorStatusWithOptions(&stdout, &stderr, true); code != 0 {
		t.Fatalf("status code = %d; stderr=%q", code, stderr.String())
	}
	var payload struct {
		Running          bool   `json:"running"`
		LifecycleManager string `json:"lifecycle_manager"`
		ServiceLabel     string `json:"service_label"`
		ManagerPID       int    `json:"manager_pid"`
		OwnershipStatus  string `json:"ownership_status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Running || payload.LifecycleManager != "launchd" || payload.ServiceLabel != defaultSupervisorLaunchdLabel || payload.ManagerPID != 4242 || payload.OwnershipStatus != "socket_unreachable" {
		t.Fatalf("status ownership = %+v, want running legacy launchd owner with unavailable socket", payload)
	}
}
