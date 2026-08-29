package dolt_test

// Backup-remote name aliasing (gc-534qw). ensure_backup_remote looked a
// database's backup destination up strictly by the name `<db>-backup`. A
// database whose backup remote carries a different name — `default`, which is
// what `bd backup` configures — was therefore treated as having no backup at
// all, and the auto-configure fallback then tried to add `<db>-backup` at the
// very path the existing remote already occupies. Dolt refuses that:
//
//	Error 1105 (HY000): address conflict with a remote: 'default' -> file://...
//
// The script discarded that stderr and reported an opaque "backup add failed".
// Two consequences, both observed live on this city's `gcd` database: that
// database stopped being backed up entirely, and because the whole-city stamp
// is gated on a clean sweep, `.beads/dolt-backup-state.json` stopped advancing
// for every database — so `gc doctor` bd-backup-freshness warned permanently
// and the reaper's bulk-prune gate re-latched.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addConflictStderr is the verbatim Dolt 2.1.0 refusal, reproduced against this
// city's gcd database on 2026-08-29.
const addConflictStderr = "Error 1105 (HY000): address conflict with a remote: " +
	"'default' -> file://ARTIFACT/legacy"

// writeAliasBackupFakeDolt fakes a server with prod + legacy. prod carries the
// managed `prod-backup` name; legacy carries aliasName pointing at aliasPath
// (with the literal ARTIFACT replaced by the artifact dir), or no remote at all
// when aliasName is empty. `backup add` always fails with the address-conflict
// refusal, exactly as Dolt does when the destination is already claimed.
func writeAliasBackupFakeDolt(t *testing.T, binDir, aliasName, aliasPath string) string {
	t.Helper()
	logPath := filepath.Join(binDir, "dolt.log")
	legacyArm := "    :\n"
	if aliasName != "" {
		legacyArm = fmt.Sprintf("    printf '%s file://%%s {}\\n' \"%s\"\n", aliasName, aliasPath)
	}
	writeExecutable(t, filepath.Join(binDir, "dolt"), fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
printf 'dolt %%s\n' "$*" >> %s
if [ "${1:-}" = "version" ]; then
  printf 'dolt version 2.3.1\n'
  exit 0
fi
case "$*" in
  *"SHOW DATABASES"*)
    printf 'Database\nprod\nlegacy\n'
    exit 0
    ;;
esac
artifact_dir="${GC_BACKUP_ARTIFACT_DIR:-$GC_CITY_PATH/.dolt-backup}"
if [ "${1:-} ${2:-}" = "backup -v" ]; then
  if [ "$(basename "$PWD")" = "prod" ]; then
    printf 'prod-backup file://%%s/prod {}\n' "$artifact_dir"
  else
%s  fi
  exit 0
fi
if [ "${1:-} ${2:-}" = "backup add" ]; then
  printf '%%s\n' "%s" >&2
  exit 1
fi
if [ "${1:-} ${2:-}" = "backup sync" ]; then
  exit 0
fi
exit 0
`, shellQuote(logPath), legacyArm, addConflictStderr))
	return logPath
}

// aliasCity builds a two-database city with a .beads workspace holding a stale
// stamp, and returns (cityPath, dataDir, statePath, staleContents).
func aliasCity(t *testing.T) (string, string, string, string) {
	t.Helper()
	cityPath := t.TempDir()
	dataDir := filepath.Join(cityPath, "dolt-data")
	beadsDir := filepath.Join(cityPath, ".beads")
	for _, path := range []string{
		filepath.Join(dataDir, "prod", ".dolt"),
		filepath.Join(dataDir, "legacy", ".dolt"),
		beadsDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	statePath := filepath.Join(beadsDir, "dolt-backup-state.json")
	stale := "{\n  \"last_sync\": \"2026-08-28T18:05:04Z\",\n  \"duration\": \"1s\"\n}\n"
	if err := os.WriteFile(statePath, []byte(stale), 0o600); err != nil {
		t.Fatalf("write stale state: %v", err)
	}
	return cityPath, dataDir, statePath, stale
}

// TestBackupScriptAcceptsBackupRemoteUnderADifferentName is the live gcd shape.
// A remote named `default` already backs the database up to the managed
// artifact path; that IS coverage, so it must be used rather than duplicated.
func TestBackupScriptAcceptsBackupRemoteUnderADifferentName(t *testing.T) {
	cityPath, dataDir, statePath, stale := aliasCity(t)
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	aliasPath := filepath.Join(cityPath, ".dolt-backup", "legacy")
	doltLogPath := writeAliasBackupFakeDolt(t, binDir, "default", aliasPath)

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 2/2") {
		t.Fatalf("a differently-named remote at the managed path is coverage:\n%s", out)
	}
	doltLog, err := os.ReadFile(doltLogPath)
	if err != nil {
		t.Fatalf("read dolt log: %v", err)
	}
	// The sync must address the remote by the name that actually exists.
	if !strings.Contains(string(doltLog), "backup sync --prune-with-grace-period 1h default") {
		t.Fatalf("legacy must sync via its existing remote name:\n%s", doltLog)
	}
	if strings.Contains(string(doltLog), "backup add legacy-backup") {
		t.Fatalf("must not duplicate a destination that is already backed up:\n%s", doltLog)
	}
	// The whole-city stamp is gated on a clean sweep, so it only advances once
	// no database is spuriously counted as failed.
	if !strings.Contains(out, "state: ok") {
		t.Fatalf("a clean sweep must stamp the freshness signal:\n%s", out)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(got) == stale {
		t.Fatalf("stamp did not advance; still %q", stale)
	}
}

// TestBackupScriptSurfacesBackupAddFailureStderr keeps the next occurrence
// diagnosable. The address-conflict refusal was discarded by `2>&1 >/dev/null`,
// leaving only "backup add failed" — which is why this took a day to place.
func TestBackupScriptSurfacesBackupAddFailureStderr(t *testing.T) {
	cityPath, dataDir, _, _ := aliasCity(t)
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	// No remote at all for legacy, so auto-configure runs and fails.
	_ = writeAliasBackupFakeDolt(t, binDir, "", "")

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 1/2") {
		t.Fatalf("an unconfigurable database must still be counted failed:\n%s", out)
	}
	if !strings.Contains(out, "address conflict with a remote") {
		t.Fatalf("the underlying dolt refusal must be surfaced:\n%s", out)
	}
}

// TestBackupScriptRejectsDifferentlyNamedRemoteOutsideArtifactPath is the
// guard. Accepting any name must not become accepting any destination: a
// remote pointing outside the managed artifact path is not coverage this
// script may claim, whatever it is called.
func TestBackupScriptRejectsDifferentlyNamedRemoteOutsideArtifactPath(t *testing.T) {
	cityPath, dataDir, statePath, stale := aliasCity(t)
	binDir := t.TempDir()
	_ = writeDogFakeGC(t, binDir)
	outside := filepath.Join(t.TempDir(), "elsewhere", "legacy")
	doltLogPath := writeAliasBackupFakeDolt(t, binDir, "default", outside)

	out := runDogScript(t, "mol-dog-backup.sh", binDir, cityPath, dataDir)
	if !strings.Contains(out, "synced: 1/2") {
		t.Fatalf("an off-path remote must not count as coverage:\n%s", out)
	}
	doltLog, err := os.ReadFile(doltLogPath)
	if err != nil {
		t.Fatalf("read dolt log: %v", err)
	}
	if strings.Contains(string(doltLog), "backup sync --prune-with-grace-period 1h default") {
		t.Fatalf("must not sync through an off-path remote:\n%s", doltLog)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(got) != stale {
		t.Fatalf("incomplete coverage must leave the stamp stale:\nwant %q\ngot %q", stale, got)
	}
}
