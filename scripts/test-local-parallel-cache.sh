#!/usr/bin/env bash
#
# Exercise scripts/test-local-parallel's cache boundary with a fake Go
# executable. This stays a shell test so its subprocesses do not change the
# checked Go test-resource census.

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$TEST_DIR/.." && pwd)"
LOCAL_PARALLEL="$TEST_DIR/test-local-parallel"

if ! grep -q 'GC_TEST_LOCAL_PARALLEL_CACHE_SELFTEST' "$LOCAL_PARALLEL"; then
  echo "cache self-test guard is not passed through the local runner" >&2
  exit 1
fi

WORK="$(mktemp -d -p /var/tmp gc-test-local-parallel-cache.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
BIN_DIR="$WORK/bin"
mkdir "$BIN_DIR"

cat >"$BIN_DIR/go" <<'FAKE_GO'
#!/usr/bin/env bash

set -euo pipefail

operation="${1:-}"
fixture_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
record_file="$fixture_root/go-record"
shared_cache="$fixture_root/shared-cache"
clean_marker="$fixture_root/shared-cache-cleaned"

printf '%s\t%s\n' "$operation" "${GOCACHE:-}" >>"$record_file"

case "$operation" in
  env)
    case "${2:-}" in
      GOPATH) printf '%s\n' "$fixture_root/gopath" ;;
      GOCACHE) printf '%s\n' "${GOCACHE:?}" ;;
      GOMODCACHE) printf '%s\n' "$fixture_root/gomodcache" ;;
      GOTMPDIR) printf '%s\n' "$fixture_root/gotmp" ;;
      GOROOT) printf '%s\n' "$fixture_root/goroot" ;;
      *) echo "unexpected go env key: ${2:-<empty>}" >&2; exit 99 ;;
    esac
    ;;
  list)
    printf '%s\n' github.com/gastownhall/gascity/internal/testutil
    ;;
  test)
    cache="${GOCACHE:?go test did not receive GOCACHE}"
    mkdir -p "$cache"
    printf 'artifact\n' >"$cache/artifact"
    if [[ ! -e "$clean_marker" ]]; then
      GOCACHE="$shared_cache" "$0" clean -cache
      : >"$clean_marker"
    fi
    if [[ ! -f "$cache/artifact" ]]; then
      echo "shared-cache clean removed the active test artifact" >&2
      exit 88
    fi
    for arg in "$@"; do
      if [[ "$arg" == "-list" ]]; then
        printf '%s\n' TestPrivateCache
        exit 0
      fi
    done
    ;;
  clean)
    [[ "${2:-}" == "-cache" ]] || {
      echo "unexpected fake go clean args: $*" >&2
      exit 99
    }
    rm -rf "${GOCACHE:?}"
    ;;
  *)
    echo "unexpected fake go operation: $*" >&2
    exit 99
    ;;
esac
FAKE_GO
chmod +x "$BIN_DIR/go"

PATH="$BIN_DIR:$PATH" \
HOME="$WORK/home" \
TMPDIR=/var/tmp \
GOCACHE="$WORK/shared-cache" \
GOMODCACHE="$WORK/gomodcache" \
GOTMPDIR="$WORK/gotmp" \
GC_TEST_NO_SLICE=1 \
GC_TEST_LOCAL_PARALLEL_CACHE_SELFTEST=1 \
GC_PUSH_GATE_NO_CAP=1 \
LOCAL_TEST_JOBS=1 \
CMD_GC_PROCESS_TOTAL=1 \
GO_TEST_TIMEOUT=1s \
GO_TEST_WATCHDOG_GRACE=off \
  "$LOCAL_PARALLEL" fast >"$WORK/runner.log" 2>&1

record_file="$WORK/go-record"
shared_cache="$WORK/shared-cache"
private_cache=""
test_count=0
clean_count=0

while IFS=$'\t' read -r operation cache; do
  case "$operation" in
    test)
      test_count=$((test_count + 1))
      if [[ "$cache" == "$shared_cache" ]]; then
        echo "test invocation used shared cache $shared_cache" >&2
        exit 1
      fi
      if [[ "$cache" != /var/tmp/gc-test-local-parallel.* ]]; then
        echo "test invocation used unexpected cache $cache" >&2
        exit 1
      fi
      if [[ -z "$private_cache" ]]; then
        private_cache="$cache"
      elif [[ "$cache" != "$private_cache" ]]; then
        echo "test invocations used multiple private caches" >&2
        exit 1
      fi
      ;;
    clean)
      clean_count=$((clean_count + 1))
      if [[ "$cache" != "$shared_cache" ]]; then
        echo "clean invocation targeted $cache, want $shared_cache" >&2
        exit 1
      fi
      ;;
  esac
done <"$record_file"

[[ "$test_count" -gt 0 ]] || { echo "fake go recorded no tests" >&2; exit 1; }
[[ "$clean_count" -eq 1 ]] || { echo "fake go recorded $clean_count shared cleans" >&2; exit 1; }
[[ -n "$private_cache" ]] || { echo "no private cache was observed" >&2; exit 1; }
[[ ! -e "$private_cache" ]] || { echo "private cache was not cleaned: $private_cache" >&2; exit 1; }
[[ ! -e "$shared_cache" ]] || { echo "shared cache was not cleaned by fake cleaner" >&2; exit 1; }

echo "local parallel cache isolation: pass"
