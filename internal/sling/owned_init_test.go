package sling

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Run the real, unoverlaid binary with no selected tests: init runs before
// TestMain and must not treat the inherited temp namespace as owned state.
func TestOwnedSlingPackageInitPreservesAdjacentRoots(t *testing.T) {
	root := t.TempDir()
	for _, prefix := range []string{slingTestFormulaDirPrefix, slingTestCityDirPrefix} {
		for _, suffix := range []string{"-unlocked", "-held"} {
			dir := filepath.Join(root, prefix+"2147483647"+suffix)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "canary"), []byte(dir), 0o600); err != nil {
				t.Fatal(err)
			}
			lock, err := os.Create(filepath.Join(dir, ".gc-test-alive.lock"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.Close() })
			if suffix == "-held" {
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "TMPDIR="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unoverlaid init: %v\n%s", err, out)
	}
	for _, prefix := range []string{slingTestFormulaDirPrefix, slingTestCityDirPrefix} {
		for _, suffix := range []string{"-unlocked", "-held"} {
			dir := filepath.Join(root, prefix+"2147483647"+suffix)
			if got, err := os.ReadFile(filepath.Join(dir, "canary")); err != nil || string(got) != dir {
				t.Errorf("package init deleted/changed adjacent canary %s: %q, %v", dir, got, err)
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Errorf("owned teardown left %d entries; want exactly four untouched canary roots", len(entries))
	}
}
