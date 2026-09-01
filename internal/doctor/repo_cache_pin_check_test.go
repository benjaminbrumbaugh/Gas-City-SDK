package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/packman"
)

// gc-y8nnl / gc-w4bpj. A repo-cache directory is named sha256(cloneURL+commit),
// so its name is a promise about which commit it holds. When that promise was
// broken — the directory keyed for 249e7b479 checked out to 0060ab901 — every
// config load in the city failed for the width of the window, and the first
// thing anyone learned was that unrelated subsystems had stopped.

const (
	pinSource = "https://github.com/benjaminbrumbaugh/Gas-City-SDK.git//examples/bd"
	pinKeyed  = "249e7b4795a232d888739e0dbb1b76708072ecc9"
	pinOther  = "0060ab901785c769810ed81623064adb183a27ea"
)

// pinCity writes a packs.lock pinning source at commit, and materializes a
// cache directory that LOOKS like a real checkout.
func pinCity(t *testing.T, commit string) (cityPath, cacheDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	cityPath = t.TempDir()
	lock := fmt.Sprintf("schema = 1\n\n[packs.%q]\nversion = \"sha:%s\"\ncommit = %q\nfetched = \"2026-08-30T00:00:00Z\"\n",
		pinSource, commit, commit)
	if err := os.WriteFile(filepath.Join(cityPath, "packs.lock"), []byte(lock), 0o644); err != nil {
		t.Fatalf("WriteFile(packs.lock): %v", err)
	}

	var err error
	cacheDir, err = packman.RepoCachePath(pinSource, commit)
	if err != nil {
		t.Fatalf("RepoCachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cacheDir, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return cityPath, cacheDir
}

func checkWithHead(head string) *RepoCachePinCheck {
	return &RepoCachePinCheck{
		runGit: func(string, ...string) (string, error) { return head, nil },
	}
}

// The incident's own shape: the directory keyed for one commit holds another.
func TestRepoCachePinReportsADriftedCheckout(t *testing.T) {
	cityPath, cacheDir := pinCity(t, pinKeyed)

	res := checkWithHead(pinOther).Run(&CheckContext{CityPath: cityPath})

	if res.Status != StatusError {
		t.Fatalf("status = %v, want error; msg = %s", res.Status, res.Message)
	}
	joined := res.Message + " " + strings.Join(res.Details, " ")
	// The finding has to name all three, or it cannot be acted on: which pin,
	// what the directory actually holds, and where.
	for _, want := range []string{pinKeyed, pinOther, cacheDir} {
		if !strings.Contains(joined, want) {
			t.Errorf("finding does not name %q:\n  %s", want, joined)
		}
	}
}

// A cache holding exactly what it is keyed for is the ordinary case and must be
// silent, or the check is noise on every healthy city.
func TestRepoCachePinPassesWhenTheCacheHoldsItsCommit(t *testing.T) {
	cityPath, _ := pinCity(t, pinKeyed)

	res := checkWithHead(pinKeyed).Run(&CheckContext{CityPath: cityPath})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK; msg = %s, details = %v", res.Status, res.Message, res.Details)
	}
}

// An abbreviated PIN still matches the full HEAD. gitutil.SameCommit tolerates
// an abbreviated expected value, which is the direction that occurs in
// practice: `git rev-parse HEAD` always returns a full sha, while packs.lock
// can carry a short one. Reading that as drift would report a healthy cache on
// every city whose lock was written with an abbreviation.
func TestRepoCachePinAcceptsAnAbbreviatedPin(t *testing.T) {
	cityPath, _ := pinCity(t, pinKeyed[:12])

	res := checkWithHead(pinKeyed).Run(&CheckContext{CityPath: cityPath})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK for an abbreviated pin; details = %v", res.Status, res.Details)
	}
}

// Case differences are the same commit too.
func TestRepoCachePinAcceptsADifferentlyCasedHead(t *testing.T) {
	cityPath, _ := pinCity(t, pinKeyed)

	res := checkWithHead(strings.ToUpper(pinKeyed)).Run(&CheckContext{CityPath: cityPath})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK for an upper-cased HEAD; details = %v", res.Status, res.Details)
	}
}

// An ABSENT cache is not this check's business. The loader already reports it
// with a remediation that works, and duplicating that would add a second voice
// saying the same thing rather than a second diagnosis.
func TestRepoCachePinIgnoresAnAbsentCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	cityPath := t.TempDir()
	lock := fmt.Sprintf("schema = 1\n\n[packs.%q]\nversion = \"sha:%s\"\ncommit = %q\n", pinSource, pinKeyed, pinKeyed)
	if err := os.WriteFile(filepath.Join(cityPath, "packs.lock"), []byte(lock), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	called := false
	c := &RepoCachePinCheck{runGit: func(string, ...string) (string, error) {
		called = true
		return "", nil
	}}
	res := c.Run(&CheckContext{CityPath: cityPath})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK for an absent cache; msg = %s", res.Status, res.Message)
	}
	if called {
		t.Fatal("git was run against a directory that does not exist")
	}
}

// A city with no packs.lock pins nothing. That is a fresh city, not a fault.
func TestRepoCachePinPassesWithNoLockfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	res := NewRepoCachePinCheck().Run(&CheckContext{CityPath: t.TempDir()})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, want OK; msg = %s", res.Status, res.Message)
	}
}

// An unreadable HEAD is reported as an error rather than silently passing:
// "we could not look" must never render as "we looked and it was fine".
func TestRepoCachePinReportsAnUnreadableHead(t *testing.T) {
	cityPath, _ := pinCity(t, pinKeyed)

	c := &RepoCachePinCheck{runGit: func(string, ...string) (string, error) {
		return "", fmt.Errorf("not a git repository")
	}}
	res := c.Run(&CheckContext{CityPath: cityPath})

	if res.Status == StatusOK {
		t.Fatalf("an unreadable HEAD reported OK; msg = %s", res.Message)
	}
}

// The check advertises a fix, because the repair already exists
// (EnsureRepoInCache) and telling an operator to repair it by hand is how the
// cache got mutated in the first place.
func TestRepoCachePinAdvertisesAFix(t *testing.T) {
	if !NewRepoCachePinCheck().CanFix() {
		t.Fatal("CanFix() = false; the drifted-checkout repair exists and is regression-tested")
	}
}
