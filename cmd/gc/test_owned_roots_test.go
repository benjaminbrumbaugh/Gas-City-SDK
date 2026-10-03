package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Creation is not authority to sweep the inherited allocation namespace.
func TestOwnedRootAllocationPreservesAdjacentRoots(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	foreign := filepath.Join(parent, "pfx2147483647-canary")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := holdAliveSentinel(foreign)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(foreign, old, old); err != nil {
		t.Fatal(err)
	}
	root, sentinel, err := createActiveTestTempRoot("pfx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Close()
		_ = os.RemoveAll(root)
	})
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("allocation swept adjacent root: %v", err)
	}
}

func TestOwnedSocketAllocationPreservesAdjacentRoots(t *testing.T) {
	parent := t.TempDir()
	foreign := filepath.Join(parent, "gct-2147483647-canary")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := holdAliveSentinel(foreign)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(foreign, old, old); err != nil {
		t.Fatal(err)
	}
	_, cleanup, sentinel, err := cmdGCTmuxSocketRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sentinel != nil {
			_ = sentinel.Close()
		}
		_ = os.RemoveAll(cleanup)
	})
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("socket allocation swept adjacent root: %v", err)
	}
}

func TestOwnedDoltGuardCannotClaimCheckoutOrAdjacentRoots(t *testing.T) {
	root := t.TempDir()
	guard := newDoltLeakGuardedTestingM(nil, root)
	configs := []DoltProcInfo{
		{PID: 901, Argv: []string{"dolt", "sql-server", "--config", filepath.Join(root, "owned.yaml")}},
		{PID: 902, Argv: []string{"dolt", "sql-server", "--config", filepath.Join(guard.sourceRoot, "foreign.yaml")}},
		{PID: 903, Argv: []string{"dolt", "sql-server", "--config", filepath.Join(guard.checkoutRoot, "foreign.yaml")}},
		{PID: 904, Argv: []string{"dolt", "sql-server", "--config", root + "-adjacent/foreign.yaml"}},
	}
	got, err := snapshotDoltProcessesForConfigRoots(func() ([]DoltProcInfo, error) { return configs, nil }, guard.leakRoots())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[901].PID != 901 {
		t.Fatalf("kill attribution includes foreign process: %+v", got)
	}
}
