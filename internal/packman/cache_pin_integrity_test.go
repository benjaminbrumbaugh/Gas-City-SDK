package packman

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The repo cache is content-addressed: a directory name is
// sha256(cloneURL + commit), and every reader — the config loader above all —
// derives the directory it reads from the commit it wants. A directory name is
// therefore a promise about which commit that directory holds.
//
// gc-w4bpj is what breaking the promise costs. The directory keyed for
// 249e7b479 was fetched and checked out to 0060ab901 in place for ~55 seconds
// while packs.lock named 0060ab901, whose own directory had never been
// created. For the width of that window every config load in the city failed —
// 886 identical lines in the supervisor log — so no order could execute and no
// named session could be resolved or started.
//
// These tests run real git rather than the package's runGit/runNetworkGit
// stubs. "Is the other commit's directory still at its own commit" and "does
// this directory hold the tree it is named for" are precisely the questions a
// faked git waves through.

// gitInDir runs git in dir and fails the test on error.
func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %s: %v", strings.Join(args, " "), dir, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out))
}

// twoCommitPackRepo builds a real upstream repository carrying a pack.toml (so
// the cache's own pack validation is satisfied) and a marker file whose content
// differs between the two commits, so serving the wrong tree is observable
// rather than merely inferable from a sha.
func twoCommitPackRepo(t *testing.T) (source, first, second string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "upstream")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	gitInDir(t, repo, "init", "--quiet", "--initial-branch=main")

	write := func(marker string) {
		if err := os.WriteFile(filepath.Join(repo, "pack.toml"),
			[]byte("[pack]\nname = \"repo\"\nschema = 1\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(pack.toml): %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "marker.txt"), []byte(marker), 0o644); err != nil {
			t.Fatalf("WriteFile(marker.txt): %v", err)
		}
	}

	write("first")
	gitInDir(t, repo, "add", "-A")
	gitInDir(t, repo, "commit", "--quiet", "-m", "first")
	first = gitInDir(t, repo, "rev-parse", "HEAD")

	write("second")
	gitInDir(t, repo, "add", "-A")
	gitInDir(t, repo, "commit", "--quiet", "-m", "second")
	second = gitInDir(t, repo, "rev-parse", "HEAD")

	if first == second {
		t.Fatalf("fixture produced one commit twice: %s", first)
	}
	return "file://" + repo, first, second
}

func TestEnsureRepoInCacheGivesEachCommitItsOwnDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	source, first, second := twoCommitPackRepo(t)

	firstDir, err := EnsureRepoInCache("", source, first)
	if err != nil {
		t.Fatalf("EnsureRepoInCache(first): %v", err)
	}
	if head := gitInDir(t, firstDir, "rev-parse", "HEAD"); head != first {
		t.Fatalf("first cache HEAD = %s, want %s", head, first)
	}

	// The second commit is the repin. It must land in its own directory, and
	// the first commit's directory must be untouched: another city, or another
	// import in this one, is still pinned to it and resolves it by name.
	secondDir, err := EnsureRepoInCache("", source, second)
	if err != nil {
		t.Fatalf("EnsureRepoInCache(second): %v", err)
	}
	if secondDir == firstDir {
		t.Fatalf("both commits resolved to one directory %q; a repin would check the\n"+
			"new commit out over the old one and every city pinned to the old key\n"+
			"would be served the wrong tree (gc-w4bpj)", firstDir)
	}
	if head := gitInDir(t, secondDir, "rev-parse", "HEAD"); head != second {
		t.Fatalf("second cache HEAD = %s, want %s", head, second)
	}
	if head := gitInDir(t, firstDir, "rev-parse", "HEAD"); head != first {
		t.Fatalf("the repin moved the first commit's cache to %s; %q is keyed for %s\n"+
			"and this is exactly the in-place checkout that took the city offline (gc-w4bpj)",
			head, firstDir, first)
	}

	// Content, not just shas: each directory holds the tree it is named for.
	for _, tc := range []struct{ dir, want string }{{firstDir, "first"}, {secondDir, "second"}} {
		got, err := os.ReadFile(filepath.Join(tc.dir, "marker.txt"))
		if err != nil {
			t.Fatalf("ReadFile(%s/marker.txt): %v", tc.dir, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s holds marker %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// A cache directory found checked out at a foreign commit is repaired in place
// — that IS its own directory, so moving it back to the commit it is keyed for
// is the repair, not a violation. This is the recovery `gc import install`
// performs, and the incident's operator needed it to work.
func TestEnsureRepoInCacheRestoresADirectoryMutatedOutOfBand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	source, first, second := twoCommitPackRepo(t)

	firstDir, err := EnsureRepoInCache("", source, first)
	if err != nil {
		t.Fatalf("EnsureRepoInCache(first): %v", err)
	}

	// Reproduce the incident's mutation: something outside gc checks the other
	// commit out inside this directory.
	gitInDir(t, firstDir, "checkout", "--quiet", second)
	if head := gitInDir(t, firstDir, "rev-parse", "HEAD"); head != second {
		t.Fatalf("fixture failed to mutate the cache: HEAD = %s", head)
	}

	restored, err := EnsureRepoInCache("", source, first)
	if err != nil {
		t.Fatalf("EnsureRepoInCache did not repair an out-of-band checkout: %v", err)
	}
	if restored != firstDir {
		t.Fatalf("repair moved the cache to %q, want %q", restored, firstDir)
	}
	if head := gitInDir(t, firstDir, "rev-parse", "HEAD"); head != first {
		t.Fatalf("cache HEAD = %s after repair, want %s", head, first)
	}
	if got, err := os.ReadFile(filepath.Join(firstDir, "marker.txt")); err != nil || string(got) != "first" {
		t.Fatalf("marker.txt = %q (err %v) after repair, want %q", got, err, "first")
	}
}

// The invariant stated as an assertion. Every write to the repo cache passes
// assertCanonicalRepoCachePath, so a caller that computes a path some other way
// is refused rather than allowed to make one directory answer to a name that
// means a different commit.
func TestAssertCanonicalRepoCachePathRefusesAForeignDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))

	const source = "https://github.com/benjaminbrumbaugh/Gas-City-SDK.git//examples/bd"
	const keyed = "249e7b4795a232d888739e0dbb1b76708072ecc9"
	const other = "0060ab901785c769810ed81623064adb183a27ea"

	keyedDir, err := RepoCachePath(source, keyed)
	if err != nil {
		t.Fatalf("RepoCachePath: %v", err)
	}

	// The canonical directory for the commit is accepted, including through a
	// non-normalized spelling of the same path.
	if err := assertCanonicalRepoCachePath("write", source, keyed, keyedDir); err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if err := assertCanonicalRepoCachePath("write", source, keyed, keyedDir+string(filepath.Separator)); err != nil {
		t.Fatalf("canonical path rejected for an unclean spelling: %v", err)
	}

	// The incident's shape: writing `other` into the directory keyed for
	// `keyed`.
	err = assertCanonicalRepoCachePath("write", source, other, keyedDir)
	if err == nil {
		t.Fatal("writing a foreign commit into another commit's cache directory was accepted")
	}
	otherDir, pathErr := RepoCachePath(source, other)
	if pathErr != nil {
		t.Fatalf("RepoCachePath: %v", pathErr)
	}
	for _, want := range []string{"non-canonical path", keyedDir, otherDir, other} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q, so it cannot be acted on:\n  %v", want, err)
		}
	}
}
