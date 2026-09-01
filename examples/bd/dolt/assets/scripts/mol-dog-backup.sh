#!/usr/bin/env bash
# mol-dog-backup — sync Dolt databases to backup remotes and offsite storage.
#
# Converted from the former mol-dog-backup formula. All operations are deterministic:
# dolt backup sync per DB, rsync backup artifacts to offsite path. No LLM judgment needed.
#
# Runs as an exec order (no LLM, no agent, no wisp).
set -euo pipefail

PACK_DIR="${GC_PACK_DIR:-$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
. "$PACK_DIR/assets/scripts/runtime.sh"
. "$PACK_DIR/assets/scripts/_notify.sh"

resolve_alert_state_script() {
    local candidate
    local system_packs="${GC_SYSTEM_PACKS_DIR:-$GC_CITY_PATH/.gc/system/packs}"

    if [ -n "${GC_ALERT_STATE_SCRIPT:-}" ] && [ -f "$GC_ALERT_STATE_SCRIPT" ]; then
        printf '%s\n' "$GC_ALERT_STATE_SCRIPT"
        return 0
    fi
    for candidate in \
        "$system_packs/core/assets/scripts/alert-state.sh" \
        "$PACK_DIR/../core/assets/scripts/alert-state.sh" \
        "$PACK_DIR/../../../internal/bootstrap/packs/core/assets/scripts/alert-state.sh"; do
        if [ -f "$candidate" ]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

BACKUP_ALERT_STATE_SCRIPT="$(resolve_alert_state_script || true)"
if [ -n "$BACKUP_ALERT_STATE_SCRIPT" ]; then
    # shellcheck disable=SC1090
    . "$BACKUP_ALERT_STATE_SCRIPT"
else
    echo "backup: alert-state helper unavailable; refusing unbounded alert delivery" >&2
    exit 1
fi

PORT="$GC_DOLT_PORT"
HOST="${GC_DOLT_HOST:-127.0.0.1}"
USER="${GC_DOLT_USER:-root}"
OFFSITE_PATH="${GC_BACKUP_OFFSITE_PATH:-}"
BACKUP_ARTIFACT_DIR="${GC_BACKUP_ARTIFACT_DIR:-$GC_CITY_PATH/.dolt-backup}"
SYSTEM_DBS="^(information_schema|mysql|dolt_cluster|__gc_probe|performance_schema|sys)$"
# Dolt 2.3.1 adds manifest-aware pruning for file:// backup syncs. Requiring
# that floor keeps backup coverage from silently remaining unbounded.
MIN_DOLT_BACKUP_VERSION="2.3.1"
BACKUP_PRUNE_GRACE_PERIOD="${GC_DOLT_BACKUP_PRUNE_GRACE_PERIOD:-1h}"
BACKUP_LOCK_FILE="${GC_DOLT_BACKUP_LOCK_FILE:-$GC_CITY_PATH/.gc/runtime/packs/dolt/backup-sync.lock}"
BACKUP_LOCK_WAIT_SECONDS="${GC_DOLT_BACKUP_LOCK_WAIT_SECONDS:-5}"
BACKUP_ALERT_STATE_FILE="${GC_BACKUP_ALERT_STATE_FILE:-$PACK_STATE_DIR/backup-alert-state.json}"
BACKUP_ALERT_TIMEOUT_SECONDS="${GC_BACKUP_ALERT_TIMEOUT_SECONDS:-5}"
BACKUP_PRUNE_ALERT_STATE_FILE="${GC_BACKUP_PRUNE_ALERT_STATE_FILE:-$PACK_STATE_DIR/backup-prune-alert-state.json}"
BACKUP_SIZE_WARN_BYTES="${GC_BACKUP_SIZE_WARN_BYTES:-1073741824}"
BACKUP_SIZE_HIGH_BYTES="${GC_BACKUP_SIZE_HIGH_BYTES:-2147483648}"
BACKUP_SIZE_RECOVERY_BYTES="${GC_BACKUP_SIZE_RECOVERY_BYTES:-943718400}"
BACKUP_SIZE_ALERT_RECIPIENT="${GC_BACKUP_SIZE_ALERT_RECIPIENT:-mayor}"
BACKUP_SIZE_ALERT_STATE_FILE="${GC_BACKUP_SIZE_ALERT_STATE_FILE:-$PACK_STATE_DIR/backup-size-alert-state.json}"
BACKUP_SYNC_STATE_FILE="${GC_BACKUP_SYNC_STATE_FILE:-$GC_CITY_PATH/.beads/dolt-backup-state.json}"

dolt_sql() {
    DOLT_CLI_PASSWORD="${GC_DOLT_PASSWORD:-}" \
        run_bounded 30 \
        dolt --host "$HOST" --port "$PORT" --user "$USER" --no-tls sql "$@"
}

dolt_version_at_least() {
    current="${1#v}"
    minimum="$2"
    current="${current%%+*}"
    minimum="${minimum%%+*}"
    case "$current" in
        *-*) return 1 ;;
    esac
    IFS=. read -r cur_major cur_minor cur_patch <<EOF
$current
EOF
    IFS=. read -r min_major min_minor min_patch <<EOF
$minimum
EOF
    for part in "$cur_major" "$cur_minor" "$cur_patch" "$min_major" "$min_minor" "$min_patch"; do
        case "$part" in
            ''|*[!0-9]*) return 1 ;;
        esac
    done
    cur_major=$((10#$cur_major))
    cur_minor=$((10#$cur_minor))
    cur_patch=$((10#$cur_patch))
    min_major=$((10#$min_major))
    min_minor=$((10#$min_minor))
    min_patch=$((10#$min_patch))
    if [ "$cur_major" -ne "$min_major" ]; then
        [ "$cur_major" -gt "$min_major" ]
        return $?
    fi
    if [ "$cur_minor" -ne "$min_minor" ]; then
        [ "$cur_minor" -gt "$min_minor" ]
        return $?
    fi
    [ "$cur_patch" -ge "$min_patch" ]
}

append_failed_db() {
    db_failure="$1"
    FAILED=$((FAILED + 1))
    if [ -n "$FAILED_DBS" ]; then
        FAILED_DBS="$FAILED_DBS, $db_failure"
    else
        FAILED_DBS="$db_failure"
    fi
}

backup_failure_fingerprint() {
    local normalized_dbs
    local normalized

    normalized_dbs=$(printf '%s\n' "$FAILED_DBS" \
        | tr ',' '\n' \
        | sed 's/^ //' \
        | LC_ALL=C sort \
        | awk 'BEGIN { sep = "" } { printf "%s%s", sep, $0; sep = ", " } END { print "" }')
    normalized=$(printf 'failed-count=%s;failed-dbs=%s' "$FAILED_COUNT" "$normalized_dbs" \
        | sed -E 's/[[:space:]]+/ /g')
    alert_state_fingerprint "backup-failure|$normalized"
}

backup_alert_send() {
    local subject=""
    local message=""

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --subject)
                subject="$2"
                shift 2
                ;;
            --message)
                message="$2"
                shift 2
                ;;
            *)
                return 2
                ;;
        esac
    done
    dolt_escalate "$subject" "$message"
}

backup_alert_failure() {
    local fingerprint="$1"
    local subject="$2"
    local message="$3"

    alert_state_failure \
        "$BACKUP_ALERT_STATE_FILE" \
        "$fingerprint" \
        "$BACKUP_ALERT_TIMEOUT_SECONDS" \
        "$subject" \
        "$message" \
        "backup_alert_send"
    if [ "$ALERT_STATE_DELIVERY" != "delivered" ] && [ "$ALERT_STATE_RESULT" != "suppressed" ]; then
        echo "backup: alert delivery failed or timed out" >&2
    fi
}

backup_alert_recovery() {
    alert_state_recovery \
        "$BACKUP_ALERT_STATE_FILE" \
        "$BACKUP_ALERT_TIMEOUT_SECONDS" \
        "RECOVERY: Dolt backup sync recovered [MEDIUM]" \
        "Dolt backup sync completed without failed databases." \
        "backup_alert_send"
    if [ "$ALERT_STATE_DELIVERY" != "delivered" ] && [ "$ALERT_STATE_RESULT" != "none" ]; then
        echo "backup: recovery alert delivery failed or timed out" >&2
    fi
}

backup_size_alert_send() {
    local subject=""
    local message=""

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --subject)
                subject="$2"
                shift 2
                ;;
            --message)
                message="$2"
                shift 2
                ;;
            *)
                return 2
                ;;
        esac
    done
    gc mail send "$BACKUP_SIZE_ALERT_RECIPIENT" -s "$subject" -m "$message"
}

backup_prune_alert_failure() {
    local fingerprint

    fingerprint=$(alert_state_fingerprint "backup-prune-failure|$PRUNE_FAILED_DBS")
    alert_state_failure \
        "$BACKUP_PRUNE_ALERT_STATE_FILE" \
        "$fingerprint" \
        "$BACKUP_ALERT_TIMEOUT_SECONDS" \
        "Dolt backup pruning failed [HIGH]" \
        "Backup sync completed for $SYNCED/$TOTAL databases, but manifest-aware pruning was not confirmed for:$PRUNE_FAILED_DBS

Inspect the named file:// backup destinations and Dolt errors. Do not delete manifest-referenced files manually. If safe remediation is unclear, request queued outside help through external coordination." \
        "backup_size_alert_send"
}

backup_prune_alert_recovery() {
    alert_state_recovery \
        "$BACKUP_PRUNE_ALERT_STATE_FILE" \
        "$BACKUP_ALERT_TIMEOUT_SECONDS" \
        "RECOVERY: Dolt backup pruning recovered [MEDIUM]" \
        "Manifest-aware pruning completed or safely skipped for every successful backup sync." \
        "backup_size_alert_send"
}

normalize_backup_size_thresholds() {
    case "$BACKUP_SIZE_WARN_BYTES" in
        ''|*[!0-9]*) BACKUP_SIZE_WARN_BYTES=1073741824 ;;
    esac
    case "$BACKUP_SIZE_HIGH_BYTES" in
        ''|*[!0-9]*) BACKUP_SIZE_HIGH_BYTES=2147483648 ;;
    esac
    case "$BACKUP_SIZE_RECOVERY_BYTES" in
        ''|*[!0-9]*) BACKUP_SIZE_RECOVERY_BYTES=943718400 ;;
    esac
    if [ "$BACKUP_SIZE_RECOVERY_BYTES" -ge "$BACKUP_SIZE_WARN_BYTES" ] \
        || [ "$BACKUP_SIZE_WARN_BYTES" -ge "$BACKUP_SIZE_HIGH_BYTES" ]; then
        echo "backup: invalid size thresholds; using 900MiB recovery / 1GiB warning / 2GiB high defaults" >&2
        BACKUP_SIZE_RECOVERY_BYTES=943718400
        BACKUP_SIZE_WARN_BYTES=1073741824
        BACKUP_SIZE_HIGH_BYTES=2147483648
    fi
}

backup_artifact_size_bytes() {
    local size_kib

    [ -d "$BACKUP_ARTIFACT_DIR" ] || return 1
    size_kib=$(du -sk "$BACKUP_ARTIFACT_DIR" 2>/dev/null | awk 'NR == 1 {print $1}' || true)
    case "$size_kib" in
        ''|*[!0-9]*) return 1 ;;
    esac
    printf '%s\n' "$((size_kib * 1024))"
}

check_backup_size() {
    local size_bytes
    local severity
    local subject
    local fingerprint
    local message

    normalize_backup_size_thresholds
    if ! size_bytes=$(backup_artifact_size_bytes); then
        echo "backup: artifact size unavailable for $BACKUP_ARTIFACT_DIR" >&2
        return 0
    fi

    severity=""
    subject=""
    if [ "$size_bytes" -ge "$BACKUP_SIZE_HIGH_BYTES" ]; then
        severity="high"
        subject="Dolt backup size high [HIGH]"
    elif [ "$size_bytes" -ge "$BACKUP_SIZE_WARN_BYTES" ]; then
        severity="warning"
        subject="Dolt backup size warning [MEDIUM]"
    elif [ "$size_bytes" -le "$BACKUP_SIZE_RECOVERY_BYTES" ]; then
        alert_state_recovery \
            "$BACKUP_SIZE_ALERT_STATE_FILE" \
            "$BACKUP_ALERT_TIMEOUT_SECONDS" \
            "RECOVERY: Dolt backup size below warning threshold [MEDIUM]" \
            "Backup artifacts recovered to ${size_bytes} bytes (recovery threshold: ${BACKUP_SIZE_RECOVERY_BYTES} bytes)." \
            "backup_size_alert_send"
        return 0
    else
        # Hysteresis band: retain the prior alert state without sending mail.
        return 0
    fi

    fingerprint=$(alert_state_fingerprint "backup-size|$severity")
    message="Backup artifacts: ${size_bytes} bytes
Warning threshold: ${BACKUP_SIZE_WARN_BYTES} bytes
High threshold: ${BACKUP_SIZE_HIGH_BYTES} bytes
Path: $BACKUP_ARTIFACT_DIR
Latest sync: $SYNCED/$TOTAL succeeded; failed=$FAILED_COUNT
Prune result: completed=$PRUNE_COMPLETED, safely-skipped=$PRUNE_SKIPPED, failed=$PRUNE_FAILED, grace=$BACKUP_PRUNE_GRACE_PERIOD

Inspect backup pruning and Dolt compaction. Do not delete manifest-referenced files manually. If safe remediation is unclear, request queued outside help through external coordination."
    alert_state_failure \
        "$BACKUP_SIZE_ALERT_STATE_FILE" \
        "$fingerprint" \
        "$BACKUP_ALERT_TIMEOUT_SECONDS" \
        "$subject" \
        "$message" \
        "backup_size_alert_send"
}

backup_url_from_list() {
    local target="$1"
    local line
    local value

    while IFS= read -r line; do
        case "$line" in
            "$target"[[:space:]]*)
                value="${line#"$target"}"
                value="${value#"${value%%[![:space:]]*}"}"
                value="${value%\{\}}"
                value="${value% }"
                printf '%s\n' "$value"
                return 0
                ;;
        esac
    done
    return 1
}

backup_destination_is_safe() {
    local candidate="$1"
    local live_db="$2"

    python3 - "$candidate" "$live_db" "$BACKUP_ARTIFACT_DIR" "$DOLT_DATA_DIR" "$GC_CITY_PATH" <<'PY'
import os
import stat
import sys

candidate, live_db, artifact_root, data_root, city_root = map(os.path.abspath, sys.argv[1:])

def inside(path, root):
    try:
        return os.path.commonpath((path, root)) == root
    except ValueError:
        return False

if not inside(artifact_root, city_root) or not inside(candidate, artifact_root):
    raise SystemExit(1)

# The city root is the authority anchor. Reject every existing symlink or
# non-directory component below it so a configured path cannot escape through
# an alias after a lexical containment check.
relative = os.path.relpath(candidate, city_root)
current = city_root
for component in relative.split(os.sep):
    if component in ("", "."):
        continue
    current = os.path.join(current, component)
    if not os.path.lexists(current):
        continue
    mode = os.lstat(current).st_mode
    if stat.S_ISLNK(mode) or not stat.S_ISDIR(mode):
        raise SystemExit(1)

candidate_real = os.path.realpath(candidate)
artifact_real = os.path.realpath(artifact_root)
data_real = os.path.realpath(data_root)
city_real = os.path.realpath(city_root)

if not inside(artifact_real, city_real) or not inside(candidate_real, artifact_real):
    raise SystemExit(1)

# Neither the artifact root nor a database destination may contain, equal, or
# sit inside the live Dolt tree.
for backup_path in (artifact_real, candidate_real):
    if inside(backup_path, data_real) or inside(data_real, backup_path):
        raise SystemExit(1)

if os.path.exists(candidate) and os.path.exists(live_db) and os.path.samefile(candidate, live_db):
    raise SystemExit(1)
PY
}

acquire_backup_lock() {
    case "$BACKUP_LOCK_WAIT_SECONDS" in
        ''|*[!0-9]*) BACKUP_LOCK_WAIT_SECONDS=5 ;;
    esac
    if ! command -v flock >/dev/null 2>&1; then
        SUMMARY="backup — flock-missing"
        backup_alert_failure \
            "backup-flock-missing" \
            "Dolt backup: flock missing for backup sync [HIGH]" \
            "Skipping backup sync because flock is unavailable; concurrent dolt backup sync can overload the shared sql-server."
        dolt_notify_done "$SUMMARY"
        echo "backup: $SUMMARY"
        exit 1
    fi

    mkdir -p "$(dirname "$BACKUP_LOCK_FILE")"
    exec 9>"$BACKUP_LOCK_FILE"
    if ! flock -w "$BACKUP_LOCK_WAIT_SECONDS" 9; then
        SUMMARY="backup — skipped: already running"
        dolt_notify_done "$SUMMARY"
        echo "backup: $SUMMARY"
        exit 0
    fi
}

# stamp_backup_sync_state records a completed Dolt backup sync where the
# consumers of backup freshness actually look. Three readers share this file
# and all three agree on the format written here:
#
#   1. reaper.sh step 6 gates its closed-session-bead bulk prune on it. A scope
#      with a registered destination (.beads/dolt-backup.json) is judged on
#      dolt-backup-state.json, and this script is the live backup mechanism but
#      never wrote that file — so the gate latched closed permanently and
#      session beads accumulated forever. Only `bd backup sync` wrote it, and
#      that path is inoperative when backup.enabled=false, the documented
#      default for shared-server mode.
#   2. `gc doctor` bd-backup-freshness (scanDoltBackupFreshness) reads
#      last_sync as RFC3339.
#   3. `bd backup status` unmarshals beads' doltBackupState — last_sync into a
#      time.Time (RFC3339), duration as a free-form string.
#
# Format constraints, tightest first: the reaper parses last_sync with a
# single-line sed and a %Y-%m-%dT%H:%M:%SZ strptime, so last_sync is emitted on
# its own line as whole-second UTC with no fractional part. That is also valid
# RFC3339 for the two Go readers.
stamp_backup_sync_state() {
    local elapsed="$1"
    # Target defaults to the city's own state file. Peer scopes pass their own:
    # this script backs up databases belonging to other beads workspaces, and
    # each of those scopes is judged on its own copy of this file.
    local target="${2:-$BACKUP_SYNC_STATE_FILE}"
    local tmp
    local synced_at

    synced_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    tmp=$(mktemp "$target.tmp.XXXXXX" 2>/dev/null) || return 1
    if ! ( umask 077; printf '{\n  "last_sync": "%s",\n  "duration": "%ss"\n}\n' \
        "$synced_at" "$elapsed" > "$tmp" ); then
        rm -f "$tmp" 2>/dev/null || true
        return 1
    fi
    if ! mv -f "$tmp" "$target" 2>/dev/null; then
        rm -f "$tmp" 2>/dev/null || true
        return 1
    fi
    return 0
}

# peer_scope_roots emits one absolute workspace root per line for every scope
# this city's routes bind, excluding the city itself. Route paths are relative
# to the scope root that owns the file, so they resolve against GC_CITY_PATH. A
# route pointing at a directory that does not exist is skipped silently — a
# stale route must not fail a backup run.
peer_scope_roots() {
    local routes="$GC_CITY_PATH/.beads/routes.jsonl"
    local route_line
    local rel
    local abs

    [ -f "$routes" ] || return 0
    while IFS= read -r route_line || [ -n "$route_line" ]; do
        [ -n "$route_line" ] || continue
        case "$route_line" in
            *'"path"'*) ;;
            *) continue ;;
        esac
        rel="${route_line##*\"path\":\"}"
        rel="${rel%%\"*}"
        [ -n "$rel" ] || continue
        abs=$( (CDPATH= cd -- "$GC_CITY_PATH/$rel" 2>/dev/null && pwd) || true )
        [ -n "$abs" ] || continue
        if same_path "$abs" "$GC_CITY_PATH"; then
            continue
        fi
        printf '%s\n' "$abs"
    done < "$routes"
}

# scope_dolt_database emits the Dolt database a scope's beads workspace is bound
# to. This is the same binding `gc doctor` resolves per scope root, so stamping
# keyed on it lands where the freshness check reads.
scope_dolt_database() {
    local meta="$1/.beads/metadata.json"

    [ -f "$meta" ] || return 1
    sed -n 's/.*"dolt_database"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$meta" | head -1
}

# stamp_peer_scope_states refreshes the freshness signal of every peer scope
# whose database this run backed up, and emits the number stamped.
#
# This script is the mechanism that actually backs those databases up — a rig's
# store lives in the city's Dolt data dir and its backup lands in the city's
# artifact dir — but it used to stamp only the city's own file. `gc doctor`
# bd-backup-freshness reads the file per scope root and reaper.sh step 6 gates
# its bulk prune on it, so a rig backed up on schedule still looked permanently
# un-backed-up and kept its prune gate latched (gc-i37v7).
#
# Called only on a clean sweep, so every enumerated database synced.
stamp_peer_scope_states() {
    local elapsed="$1"
    local stamped=0
    local scope_root
    local scope_db
    local scope_state

    while IFS= read -r scope_root; do
        [ -n "$scope_root" ] || continue
        [ -d "$scope_root/.beads" ] || continue
        scope_db=$(scope_dolt_database "$scope_root") || continue
        [ -n "$scope_db" ] || continue
        if ! printf '%s\n' "$DATABASES" | grep -qx -- "$scope_db"; then
            continue
        fi
        scope_state="$scope_root/.beads/dolt-backup-state.json"
        # Refresh an existing stamp, or create one for a scope that has a
        # registered Dolt destination. Never invent one for a scope with
        # neither: absence means no backup is registered there, which
        # DoltBackupCheck reports, and writing a stamp would both assert
        # coverage nobody configured and newly enrol that scope in
        # bd-backup-freshness. Mirrors the no-workspace rule one level out.
        if [ ! -f "$scope_state" ] && [ ! -f "$scope_root/.beads/dolt-backup.json" ]; then
            continue
        fi
        if stamp_backup_sync_state "$elapsed" "$scope_state"; then
            stamped=$((stamped + 1))
        else
            echo "backup: warning: could not stamp $scope_state;" \
                "that scope's reaper bulk prune will keep reading a stale timestamp" >&2
        fi
    done <<SCOPE_ROOTS
$(peer_scope_roots)
SCOPE_ROOTS
    printf '%s\n' "$stamped"
}

# --- Step 1: Preflight Dolt version before backup sync ---

DOLT_VERSION="$(dolt version 2>/dev/null | awk 'NR == 1 {print $NF}' || true)"
if ! dolt_version_at_least "$DOLT_VERSION" "$MIN_DOLT_BACKUP_VERSION"; then
    backup_alert_failure \
        "backup-dolt-too-old|version=${DOLT_VERSION:-unknown}|required=$MIN_DOLT_BACKUP_VERSION" \
        "Dolt backup: dolt-too-old for backup sync [HIGH]" \
        "Skipping backup sync: dolt version ${DOLT_VERSION:-unknown} is below required ${MIN_DOLT_BACKUP_VERSION}. Gas City requires manifest-aware file-backup pruning before backup sync."
    SUMMARY="backup — dolt-too-old: ${DOLT_VERSION:-unknown}, required: $MIN_DOLT_BACKUP_VERSION"
    dolt_notify_done "$SUMMARY"
    echo "backup: $SUMMARY"
    exit 1
fi

acquire_backup_lock

# --- Step 2: Sync databases to backup remotes ---

# If GC_BACKUP_DATABASES is set, use it; otherwise auto-discover every user
# database in the data dir. Discovery used to require an existing <db>-backup
# remote, silently excluding unconfigured DBs from backup coverage — which is
# how production DBs ended up unrecoverable after journal corruption (#3176:
# beads_hq had no named remote, so it was never synced). DBs without the
# remote now get one auto-configured below.
if [ -n "${GC_BACKUP_DATABASES:-}" ]; then
    DATABASES=$(echo "$GC_BACKUP_DATABASES" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' || true)
else
    ALL_DBS=$(dolt_sql -r csv -q "SHOW DATABASES" 2>/dev/null | tail -n +2 | \
        grep -viE "$SYSTEM_DBS" || true)
    DATABASES=""
    for db in $ALL_DBS; do
        if [ -d "$DOLT_DATA_DIR/$db/.dolt" ]; then
            DATABASES="$DATABASES $db"
        fi
    done
    DATABASES=$(echo "$DATABASES" | tr ' ' '\n' | grep -v '^$' || true)
fi

if [ -z "$DATABASES" ]; then
    backup_alert_recovery
    echo "backup: no databases found, skipping"
    exit 0
fi

# ensure_backup_remote guarantees db has a named <db>-backup remote, creating
# one under the backup artifact dir when missing. Auto-configuration is logged
# loudly so operators can see when coverage was established rather than
# assumed. Returns 1 when the remote cannot be configured.
ensure_backup_remote() {
    remote_db="$1"
    remote_db_dir="$DOLT_DATA_DIR/$remote_db"
    expected_remote_path="$BACKUP_ARTIFACT_DIR/$remote_db"
    BACKUP_REMOTE_FAILURE="backup add failed"
    # The name the sync loop must address this database's backup by. Defaults to
    # the managed name and is rewritten below when an existing remote under a
    # different name already covers the managed path.
    BACKUP_REMOTE_NAME="${remote_db}-backup"
    [ -d "$remote_db_dir/.dolt" ] || return 0 # sync loop reports not-found
    if ! backup_destination_is_safe "$expected_remote_path" "$remote_db_dir"; then
        BACKUP_REMOTE_FAILURE="unsafe backup artifact path"
        echo "backup: refusing unsafe backup destination for $remote_db: $expected_remote_path" >&2
        return 1
    fi
    # Read the listing once: it is consulted twice, by name and then by path.
    backup_list=$(cd "$remote_db_dir" && run_bounded 30 dolt backup -v 2>/dev/null || true)
    backup_url=$(printf '%s\n' "$backup_list" | backup_url_from_list "${remote_db}-backup" || true)
    if [ -n "$backup_url" ]; then
        case "$backup_url" in
            file://*)
                if same_path "${backup_url#file://}" "$expected_remote_path"; then
                    return 0
                fi
                ;;
        esac
        BACKUP_REMOTE_FAILURE="backup destination mismatch"
        echo "backup: ${remote_db}-backup points outside managed artifact path: $backup_url" >&2
        return 1
    fi
    # No remote under the managed name — but a backup configured by another tool
    # may already cover the managed path under a different name (`bd backup`
    # names its remote `default`). That is coverage, and adding a second remote
    # at the same address is not merely redundant: Dolt rejects it outright with
    # "address conflict with a remote", which used to mark the database failed
    # and, because the whole-city stamp is gated on a clean sweep, froze
    # bd-backup-freshness for every other database too (gc-534qw). Adopt the
    # existing name instead. Only the resolved destination decides the match, so
    # this cannot admit a backup living anywhere but the managed path.
    existing_name=$(printf '%s\n' "$backup_list" | backup_name_for_path "$expected_remote_path" || true)
    if [ -n "$existing_name" ]; then
        BACKUP_REMOTE_NAME="$existing_name"
        echo "backup: $remote_db already backed up to the managed path by remote '$existing_name'"
        return 0
    fi
    remote_url="file://$expected_remote_path"
    mkdir -p "$BACKUP_ARTIFACT_DIR/$remote_db"
    # Keep dolt's refusal. Discarding it left only the opaque "backup add
    # failed", which is what made the address conflict above take a day to
    # place.
    if add_output=$(cd "$remote_db_dir" && run_bounded 30 dolt backup add "${remote_db}-backup" "$remote_url" 2>&1); then
        echo "backup: auto-configured missing backup remote ${remote_db}-backup -> $remote_url"
        return 0
    fi
    if [ -n "$add_output" ]; then
        printf '%s\n' "$add_output" | while IFS= read -r add_line || [ -n "$add_line" ]; do
            printf 'backup: %s: %s\n' "$remote_db" "$add_line" >&2
        done
    fi
    return 1
}

TOTAL=$(printf '%s\n' "$DATABASES" | awk 'NF {count++} END {print count + 0}')
SYNCED=0
FAILED=0
FAILED_DBS=""
PRUNE_COMPLETED=0
PRUNE_SKIPPED=0
PRUNE_FAILED=0
PRUNE_VERIFIED=0
PRUNE_UNVERIFIED=0
PRUNE_FAILED_DBS=""
SYNC_STARTED_EPOCH=$(date -u '+%s')

for db in $DATABASES; do
    if ! ensure_backup_remote "$db"; then
        PRUNE_UNVERIFIED=$((PRUNE_UNVERIFIED + 1))
        append_failed_db "$db($BACKUP_REMOTE_FAILURE)"
        continue
    fi
    db_dir="$DOLT_DATA_DIR/$db"
    if [ ! -d "$db_dir/.dolt" ]; then
        PRUNE_UNVERIFIED=$((PRUNE_UNVERIFIED + 1))
        append_failed_db "$db(database not found)"
        continue
    fi
    sync_output=""
    if sync_output=$(cd "$db_dir" && run_bounded 120 dolt backup sync --prune-with-grace-period "$BACKUP_PRUNE_GRACE_PERIOD" "$BACKUP_REMOTE_NAME" 2>&1); then
        SYNCED=$((SYNCED + 1))
        if printf '%s\n' "$sync_output" | grep -qiE 'not supported.*only file:// backups can be pruned|pruning .* failed, continuing with sync'; then
            PRUNE_FAILED=$((PRUNE_FAILED + 1))
            PRUNE_FAILED_DBS="$PRUNE_FAILED_DBS $db"
        elif printf '%s\n' "$sync_output" | grep -qiE 'not quiescent|; skipped ' ; then
            PRUNE_SKIPPED=$((PRUNE_SKIPPED + 1))
            PRUNE_VERIFIED=$((PRUNE_VERIFIED + 1))
        else
            PRUNE_COMPLETED=$((PRUNE_COMPLETED + 1))
            PRUNE_VERIFIED=$((PRUNE_VERIFIED + 1))
        fi
    else
        PRUNE_FAILED=$((PRUNE_FAILED + 1))
        PRUNE_FAILED_DBS="$PRUNE_FAILED_DBS $db(sync failed)"
        append_failed_db "$db(sync failed)"
    fi
done

FAILED_COUNT=$FAILED
OFFSITE_STATUS="skipped"

# Stamp only a clean sweep. A partial sync must leave the timestamp stale: the
# reaper gate exists to withhold bulk deletion while backup coverage is
# incomplete, and refreshing it here on partial success would defeat exactly
# that. Offsite rsync is deliberately not a precondition — it runs after this
# and is non-fatal, so the databases are already backed up either way.
STATE_STAMP="skipped"
PEER_SCOPES_STAMPED=0
if [ "$FAILED_COUNT" -eq 0 ] && [ "$SYNCED" -gt 0 ]; then
    SYNC_ELAPSED=$(( $(date -u '+%s') - SYNC_STARTED_EPOCH ))
    if [ ! -d "$(dirname "$BACKUP_SYNC_STATE_FILE")" ]; then
        # Not a beads workspace. Creating the directory here would fabricate one
        # that beads.FindBeadsDir() would then resolve, so report and move on.
        STATE_STAMP="no-workspace"
    elif stamp_backup_sync_state "$SYNC_ELAPSED"; then
        STATE_STAMP="ok"
    else
        STATE_STAMP="failed (non-fatal)"
        echo "backup: warning: could not stamp $BACKUP_SYNC_STATE_FILE;" \
            "reaper bulk prune will keep reading a stale backup timestamp" >&2
    fi
    # Peer scopes are stamped on the same clean-sweep condition, and
    # independently of whether the CITY has a beads workspace: a city without
    # one still backs up its rigs' databases, and those rigs are still judged on
    # their own freshness files.
    PEER_SCOPES_STAMPED=$(stamp_peer_scope_states "$SYNC_ELAPSED")
fi

# --- Step 3: Rsync backup artifacts to offsite storage ---

if [ -n "$OFFSITE_PATH" ]; then
    if [ ! -d "$BACKUP_ARTIFACT_DIR" ]; then
        OFFSITE_STATUS="missing-artifacts"
    elif same_path "$BACKUP_ARTIFACT_DIR" "$DOLT_DATA_DIR"; then
        OFFSITE_STATUS="invalid-source"
    elif run_bounded 300 rsync -a --delete "$BACKUP_ARTIFACT_DIR/" "$OFFSITE_PATH/" 2>/dev/null; then
        OFFSITE_STATUS="ok"
    else
        OFFSITE_STATUS="failed (non-fatal)"
    fi
fi

# --- Step 4: Report manifest-aware prune health ---

if [ "$PRUNE_FAILED" -gt 0 ]; then
    backup_prune_alert_failure
elif [ "$PRUNE_UNVERIFIED" -eq 0 ] && [ "$PRUNE_VERIFIED" -eq "$TOTAL" ]; then
    backup_prune_alert_recovery
fi

# --- Step 5: Bound and report backup artifact size ---

check_backup_size

# --- Step 6: Report sync health ---

if [ "$FAILED_COUNT" -gt 0 ]; then
    backup_alert_failure \
        "$(backup_failure_fingerprint)" \
        "Dolt backup: $FAILED_COUNT/$TOTAL databases failed to sync [MEDIUM]" \
        "Failed databases:$FAILED_DBS"
else
    backup_alert_recovery
fi

SUMMARY="backup — synced: $SYNCED/$TOTAL, offsite: $OFFSITE_STATUS, state: $STATE_STAMP"
if [ "$PEER_SCOPES_STAMPED" -gt 0 ]; then
    SUMMARY="$SUMMARY (+$PEER_SCOPES_STAMPED peer scope(s))"
fi
dolt_notify_done "$SUMMARY"
echo "backup: $SUMMARY"
