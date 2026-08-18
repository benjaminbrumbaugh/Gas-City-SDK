package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRouteDecisionCommandGroupIsHiddenAndExplicit(t *testing.T) {
	root := newRootCmd(&bytes.Buffer{}, &bytes.Buffer{})
	group, _, err := root.Find([]string{"route-decision"})
	if err != nil || group == nil || !group.Hidden {
		t.Fatalf("route-decision group = (%v, %v), want hidden command", group, err)
	}
	want := map[string]bool{"import-legacy": false, "backup": false, "export": false, "verify": false}
	for _, child := range group.Commands() {
		if _, ok := want[child.Name()]; ok {
			want[child.Name()] = child.Hidden
		}
	}
	for name, hidden := range want {
		if !hidden {
			t.Errorf("route-decision %s missing or visible", name)
		}
	}
}

func TestRouteDecisionCommandRefusesRunningCityBeforeOpeningLedger(t *testing.T) {
	cityRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(cityRoot, ".gc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityRoot, "city.toml"), []byte("schema = 1\n[workspace]\nname = 'test'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(cityRoot, ".gc", "controller.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close() //nolint:errcheck
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck

	var stdout, stderr bytes.Buffer
	code := run([]string{"route-decision", "verify", "--city", cityRoot}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "city controller is running") {
		t.Fatalf("run code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(cityRoot, ".gc", "routing-decisions.db")); !os.IsNotExist(err) {
		t.Fatalf("running-city refusal touched ledger: %v", err)
	}
}
