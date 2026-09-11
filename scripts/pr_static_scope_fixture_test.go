package scripts_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type prStaticScopeFixture struct {
	repoRoot           string
	productionMakefile string
	staticSelector     string
	fakeLint           string
	fakeGo             string
	lintLog            string
	goLog              string
	realGo             string
	homeDir            string
}

func newPRStaticScopeFixture(t *testing.T, files map[string]string) prStaticScopeFixture {
	t.Helper()

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("create temporary repository: %v", err)
	}
	writeTestFile(t, filepath.Join(repo, "go.mod"), "module example.com/static-scope\n\ngo 1.23\n")
	for name, content := range files {
		writeTestFile(t, filepath.Join(repo, name), content)
	}
	selectorPath := filepath.Join(repoRoot(t), "scripts", "ci-static-select")
	selector, err := os.ReadFile(selectorPath)
	if err != nil {
		t.Fatalf("read production static selector: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatalf("create fixture scripts directory: %v", err)
	}
	writeExecutable(t, filepath.Join(repo, "scripts", "ci-static-select"), string(selector))

	toolDir := t.TempDir()
	lintLog := filepath.Join(toolDir, "golangci.calls")
	goLog := filepath.Join(toolDir, "go.calls")
	fakeLint := filepath.Join(toolDir, "golangci-lint")
	writeExecutable(t, fakeLint, `#!/bin/sh
set -eu
: "${STATIC_SCOPE_LINT_LOG:?}"
printf 'CALL\000' >> "$STATIC_SCOPE_LINT_LOG"
for arg in "$@"; do
  printf 'ARG\000%s\000' "$arg" >> "$STATIC_SCOPE_LINT_LOG"
done
printf 'END\000' >> "$STATIC_SCOPE_LINT_LOG"
`)
	realGo := "go"
	fakeGo := filepath.Join(toolDir, "go")
	writeExecutable(t, fakeGo, `#!/bin/sh
set -eu
: "${STATIC_SCOPE_GO_LOG:?}"
: "${STATIC_SCOPE_REAL_GO:?}"
if [ "${1-}" = "vet" ]; then
  printf 'CALL\000' >> "$STATIC_SCOPE_GO_LOG"
  for arg in "$@"; do
    printf 'ARG\000%s\000' "$arg" >> "$STATIC_SCOPE_GO_LOG"
  done
  printf 'END\000' >> "$STATIC_SCOPE_GO_LOG"
  exit 0
fi
exec "$STATIC_SCOPE_REAL_GO" "$@"
`)

	fixture := prStaticScopeFixture{
		repoRoot:           repo,
		productionMakefile: filepath.Join(repoRoot(t), "Makefile"),
		staticSelector:     filepath.Join(repo, "scripts", "ci-static-select"),
		fakeLint:           fakeLint,
		fakeGo:             fakeGo,
		lintLog:            lintLog,
		goLog:              goLog,
		realGo:             realGo,
		homeDir:            t.TempDir(),
	}
	setupMakefile := filepath.Join(t.TempDir(), "git-init.mk")
	writeTestFile(t, setupMakefile, `.PHONY: init
init:
	@git init -q -b main
	@git config user.email static-scope@example.invalid
	@git config user.name static-scope-test
	@git add .
	@git commit -qm baseline
`)
	cmd := makeCommand("--no-print-directory", "-C", repo, "-f", setupMakefile, "init")
	cmd.Env = fixture.commandEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initialize temporary Git repository: %v\n%s", err, output)
	}
	return fixture
}

func (f prStaticScopeFixture) commandEnv() []string {
	env := make([]string, 0, len(os.Environ())+7)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" ||
			name == "STATIC_SCOPE_LINT_LOG" ||
			name == "STATIC_SCOPE_GO_LOG" ||
			name == "STATIC_SCOPE_REAL_GO" ||
			name == "SYS_USR_CGO_FALLBACK" ||
			name == "EVENT_NAME" ||
			name == "PR_BASE_SHA" ||
			name == "GOFLAGS" ||
			name == "GOENV" ||
			name == "GOWORK" ||
			name == "LINT_FLAGS" ||
			name == "GIT_CONFIG" ||
			strings.HasPrefix(name, "GIT_CONFIG_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"HOME="+f.homeDir,
		"STATIC_SCOPE_LINT_LOG="+f.lintLog,
		"STATIC_SCOPE_GO_LOG="+f.goLog,
		"STATIC_SCOPE_REAL_GO="+f.realGo,
		"SYS_USR_CGO_FALLBACK=0",
		"GOFLAGS=-mod=readonly",
		"GOENV=off",
		"GOWORK=off",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}

func (f prStaticScopeFixture) resetCalls(t *testing.T) {
	t.Helper()
	for label, path := range map[string]string{"golangci": f.lintLog, "go": f.goLog} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("reset fake %s log: %v", label, err)
		}
	}
}

func (f prStaticScopeFixture) calls(t *testing.T) [][]string {
	t.Helper()
	return readFramedCalls(t, f.lintLog, "golangci")
}

func (f prStaticScopeFixture) goCalls(t *testing.T) [][]string {
	t.Helper()
	return readFramedCalls(t, f.goLog, "go")
}

func readFramedCalls(t *testing.T, path, label string) [][]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read fake %s log: %v", label, err)
	}
	if len(body) == 0 {
		return nil
	}
	fields := bytes.Split(body, []byte{0})
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	calls := make([][]string, 0)
	for index := 0; index < len(fields); {
		if string(fields[index]) != "CALL" {
			t.Fatalf("malformed fake %s log token %q at %d", label, fields[index], index)
		}
		index++
		call := make([]string, 0)
		for {
			if index >= len(fields) {
				t.Fatalf("unterminated fake %s call", label)
			}
			switch string(fields[index]) {
			case "END":
				index++
				calls = append(calls, call)
				goto nextCall
			case "ARG":
				if index+1 >= len(fields) {
					t.Fatalf("missing fake %s argument after token %d", label, index)
				}
				call = append(call, string(fields[index+1]))
				index += 2
			default:
				t.Fatalf("malformed fake %s call token %q at %d", label, fields[index], index)
			}
		}
	nextCall:
		continue
	}
	return calls
}

func (f prStaticScopeFixture) requireNoCalls(t *testing.T) {
	t.Helper()
	if got := f.calls(t); len(got) != 0 {
		t.Errorf("golangci calls = %v, want no-op", got)
	}
	if got := f.goCalls(t); len(got) != 0 {
		t.Errorf("go calls = %v, want no-op", got)
	}
}
