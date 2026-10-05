package scripts_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalParallelBuildsWithPrivateCacheDuringSharedCacheClean guards the
// process boundary that failed when another session ran go clean -cache
// against the host-wide cache. The fake go runs go clean -cache against that
// shared path during the first test invocation and verifies the artifact in
// the cache supplied to that invocation remains available. This observes
// runner-to-child environment plumbing and cache ownership; it is not a
// compiler conformance test.
func TestLocalParallelBuildsWithPrivateCacheDuringSharedCacheClean(t *testing.T) {
	t.Parallel()

	repoRoot := repoRoot(t)
	fixtureRoot := t.TempDir()
	binDir := filepath.Join(fixtureRoot, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	sharedCache := filepath.Join(fixtureRoot, "shared-cache")
	if err := os.Mkdir(sharedCache, 0o755); err != nil {
		t.Fatalf("create shared cache: %v", err)
	}
	cleanMarker := filepath.Join(fixtureRoot, "shared-cache-cleaned")
	recordFile := filepath.Join(fixtureRoot, "go-record")

	fakeGo := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
operation="${1:-}"
printf '%%s\t%%s\n' "$operation" "${GOCACHE:-}" >> %q

case "$operation" in
  env)
    case "${2:-}" in
      GOPATH) printf '%%s\n' %q ;;
      GOCACHE) printf '%%s\n' "${GOCACHE:?}" ;;
      GOMODCACHE) printf '%%s\n' %q ;;
      GOTMPDIR) printf '%%s\n' %q ;;
      GOROOT) printf '%%s\n' %q ;;
      *) echo "unexpected go env key: ${2:-<empty>}" >&2; exit 99 ;;
    esac
    ;;
  list)
    printf '%%s\n' github.com/gastownhall/gascity/internal/testutil
    ;;
  test)
    cache="${GOCACHE:?go test did not receive GOCACHE}"
    mkdir -p "$cache"
    printf 'artifact\n' > "$cache/artifact"
    if [[ ! -e %q ]]; then
      GOCACHE=%q "$0" clean -cache
      : > %q
    fi
    if [[ ! -f "$cache/artifact" ]]; then
      echo "shared cache clean removed the active test artifact" >&2
      exit 88
    fi
    for arg in "$@"; do
      if [[ "$arg" == "-list" ]]; then
        printf '%%s\n' TestPrivateCache
        exit 0
      fi
    done
    ;;
  clean)
    [[ "${2:-}" == "-cache" ]] || { echo "unexpected fake go clean args: $*" >&2; exit 99; }
    rm -rf "${GOCACHE:?}"
    ;;
  *)
    echo "unexpected fake go operation: $*" >&2
    exit 99
    ;;
esac
	`, recordFile,
		filepath.Join(fixtureRoot, "gopath"),
		filepath.Join(fixtureRoot, "gomodcache"), filepath.Join(fixtureRoot, "gotmp"),
		filepath.Join(fixtureRoot, "goroot"), cleanMarker, sharedCache, cleanMarker)
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}

	cmd := exec.Command(filepath.Join(repoRoot, "scripts", "test-local-parallel"), "fast")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(fixtureRoot, "home"),
		"TMPDIR=/var/tmp",
		"GOCACHE="+sharedCache,
		"GOMODCACHE="+filepath.Join(fixtureRoot, "gomodcache"),
		"GOTMPDIR="+filepath.Join(fixtureRoot, "gotmp"),
		"GC_TEST_NO_SLICE=1",
		"GC_PUSH_GATE_NO_CAP=1",
		"LOCAL_TEST_JOBS=1",
		"CMD_GC_PROCESS_TOTAL=1",
		"GO_TEST_TIMEOUT=1s",
		"GO_TEST_WATCHDOG_GRACE=off",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("local parallel runner failed: %v\n%s", err, output)
	}

	record, err := os.Open(recordFile)
	if err != nil {
		t.Fatalf("open fake go record: %v", err)
	}
	defer func() {
		if err := record.Close(); err != nil {
			t.Errorf("close fake go record: %v", err)
		}
	}()
	var testCaches []string
	var cleanCaches []string
	scanner := bufio.NewScanner(record)
	for scanner.Scan() {
		operation, cache, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			continue
		}
		switch operation {
		case "test":
			testCaches = append(testCaches, cache)
		case "clean":
			cleanCaches = append(cleanCaches, cache)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fake go record: %v", err)
	}
	if len(testCaches) == 0 {
		t.Fatalf("fake go recorded no test invocations; record:\n%s", readFileForTest(t, recordFile))
	}
	if len(cleanCaches) != 1 || cleanCaches[0] != sharedCache {
		t.Fatalf("fake go clean cache = %v, want exactly shared cache %q; record:\n%s", cleanCaches, sharedCache, readFileForTest(t, recordFile))
	}
	privateCache := ""
	for _, cache := range testCaches {
		if cache == sharedCache {
			t.Fatalf("test invocation used shared cache %q; all caches: %v; record:\n%s", sharedCache, testCaches, readFileForTest(t, recordFile))
		}
		if !strings.HasPrefix(cache, "/var/tmp/gc-test-local-parallel.") {
			t.Fatalf("test invocation used unexpected non-private cache %q; all caches: %v", cache, testCaches)
		}
		if privateCache == "" {
			privateCache = cache
		} else if cache != privateCache {
			t.Fatalf("test invocations used multiple private caches: %v", testCaches)
		}
	}
	if _, err := os.Stat(privateCache); !os.IsNotExist(err) {
		t.Fatalf("runner-owned cache %q was not cleaned on exit: stat error %v", privateCache, err)
	}
	if _, err := os.Stat(sharedCache); !os.IsNotExist(err) {
		t.Fatalf("shared cache was not cleaned by the simulated external cleaner: stat error %v", err)
	}
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
