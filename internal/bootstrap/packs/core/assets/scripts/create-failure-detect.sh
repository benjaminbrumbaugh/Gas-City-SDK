#!/usr/bin/env bash
# create-failure-detect — alert when session creates are failing fleet-wide.
#
# Every session start projects a session bead. A start that never reaches
# creation_complete is rolled back and closed with
# close_reason="session create failed: aborted before creation_complete".
# That close_reason is the only trace such a failure leaves: the agent never
# runs, no work bead moves, and `gc doctor` stays green because the pools are
# configured correctly — they just cannot spawn.
#
# On 2026-08-05 that regime ran for ~19 hours at a ~100% failure rate (1,550
# of 1,590 session closures) with nobody watching, because no surface reports
# the ratio. This order watches it: over a rolling window it compares failed
# creates against all session closures and escalates when the share crosses a
# threshold.
#
# Judgment stays out of the detector — it reports a ratio and names the
# templates carrying it. Diagnosis is the operator's.
#
# Runs as an exec order (no LLM, no agent, no wisp).
set -euo pipefail

# Trace bd invocations to $GC_BD_TRACE when set (no-op otherwise).
__SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$__SCRIPT_DIR/_bd_trace.sh" "create-failure-detect"

# jq is a hard dependency: a missing jq would zero both counts and report a
# healthy 0% share during a total spawn outage — the exact blindness this
# order exists to remove. Fail loud instead.
if ! command -v jq >/dev/null 2>&1; then
    echo "create-failure-detect: jq is required but not found in PATH" >&2
    exit 1
fi

CITY="${GC_CITY:-.}"
PACK_STATE_DIR="${GC_PACK_STATE_DIR:-${GC_CITY_RUNTIME_DIR:-$CITY/.gc/runtime}/packs/core}"
STATE_FILE="$PACK_STATE_DIR/create-failure-alert.json"

# Rolling window, in minutes, of session closures to score. Well inside the
# reaper's 720h session-bead purge age, so the window is never truncated by
# retention.
WINDOW_MINUTES="${GC_CREATE_FAILURE_WINDOW_MINUTES:-60}"
# Percentage of session closures that must be failed creates before alerting.
# Ordinary days run a low single-digit background rate; the 2026-08-05 outage
# ran at 97.5%.
THRESHOLD="${GC_CREATE_FAILURE_THRESHOLD:-50}"
# Absolute floor on window volume. A percentage over a handful of closures is
# noise: a quiet city that closes 4 sessions, 3 of them failed creates, is not
# an outage. Set to 0 to score every window.
MIN_CLOSURES="${GC_CREATE_FAILURE_MIN_CLOSURES:-20}"
# Seconds to wait before re-alerting on a still-failing fleet. The order runs
# every 5 minutes; without this a 19-hour outage sends ~228 mails, and every
# mail is a bead plus a Dolt commit. Set to 0 to alert on every run.
ALERT_COOLDOWN="${GC_CREATE_FAILURE_ALERT_COOLDOWN:-3600}"

# The canonical close_reason for a rolled-back create, expanded by
# session.CanonicalCloseReason from state code "failed-create". Matched as a
# prefix so a future suffix on the reason does not silently stop detection.
FAILED_CREATE_PREFIX="session create failed"

resolve_escalate_script() {
    local candidate
    local pack
    local system_packs="${GC_SYSTEM_PACKS_DIR:-$CITY/.gc/system/packs}"

    if [ -n "${GC_ESCALATE_SCRIPT:-}" ]; then
        printf '%s\n' "$GC_ESCALATE_SCRIPT"
        return
    fi
    for pack in ${GC_ESCALATE_SEARCH_PACKS:-gastown maintenance bd core}; do
        candidate="$system_packs/$pack/assets/scripts/escalate.sh"
        if [ -x "$candidate" ]; then
            printf '%s\n' "$candidate"
            return
        fi
    done
    printf '%s\n' "$__SCRIPT_DIR/escalate.sh"
}

ESCALATE_SCRIPT="$(resolve_escalate_script)"

# Window start as RFC3339. GNU date first, BSD date second — same portability
# shape cross-rig-deps.sh uses.
CUTOFF=$(date -u -d "-${WINDOW_MINUTES} minutes" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || \
         date -u -v-"${WINDOW_MINUTES}"M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null) || exit 0

# Session beads live in the city's HQ store; `gc bd list` without --rig is
# HQ-scoped from the city cwd. --closed-after bounds the scan to the window so
# the 5-minute cadence never pulls the full retained history.
SESSIONS=$(gc bd list --type=session --status=closed --closed-after "$CUTOFF" --json --limit=0 2>/dev/null) || exit 0
if [ -z "$SESSIONS" ]; then
    exit 0
fi

TOTAL=$(printf '%s' "$SESSIONS" | jq 'length' 2>/dev/null) || exit 0
[ -n "$TOTAL" ] || exit 0
if [ "$TOTAL" -eq 0 ]; then
    exit 0
fi

FAILED=$(printf '%s' "$SESSIONS" \
    | jq --arg prefix "$FAILED_CREATE_PREFIX" \
        '[.[] | select(((.metadata.close_reason // "") | startswith($prefix)))] | length' 2>/dev/null) || exit 0
[ -n "$FAILED" ] || exit 0

SHARE=$(( FAILED * 100 / TOTAL ))

if [ "$FAILED" -gt 0 ]; then
    echo "create-failure-detect: failed=$FAILED total=$TOTAL share=${SHARE}% window=${WINDOW_MINUTES}m threshold=${THRESHOLD}%"
fi

if [ "$MIN_CLOSURES" -gt 0 ] && [ "$TOTAL" -lt "$MIN_CLOSURES" ]; then
    exit 0
fi

if [ "$SHARE" -lt "$THRESHOLD" ]; then
    exit 0
fi

# Suppress repeats while the same outage keeps failing.
NOW_EPOCH=$(date -u '+%s')
if [ "$ALERT_COOLDOWN" -gt 0 ] && [ -f "$STATE_FILE" ]; then
    LAST_ALERT=$(jq -r '.last_alert_at // 0' "$STATE_FILE" 2>/dev/null || echo 0)
    case "$LAST_ALERT" in
        ''|*[!0-9]*) LAST_ALERT=0 ;;
    esac
    if [ "$LAST_ALERT" -gt 0 ] && [ "$(( NOW_EPOCH - LAST_ALERT ))" -lt "$ALERT_COOLDOWN" ]; then
        exit 0
    fi
fi

# Name the templates carrying the failures — one broken template and a
# fleet-wide spawn outage look identical in the ratio alone.
BREAKDOWN=$(printf '%s' "$SESSIONS" \
    | jq -r --arg prefix "$FAILED_CREATE_PREFIX" '
        [.[] | select(((.metadata.close_reason // "") | startswith($prefix)))]
        | group_by(.metadata.template // "unknown")
        | map({template: (.[0].metadata.template // "unknown"), count: length})
        | sort_by(-.count)
        | .[:5]
        | map("  \(.count)  \(.template)")
        | join("\n")' 2>/dev/null) || BREAKDOWN=""

if ! "$ESCALATE_SCRIPT" \
    --subject "ESCALATION: session create-failure rate ${SHARE}%" \
    --severity HIGH \
    --message "$FAILED of $TOTAL session closures in the last ${WINDOW_MINUTES}m were failed creates (${SHARE}%, threshold ${THRESHOLD}%).

Agents at these templates are not starting. The work beads stay ready and
unclaimed, and gc doctor stays green — a failed create leaves no other trace.

Failed creates by template:
$BREAKDOWN

Triage:
- Recent starts and their outcomes: gc dolt logs -n 500 | grep 'session lifecycle: op=start'
  A 60s start_call with outcome=deadline_exceeded is a readiness timeout;
  a create that rolls back in under a second is a pre-start failure.
- Confirm the agent binary resolves in the supervisor environment, not just
  in an interactive shell.
- Failed session beads: gc bd list --type=session --status=closed --closed-after $CUTOFF --json"; then
    # A detector that cannot deliver its alert must not fail quietly — that is
    # the same silence this order exists to remove. Exit non-zero so the
    # controller logs the exec order's output, and leave the cooldown state
    # unwritten so the next run retries the alert.
    echo "create-failure-detect: escalation failed at ${SHARE}% create-failure rate ($FAILED/$TOTAL in ${WINDOW_MINUTES}m); will retry next run" >&2
    exit 1
fi

mkdir -p "$PACK_STATE_DIR"
printf '{"last_alert_at":%s,"share":%s,"failed":%s,"total":%s}\n' \
    "$NOW_EPOCH" "$SHARE" "$FAILED" "$TOTAL" > "$STATE_FILE"
