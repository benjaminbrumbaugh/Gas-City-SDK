#!/usr/bin/env bash
# alert-state — producer-local, edge-triggered alert state.
#
# This file is sourced by deterministic maintenance producers.  The caller
# owns the state-file path and the condition fingerprint; this helper only
# provides atomic persistence, bounded delivery, and first/change/recovery
# transitions.  State is intentionally one file per producer so unrelated
# conditions cannot suppress one another.

# alert_state_normalize_body BODY
#
# Normalize a final, possibly multi-line condition body for fingerprinting.
# Line order is not significant.  Reaper backup-age measurements are volatile,
# so only the age value is normalized; counts, database names, thresholds, and
# error text remain part of the condition and therefore trigger a change.
alert_state_normalize_body() {
    printf '%s' "${1:-}" \
        | sed -E \
            -e 's/age=[0-9]+s/age=<stale>/g' \
            -e 's/age=(unparseable|absent)/age=<unknown>/g' \
        | awk '
            NF {
                gsub(/[[:space:]]+/, " ")
                sub(/^ /, "")
                sub(/ $/, "")
                print
            }
        ' \
        | LC_ALL=C sort \
        | awk 'BEGIN { sep = "" } { printf "%s%s", sep, $0; sep = " " } END { print "" }'
}

# alert_state_fingerprint NORMALIZED_BODY
#
# Return a stable, opaque fingerprint.  The normalized input is deliberately
# assembled by the producer and not persisted verbatim, keeping the state file
# small and safe to parse without jq.  cksum is only a last-resort local
# fallback when a SHA-256 implementation is unavailable.
alert_state_fingerprint() {
    local body="${1:-}"
    local digest
    local prefix

    if command -v shasum >/dev/null 2>&1; then
        digest=$(printf '%s' "$body" | shasum -a 256 | awk '{print $1}')
        prefix="sha256"
    elif command -v sha256sum >/dev/null 2>&1; then
        digest=$(printf '%s' "$body" | sha256sum | awk '{print $1}')
        prefix="sha256"
    else
        digest=$(printf '%s' "$body" | cksum | awk '{print $1}')
        prefix="cksum"
    fi
    printf '%s:%s\n' "$prefix" "$digest"
}

alert_state_read() {
    local state_file="${1:-}"

    ALERT_STATE_STATUS=""
    ALERT_STATE_FINGERPRINT=""
    [ -n "$state_file" ] && [ -f "$state_file" ] || return 0
    ALERT_STATE_STATUS=$(sed -n 's/.*"status":"\([^"]*\)".*/\1/p' "$state_file" 2>/dev/null | head -1 || true)
    ALERT_STATE_FINGERPRINT=$(sed -n 's/.*"fingerprint":"\([^"]*\)".*/\1/p' "$state_file" 2>/dev/null | head -1 || true)
}

alert_state_write() {
    local state_file="${1:-}"
    local fingerprint="${2:-}"
    local state_dir
    local tmp

    [ -n "$state_file" ] || return 1
    state_dir=$(dirname "$state_file")
    mkdir -p "$state_dir" 2>/dev/null || return 1
    tmp=$(mktemp "$state_file.tmp.XXXXXX" 2>/dev/null) || return 1
    if ! ( umask 077; printf '{"version":1,"status":"alerting","fingerprint":"%s"}\n' "$fingerprint" > "$tmp" ); then
        rm -f "$tmp" 2>/dev/null || true
        return 1
    fi
    if ! mv -f "$tmp" "$state_file" 2>/dev/null; then
        rm -f "$tmp" 2>/dev/null || true
        return 1
    fi
    return 0
}

alert_state_reset() {
    local state_file="${1:-}"

    [ -n "$state_file" ] || return 0
    rm -f "$state_file" 2>/dev/null || true
}

# alert_state_deliver_bounded TIMEOUT_SECONDS SENDER SUBJECT MESSAGE
#
# Execute the producer's already-resolved sender without allowing a blocked
# mail/API transport to hold the maintenance order indefinitely.  The sender
# is a script path or shell function name (for example dolt_escalate).
alert_state_deliver_bounded() {
    local timeout_seconds="${1:-5}"
    local sender="${2:-}"
    local subject="${3:-}"
    local message="${4:-}"
    local pid
    local started
    local now

    case "$timeout_seconds" in
        ''|*[!0-9]*) timeout_seconds=5 ;;
    esac
    [ "$timeout_seconds" -gt 0 ] || timeout_seconds=1
    [ -n "$sender" ] || return 1

    "$sender" --subject "$subject" --message "$message" >/dev/null 2>&1 &
    pid=$!
    started=$(date +%s)
    while kill -0 "$pid" 2>/dev/null; do
        now=$(date +%s)
        if [ $((now - started)) -ge "$timeout_seconds" ]; then
            kill -TERM "$pid" 2>/dev/null || true
            kill -KILL "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            return 124
        fi
        sleep 0.05
    done
    wait "$pid"
}

# alert_state_failure STATE_FILE FINGERPRINT TIMEOUT SUBJECT MESSAGE SENDER
#
# Set ALERT_STATE_RESULT to sent, failed, or suppressed and
# ALERT_STATE_DELIVERY to delivered/failed/not-sent.  The fingerprint is
# persisted only after confirmed delivery, so a failed or timed-out sender is
# retried on the next run instead of becoming a silently suppressed alert.  A
# failed state write after delivery remains fail-open: the alert was delivered,
# but the next run may deliver a duplicate because durable deduplication was not
# confirmed.
alert_state_failure() {
    local state_file="${1:-}"
    local fingerprint="${2:-}"
    local timeout_seconds="${3:-5}"
    local subject="${4:-}"
    local message="${5:-}"
    local sender="${6:-}"
    local transition="first"
    local delivery_message

    ALERT_STATE_RESULT="failed"
    ALERT_STATE_DELIVERY="failed"
    alert_state_read "$state_file"
    if [ "$ALERT_STATE_STATUS" = "alerting" ] && [ "$ALERT_STATE_FINGERPRINT" = "$fingerprint" ]; then
        ALERT_STATE_RESULT="suppressed"
        ALERT_STATE_DELIVERY="not-sent"
        return 0
    fi
    if [ "$ALERT_STATE_STATUS" = "alerting" ]; then
        transition="changed"
    fi
    ALERT_STATE_TRANSITION="$transition"
    delivery_message="Transition: $transition

$message"
    # The producer includes the transition label in its message; keeping the
    # persisted value to the condition fingerprint makes the file stable.
    if alert_state_deliver_bounded "$timeout_seconds" "$sender" "$subject" "$delivery_message"; then
        ALERT_STATE_DELIVERY="delivered"
        ALERT_STATE_RESULT="sent"
        alert_state_write "$state_file" "$fingerprint" || true
    fi
}

# alert_state_recovery STATE_FILE TIMEOUT SUBJECT MESSAGE SENDER
#
# Emit at most one recovery edge.  Reset only after confirmed delivery so a
# failed or timed-out recovery is retried rather than lost.
alert_state_recovery() {
    local state_file="${1:-}"
    local timeout_seconds="${2:-5}"
    local subject="${3:-}"
    local message="${4:-}"
    local sender="${5:-}"

    ALERT_STATE_RESULT="none"
    ALERT_STATE_DELIVERY="not-sent"
    alert_state_read "$state_file"
    [ "$ALERT_STATE_STATUS" = "alerting" ] || return 0
    if alert_state_deliver_bounded "$timeout_seconds" "$sender" "$subject" "$message"; then
        ALERT_STATE_DELIVERY="delivered"
        ALERT_STATE_RESULT="sent"
        alert_state_reset "$state_file"
    else
        ALERT_STATE_DELIVERY="failed"
        ALERT_STATE_RESULT="failed"
    fi
}
