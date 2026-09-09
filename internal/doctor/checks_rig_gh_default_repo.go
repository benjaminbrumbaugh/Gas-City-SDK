package doctor

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
)

// githubHost is the only forge host this check reasons about. Remotes on any
// other host (including GitHub Enterprise) are ignored rather than guessed at,
// so the check stays quiet instead of warning about a topology it cannot read.
const githubHost = "github.com"

// defaultPushRemote is the remote git pushes to when remote.pushDefault is unset.
const defaultPushRemote = "origin"

// RigGHDefaultRepoCheck warns when the GitHub CLI would resolve a rig repo to a
// different repository than the one `git push` targets.
//
// With two or more GitHub remotes and no `gh repo set-default`, gh picks a base
// repo by remote name alone — "upstream", then "github", then "origin", then
// anything else — so a fork whose canonical repo is wired up as `upstream`
// silently sends every bare `gh pr create` / `gh pr view` to upstream. Pack
// handoffs that shell out to gh then create cross-fork PRs or validate a PR
// number against the wrong repository, and nothing in the output says so.
//
// SeverityAdvisory; WarmupEligible. Detection is local git config only — no gh
// binary and no network call.
type RigGHDefaultRepoCheck struct {
	rig     config.Rig
	gitPath func(name string) (string, error) // injectable for tests
}

// NewRigGHDefaultRepoCheck creates a gh default-repository check for the given rig.
func NewRigGHDefaultRepoCheck(rig config.Rig) *RigGHDefaultRepoCheck {
	return &RigGHDefaultRepoCheck{rig: rig, gitPath: exec.LookPath}
}

// Name returns the check identifier.
func (c *RigGHDefaultRepoCheck) Name() string { return "rig:" + c.rig.Name + ":gh-default-repo" }

// WarmupEligible returns true so this check runs during gc start warm-up.
func (c *RigGHDefaultRepoCheck) WarmupEligible() bool { return true }

// CanFix returns false: `gh repo set-default` writes the shared repository
// config that every worktree of the rig reads, so it stays an operator decision.
func (c *RigGHDefaultRepoCheck) CanFix() bool { return false }

// Fix is a no-op.
func (c *RigGHDefaultRepoCheck) Fix(_ *CheckContext) error { return nil }

// Run compares the repository gh would resolve implicitly against the one git
// pushes to.
func (c *RigGHDefaultRepoCheck) Run(_ *CheckContext) *CheckResult {
	r := &CheckResult{Name: c.Name(), Severity: SeverityAdvisory}

	gitBin, err := c.gitPath("git")
	if err != nil {
		return c.unableToDetermine(r)
	}
	if _, err := runGitCommand(gitBin, c.rig.Path, "rev-parse", "--git-dir"); err != nil {
		return c.unableToDetermine(r)
	}
	lines, err := gitConfigLines(gitBin, c.rig.Path, `^remote\.`)
	if err != nil {
		return c.unableToDetermine(r)
	}
	urls, pushURLs, resolved, pushDefault := parseRemoteConfig(lines)

	repos := make(map[string]string, len(urls))
	for name, raw := range urls {
		if pushURL := pushURLs[name]; pushURL != "" {
			raw = pushURL
		}
		if repo, ok := gitHubRepoFromRemoteURL(raw); ok {
			repos[name] = repo
		}
	}
	if len(repos) < 2 {
		r.Status = StatusOK
		r.Message = fmt.Sprintf("rig %q: gh base-repo resolution is unambiguous (%d GitHub remote(s))", c.rig.Name, len(repos))
		return r
	}

	ordered := ghRemoteOrder(repos)
	for _, name := range ordered {
		value, pinned := resolved[name]
		if !pinned {
			continue
		}
		pinnedRepo := repos[name]
		if value != "" && !strings.EqualFold(value, "base") {
			pinnedRepo = value
		}
		r.Status = StatusOK
		r.Message = fmt.Sprintf("rig %q: gh default repository pinned to %s (remote %q)", c.rig.Name, pinnedRepo, name)
		return r
	}

	pushRemote := pushDefault
	if pushRemote == "" {
		pushRemote = defaultPushRemote
	}
	pushRepo, ok := repos[pushRemote]
	if !ok {
		r.Status = StatusOK
		r.Message = fmt.Sprintf("rig %q: push remote %q is not a GitHub remote — no expected repository to compare against", c.rig.Name, pushRemote)
		return r
	}

	// Compare repositories rather than remote names: several remotes may point
	// at the same repository, and then gh and git agree despite gh picking a
	// different remote.
	ghRemote := ordered[0]
	ghRepo := repos[ghRemote]
	if strings.EqualFold(ghRepo, pushRepo) {
		r.Status = StatusOK
		if ghRemote == pushRemote {
			r.Message = fmt.Sprintf("rig %q: gh and git push both target %s (remote %q)", c.rig.Name, ghRepo, pushRemote)
		} else {
			r.Message = fmt.Sprintf("rig %q: gh and git push both target %s (remotes %q and %q)", c.rig.Name, ghRepo, ghRemote, pushRemote)
		}
		return r
	}

	r.Status = StatusWarning
	r.Message = fmt.Sprintf("rig %q: gh resolves to %s but git push targets %s", c.rig.Name, ghRepo, pushRepo)
	r.FixHint = fmt.Sprintf("(cd %q && gh repo set-default %s)", c.rig.Path, pushRepo)
	r.Details = []string{
		fmt.Sprintf("no `gh repo set-default` recorded; remote %q (%s) outranks %q in gh's name-only ordering", ghRemote, ghRepo, pushRemote),
		fmt.Sprintf("git push targets remote %q (%s)", pushRemote, pushRepo),
		"formulas that shell out to `gh pr create` / `gh pr view` would read and write the wrong repository",
	}
	return r
}

// unableToDetermine reports the shared warning for a path git cannot read.
func (c *RigGHDefaultRepoCheck) unableToDetermine(r *CheckResult) *CheckResult {
	r.Status = StatusWarning
	r.Message = fmt.Sprintf("rig %q: unable to determine remotes — git unavailable or path is not a git repo", c.rig.Name)
	return r
}

// gitConfigLines returns the `git config --get-regexp` output lines for pattern.
// A no-match exit status is not an error: it means the repo sets no such key.
func gitConfigLines(gitBin, dir, pattern string) ([]string, error) {
	out, err := runGitCommand(gitBin, dir, "config", "--get-regexp", pattern)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// parseRemoteConfig splits `remote.*` config lines into remote URLs, push URLs,
// the gh-resolved markers `gh repo set-default` writes, and remote.pushDefault.
//
// git lowercases section and variable names but preserves subsection names, so
// a remote's own name keeps its case while `remote.pushDefault` comes back as
// `remote.pushdefault`. Remote names may themselves contain dots, so the
// variable is taken from the last dot-separated segment.
func parseRemoteConfig(lines []string) (urls, pushURLs, resolved map[string]string, pushDefault string) {
	urls = make(map[string]string)
	pushURLs = make(map[string]string)
	resolved = make(map[string]string)
	for _, line := range lines {
		// A valueless key (set with no value) is printed alone, with no
		// trailing space, so an absent separator means an empty value.
		key, value, _ := strings.Cut(line, " ")
		rest, isRemote := strings.CutPrefix(key, "remote.")
		if !isRemote {
			continue
		}
		dot := strings.LastIndex(rest, ".")
		if dot < 0 {
			if strings.EqualFold(rest, "pushdefault") {
				pushDefault = value
			}
			continue
		}
		name, variable := rest[:dot], rest[dot+1:]
		switch strings.ToLower(variable) {
		case "url":
			urls[name] = value
		case "pushurl":
			pushURLs[name] = value
		case "gh-resolved":
			resolved[name] = value
		}
	}
	return urls, pushURLs, resolved, pushDefault
}

// ghRemoteOrder returns the remote names in the order gh considers them when
// choosing a base repository: descending name score, ties broken by git's own
// alphabetical listing order.
func ghRemoteOrder(repos map[string]string) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	sort.SliceStable(names, func(i, j int) bool {
		return ghRemoteNameScore(names[i]) > ghRemoteNameScore(names[j])
	})
	return names
}

// ghRemoteNameScore mirrors the GitHub CLI's remote-name ranking, which is the
// only signal gh has when no default repository has been recorded. Higher wins.
func ghRemoteNameScore(name string) int {
	switch strings.ToLower(name) {
	case "upstream":
		return 3
	case "github":
		return 2
	case "origin":
		return 1
	default:
		return 0
	}
}

// gitHubRepoFromRemoteURL extracts "owner/repo" from a github.com remote URL,
// accepting the https, ssh, git, and scp-like forms git writes.
func gitHubRepoFromRemoteURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp-like: [user@]host:owner/repo
		hostPart := raw
		if at := strings.LastIndex(raw, "@"); at >= 0 {
			hostPart = raw[at+1:]
		}
		h, p, ok := strings.Cut(hostPart, ":")
		if !ok {
			return "", false
		}
		host, path = h, p
	}

	host = strings.ToLower(host)
	if host != githubHost && host != "www."+githubHost {
		return "", false
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	owner, repo, ok := strings.Cut(path, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", false
	}
	return owner + "/" + repo, true
}
