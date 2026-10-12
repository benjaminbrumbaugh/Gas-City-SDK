package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	removeRetryAttempts = 10
	removeRetryDelay    = 50 * time.Millisecond
)

// retryRemoveAllForTest retries removal to absorb a lingering embedded-dolt
// background writer that can hold files open past the owning bd subprocess's
// apparent exit. It logs a final failure so TempDir's own cleanup still gets
// the last word without turning a teardown race into a product failure.
func retryRemoveAllForTest(t *testing.T, dir string, remove func(string) error) {
	t.Helper()
	if err := retryRemoveAll(dir, remove, removeRetryAttempts, removeRetryDelay); err != nil {
		t.Logf("guarded removal of %s exhausted %d attempts: %v", dir, removeRetryAttempts, err)
	}
}

func retryRemoveAll(dir string, remove func(string) error, attempts int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = remove(dir)
		if lastErr == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	if lastErr == nil {
		return errors.New("removal attempt budget must be positive")
	}
	return lastErr
}

// guardedTempDir registers retrying cleanup after t.TempDir's cleanup. Go
// runs cleanups in LIFO order, so the retry runs first and leaves TempDir's
// single-shot RemoveAll nothing to trip over.
func guardedTempDir(t *testing.T) string {
	t.Helper()
	return guardedTempDirWith(t, os.RemoveAll)
}

func guardedTempDirWith(t *testing.T, remove func(string) error) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { retryRemoveAllForTest(t, dir, remove) })
	return dir
}

// testOwnedHome pins HOME to a guarded directory. bd writes $HOME/.beads/
// while resolving its configuration, so HOME needs the same cleanup contract
// as the test's working directory.
func testOwnedHome(t *testing.T) string {
	t.Helper()
	home := guardedTempDir(t)
	t.Setenv("HOME", home)
	return home
}

func TestRetryRemoveAllRetriesUntilRemovalSucceeds(t *testing.T) {
	calls := 0
	err := retryRemoveAll(t.TempDir(), func(string) error {
		calls++
		if calls < 3 {
			return errors.New("directory not empty")
		}
		return nil
	}, 10, 0)
	if calls != 3 {
		t.Fatalf("remove called %d times, want 3 (two failures, then success)", calls)
	}
	if err != nil {
		t.Fatalf("retryRemoveAll returned %v, want nil once a removal succeeds", err)
	}
}

func TestRetryRemoveAllStopsAtItsAttemptBudget(t *testing.T) {
	calls := 0
	wantErr := errors.New("directory not empty")
	err := retryRemoveAll(t.TempDir(), func(string) error {
		calls++
		return wantErr
	}, 4, 0)
	if calls != 4 {
		t.Fatalf("remove called %d times, want 4 (the attempt budget)", calls)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("retryRemoveAll returned %v, want the final failure %v", err, wantErr)
	}
}

func TestGuardedTempDirRegistersTheRetryingRemoval(t *testing.T) {
	calls := 0
	t.Run("guarded", func(t *testing.T) {
		guardedTempDirWith(t, func(string) error {
			calls++
			if calls < 3 {
				return errors.New("directory not empty")
			}
			return nil
		})
	})
	if calls != 3 {
		t.Fatalf("registered remove called %d times, want 3 (two failures, then success)", calls)
	}
}

func TestGuardedTempDirRemovesItsDirWhenTheTestEnds(t *testing.T) {
	var dir string
	t.Run("guarded", func(t *testing.T) {
		dir = guardedTempDir(t)
		if err := os.WriteFile(filepath.Join(dir, "leftover"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Stat(%s) after the subtest returned err=%v, want the dir removed", dir, err)
	}
}

func TestTestOwnedHomePinsHOMEToAGuardedTempDir(t *testing.T) {
	var home string
	t.Run("pinned", func(t *testing.T) {
		home = testOwnedHome(t)
		if got := os.Getenv("HOME"); got != home {
			t.Fatalf("HOME = %q, want the test-owned dir %q", got, home)
		}
		if err := os.MkdirAll(filepath.Join(home, ".beads"), 0o700); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("Stat(%s) after the subtest returned err=%v, want the test-owned HOME removed", home, err)
	}
}

func TestGuardedTempDirRemovalRunsBeforeTempDirsOwnCleanup(t *testing.T) {
	var removedBeforeBase bool
	t.Run("guarded", func(t *testing.T) {
		_ = t.TempDir()
		var dir string
		t.Cleanup(func() {
			_, err := os.Stat(dir)
			removedBeforeBase = os.IsNotExist(err)
		})
		dir = guardedTempDir(t)
	})
	if !removedBeforeBase {
		t.Fatal("guardedTempDir cleanup did not run before TempDir's own cleanup")
	}
}
