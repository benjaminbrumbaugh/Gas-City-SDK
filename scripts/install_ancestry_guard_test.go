package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestInstallAncestryGuard runs the shell self-test for
// scripts/install-ancestry-guard.sh, the guard that stops `make install` from
// replacing an installed binary with one that is not its descendant (gc-km00g).
//
// gc-ffi96 added that check to the city's assets/scripts/gc-deploy.sh, where it
// works. `make install` is the other path to the same file, is the documented
// way to install gc, and had no check, no backup, and no record of what it
// replaced — so an install dropped eleven once-running commits and reported
// success. A silent downgrade is indistinguishable from an upgrade in every
// signal an install emits, which is why the guard is fail-closed and why the
// self-test covers the undeterminable cases as carefully as the bad ones.
//
// Hermetic: real throwaway git repos and shell-script stand-ins for the
// installed binary, no network and no bd calls. HOME is overridden so a
// developer's global git config cannot reach the temp repos.
func TestInstallAncestryGuard(t *testing.T) {
	root := repoRoot(t)

	cmd := exec.Command(filepath.Join(root, "scripts", "test-install-ancestry-guard.sh"))
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-install-ancestry-guard.sh failed: %v\n%s", err, out)
	}
}
