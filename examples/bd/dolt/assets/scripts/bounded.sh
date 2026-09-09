# Bounded execution for Dolt diagnostics.
#
# Sourced by assets/scripts/runtime.sh (every dolt CLI command) and by
# doctor/check-dolt/run.sh, which runs before a server exists and so
# cannot take runtime.sh's port resolution. Both need the same answer to
# one question, and it is the reason this file exists as its own unit:
#
#   Did the probe run and exceed its bound, or did the probe never run?
#
# POSIX sh. No side effects beyond defining TIMEOUT_BIN,
# RUN_BOUNDED_UNAVAILABLE_RC, and the run_bounded* functions.

# Resolve an external bounded-execution helper. Prefer gtimeout
# (coreutils on macOS), fall back to timeout (coreutils on Linux).
# Neither ships with a stock macOS, so an empty TIMEOUT_BIN is the
# common case here, not an error: run_bounded falls back to python3
# and then to a portable shell watchdog, both of which bound the
# command just as hard.
if command -v gtimeout >/dev/null 2>&1; then
  TIMEOUT_BIN="gtimeout"
elif command -v timeout >/dev/null 2>&1; then
  TIMEOUT_BIN="timeout"
else
  TIMEOUT_BIN=""
fi

# Exit code for "the probe could not be executed" — the bound could not
# be applied, so the command never ran. This MUST NOT be 124: 124 is the
# coreutils convention for "the command ran and exceeded its bound", and
# conflating the two is a live incident, not a style point. A stock macOS
# host has no `timeout` binary; encoding that as a timeout made
# `gc dolt health` publish server.reachable=false for a healthy data
# plane, which the deacon patrol's escalation table reads as CRITICAL and
# answers with a Dolt restart — destroying the evidence the runbook exists
# to preserve (sdk-4is). Callers must branch on it separately from 124 and
# must never score it as a health verdict.
RUN_BOUNDED_UNAVAILABLE_RC=125

# run_bounded_secs BOUND — echo BOUND as whole seconds, or fail if it
# cannot be expressed that way. GNU timeout accepts unit suffixes
# ("30s", "5m"); the python3 and shell fallbacks do not, so an operator
# who sets GC_DOLT_RIG_LIST_TIMEOUT_SECS=5m would otherwise get a
# python traceback or an arithmetic error dressed up as a probe result.
# A bound we cannot apply means the probe cannot run — the caller is
# told exactly that rather than being handed a fabricated timeout.
run_bounded_secs() {
  case "$1" in
    ''|*[!0-9s]*) return 1 ;;
  esac
  _rb_secs="${1%s}"
  case "$_rb_secs" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$_rb_secs" -gt 0 ] || return 1
  printf '%s' "$_rb_secs"
}

# run_bounded_shell SECS CMD... — Portable last-resort bound, used when
# neither a timeout binary nor python3 exists (a stock macOS host with
# no Xcode command line tools). Runs CMD in the background under a
# watchdog subshell that polls once a second, then applies the same
# SIGTERM -> 2s grace -> SIGKILL escalation as the other two paths.
#
# The watchdog touches a flag file *before* it signals, and the parent
# reads that flag rather than inferring intent from the child's exit
# status. A killed child exits 143/137, but so can a child that chose
# to die on its own signal — only the flag distinguishes "we stopped it"
# from "it stopped itself", and getting that wrong is how a bounded
# probe starts fabricating timeouts.
#
# Resolution is whole seconds, so the effective bound is [SECS, SECS+1).
run_bounded_shell() {
  _rb_limit="$1"; shift

  _rb_seq=$((${_rb_seq:-0} + 1))
  _rb_flag="${TMPDIR:-/tmp}/.gc-dolt-run-bounded.$$.$_rb_seq"
  rm -f "$_rb_flag" 2>/dev/null

  "$@" &
  _rb_pid=$!

  (
    _rb_left="$_rb_limit"
    while [ "$_rb_left" -gt 0 ]; do
      kill -0 "$_rb_pid" 2>/dev/null || exit 0
      sleep 1
      _rb_left=$((_rb_left - 1))
    done
    kill -0 "$_rb_pid" 2>/dev/null || exit 0
    : > "$_rb_flag"
    kill -TERM "$_rb_pid" 2>/dev/null
    _rb_grace=2
    while [ "$_rb_grace" -gt 0 ]; do
      sleep 1
      kill -0 "$_rb_pid" 2>/dev/null || exit 0
      _rb_grace=$((_rb_grace - 1))
    done
    kill -KILL "$_rb_pid" 2>/dev/null
  ) &
  _rb_watch=$!

  wait "$_rb_pid"
  _rb_rc=$?

  kill -TERM "$_rb_watch" 2>/dev/null
  wait "$_rb_watch" 2>/dev/null

  if [ -f "$_rb_flag" ]; then
    rm -f "$_rb_flag" 2>/dev/null
    return 124
  fi
  rm -f "$_rb_flag" 2>/dev/null
  return "$_rb_rc"
}

# run_bounded SECS CMD...  — Run CMD with a wall-clock bound. Exits 124
# when CMD ran and exceeded that bound (coreutils convention), and
# $RUN_BOUNDED_UNAVAILABLE_RC when CMD could not be bounded and so never
# ran at all. Every path applies the same SIGTERM -> 2s grace -> SIGKILL
# escalation, so an uncooperative child that ignores SIGTERM (e.g. a dolt
# client stuck in kernel socket wait) is still reaped rather than leaked
# as a zombie — the failure mode the bounded helper exists to prevent.
#
# There is always a mechanism: the shell running this function is itself
# one. The unavailable path is reserved for a bound that cannot be
# applied, never for a host that merely lacks coreutils.
run_bounded() {
  _rb_bound="$1"; shift
  if [ -n "$TIMEOUT_BIN" ]; then
    "$TIMEOUT_BIN" --kill-after=2 "$_rb_bound" "$@"
    return $?
  fi

  _t=$(run_bounded_secs "$_rb_bound") || {
    printf 'dolt runtime: diagnostic unavailable — cannot apply the bound %s to `%s`; the command was NOT run\n' \
      "$_rb_bound" "$*" >&2
    return "$RUN_BOUNDED_UNAVAILABLE_RC"
  }

  if command -v python3 >/dev/null 2>&1; then
    python3 - "$_t" "$@" <<'PY'
import subprocess
import sys

limit = float(sys.argv[1])
cmd = sys.argv[2:]

proc = subprocess.Popen(cmd)
try:
    proc.wait(timeout=limit)
except subprocess.TimeoutExpired:
    proc.terminate()
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
    sys.exit(124)
sys.exit(proc.returncode)
PY
    return $?
  fi

  run_bounded_shell "$_t" "$@"
}
