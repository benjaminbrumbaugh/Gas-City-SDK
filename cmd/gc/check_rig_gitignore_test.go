package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
)

func TestRigGitignoreDoctorCheck_MissingCanonicalEntriesWarns(t *testing.T) {
	rigPath := initRigGitignoreTestRepo(t, "node_modules/\n")
	check := NewRigGitignoreDoctorCheck(config.Rig{Name: "legacy", Path: rigPath})

	result := check.Run(&doctor.CheckContext{})

	if result.Status != doctor.StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning", result.Status, result.Message)
	}
	if result.Severity != doctor.SeverityAdvisory {
		t.Fatalf("severity = %d, want SeverityAdvisory", result.Severity)
	}
	for _, entry := range rigGitignoreEntries {
		if !resultDetailsContain(result.Details, entry) {
			t.Errorf("details = %v, want missing entry %q", result.Details, entry)
		}
	}
	if got, err := os.ReadFile(filepath.Join(rigPath, ".gitignore")); err != nil || string(got) != "node_modules/\n" {
		t.Errorf("check mutated .gitignore: got %q, err %v", got, err)
	}
}

func TestRigGitignoreDoctorCheck_TrackedCoveredPathWarnsDespiteRules(t *testing.T) {
	rigPath := initRigGitignoreTestRepo(t, ".beads/*\n!.beads/identity.toml\n")
	trackedPath := filepath.Join(rigPath, ".beads", "formulas", "legacy.toml")
	if err := os.MkdirAll(filepath.Dir(trackedPath), 0o755); err != nil {
		t.Fatalf("mkdir tracked path: %v", err)
	}
	if err := os.WriteFile(trackedPath, []byte("legacy = true\n"), 0o644); err != nil {
		t.Fatalf("write tracked path: %v", err)
	}
	runGit(t, rigPath, "add", "-f", ".beads/formulas/legacy.toml")

	check := NewRigGitignoreDoctorCheck(config.Rig{Name: "legacy", Path: rigPath})
	result := check.Run(&doctor.CheckContext{})

	if result.Status != doctor.StatusWarning {
		t.Fatalf("status = %d (%s), want StatusWarning", result.Status, result.Message)
	}
	if !resultDetailsContain(result.Details, ".beads/formulas/legacy.toml") {
		t.Fatalf("details = %v, want tracked covered path", result.Details)
	}
	if resultDetailsContain(result.Details, "missing canonical entry") {
		t.Fatalf("details = %v, canonical entries should be recognized", result.Details)
	}
}

func TestRigGitignoreDoctorCheck_CanonicalRulesAndIdentityOnlyPass(t *testing.T) {
	rigPath := initRigGitignoreTestRepo(t, ".beads/*\n!.beads/identity.toml\n")
	identityPath := filepath.Join(rigPath, ".beads", "identity.toml")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		t.Fatalf("mkdir identity path: %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("id = \"legacy\"\n"), 0o644); err != nil {
		t.Fatalf("write identity path: %v", err)
	}
	runGit(t, rigPath, "add", ".gitignore", ".beads/identity.toml")

	result := NewRigGitignoreDoctorCheck(config.Rig{Name: "healthy", Path: rigPath}).Run(&doctor.CheckContext{})

	if result.Status != doctor.StatusOK {
		t.Fatalf("status = %d (%s), want StatusOK", result.Status, result.Message)
	}
	if len(result.Details) != 0 || result.FixHint != "" {
		t.Fatalf("result = %#v, want no diagnostic details or fix hint", result)
	}
}

func TestBuildDoctorChecksRegistersRigGitignoreCheck(t *testing.T) {
	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[workspace]\nname = \"demo\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	rigPath := initRigGitignoreTestRepo(t, ".beads/*\n!.beads/identity.toml\n")
	checks := buildDoctorChecks(cityDir, &config.City{
		Workspace: config.Workspace{Name: "demo"},
		Rigs:      []config.Rig{{Name: "legacy", Path: rigPath}},
	}, nil, buildDoctorChecksOpts{
		ControllerRunning:    false,
		SkipCityDoltCheck:    true,
		SkipManagedDoltCheck: true,
	})

	names := doctorCheckNames(checks)
	got := doctorCheckIndex(names, "rig:legacy:gitignore")
	if got < 0 {
		t.Fatalf("rig gitignore check missing from %v", names)
	}
	if got <= doctorCheckIndex(names, "rig:legacy:git") {
		t.Fatalf("rig gitignore check index = %d, want after rig git in %v", got, names)
	}
}

func initRigGitignoreTestRepo(t *testing.T, gitignore string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	return repo
}

func resultDetailsContain(details []string, want string) bool {
	for _, detail := range details {
		if strings.Contains(detail, want) {
			return true
		}
	}
	return false
}
