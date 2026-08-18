//go:build darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// TestWatchConfigTargets_DarwinCleanupReleasesWatcherDescriptors guards the
// lifecycle contract relied on by the controller: every watcher returned by
// watchConfigTargets can be closed repeatedly without retaining kqueue-backed
// descriptors. The repeated create/close cycle makes descriptor retention
// visible on macOS without depending on process-global descriptor counts.
func TestWatchConfigTargets_DarwinCleanupReleasesWatcherDescriptors(t *testing.T) {
	root := t.TempDir()
	baseline, ok := darwinOpenDescriptorCount()
	if !ok {
		t.Skip("lsof unavailable; cannot inspect Darwin watcher descriptors")
	}
	for i := 0; i < 256; i++ {
		dir := filepath.Join(root, "watch", string(rune('a'+i/26)), string(rune('a'+i%26)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}

		var dirty atomic.Bool
		var stderr bytes.Buffer
		cleanup := watchConfigTargets([]config.WatchTarget{{Path: dir}}, &dirty, nil, &stderr)
		cleanup()
		cleanup()
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll(%q): %v", dir, err)
		}
	}

	final, ok := darwinOpenDescriptorCount()
	if !ok {
		t.Fatal("lsof failed while checking descriptors after watcher cleanup")
	}
	if final > baseline+32 {
		t.Fatalf("open descriptor count grew from %d to %d after watcher cleanup cycles", baseline, final)
	}

	var dirty atomic.Bool
	var stderr bytes.Buffer
	cleanup := watchConfigTargets([]config.WatchTarget{{Path: root}}, &dirty, nil, &stderr)
	cleanup()
	if got := stderr.String(); got != "" {
		t.Fatalf("watcher lifecycle emitted errors after cleanup: %s", got)
	}
}

func darwinOpenDescriptorCount() (int, bool) {
	cmd := exec.Command("lsof", "-nP", "-p", strconv.Itoa(os.Getpid()))
	output, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, false
	}
	return len(lines) - 1, true
}
