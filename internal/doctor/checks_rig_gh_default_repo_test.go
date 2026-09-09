package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// RED tests for RigGHDefaultRepoCheck (sdk-42t). A rig repo with several
// GitHub remotes and no `gh repo set-default` resolution lets the GitHub CLI
// silently pick a remote other than the one `git push` targets, so pack
// handoffs that shell out to `gh pr create` / `gh pr view` read and write the
// wrong repository.

type ghRemote struct {
	name string
	url  string
}

func TestRigGHDefaultRepoCheck_SingleGitHubRemote_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK", r.Status, r.Message)
	}
	if r.FixHint != "" {
		t.Errorf("FixHint = %q, want empty for OK result", r.FixHint)
	}
}

func TestRigGHDefaultRepoCheck_NoRemotes_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, nil)

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK", r.Status, r.Message)
	}
}

// The exact shape reported in sdk-42t: origin is the fork, upstream is the
// canonical repo, and gh's implicit ordering ranks upstream above origin.
func TestRigGHDefaultRepoCheck_UpstreamOutranksOrigin_WarnsAdvisory(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/benjaminbrumbaugh/Gas-City-SDK.git"},
		{"preservation", "https://github.com/benjaminbrumbaugh/Gas-City-SDK-Archive.git"},
		{"upstream", "https://github.com/gastownhall/gascity.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning", r.Status, r.Message)
	}
	if r.Severity != SeverityAdvisory {
		t.Fatalf("severity = %d, want SeverityAdvisory", r.Severity)
	}
	if !strings.Contains(r.Message, "gastownhall/gascity") {
		t.Errorf("message = %q, want the repo gh would resolve to", r.Message)
	}
	if !strings.Contains(r.Message, "benjaminbrumbaugh/Gas-City-SDK") {
		t.Errorf("message = %q, want the repo git push targets", r.Message)
	}
	if !strings.Contains(r.FixHint, "gh repo set-default benjaminbrumbaugh/Gas-City-SDK") {
		t.Errorf("FixHint = %q, want a gh repo set-default hint for the push remote", r.FixHint)
	}
	if !strings.Contains(r.FixHint, rigPath) {
		t.Errorf("FixHint = %q, want the rig path so the hint is runnable", r.FixHint)
	}
	joined := strings.Join(r.Details, "\n")
	if !strings.Contains(joined, "upstream") || !strings.Contains(joined, "origin") {
		t.Errorf("Details = %v, want both remote names as evidence", r.Details)
	}
}

func TestRigGHDefaultRepoCheck_ExplicitDefaultSet_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"upstream", "https://github.com/acme/canonical.git"},
	})
	runGitForGHDefaultRepoTest(t, rigPath, "config", "remote.origin.gh-resolved", "base")

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK once gh repo set-default has run", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "origin") {
		t.Errorf("message = %q, want the resolved remote name", r.Message)
	}
}

// A valueless `gh-resolved` key is printed by git as a bare key with no
// value, which must still count as a recorded default.
func TestRigGHDefaultRepoCheck_ValuelessResolvedKey_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"upstream", "https://github.com/acme/canonical.git"},
	})
	cfgPath := filepath.Join(rigPath, ".git", "config")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read git config: %v", err)
	}
	patched := strings.Replace(string(cfg),
		"[remote \"origin\"]\n", "[remote \"origin\"]\n\tgh-resolved\n", 1)
	if patched == string(cfg) {
		t.Fatalf("could not inject valueless key into config:\n%s", cfg)
	}
	if err := os.WriteFile(cfgPath, []byte(patched), 0o600); err != nil {
		t.Fatalf("write git config: %v", err)
	}

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK for a valueless gh-resolved key", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "acme/fork") {
		t.Errorf("message = %q, want the pinned remote's repository", r.Message)
	}
}

// origin outranks every unrecognized remote name, so gh already agrees with
// git push and there is nothing to warn about.
func TestRigGHDefaultRepoCheck_OriginOutranksOtherRemotes_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"preservation", "https://github.com/acme/archive.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK when gh already picks origin", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "acme/fork") {
		t.Errorf("message = %q, want the agreed repository", r.Message)
	}
}

// Two remote names for one repository: gh picks "upstream", git pushes to
// "origin", but both resolve to the same repository, so there is nothing to warn about.
func TestRigGHDefaultRepoCheck_DistinctRemotesSameRepository_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"upstream", "git@github.com:acme/fork.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK when both remotes are the same repository", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "acme/fork") {
		t.Errorf("message = %q, want the shared repository", r.Message)
	}
	if r.FixHint != "" {
		t.Errorf("FixHint = %q, want empty for OK result", r.FixHint)
	}
}

func TestRigGHDefaultRepoCheck_PushDefaultOverridesOrigin_Warns(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"github", "https://github.com/acme/mirror.git"},
		{"origin", "https://github.com/acme/fork.git"},
	})
	runGitForGHDefaultRepoTest(t, rigPath, "config", "remote.pushDefault", "origin")

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning when gh picks the mirror", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "acme/mirror") || !strings.Contains(r.Message, "acme/fork") {
		t.Errorf("message = %q, want both repositories named", r.Message)
	}
}

func TestRigGHDefaultRepoCheck_PushDefaultAgreesWithGH_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"upstream", "https://github.com/acme/canonical.git"},
	})
	runGitForGHDefaultRepoTest(t, rigPath, "config", "remote.pushDefault", "upstream")

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK when push and gh agree", r.Status, r.Message)
	}
}

// Only one remote is hosted on GitHub, so gh has nothing to disambiguate.
func TestRigGHDefaultRepoCheck_NonGitHubRemotesIgnored_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "https://github.com/acme/fork.git"},
		{"upstream", "https://gitlab.com/acme/canonical.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK when only one remote is on GitHub", r.Status, r.Message)
	}
}

func TestRigGHDefaultRepoCheck_SSHRemoteURLsParsed_Warns(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"origin", "git@github.com:acme/fork.git"},
		{"upstream", "ssh://git@github.com/acme/canonical.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning for ssh remotes too", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "acme/canonical") || !strings.Contains(r.Message, "acme/fork") {
		t.Errorf("message = %q, want owner/repo parsed from ssh URLs", r.Message)
	}
	if !strings.Contains(r.FixHint, "gh repo set-default acme/fork") {
		t.Errorf("FixHint = %q, want set-default for the ssh push remote", r.FixHint)
	}
}

// The push remote is missing entirely, so there is no ground truth for what
// gh ought to resolve to; stay quiet rather than guess.
func TestRigGHDefaultRepoCheck_NoPushRemote_OK(t *testing.T) {
	rigPath := initGitRepoWithRemotes(t, []ghRemote{
		{"github", "https://github.com/acme/mirror.git"},
		{"upstream", "https://github.com/acme/canonical.git"},
	})

	r := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: rigPath}).Run(&CheckContext{})

	if r.Status != StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK with no origin and no pushDefault", r.Status, r.Message)
	}
}

func TestRigGHDefaultRepoCheck_NotGitRepository_WarnsUnableToDetermine(t *testing.T) {
	c := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: t.TempDir()})

	r := c.Run(&CheckContext{})

	if r.Status != StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning", r.Status, r.Message)
	}
	if r.Severity != SeverityAdvisory {
		t.Fatalf("severity = %d, want SeverityAdvisory", r.Severity)
	}
	if !strings.Contains(r.Message, "unable to determine remotes") {
		t.Errorf("message = %q, want unable-to-determine warning", r.Message)
	}
}

func TestRigGHDefaultRepoCheck_GitUnavailable_WarnsUnableToDetermine(t *testing.T) {
	c := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: t.TempDir()})
	c.gitPath = func(string) (string, error) { return "", errors.New("git unavailable") }

	r := c.Run(&CheckContext{})

	if r.Status != StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "unable to determine remotes") {
		t.Errorf("message = %q, want unable-to-determine warning", r.Message)
	}
}

func TestRigGHDefaultRepoCheck_Metadata(t *testing.T) {
	c := NewRigGHDefaultRepoCheck(config.Rig{Name: "testrig", Path: t.TempDir()})

	if got, want := c.Name(), "rig:testrig:gh-default-repo"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if !c.WarmupEligible() {
		t.Error("WarmupEligible() = false, want true — the check is local and cheap")
	}
	if c.CanFix() {
		t.Error("CanFix() = true, want false — set-default writes shared repo config")
	}
	if err := c.Fix(&CheckContext{}); err != nil {
		t.Errorf("Fix() = %v, want nil no-op", err)
	}
}

func initGitRepoWithRemotes(t *testing.T, remotes []ghRemote) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	// Isolate from ambient user/system git config: a machine-global
	// remote.pushDefault would otherwise change what the check compares.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	runGitForGHDefaultRepoTest(t, dir, "init")
	runGitForGHDefaultRepoTest(t, dir, "config", "user.name", "GH Default Repo Test")
	runGitForGHDefaultRepoTest(t, dir, "config", "user.email", "gh-default-repo@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("initial\n"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGitForGHDefaultRepoTest(t, dir, "add", "README.md")
	runGitForGHDefaultRepoTest(t, dir, "commit", "-m", "initial")
	for _, remote := range remotes {
		runGitForGHDefaultRepoTest(t, dir, "remote", "add", remote.name, remote.url)
	}
	return dir
}

func runGitForGHDefaultRepoTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
