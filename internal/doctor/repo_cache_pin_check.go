package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	gitutil "github.com/gastownhall/gascity/internal/git"
	"github.com/gastownhall/gascity/internal/packman"
)

// RepoCachePinCheck reports any pinned repo-cache directory whose checkout is
// not at the commit its directory name encodes.
//
// The shared repo cache is content-addressed: a directory name is
// sha256(cloneURL + commit) (config.RepoCacheKey), and every reader — the
// config loader above all — derives the path it reads from the commit it wants.
// A directory name is therefore a promise about which commit that directory
// holds.
//
// gc-w4bpj is what breaking that promise costs. The directory keyed for
// 249e7b479 was checked out to 0060ab901 in place for about 55 seconds; for the
// width of that window every config load in the city failed, 886 identical
// lines in the supervisor log, so no order could execute and no named session
// could be resolved or started.
//
// The loader already rejects this, but only lazily, per import, and by failing
// the ENTIRE config load — so the first thing an operator learns is that
// unrelated subsystems have stopped. This check asks the same question ahead of
// time and answers it as one named, repairable finding (gc-y8nnl).
//
// gc's own writers cannot produce the state: every path derives its cache path
// from packman.RepoCachePath, and EnsureRepoInCache asserts that before writing.
// The mutation in the incident came from OUTSIDE gc — the directory was fetched
// and checked out by hand — which is exactly why a check is worth having: the
// population at risk is every city on the machine that pins the mutated key,
// not just the one whose repin caused it.
type RepoCachePinCheck struct {
	// runGit is the git runner, replaced in tests.
	runGit func(dir string, args ...string) (string, error)
}

// NewRepoCachePinCheck builds the check with the real git runner.
func NewRepoCachePinCheck() *RepoCachePinCheck {
	return &RepoCachePinCheck{runGit: runRepoCachePinGit}
}

// Name returns the check identifier shown by gc doctor.
func (c *RepoCachePinCheck) Name() string { return "repo-cache-pin" }

// WarmupEligible opts this check INTO `gc start`'s warm-up scan.
//
// The state it detects breaks every config load in the city, which makes start
// the moment you most want to know: a city coming up against a mutated cache
// fails its first reconcile and reports a cascade of unrelated symptoms —
// order dispatch, session resolution, desired-state — instead of the one cause.
// The cost is one `git rev-parse` per pinned import, only for imports whose
// cache directory already exists, which is a handful of execs against warm
// local repositories.
func (c *RepoCachePinCheck) WarmupEligible() bool { return true }

// CanFix reports that a drifted checkout is repairable.
//
// The repair is packman.EnsureRepoInCache, which moves the directory back to
// the commit it is keyed for under the repo-cache write lock, offline when the
// object is already present. It is the same recovery `gc import install`
// performs, and gc-w4bpj added a regression test proving it works.
func (c *RepoCachePinCheck) CanFix() bool { return true }

type repoCachePinFinding struct {
	source string
	commit string
	head   string
	dir    string
}

func (c *RepoCachePinCheck) inspect(ctx *CheckContext) ([]repoCachePinFinding, error) {
	lock, err := readRepoCachePins(ctx.CityPath)
	if err != nil {
		return nil, err
	}
	sources := make([]string, 0, len(lock))
	for source := range lock {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	var findings []repoCachePinFinding
	for _, source := range sources {
		commit := lock[source]
		dir, err := packman.RepoCachePath(source, commit)
		if err != nil {
			return nil, fmt.Errorf("resolving cache path for %s: %w", source, err)
		}
		// An ABSENT directory is not this check's business. The loader reports
		// it with a remediation that works, and reporting it here too would
		// duplicate a diagnosis rather than add one.
		gitInfo, statErr := os.Stat(filepath.Join(dir, ".git"))
		if gitutil.MissingCheckoutMarker(gitInfo, statErr) || statErr != nil {
			continue
		}
		// A bundled source at its canonical pin is served from embedded
		// content and validated by its own synthetic-repo marker, not by a git
		// HEAD, so asking git about it would report a false drift.
		if config.IsBundledSourceAtCanonicalPin(source, commit) {
			continue
		}
		head, err := c.runGit(dir, "rev-parse", "HEAD")
		if err != nil {
			// Unreadable is not drifted. Say so rather than guessing.
			return nil, fmt.Errorf("reading HEAD of %s: %w", dir, err)
		}
		if gitutil.SameCommit(head, commit) {
			continue
		}
		findings = append(findings, repoCachePinFinding{
			source: source, commit: commit, head: strings.TrimSpace(head), dir: dir,
		})
	}
	return findings, nil
}

// Run checks every commit this city pins against the cache directory that
// commit hashes to.
func (c *RepoCachePinCheck) Run(ctx *CheckContext) *CheckResult {
	r := &CheckResult{Name: c.Name()}
	findings, err := c.inspect(ctx)
	if err != nil {
		r.Status = StatusError
		r.Message = fmt.Sprintf("checking repo cache pins: %v", err)
		return r
	}
	if len(findings) == 0 {
		r.Status = StatusOK
		r.Message = "every pinned repo cache holds the commit it is keyed for"
		return r
	}
	r.Status = StatusError
	r.Message = fmt.Sprintf("%d repo cache director%s checked out at the wrong commit",
		len(findings), map[bool]string{true: "y is", false: "ies are"}[len(findings) == 1])
	for _, f := range findings {
		r.Details = append(r.Details, fmt.Sprintf(
			"%s: cache is keyed for %s but HEAD is %s (%s) — every config load resolving this pin fails until it is restored",
			f.source, f.commit, f.head, f.dir))
	}
	r.FixHint = "run \"gc doctor --fix\", or \"gc import install\", to restore each directory to the commit it is keyed for"
	return r
}

// Fix restores each drifted directory to the commit it is keyed for.
func (c *RepoCachePinCheck) Fix(ctx *CheckContext) error {
	findings, err := c.inspect(ctx)
	if err != nil {
		return err
	}
	for _, f := range findings {
		if _, err := packman.EnsureRepoInCache(ctx.CityPath, f.source, f.commit); err != nil {
			return fmt.Errorf("restoring %s to %s: %w", f.dir, f.commit, err)
		}
	}
	return nil
}

// readRepoCachePins returns this city's source -> commit pins from packs.lock.
// A city with no packs.lock pins nothing, which is not an error.
func readRepoCachePins(cityPath string) (map[string]string, error) {
	lock, err := packman.ReadLockfile(fsys.OSFS{}, cityPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	pins := make(map[string]string, len(lock.Packs))
	for source, pack := range lock.Packs {
		if strings.TrimSpace(pack.Commit) == "" {
			continue
		}
		pins[source] = pack.Commit
	}
	return pins, nil
}

func runRepoCachePinGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitutil.HermeticEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}
