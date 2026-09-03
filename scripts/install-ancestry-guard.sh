#!/usr/bin/env bash
# install-ancestry-guard.sh
#
# Refuse an install that would UNINSTALL work from the binary it replaces.
#
# gc-ffi96 added exactly this guard to the city's assets/scripts/gc-deploy.sh,
# and it works. It is also not the only way a binary reaches a running city:
# `make install` performs a plain atomic replace of $(INSTALL_DIR)/gc with no
# ancestry check, no backup, and no statement of what it replaced — and it is
# the DOCUMENTED way to install gc, and shorter to type than the guarded path.
#
# That hole fired (gc-km00g). The installed lineage on the reporting machine is
# not linear, which it must be if every install were guarded:
#
#   git merge-base --is-ancestor 6ed0780a0 8c3e2b7ed  ->  NO
#   git merge-base --is-ancestor 6ed0780a0 d8a2039fc  ->  NO
#
# Installing d8a2039fc over 6ed0780a0 dropped eleven once-running commits,
# four of them External Coordination fixes, out of the running city. The bead
# those commits fix (gc-azdsp) then read as "still not fixed" when the truth
# was "fixed, then un-deployed by an unguarded install". Nothing recorded it.
#
# WHY A SILENT DOWNGRADE IS THE DANGEROUS SHAPE. Installing an OLDER binary is
# indistinguishable from an upgrade in every signal an install emits: the copy
# succeeds, the binary runs fine (that is the point), and the exit code is 0.
# Nothing is corrupt afterwards; work simply stops being present. So this guard
# is FAIL-CLOSED — an ancestry that cannot be positively established is treated
# the same as a bad one, because "probably fine" is precisely the reasoning
# that loses eleven commits.
#
# Exit codes:
#   0  safe — the source is the same commit as, or a descendant of, the
#      installed one; or there is nothing installed to lose
#   1  REFUSED — installing would remove work; the lost commits are listed
#   2  usage error
set -uo pipefail

REPO="."; TARGET=""; SOURCE_COMMIT=""; ALLOW_ROLLBACK="${ALLOW_ROLLBACK:-0}"; LABEL="install"

usage() {
  cat >&2 <<'USAGE'
usage: install-ancestry-guard.sh --target <installed-binary-path> [--repo <dir>]
                                 [--source-commit <sha>] [--allow-rollback]
                                 [--label <name>]

Run this BEFORE replacing an installed binary. A non-zero exit is a refusal:
the install would remove work that is currently present. Re-run with
--allow-rollback (or ALLOW_ROLLBACK=1) only when the revert is deliberate.
USAGE
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo)          [ "$#" -ge 2 ] || usage; REPO="$2"; shift 2 ;;
    --target)        [ "$#" -ge 2 ] || usage; TARGET="$2"; shift 2 ;;
    --source-commit) [ "$#" -ge 2 ] || usage; SOURCE_COMMIT="$2"; shift 2 ;;
    --label)         [ "$#" -ge 2 ] || usage; LABEL="$2"; shift 2 ;;
    --allow-rollback) ALLOW_ROLLBACK=1; shift ;;
    -h|--help)       usage ;;
    *) printf 'install-ancestry-guard: unknown argument %s\n' "$1" >&2; usage ;;
  esac
done
[ -n "$TARGET" ] || usage

say()  { printf 'install-ancestry-guard: %s\n' "$*"; }
warn() { printf 'install-ancestry-guard: WARNING: %s\n' "$*" >&2; }

# A refusal names the override in the same breath. A guard whose escape hatch
# is undiscoverable gets worked around by deleting the guard.
refuse() {
  if [ "$ALLOW_ROLLBACK" = "1" ]; then
    warn "--allow-rollback given; proceeding anyway."
    printf '%s\n' "$*" >&2
    return 0
  fi
  {
    printf '\ninstall-ancestry-guard: REFUSED (%s)\n\n' "$LABEL"
    printf '%s\n\n' "$*"
    printf 'Re-run with ALLOW_ROLLBACK=1 if this revert is deliberate. A bare retry\n'
    printf 'of the same command will not pass — that is the point.\n'
  } >&2
  exit 1
}

# Nothing installed yet: there is no work to lose, so there is nothing to
# check. This is the ordinary first install and must not be made noisy.
if [ ! -e "$TARGET" ]; then
  say "no binary at $TARGET yet; first install, nothing to lose"
  exit 0
fi

# The version string is the only durable record of what is installed — the
# binary does not carry a git checkout with it. `gc version` prints e.g.
# 1.4.0-918-g15371862e; the commit is the -g suffix.
CUR_VERSION="$("$TARGET" version 2>/dev/null | head -1 || true)"
if [ -z "$CUR_VERSION" ]; then
  refuse "there is a file at $TARGET but it did not answer 'version', so it is
not possible to tell what is installed or whether replacing it would move
forward or backward."
  # Only reachable with ALLOW_ROLLBACK=1, which has already accepted that the
  # replacement is unverifiable. Stop here rather than fall through and refuse
  # a second time for the same missing information.
  exit 0
fi

CUR_COMMIT=""
if [[ "$CUR_VERSION" =~ -g([0-9a-f]{7,40})([^0-9a-f].*)?$ ]]; then
  CUR_COMMIT="${BASH_REMATCH[1]}"
fi

# Not a git checkout: a tarball or vendored build. Ancestry is unknowable, not
# violated, and refusing here would break every legitimate non-git install. So
# report it LOUDLY and let it through — the caller still writes a backup and an
# audit line, which is what makes the replacement reconstructible after the
# fact (gc-km00g acceptance 2).
if ! git -C "$REPO" rev-parse --git-dir >/dev/null 2>&1; then
  warn "$REPO is not a git checkout, so ancestry cannot be verified.
  Replacing $CUR_VERSION at $TARGET without an ancestry check.
  If a running city depends on that binary, verify by hand what it contained."
  exit 0
fi

[ -n "$SOURCE_COMMIT" ] || SOURCE_COMMIT="$(git -C "$REPO" rev-parse --verify 'HEAD^{commit}' 2>/dev/null || true)"
[ -n "$SOURCE_COMMIT" ] || refuse "could not resolve the commit being installed from $REPO."
SRC_FULL="$(git -C "$REPO" rev-parse --verify "${SOURCE_COMMIT}^{commit}" 2>/dev/null || true)"
[ -n "$SRC_FULL" ] || refuse "the commit being installed ($SOURCE_COMMIT) does not resolve in $REPO."

if [ -z "$CUR_COMMIT" ]; then
  # Deliberately NOT "probably fine". The failure being prevented is a silent
  # success, so an undeterminable ancestry has to fail the same way a bad one
  # does — otherwise the one case where the guard cannot see is the one case
  # it waves through.
  refuse "cannot determine the installed commit from the version string
'$CUR_VERSION', so it is not possible to tell whether installing
${SRC_FULL:0:9} would move forward or backward."
fi

if ! git -C "$REPO" cat-file -e "${CUR_COMMIT}^{commit}" 2>/dev/null; then
  refuse "the installed commit $CUR_COMMIT is not present in $REPO (a shallow
clone, or a binary built from a different checkout), so ancestry cannot be
determined. Fetch the branch that carries it, then re-run."
fi
CUR_FULL="$(git -C "$REPO" rev-parse --verify "${CUR_COMMIT}^{commit}" 2>/dev/null)"

if [ "$CUR_FULL" = "$SRC_FULL" ]; then
  say "reinstalling the same commit (${CUR_COMMIT:0:9}); nothing is lost"
  exit 0
fi
if git -C "$REPO" merge-base --is-ancestor "$CUR_FULL" "$SRC_FULL" 2>/dev/null; then
  ahead="$(git -C "$REPO" rev-list --count "$CUR_FULL..$SRC_FULL" 2>/dev/null || echo '?')"
  say "${SRC_FULL:0:9} is a descendant of the installed ${CUR_COMMIT:0:9} (+$ahead commit(s))"
  exit 0
fi
if [ -z "$(git -C "$REPO" merge-base "$CUR_FULL" "$SRC_FULL" 2>/dev/null)" ]; then
  # Unrelated histories get their own sentence: there is no "lost commits" list
  # to print, because the two sides share nothing at all.
  refuse "the installed commit ${CUR_COMMIT:0:9} and the commit being installed
${SRC_FULL:0:9} have UNRELATED histories — no common ancestor — so this is not
an upgrade in any sense."
fi

lost="$(git -C "$REPO" log --oneline --no-decorate "$SRC_FULL..$CUR_FULL" 2>/dev/null || true)"
lost_n="$(git -C "$REPO" rev-list --count "$SRC_FULL..$CUR_FULL" 2>/dev/null || echo '?')"
refuse "installing ${SRC_FULL:0:9} would UNINSTALL work: the installed binary is
$CUR_VERSION (${CUR_COMMIT:0:9}), which is NOT an ancestor of ${SRC_FULL:0:9}.

$lost_n commit(s) present in the installed binary would be lost:
$(printf '%s\n' "$lost" | sed 's/^/    /')"
