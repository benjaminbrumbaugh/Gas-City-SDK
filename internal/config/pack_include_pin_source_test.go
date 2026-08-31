package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gc-w4bpj concluded, from the outage's log line naming a cache directory that
// had never existed, that "the reader resolved the import to origin/main HEAD
// rather than the locked sha". Whatever else happened that night, that is not
// what the loader does: resolveInstalledRemoteImport and resolveLockedRemoteImport
// are the only two paths that resolve a remote import, and both derive the cache
// directory from packs.lock's `commit` alone. The declared version — which may
// well be a branch, and in the incident's own pack.toml was a sha — reaches the
// resolver only to be quoted back inside a remediation string.
//
// The distinction matters operationally, because it is the difference between
// "a writer published a pin before materializing it" and "the reader chases a
// moving ref", and only the first is a thing a repin procedure can avoid. This
// test pins the property so a future change cannot quietly introduce the defect
// the bead suspected.
func TestRemoteImportResolvesTheLockedCommitNotAMovingRef(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	ResetRemoteCacheValidationCache()
	t.Cleanup(ResetRemoteCacheValidationCache)

	const source = "https://github.com/example/gastown.git"

	// One upstream, two commits, `main` at the newer one — the shape a repin
	// walks through.
	seedDir := filepath.Join(dir, "seed")
	mustMkdirAll(t, seedDir, 0o755)
	git := func(args ...string) string {
		t.Helper()
		out, err := runRepoCacheGit(seedDir, append([]string{
			"-c", "user.name=Test", "-c", "user.email=test@example.com",
		}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	writePack := func(name string) {
		writeTestFile(t, seedDir, "pack.toml", "[pack]\nname = \""+name+"\"\nschema = 1\n")
	}

	git("init", "--initial-branch=main")
	writePack("locked")
	git("add", "pack.toml")
	git("commit", "-m", "locked")
	lockedCommit := git("rev-parse", "HEAD")

	writePack("tip")
	git("add", "pack.toml")
	git("commit", "-m", "tip")
	tipCommit := git("rev-parse", "HEAD")

	if lockedCommit == tipCommit {
		t.Fatalf("fixture produced one commit twice: %s", lockedCommit)
	}

	// Both commits are cached, so a resolver that followed the ref would
	// succeed and hand back the WRONG directory rather than fail — which is what
	// makes this assertion sharp.
	cacheRoot := filepath.Join(home, ".gc", "cache", "repos")
	mustMkdirAll(t, cacheRoot, 0o755)
	cacheFor := func(commit string) string {
		t.Helper()
		dst := filepath.Join(cacheRoot, RepoCacheKey(source, commit))
		if _, err := runRepoCacheGit(seedDir, "clone", "--quiet", seedDir, dst); err != nil {
			t.Fatalf("clone cache for %s: %v", commit, err)
		}
		if _, err := runRepoCacheGit(dst, "checkout", "--quiet", commit); err != nil {
			t.Fatalf("checkout %s: %v", commit, err)
		}
		return dst
	}
	lockedDir := cacheFor(lockedCommit)
	tipDir := cacheFor(tipCommit)

	writeTestFile(t, dir, "packs.lock", ""+
		"schema = 1\n\n"+
		"[packs.\""+source+"\"]\n"+
		"version = \"main\"\n"+
		"commit = \""+lockedCommit+"\"\n"+
		"fetched = \"2026-08-30T00:00:00Z\"\n")

	// The declared version is the moving ref. Only the lock's commit may decide.
	got, err := resolveInstalledRemoteImport(source, "main", dir, false)
	if err != nil {
		t.Fatalf("resolveInstalledRemoteImport: %v", err)
	}
	if got == tipDir {
		t.Fatalf("resolver followed the declared ref to the branch tip %s;\n"+
			"the locked commit %s is the only thing that may select a cache directory (gc-w4bpj)",
			tipCommit, lockedCommit)
	}
	if got != lockedDir {
		t.Fatalf("resolved cache = %q, want the locked commit's directory %q", got, lockedDir)
	}
	if data, err := os.ReadFile(filepath.Join(got, "pack.toml")); err != nil {
		t.Fatalf("ReadFile(pack.toml): %v", err)
	} else if want := "name = \"locked\""; !strings.Contains(string(data), want) {
		t.Fatalf("resolved cache serves %q, want the tree containing %q", data, want)
	}

	// Same property through resolveLockedRemoteImport, the include-resolution
	// entry point, which reads the same lock independently.
	dirFromInclude, ok, err := resolveLockedRemoteImport(source, dir, false)
	if err != nil || !ok {
		t.Fatalf("resolveLockedRemoteImport: ok = %v, err = %v", ok, err)
	}
	if dirFromInclude != lockedDir {
		t.Fatalf("include resolution = %q, want %q", dirFromInclude, lockedDir)
	}
}

// A lock naming a commit that no writer has cached must say so and stop. It must
// not quietly serve some other cached commit of the same source: that is the
// failure mode that would have turned gc-w4bpj's 90-second outage into a silent
// wrong-content load nobody noticed.
func TestUncachedLockedCommitDoesNotFallBackToAnotherCachedCommit(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	ResetRemoteCacheValidationCache()
	t.Cleanup(ResetRemoteCacheValidationCache)

	const source = "https://github.com/example/gastown.git"
	const cachedCommit = "249e7b4795a232d888739e0dbb1b76708072ecc9"
	const lockedCommit = "0060ab901785c769810ed81623064adb183a27ea"

	// A perfectly good cache for a DIFFERENT commit of the same source.
	cacheRoot := filepath.Join(home, ".gc", "cache", "repos")
	present := filepath.Join(cacheRoot, RepoCacheKey(source, cachedCommit))
	mustMkdirAll(t, filepath.Join(present, ".git"), 0o755)
	writeTestFile(t, present, "pack.toml", "[pack]\nname = \"stale\"\nschema = 1\n")

	writeTestFile(t, dir, "packs.lock", ""+
		"schema = 1\n\n"+
		"[packs.\""+source+"\"]\n"+
		"version = \"sha:"+lockedCommit+"\"\n"+
		"commit = \""+lockedCommit+"\"\n"+
		"fetched = \"2026-08-30T00:00:00Z\"\n")

	_, err := resolveInstalledRemoteImport(source, "sha:"+lockedCommit, dir, false)
	if err == nil {
		t.Fatal("an uncached locked commit resolved successfully; it must not fall back to another cached commit")
	}
	for _, want := range []string{lockedCommit, "not cached"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q:\n  %v", want, err)
		}
	}
	if strings.Contains(err.Error(), cachedCommit) {
		t.Errorf("error points at the other cached commit %s, which is not what the lock asked for:\n  %v", cachedCommit, err)
	}
}
