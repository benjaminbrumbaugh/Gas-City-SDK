package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The city went fully offline for ~90s when every `gc` config load failed with
// "remote import ... is locked but not cached at <path>; run gc import install"
// (gc-w4bpj). Two things about that message made the incident unreadable.
//
// First, it named the cache directory but not the commit. The cache path is a
// sha256 of source+commit, so it is not something an operator can decode. By
// the time anyone looked, packs.lock had moved on — and `gc import install`
// then correctly installed whatever the lock said at that later moment while
// the logged directory stayed absent. That reads as a broken install rather
// than as a stale line about a pin that is no longer current, and it is exactly
// the wrong conclusion the incident report reached.
//
// Second, the same remediation was printed for a genuinely different situation:
// an import with no packs.lock entry at all. There `gc import install` is not
// merely unhelpful, it cannot work — it is packman.InstallLocked, which
// iterates the entries already in the lock. With no entry for the source it
// exits 0 having created nothing, leaving the operator in the state that
// produced the error.

func TestLockedButNotCachedErrorNamesTheCommit(t *testing.T) {
	const commit = "0060ab901785c769810ed81623064adb183a27ea"
	const source = "https://github.com/benjaminbrumbaugh/Gas-City-SDK.git//examples/bd"

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, RepoCacheKey(source, commit))

	err := validateInstalledRemoteCache(source, cacheDir, commit)
	if err == nil {
		t.Fatalf("expected an error for an absent cache directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, commit) {
		t.Errorf("error does not name the commit it wanted, so it cannot be read after packs.lock moves on:\n  %s", msg)
	}
	if !strings.Contains(msg, cacheDir) {
		t.Errorf("error dropped the cache path:\n  %s", msg)
	}
	// This case IS repairable by install: the lock names the commit, so
	// InstallLocked will fetch it. The remediation must stay.
	if !strings.Contains(msg, "gc import install") {
		t.Errorf("locked-but-not-cached should still point at gc import install:\n  %s", msg)
	}
}

func TestMissingLockEntryDoesNotSendOperatorToImportInstallAlone(t *testing.T) {
	const source = "https://github.com/example/pack.git//pack"

	got := noLockEntryRemediation(source, "sha:249e7b4795a232d888739e0dbb1b76708072ecc9")

	// The whole point: install alone is a dead end here, and the message has to
	// say so rather than leave the operator to discover it by running it.
	if !strings.Contains(got, "gc import add") && !strings.Contains(got, "gc import upgrade") {
		t.Errorf("remediation for a missing lock entry names no command that can create one:\n  %s", got)
	}
	if !strings.Contains(got, "exit 0") && !strings.Contains(got, "nothing to restore") {
		t.Errorf("remediation does not warn that gc import install will appear to succeed:\n  %s", got)
	}
}

// resolveInstalledRemoteImport is the loader path the city actually took. With
// no packs.lock at all, the error must carry the corrected remediation rather
// than the bare install pointer.
func TestResolveInstalledRemoteImportWithoutLockExplainsTheRealRepair(t *testing.T) {
	cityRoot := t.TempDir()
	const source = "https://github.com/example/pack.git//pack"

	_, err := resolveInstalledRemoteImport(source, "sha:249e7b4795a232d888739e0dbb1b76708072ecc9", cityRoot, false)
	if err == nil {
		t.Fatalf("expected an error with no packs.lock present")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing packs.lock") {
		t.Fatalf("unexpected error shape: %s", msg)
	}
	if !strings.Contains(msg, "gc import add") && !strings.Contains(msg, "gc import upgrade") {
		t.Errorf("missing-packs.lock error still sends the operator only to gc import install:\n  %s", msg)
	}
}

func TestResolveInstalledRemoteImportWithoutEntryExplainsTheRealRepair(t *testing.T) {
	cityRoot := t.TempDir()
	const source = "https://github.com/example/pack.git//pack"

	// A lock that exists but records a different source.
	lock := "schema = 1\n\n[packs]\n[packs.\"https://github.com/example/other.git//other\"]\ncommit = \"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef\"\n"
	if err := os.WriteFile(filepath.Join(cityRoot, "packs.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveInstalledRemoteImport(source, "sha:249e7b4795a232d888739e0dbb1b76708072ecc9", cityRoot, false)
	if err == nil {
		t.Fatalf("expected an error with no lock entry for this source")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing packs.lock entry") {
		t.Fatalf("unexpected error shape: %s", msg)
	}
	if !strings.Contains(msg, "gc import add") && !strings.Contains(msg, "gc import upgrade") {
		t.Errorf("missing-entry error still sends the operator only to gc import install:\n  %s", msg)
	}
}
