package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileStaticTargetsWorkFromSpacedCheckout(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "checkout with space")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("create spaced checkout: %v", err)
	}
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/spaced-checkout\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(repo, "pkg", "fixture.go"), "package pkg\n\nfunc Value() int { return 1 }\n")

	root := repoRoot(t)
	for _, name := range []string{"Makefile", "scripts/ci-static-select", "scripts/version-from-git.sh"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") || name == "scripts/ci-static-select" {
			mode = 0o755
		}
		writeTestFile(t, filepath.Join(repo, name), string(data))
		if err := os.Chmod(filepath.Join(repo, name), mode); err != nil {
			t.Fatalf("make %s executable: %v", name, err)
		}
	}

	env := filteredStaticPathTestEnv(os.Environ())
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "static-path@example.invalid"},
		{"config", "user.name", "static-path-test"},
		{"add", "."},
		{"commit", "-qm", "baseline"},
	} {
		runStaticPathTestCommand(t, repo, env, "git", args...)
	}
	writeTestFile(t, filepath.Join(repo, "pkg", "fixture.go"), "package pkg\n\nfunc Value() int { return 2 }\n")

	binDir := t.TempDir()
	lintLog := filepath.Join(t.TempDir(), "golangci.calls")
	fakeLint := filepath.Join(binDir, "golangci-lint")
	writeExecutable(t, fakeLint, `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$STATIC_PATH_LINT_LOG"
`)

	for _, target := range []string{"fmt-check-changed", "lint-affected"} {
		t.Run(target, func(t *testing.T) {
			if err := os.WriteFile(lintLog, nil, 0o644); err != nil {
				t.Fatalf("reset lint log: %v", err)
			}
			cmd := makeCommand(
				"--no-print-directory",
				"-f", filepath.Join(repo, "Makefile"),
				"GOLANGCI_LINT="+fakeLint,
				target,
			)
			cmd.Dir = repo
			cmd.Env = staticPathTestEnv(env, map[string]string{
				"GOLANGCI_LINT":        fakeLint,
				"STATIC_PATH_LINT_LOG": lintLog,
				"CI_STATIC_GO":         "go",
				"SYS_USR_CGO_FALLBACK": "0",
				"LINT_CHANGED_SCOPE":   "worktree",
				"LINT_CHANGED_REF":     "HEAD",
				"GOFLAGS":              "-mod=readonly",
				"GOENV":                "off",
				"GOWORK":               "off",
			})
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make %s failed from spaced checkout: %v\n%s", target, err, output)
			}
			if strings.Contains(string(output), "No such file or directory") {
				t.Fatalf("make %s reported a missing helper from spaced checkout:\n%s", target, output)
			}
			calls, err := os.ReadFile(lintLog)
			if err != nil {
				t.Fatalf("read lint log: %v", err)
			}
			if strings.TrimSpace(string(calls)) == "" {
				t.Fatalf("make %s did not reach the static helper; output:\n%s", target, output)
			}
		})
	}
}

func runStaticPathTestCommand(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := testCommand(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}

func filteredStaticPathTestEnv(env []string) []string {
	return staticPathTestEnv(env, nil)
}

func staticPathTestEnv(env []string, values map[string]string) []string {
	filtered := make([]string, 0, len(env)+len(values))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, replace := values[name]; replace || name == "GIT_CONFIG" || strings.HasPrefix(name, "GIT_CONFIG_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	for name, value := range values {
		filtered = append(filtered, name+"="+value)
	}
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return filtered
}
