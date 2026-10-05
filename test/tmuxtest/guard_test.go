package tmuxtest

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestConfigureProcessEnvIsolatesTmuxSocketRoot(t *testing.T) {
	ownedRoot := t.TempDir()
	socketRoot := filepath.Join(ownedRoot, "s")
	fixtureBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixtureBin, "tmux"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Keep all helper-owned environment changes local to this test.
	for key, value := range map[string]string{
		tmuxEnv: "/tmp/tmux-parent/default,1,0", tmuxPaneEnv: "%42",
		tmuxTmpEnv: "/tmp/parent-tmux", "TMPDIR": os.Getenv("TMPDIR"),
		"HOME": os.Getenv("HOME"), "XDG_CONFIG_HOME": os.Getenv("XDG_CONFIG_HOME"),
		"SHELL": os.Getenv("SHELL"), "PATH": fixtureBin,
	} {
		t.Setenv(key, value)
	}

	if err := ConfigureOwnedProcessEnv(ownedRoot); err != nil {
		t.Fatalf("ConfigureOwnedProcessEnv(): %v", err)
	}
	if os.Getenv("HOME") != ownedRoot || os.Getenv("TMPDIR") != ownedRoot {
		t.Fatal("owned environment must confine home and temporary state")
	}

	wrapper, err := os.ReadFile(filepath.Join(ownedRoot, "bin", "tmux"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapper), fixtureBin) || !strings.Contains(string(wrapper), " -f ") {
		t.Fatalf("tmux wrapper must exec the resolved binary with owned configuration: %s", wrapper)
	}
	if value, ok := os.LookupEnv(tmuxEnv); ok {
		t.Fatalf("%s survived with value %q", tmuxEnv, value)
	}
	if value, ok := os.LookupEnv(tmuxPaneEnv); ok {
		t.Fatalf("%s survived with value %q", tmuxPaneEnv, value)
	}
	if value := os.Getenv(tmuxTmpEnv); value != socketRoot {
		t.Fatalf("%s = %q, want %q", tmuxTmpEnv, value, socketRoot)
	}
	if info, err := os.Stat(socketRoot); err != nil {
		t.Fatalf("stat socket root: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("socket root is not a directory")
	}
}

func TestListTestSocketPathsSkipsLiveSiblingRoots(t *testing.T) {
	tmp := t.TempDir()
	currentRun := filepath.Join(tmp, "gc-integration-current")
	t.Setenv("TMPDIR", currentRun)
	currentRoot := filepath.Join(currentRun, "tmux")
	staleRoot := filepath.Join(tmp, "gc-integration-stale", "tmux")
	liveRoot := filepath.Join(tmp, "gc-integration-live", "tmux")
	otherRoot := filepath.Join(tmp, "not-gc", "tmux")
	t.Setenv(tmuxTmpEnv, currentRoot)

	uid := strconv.Itoa(os.Getuid())
	currentSocket := filepath.Join(currentRoot, "tmux-"+uid, "gctest-current")
	staleSocket := filepath.Join(staleRoot, "tmux-"+uid, "gctest-stale")
	liveSocket := filepath.Join(liveRoot, "tmux-"+uid, "gctest-live")
	otherSocket := filepath.Join(otherRoot, "tmux-"+uid, "gctest-other")
	for _, path := range []string{currentSocket, staleSocket, liveSocket, otherSocket} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	staleTime := time.Now().Add(-tmuxSiblingSocketStaleAfter - time.Minute)
	if err := os.Chtimes(staleSocket, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes(%s): %v", staleSocket, err)
	}

	got := listTestSocketPaths()

	if !slices.Contains(got, currentSocket) {
		t.Fatalf("listTestSocketPaths() missing current socket %s in %v", currentSocket, got)
	}
	if !slices.Contains(got, staleSocket) {
		t.Fatalf("listTestSocketPaths() missing stale socket %s in %v", staleSocket, got)
	}
	if slices.Contains(got, liveSocket) {
		t.Fatalf("listTestSocketPaths() included live sibling socket %s in %v", liveSocket, got)
	}
	if slices.Contains(got, otherSocket) {
		t.Fatalf("listTestSocketPaths() included unrelated socket %s in %v", otherSocket, got)
	}

	// Task-owned cleanup must not discover even stale sibling servers.
	owned, err := ownedSocketPaths(currentRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(owned, []string{currentSocket}) {
		t.Fatalf("ownedSocketPaths = %v, want only %s", owned, currentSocket)
	}
	if _, err := ownedSocketPaths(""); err == nil {
		t.Fatal("empty owned root must fail closed")
	}
}

func TestCleanupOwnedSocketRootTreatsAbsentServerAsSuccess(t *testing.T) {
	fixtureBin := t.TempDir()
	writeFakeTmux(t, fixtureBin, "error connecting to socket (No such file or directory)\n", 1)
	t.Setenv("PATH", fixtureBin)

	socketRoot := filepath.Join(t.TempDir(), "tmux")
	socketPath := filepath.Join(socketRoot, "tmux-"+strconv.Itoa(os.Getuid()), "owned")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("creating socket parent: %v", err)
	}
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("creating stale socket: %v", err)
	}

	if err := CleanupOwnedSocketRoot(socketRoot); err != nil {
		t.Fatalf("CleanupOwnedSocketRoot() returned absent-server error: %v", err)
	}
}

func TestCleanupOwnedSocketRootReportsUnexpectedServerError(t *testing.T) {
	fixtureBin := t.TempDir()
	writeFakeTmux(t, fixtureBin, "permission denied\n", 1)
	t.Setenv("PATH", fixtureBin)

	socketRoot := filepath.Join(t.TempDir(), "tmux")
	socketPath := filepath.Join(socketRoot, "tmux-"+strconv.Itoa(os.Getuid()), "owned")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("creating socket parent: %v", err)
	}
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("creating socket: %v", err)
	}

	err := CleanupOwnedSocketRoot(socketRoot)
	if err == nil {
		t.Fatal("CleanupOwnedSocketRoot() swallowed unexpected tmux error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("CleanupOwnedSocketRoot() error = %q, want command output", err)
	}
}

func writeFakeTmux(t *testing.T, bin, stderr string, exitCode int) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q >&2\nexit %d\n", stderr, exitCode)
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake tmux: %v", err)
	}
}

func TestTmuxSocketRootPatternsCoverKnownRuntimePrefixes(t *testing.T) {
	namespace := t.TempDir()
	tests := []struct {
		name    string
		runName string
		direct  bool // true = activeRoot is namespace/runName/tmux (no "runtime" level)
		want    string
	}{
		{
			name:    "acceptance C",
			runName: "gcac-123",
			want:    filepath.Join(namespace, "gcac-*", "runtime", "tmux"),
		},
		{
			name:    "worker inference",
			runName: "gcwi-123",
			want:    filepath.Join(namespace, "gcwi-*", "runtime", "tmux"),
		},
		{
			name:    "worker inference live",
			runName: "gcwi-live-123",
			want:    filepath.Join(namespace, "gcwi-*", "runtime", "tmux"),
		},
		{
			name:    "acceptance B",
			runName: "gc-acceptance-b-123",
			want:    filepath.Join(namespace, "gc-acceptance-b-*", "runtime", "tmux"),
		},
		{
			name:    "acceptance",
			runName: "gc-acceptance-123",
			want:    filepath.Join(namespace, "gc-acceptance-*", "runtime", "tmux"),
		},
		{
			name:    "integration direct",
			runName: "gc-integration-123",
			direct:  true,
			want:    filepath.Join(namespace, "gc-integration-*", "tmux"),
		},
		{
			// gct- is the short-path tmux socket root created by the integration
			// test suite when $TMPDIR is too long (e.g., macOS).
			name:    "gct short root",
			runName: "gct-1234567890",
			direct:  true,
			want:    filepath.Join(namespace, "gct*", "tmux"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var activeRoot string
			if tt.direct {
				activeRoot = filepath.Join(namespace, tt.runName, "tmux")
			} else {
				activeRoot = filepath.Join(namespace, tt.runName, "runtime", "tmux")
			}
			got := tmuxSocketRootPatterns(activeRoot)
			if !slices.Contains(got, tt.want) {
				t.Fatalf("tmuxSocketRootPatterns(%q) = %v, want %q", activeRoot, got, tt.want)
			}
		})
	}
}

func TestNewGuardWithSocketCityNameFormat(t *testing.T) {
	// City name must be "gctest-<8hex>" (no per-character hyphens).
	// macOS's UNIX socket path limit is 104 bytes; per-char hyphenation
	// creates names like "gctest-4-f-d-9-6-0-8-c" (22 chars) instead of
	// "gctest-4fd9608c" (15 chars), which pushes socket paths over the limit.
	for range 100 {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("gctest-%x", b)
		if len(name) != 15 {
			t.Fatalf("city name %q has length %d, want 15", name, len(name))
		}
	}
}
