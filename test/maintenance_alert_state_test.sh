#!/bin/sh
# Regression test for producer-local alert edges.
#
# This observes the pack alert-state boundary, not a live supervisor or mail
# store.  It proves the state machine that reaper and backup call: first edge,
# same-fingerprint suppression, changed-condition emission, one recovery/reset,
# per-producer isolation, and bounded delivery.
set -eu

HERE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ALERT_LIB="${ALERT_LIB:-$HERE/../internal/bootstrap/packs/core/assets/scripts/alert-state.sh}"
[ -f "$ALERT_LIB" ] || { echo "FAIL: alert helper not found at $ALERT_LIB" >&2; exit 1; }
# shellcheck disable=SC1090
. "$ALERT_LIB"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/gc-alert-state-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
STATE="$WORK/reaper-alert-state.json"
BACKUP_STATE="$WORK/backup-alert-state.json"
DELIVERIES="$WORK/deliveries.log"
FAILED_DELIVERIES="$WORK/failed-deliveries.log"
SENDER="$WORK/sender.sh"
FAIL_SENDER="$WORK/fail-sender.sh"
SLOW_SENDER="$WORK/slow-sender.sh"

cat > "$SENDER" <<'EOF'
#!/bin/sh
printf '%s\n' "$2" >> "$ALERT_TEST_DELIVERIES"
exit 0
EOF
chmod 755 "$SENDER"
cat > "$FAIL_SENDER" <<'EOF'
#!/bin/sh
printf '%s\n' "$2" >> "$ALERT_TEST_FAILED_DELIVERIES"
exit 1
EOF
chmod 755 "$FAIL_SENDER"
cat > "$SLOW_SENDER" <<'EOF'
#!/bin/sh
sleep 3
exit 0
EOF
chmod 755 "$SLOW_SENDER"
export ALERT_TEST_DELIVERIES="$DELIVERIES"
export ALERT_TEST_FAILED_DELIVERIES="$FAILED_DELIVERIES"

fail=0
pass() { echo "PASS: $1"; }
bad() { echo "FAIL: $1" >&2; fail=1; }
delivery_count() { [ -f "$DELIVERIES" ] && wc -l < "$DELIVERIES" | tr -d ' ' || printf '0'; }
failed_delivery_count() { [ -f "$FAILED_DELIVERIES" ] && wc -l < "$FAILED_DELIVERIES" | tr -d ' ' || printf '0'; }

BODY_ONE=$(printf 'gm: backup stale age=10s threshold=86400s\nbeads: 2 failed databases')
BODY_ONE_AGAIN=$(printf 'beads: 2 failed databases\ngm: backup stale age=999s threshold=86400s')
BODY_CHANGED=$(printf 'gm: backup stale age=999s threshold=86400s\nbeads: 3 failed databases')
NORM_ONE=$(alert_state_normalize_body "$BODY_ONE")
NORM_ONE_AGAIN=$(alert_state_normalize_body "$BODY_ONE_AGAIN")
NORM_CHANGED=$(alert_state_normalize_body "$BODY_CHANGED")
FP_ONE=$(alert_state_fingerprint "reaper-anomalies|$NORM_ONE")
FP_ONE_AGAIN=$(alert_state_fingerprint "reaper-anomalies|$NORM_ONE_AGAIN")
FP_CHANGED=$(alert_state_fingerprint "reaper-anomalies|$NORM_CHANGED")

if [ "$FP_ONE" = "$FP_ONE_AGAIN" ]; then pass "age-only variation normalizes to one fingerprint"; else bad "age-only variation changed fingerprint"; fi
if [ "$FP_ONE" != "$FP_CHANGED" ]; then pass "changed condition produces a new fingerprint"; else bad "changed condition was normalized away"; fi

FAILED_STATE="$WORK/failed-alert-state.json"
alert_state_failure "$FAILED_STATE" "$FP_ONE" 2 "failed-first" "$BODY_ONE" "$FAIL_SENDER"
if [ "$ALERT_STATE_RESULT" = failed ] && [ ! -e "$FAILED_STATE" ]; then pass "failed delivery leaves no suppression state"; else bad "failed delivery persisted suppression state"; fi
alert_state_failure "$FAILED_STATE" "$FP_ONE" 2 "failed-retry" "$BODY_ONE" "$FAIL_SENDER"
if [ "$ALERT_STATE_RESULT" = failed ] && [ "$(failed_delivery_count)" = 2 ]; then pass "failed delivery retries identical condition"; else bad "failed delivery did not retry"; fi

alert_state_failure "$STATE" "$FP_ONE" 2 "reaper-first" "$BODY_ONE" "$SENDER"
if [ "$ALERT_STATE_RESULT" = sent ] && [ "$(delivery_count)" = 1 ]; then pass "first failure emits once"; else bad "first failure did not emit exactly once"; fi
if grep -q '"status":"alerting"' "$STATE" && grep -q '"fingerprint":"sha256:' "$STATE"; then pass "alerting state is persisted atomically as a fingerprint"; else bad "alerting state file is incomplete"; fi

alert_state_failure "$STATE" "$FP_ONE_AGAIN" 2 "reaper-repeat" "$BODY_ONE_AGAIN" "$SENDER"
if [ "$ALERT_STATE_RESULT" = suppressed ] && [ "$(delivery_count)" = 1 ]; then pass "same fingerprint is suppressed"; else bad "same fingerprint re-emitted"; fi

alert_state_failure "$STATE" "$FP_CHANGED" 2 "reaper-changed" "$BODY_CHANGED" "$SENDER"
if [ "$ALERT_STATE_TRANSITION" = changed ] && [ "$(delivery_count)" = 2 ]; then pass "fingerprint change emits a failure transition"; else bad "fingerprint change was suppressed"; fi

alert_state_recovery "$STATE" 2 "reaper-recovery" "reaper clear" "$SENDER"
if [ "$ALERT_STATE_RESULT" = sent ] && [ ! -e "$STATE" ] && [ "$(delivery_count)" = 3 ]; then pass "recovery emits once and resets state"; else bad "recovery did not reset/emit once"; fi
alert_state_recovery "$STATE" 2 "reaper-recovery-again" "reaper clear" "$SENDER"
if [ "$ALERT_STATE_RESULT" = none ] && [ "$(delivery_count)" = 3 ]; then pass "reset prevents repeated recovery"; else bad "recovery repeated after reset"; fi

alert_state_failure "$BACKUP_STATE" "$(alert_state_fingerprint 'backup-failure|failed-count=2;failed-dbs=hq(sync failed), mc(sync failed)')" 2 "backup-first" "backup failure" "$SENDER"
if [ "$(delivery_count)" = 4 ] && [ -e "$BACKUP_STATE" ] && [ ! -e "$STATE" ]; then pass "producer state files are isolated"; else bad "producer state files crossed"; fi

START=$(date +%s)
alert_state_failure "$WORK/slow-alert-state.json" "$(alert_state_fingerprint 'slow-condition')" 1 "slow" "bounded" "$SLOW_SENDER"
ELAPSED=$(( $(date +%s) - START ))
if [ "$ELAPSED" -lt 3 ]; then pass "delivery is bounded"; else bad "delivery exceeded its bound (${ELAPSED}s)"; fi

echo "----"
if [ "$fail" -eq 0 ]; then echo "ALL PASS"; else echo "FAILURES PRESENT"; fi
exit "$fail"
