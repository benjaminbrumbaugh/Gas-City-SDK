package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/git"
)

// rigGitignoreDoctorCheck reports drift between the canonical rig ignore rules
// and the paths actually tracked by the rig. It is deliberately read-only:
// repair requires an operator to choose whether to edit .gitignore and remove
// already-tracked paths from the index.
type rigGitignoreDoctorCheck struct {
	rig      config.Rig
	gitPath  func(string) (string, error)
	readFile func(string) ([]byte, error)
}

// NewRigGitignoreDoctorCheck creates a read-only rig .gitignore check.
func NewRigGitignoreDoctorCheck(rig config.Rig) *rigGitignoreDoctorCheck {
	return &rigGitignoreDoctorCheck{
		rig:      rig,
		gitPath:  exec.LookPath,
		readFile: os.ReadFile,
	}
}

// Name returns the check identifier.
func (c *rigGitignoreDoctorCheck) Name() string { return "rig:" + c.rig.Name + ":gitignore" }

// WarmupEligible keeps this filesystem/index inspection on explicit doctor
// runs; startup warm-up already has the per-rig path and git checks.
func (c *rigGitignoreDoctorCheck) WarmupEligible() bool { return false }

// CanFix returns false because adding ignore rules and removing tracked paths
// from a rig index are operator-owned repository changes.
func (c *rigGitignoreDoctorCheck) CanFix() bool { return false }

// Fix is a no-op.
func (c *rigGitignoreDoctorCheck) Fix(_ *doctor.CheckContext) error { return nil }

// Run checks both sides of the rig ignore contract: canonical rules in the
// persistent .gitignore and paths covered by those rules in Git's index.
func (c *rigGitignoreDoctorCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	r := &doctor.CheckResult{Name: c.Name(), Severity: doctor.SeverityAdvisory}
	var details []string

	gitignorePath := filepath.Join(c.rig.Path, ".gitignore")
	data, err := c.readFile(gitignorePath)
	switch {
	case err == nil:
		present := make(map[string]bool)
		for _, line := range strings.Split(string(data), "\n") {
			present[strings.TrimSpace(line)] = true
		}
		for _, entry := range rigGitignoreEntries {
			if !present[entry] {
				details = append(details, fmt.Sprintf(".gitignore is missing canonical entry %q", entry))
			}
		}
	case os.IsNotExist(err):
		for _, entry := range rigGitignoreEntries {
			details = append(details, fmt.Sprintf(".gitignore is missing canonical entry %q", entry))
		}
	default:
		details = append(details, fmt.Sprintf("unable to read %s: %v", gitignorePath, err))
	}

	tracked, err := c.trackedCoveredPaths()
	if err != nil {
		details = append(details, fmt.Sprintf("unable to inspect tracked paths: %v", err))
	} else {
		for _, path := range tracked {
			details = append(details, fmt.Sprintf("Git index tracks covered path %q", path))
		}
	}

	if len(details) == 0 {
		r.Status = doctor.StatusOK
		r.Message = fmt.Sprintf("rig %q has canonical beads ignore rules and no tracked covered paths", c.rig.Name)
		return r
	}

	r.Status = doctor.StatusWarning
	r.Message = fmt.Sprintf("rig %q has .gitignore/index drift", c.rig.Name)
	r.Details = details
	r.FixHint = fmt.Sprintf("add the canonical entries to %q and remove covered paths from the index with git rm --cached", gitignorePath)
	return r
}

func (c *rigGitignoreDoctorCheck) trackedCoveredPaths() ([]string, error) {
	gitBin, err := c.gitPath("git")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(gitBin, "ls-files", "--cached", "-z", "--", ".beads")
	cmd.Dir = c.rig.Path
	cmd.Env = git.SanitizedEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var covered []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" || path == ".beads/identity.toml" || !strings.HasPrefix(path, ".beads/") {
			continue
		}
		covered = append(covered, path)
	}
	return covered, nil
}
