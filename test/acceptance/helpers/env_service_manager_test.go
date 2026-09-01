package acceptancehelpers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gc-2rglt: Mac acceptance failed on every run because the harness kept gc
// off the host's launchd by shimming launchctl to exit 1 and relying on gc
// falling back to a bare fork. macOS makes launchd the sole lifecycle owner,
// so a failed install is terminal there and the fallback never runs. The
// harness now states the intent instead of inferring it, and these tests pin
// both halves so the accident cannot come back.

func TestAcceptanceEnvOptsOutOfServiceManager(t *testing.T) {
	env := NewEnv("", t.TempDir(), "")
	got := env.Get(supervisorServiceManagerEnv)
	if got != "none" {
		t.Fatalf("%s = %q, want \"none\": without it gc registers a supervisor in the host's launchd", supervisorServiceManagerEnv, got)
	}
}

// The env var name is duplicated from cmd/gc (gc runs here as a built
// binary, so the constant cannot be imported). Pin the spelling against the
// source of truth: a rename on one side alone would silently stop opting
// out, and the suite would go red on macOS again with no clue why.
func TestAcceptanceServiceManagerEnvMatchesCommandSource(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("repo root not resolvable: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "cmd", "gc", "supervisor_service_manager.go"))
	if err != nil {
		t.Skipf("cmd/gc source not readable: %v", err)
	}
	want := "supervisorServiceManagerEnv = \"" + supervisorServiceManagerEnv + "\""
	if !strings.Contains(string(src), want) {
		t.Fatalf("cmd/gc/supervisor_service_manager.go does not declare %s; the acceptance opt-out is dead", want)
	}
}

// The shims are the backstop for any path that does not consult the opt-out,
// so their absence must be a test failure rather than a silent handoff of the
// supervisor to the developer's or runner's real service manager.
func TestAcceptanceEnvInstallsServiceManagerShims(t *testing.T) {
	gcHome := t.TempDir()
	env := NewEnv("", gcHome, "")
	shimDir := filepath.Join(gcHome, "bin")
	for _, name := range []string{"launchctl", "systemctl"} {
		info, err := os.Stat(filepath.Join(shimDir, name))
		if err != nil {
			t.Fatalf("%s shim missing: %v", name, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s shim is not executable (mode %v)", name, info.Mode())
		}
	}
	path := env.Get("PATH")
	if first, _, _ := strings.Cut(path, ":"); first != shimDir {
		t.Fatalf("PATH starts with %q, want the shim dir %q first", first, shimDir)
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
