package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBDSplitStoreCheck_DoesNotClaimBothDirsHoldDataWhenOneIsEmpty pins gc-dm9.
//
// bd creates both .beads/dolt and .beads/embeddeddolt eagerly and leaves the
// unused one behind. The check's guards test only directory EXISTENCE
// (splitStoreDirExists is os.Stat + IsDir), so when no active local store could
// be identified it reported
//
//	"legacy split store detected: both .beads/dolt and .beads/embeddeddolt
//	 contain or may contain data, but no active local store was identified"
//
// as soon as EITHER side held a repo. On the rigs measured for this bead one
// side was empty, so the sentence was simply false, and a warning whose text is
// reliably wrong trains readers past the whole bd-split-store class — which is
// what leaves a genuine two-sided split indistinguishable from the noise.
//
// This asserts the MESSAGE is accurate. It deliberately does not assert the
// warning disappears: see the sibling test below for why.
func TestBDSplitStoreCheck_DoesNotClaimBothDirsHoldDataWhenOneIsEmpty(t *testing.T) {
	for name, tc := range map[string]struct {
		build     func(t *testing.T, beadsDir string)
		populated string
		empty     string
	}{
		// The shape observed on Wayfinder and Gas-City-Wayfinder-Plugin.
		"empty dolt beside a populated embeddeddolt": {
			build: func(t *testing.T, beadsDir string) {
				if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeDoltRepoMarker(t, filepath.Join(beadsDir, "embeddeddolt", "jc"))
			},
			populated: "embeddeddolt",
			empty:     "dolt",
		},
		// The mirror image, so the fix cannot be a one-sided special case.
		"empty embeddeddolt beside a populated dolt": {
			build: func(t *testing.T, beadsDir string) {
				writeDoltRepoMarker(t, filepath.Join(beadsDir, "dolt", "jc"))
				if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			populated: "dolt",
			empty:     "embeddeddolt",
		},
		// A directory holding only non-repo subdirectories is still empty of
		// ledgers — "contains entries" is not the same as "contains data".
		"dolt holds subdirectories but no Dolt repo": {
			build: func(t *testing.T, beadsDir string) {
				if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", "leftover", "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeDoltRepoMarker(t, filepath.Join(beadsDir, "embeddeddolt", "jc"))
			},
			populated: "embeddeddolt",
			empty:     "dolt",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			beadsDir := filepath.Join(dir, ".beads")
			if err := os.MkdirAll(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			// No metadata.json: this is the branch where no active local
			// store can be identified, which is where the false claim lived.
			tc.build(t, beadsDir)

			r := NewBDSplitStoreCheck(dir).Run(&CheckContext{})

			if strings.Contains(r.Message, "both .beads/dolt and .beads/embeddeddolt contain or may contain data") {
				t.Fatalf("message claims both directories hold data while .beads/%s is empty: %q", tc.empty, r.Message)
			}
			if !strings.Contains(r.Message, ".beads/"+tc.populated) || !strings.Contains(r.Message, ".beads/"+tc.empty) {
				t.Fatalf("message should name which directory holds data and which is empty; got %q", r.Message)
			}
			if !strings.Contains(r.Message, "is empty") {
				t.Fatalf("message should state that one directory is empty; got %q", r.Message)
			}
		})
	}
}

// TestBDSplitStoreCheck_OnePopulatedDirWithNoActiveStoreStillWarns is the
// counterweight, and it is the reason this bead's suggested fix ("only flag
// when BOTH paths contain at least one entry") was not implemented literally.
//
// One populated local directory with no active local store is unread bead data
// beside a ledger that lives elsewhere — the hazard the external-city and
// inherited-rig tests already pin. Routing it into the not-a-split branch would
// hand it to unreadStoreBesideTheActiveOne, which returns nil unless the active
// store is one of the two LOCAL dirs, so the result would have been a silent OK
// and a real signal lost.
func TestBDSplitStoreCheck_OnePopulatedDirWithNoActiveStoreStillWarns(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeDoltRepoMarker(t, filepath.Join(beadsDir, "embeddeddolt", "jc"))

	r := NewBDSplitStoreCheck(dir).Run(&CheckContext{})
	if r.Status != StatusWarning {
		t.Fatalf("status = %d, want Warning — unread local data with no active local store; msg = %q", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "no active local store") {
		t.Fatalf("message = %q, want it to keep naming the missing active local store", r.Message)
	}
	if r.FixHint == "" {
		t.Fatal("a warning must carry a fix hint")
	}
}

// TestBDSplitStoreCheck_RealSplitKeepsItsOwnMessage guards the genuine
// two-sided case — repos on BOTH sides with no identifiable active store, which
// is the layout Gas-City-SDK actually has. Its wording must survive untouched,
// because there the "both ... contain or may contain data" sentence is true.
func TestBDSplitStoreCheck_RealSplitKeepsItsOwnMessage(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	writeDoltRepoMarker(t, filepath.Join(beadsDir, "dolt", "jc"))
	writeDoltRepoMarker(t, filepath.Join(beadsDir, "embeddeddolt", "jc"))

	r := NewBDSplitStoreCheck(dir).Run(&CheckContext{})
	if r.Status != StatusWarning {
		t.Fatalf("status = %d, want Warning; msg = %q", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "both .beads/dolt and .beads/embeddeddolt contain or may contain data") {
		t.Fatalf("message = %q, want the two-sided split wording preserved", r.Message)
	}
}

// TestBDSplitStoreCheck_BothSidesEmptyKeepsItsOwnMessage guards the other
// message boundary. "Directories present, no repos anywhere" already reported
// OK and says so specifically; the new one-sided wording must not swallow it,
// because the two states call for different cleanup.
func TestBDSplitStoreCheck_BothSidesEmptyKeepsItsOwnMessage(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	for _, sub := range []string{"dolt", "embeddeddolt"} {
		if err := os.MkdirAll(filepath.Join(beadsDir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	r := NewBDSplitStoreCheck(dir).Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Fatalf("status = %d, want OK; msg = %q", r.Status, r.Message)
	}
	if r.Message != "legacy split store directories present but no Dolt repos found" {
		t.Fatalf("message = %q, want the both-empty message preserved", r.Message)
	}
}
