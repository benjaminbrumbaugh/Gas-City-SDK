package dolt_test

// Per-scope backup freshness stamping (gc-i37v7). mol-dog-backup backs up every
// database it finds, including databases owned by other beads workspaces — a rig
// scope's database is stored in the city's Dolt data dir and its backup lands in
// the city's artifact dir. But the script stamped a single freshness file,
// `$GC_CITY_PATH/.beads/dolt-backup-state.json`, so only the city scope's
// signal ever advanced.
//
// `gc doctor` bd-backup-freshness reads that file per scope root, and reaper.sh
// step 6 gates its closed-session-bead bulk prune on it, so a rig whose data was
// backed up on schedule still looked permanently un-backed-up and kept its prune
// gate latched. Observed on Gas-City-Dashboard, whose stamp was written once by
// `bd backup sync` at registration and never again.
//
// The whole-city clean-sweep gate is deliberately preserved: a partial sync
// stamps nothing, anywhere.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScopeWorkspace creates <root>/.beads bound to doltDB. When registered is
// true it also writes dolt-backup.json, the marker that a Dolt backup
// destination exists for that scope.
func writeScopeWorkspace(t *testing.T, root, doltDB string, registered bool) {
	t.Helper()
	beadsDir := filepath.Join(root, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", beadsDir, err)
	}
	meta := fmt.Sprintf("{\n  \"backend\": \"dolt\",\n  \"dolt_database\": %q,\n  \"dolt_mode\": \"server\"\n}\n", doltDB)
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	if registered {
		reg := fmt.Sprintf("{\n  \"backup_url\": \"file:///tmp/%s\",\n  \"backup_name\": \"default\"\n}\n", doltDB)
		if err := os.WriteFile(filepath.Join(beadsDir, "dolt-backup.json"), []byte(reg), 0o644); err != nil {
			t.Fatalf("write dolt-backup.json: %v", err)
		}
	}
}

// writeCityRoutes writes the city's routes.jsonl, the list of workspaces this
// city binds. Paths are relative to the city root, as they are live.
func writeCityRoutes(t *testing.T, cityPath string, routes map[string]string) {
	t.Helper()
	var b strings.Builder
	for prefix, path := range routes {
		fmt.Fprintf(&b, "{\"prefix\":%q,\"path\":%q}\n", prefix, path)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".beads", "routes.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write routes.jsonl: %v", err)
	}
}

// stampLastSync reads a dolt-backup-state.json and returns its last_sync, or ""
// when the file is absent.
func stampLastSync(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var state struct {
		LastSync string `json:"last_sync"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return state.LastSync
}

// scopeStampCity builds a city with a peer scope, both backed by databases the
// fake dolt will sync cleanly. Returns (cityPath, dataDir, peerRoot).
func scopeStampCity(t *testing.T, peerRegistered bool, peerStampSeed string) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	cityPath := filepath.Join(base, "city")
	peerRoot := filepath.Join(base, "peer")
	dataDir := filepath.Join(cityPath, "dolt-data")
	for _, db := range []string{"prod", "peerdb"} {
		if err := os.MkdirAll(filepath.Join(dataDir, db, ".dolt"), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", db, err)
		}
	}
	writeScopeWorkspace(t, cityPath, "prod", true)
	writeScopeWorkspace(t, peerRoot, "peerdb", peerRegistered)
	writeCityRoutes(t, cityPath, map[string]string{"prod": ".", "peerdb": "../peer"})
	if peerStampSeed != "" {
		seed := fmt.Sprintf("{\n  \"last_sync\": %q,\n  \"duration\": \"64.579917ms\"\n}\n", peerStampSeed)
		if err := os.WriteFile(filepath.Join(peerRoot, ".beads", "dolt-backup-state.json"), []byte(seed), 0o600); err != nil {
			t.Fatalf("seed peer stamp: %v", err)
		}
	}
	return cityPath, dataDir, peerRoot
}

// TestBackupScriptStampsPeerScopeFreshness is the Gas-City-Dashboard shape: a
// peer workspace whose database this city backs up must have its own freshness
// signal advanced, not just the city's.
func TestBackupScriptStampsPeerScopeFreshness(t *testing.T) {
	cityPath, dataDir, peerRoot := scopeStampCity(t, true, "2026-08-28T19:18:46.096559Z")
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	writeScopeStampFakeDolt(t, binDir)

	peerStamp := filepath.Join(peerRoot, ".beads", "dolt-backup-state.json")
	before := stampLastSync(t, peerStamp)

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 2/2") || !strings.Contains(out, "state: ok") {
		t.Fatalf("expected a clean sweep:\n%s", out)
	}
	after := stampLastSync(t, peerStamp)
	if after == before || after == "" {
		t.Fatalf("peer scope stamp did not advance (before %q, after %q):\n%s", before, after, out)
	}
	if strings.Contains(after, ".") {
		t.Fatalf("stamp must be whole-second UTC for the reaper's strptime, got %q", after)
	}
	// The city's own stamp must still advance too.
	if got := stampLastSync(t, filepath.Join(cityPath, ".beads", "dolt-backup-state.json")); got == "" {
		t.Fatalf("city stamp missing:\n%s", out)
	}
}

// TestBackupScriptDoesNotInventPeerScopeStamp keeps the "do not fabricate"
// rule one level out: a scope with neither an existing stamp nor a registered
// Dolt destination has no backup configured for it, which DoltBackupCheck
// reports. Writing a stamp there would assert coverage nobody registered and
// newly enroll that scope in bd-backup-freshness.
func TestBackupScriptDoesNotInventPeerScopeStamp(t *testing.T) {
	cityPath, dataDir, peerRoot := scopeStampCity(t, false, "")
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	writeScopeStampFakeDolt(t, binDir)

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 2/2") {
		t.Fatalf("expected a clean sweep:\n%s", out)
	}
	peerStamp := filepath.Join(peerRoot, ".beads", "dolt-backup-state.json")
	if _, err := os.Stat(peerStamp); err == nil {
		t.Fatalf("unregistered scope must not be given a stamp:\n%s", out)
	}
}

// TestBackupScriptLeavesPeerScopeStaleOnPartialSync preserves the invariant the
// whole gate exists for: bulk deletion is withheld while coverage is
// incomplete, so a partial sync must stamp nothing — in any scope.
func TestBackupScriptLeavesPeerScopeStaleOnPartialSync(t *testing.T) {
	cityPath, dataDir, peerRoot := scopeStampCity(t, true, "2026-08-28T19:18:46.096559Z")
	// A third database with no remote and a failing `backup add` breaks the sweep.
	if err := os.MkdirAll(filepath.Join(dataDir, "orphan", ".dolt"), 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	writeScopeStampFakeDolt(t, binDir)

	peerStamp := filepath.Join(peerRoot, ".beads", "dolt-backup-state.json")
	before := stampLastSync(t, peerStamp)

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 2/3") || !strings.Contains(out, "state: skipped") {
		t.Fatalf("expected a partial sweep that stamps nothing:\n%s", out)
	}
	if after := stampLastSync(t, peerStamp); after != before {
		t.Fatalf("partial sync advanced a peer stamp (before %q, after %q)", before, after)
	}
}

// writeScopeStampFakeDolt fakes a server exposing prod, peerdb and orphan.
// prod and peerdb each already carry their managed `<db>-backup` remote; orphan
// carries none and `backup add` fails for it, which is how a test opts into a
// partial sweep. Databases absent from the data dir are simply never enumerated.
func writeScopeStampFakeDolt(t *testing.T, binDir string) {
	t.Helper()
	logPath := filepath.Join(binDir, "dolt.log")
	writeExecutable(t, filepath.Join(binDir, "dolt"), fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
printf 'dolt %%s\n' "$*" >> %s
if [ "${1:-}" = "version" ]; then
  printf 'dolt version 2.3.1\n'
  exit 0
fi
case "$*" in
  *"SHOW DATABASES"*)
    printf 'Database\nprod\npeerdb\norphan\n'
    exit 0
    ;;
esac
artifact_dir="${GC_BACKUP_ARTIFACT_DIR:-$GC_CITY_PATH/.dolt-backup}"
db="$(basename "$PWD")"
if [ "${1:-} ${2:-}" = "backup -v" ]; then
  if [ "$db" != "orphan" ]; then
    printf '%%s-backup file://%%s/%%s {}\n' "$db" "$artifact_dir" "$db"
  fi
  exit 0
fi
if [ "${1:-} ${2:-}" = "backup add" ]; then
  exit 1
fi
if [ "${1:-} ${2:-}" = "backup sync" ]; then
  exit 0
fi
exit 0
`, shellQuote(logPath)))
}
