#!/usr/bin/env bash
# Hermetic tests for scripts/install-ancestry-guard.sh (gc-km00g).
#
# The central case is the incident's: `make install` replaced the running
# binary with a commit that was NOT a descendant of it and dropped eleven
# once-running commits, reporting success. Nothing in the install's output,
# exit code, or the resulting binary's behaviour distinguished that from an
# upgrade — which is why it went undiagnosed for a day.
#
# Real throwaway git repos and real fake binaries, because the checks that
# matter — "is the installed commit an ancestor of this one" and "what would
# be lost" — are exactly the ones a mocked git would wave through.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GUARD="${GC_INSTALL_GUARD_SCRIPT:-$ROOT/scripts/install-ancestry-guard.sh}"

fail() { printf 'install-ancestry-guard test failed: %s\n' "$*" >&2; exit 1; }
[ -x "$GUARD" ] || fail "guard is not executable: $GUARD"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git_q() { git -c core.hooksPath=/dev/null -c user.email=t@t -c user.name=t -C "$1" "${@:2}"; }

# A fake installed binary: the only thing the guard reads from it is the
# version line, which is the only thing a real installed binary carries about
# its own provenance.
mk_binary() {  # mk_binary <path> <version-string>
  mkdir -p "$(dirname "$1")"
  printf '#!/bin/sh\n[ "$1" = version ] && echo %s\n' "$2" > "$1"
  chmod +x "$1"
}

REPO="$WORK/repo"
git init -q -b main "$REPO"
git_q "$REPO" config user.email t@t; git_q "$REPO" config user.name t
echo base > "$REPO/f"; git_q "$REPO" add -A; git_q "$REPO" commit -qm base
BASE="$(git_q "$REPO" rev-parse HEAD)"

# The branch that carried the work, and the mainline that did not. This is the
# incident's exact topology: main advanced separately, so installing main over
# the branch tip is a NON-descendant install that loses the branch's commits.
git_q "$REPO" checkout -q -b deploy/embedded-dashboard-ui-off
for m in "fix(external-coordination): tell the truth about what can deliver" \
         "fix(external-coordination): requeue a transient delivery failure" \
         "fix(supervisor): make embedded dashboard optional"; do
  echo "$m" >> "$REPO/f"; git_q "$REPO" commit -qam "$m"
done
BRANCH_TIP="$(git_q "$REPO" rev-parse HEAD)"
git_q "$REPO" checkout -q main
echo mainline >> "$REPO/other"; git_q "$REPO" add -A; git_q "$REPO" commit -qm "unrelated mainline work"
MAIN_TIP="$(git_q "$REPO" rev-parse HEAD)"

# run <target> <source-commit> [env KEY=VAL ...] -> prints "<rc>|<output>"
run() {
  local target="$1" src="$2"; shift 2
  local out rc=0
  out=$(env "$@" "$GUARD" --target "$target" --repo "$REPO" --source-commit "$src" 2>&1) || rc=$?
  printf '%s|%s' "$rc" "$out"
}
rc_of()  { printf '%s' "${1%%|*}"; }
out_of() { printf '%s' "${1#*|}"; }

# --- 1. nothing installed yet: the ordinary first install --------------------
r="$(run "$WORK/nothing-here/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 0 ] || fail "1: a first install must be permitted, got: $r"

# --- 2. the same commit, and a genuine descendant, are permitted ------------
mk_binary "$WORK/bin/gc" "1.4.0-914-g${MAIN_TIP:0:9}"
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 0 ] || fail "2: reinstalling the same commit must be permitted, got: $r"

git_q "$REPO" checkout -q main
echo more >> "$REPO/other"; git_q "$REPO" commit -qam "a later mainline commit"
MAIN_AHEAD="$(git_q "$REPO" rev-parse HEAD)"
r="$(run "$WORK/bin/gc" "$MAIN_AHEAD")"
[ "$(rc_of "$r")" = 0 ] || fail "2: a descendant install must be permitted, got: $r"
case "$(out_of "$r")" in *descendant*) ;; *) fail "2: a descendant install must say so, got: $r" ;; esac

# --- 3. THE INCIDENT. A non-descendant install is refused, and says what -----
#     would be lost. Without the list the refusal is unactionable: the operator
#     cannot tell a stale branch from eleven commits of live work.
mk_binary "$WORK/bin/gc" "1.4.0-838-g${BRANCH_TIP:0:9}"
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] || fail "3: installing a non-descendant must be REFUSED, got: $r"
case "$(out_of "$r")" in *UNINSTALL*) ;; *) fail "3: the refusal must say it would uninstall work, got: $r" ;; esac
for subject in "tell the truth about what can deliver" \
               "requeue a transient delivery failure" \
               "make embedded dashboard optional"; do
  case "$(out_of "$r")" in
    *"$subject"*) ;;
    *) fail "3: the refusal must list the commit '$subject' that would be lost, got: $r" ;;
  esac
done
case "$(out_of "$r")" in *"3 commit(s)"*) ;; *) fail "3: the refusal must count what would be lost, got: $r" ;; esac

# --- 4. the override is deliberate, and only deliberate ---------------------
#     A guard with no escape hatch gets removed rather than overridden, so the
#     hatch exists — but a bare retry must not find it.
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] || fail "4: a bare retry must still refuse, got: $r"
case "$(out_of "$r")" in *ALLOW_ROLLBACK=1*) ;; *) fail "4: the refusal must name its override, got: $r" ;; esac
r="$(run "$WORK/bin/gc" "$MAIN_TIP" ALLOW_ROLLBACK=1)"
[ "$(rc_of "$r")" = 0 ] || fail "4: ALLOW_ROLLBACK=1 must permit a deliberate revert, got: $r"
case "$(out_of "$r")" in *WARNING*) ;; *) fail "4: an overridden revert must still be reported, got: $r" ;; esac
r=$(env "$GUARD" --target "$WORK/bin/gc" --repo "$REPO" --source-commit "$MAIN_TIP" --allow-rollback >/dev/null 2>&1; echo $?)
[ "$r" = 0 ] || fail "4: --allow-rollback must work as well as the env var, got rc=$r"

# --- 5. fail-closed: what it cannot establish, it refuses -------------------
#     This is the half that is easy to get wrong. The failure being prevented
#     is a SILENT SUCCESS, so an undeterminable ancestry has to fail the same
#     way a bad one does — otherwise the one case the guard cannot see is the
#     one case it lets through.
mk_binary "$WORK/bin/gc" "some-version-with-no-commit"
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] || fail "5: an unparseable installed version must refuse, got: $r"

mk_binary "$WORK/bin/gc" "1.4.0-838-gdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] || fail "5: an installed commit absent from the repo must refuse, got: $r"
case "$(out_of "$r")" in *"not present in"*) ;; *) fail "5: say WHY it could not be determined, got: $r" ;; esac

printf '#!/bin/sh\nexit 3\n' > "$WORK/bin/gc"; chmod +x "$WORK/bin/gc"
r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] || fail "5: a target that cannot report its version must refuse, got: $r"

# Unrelated histories get their own sentence: there is no lost-commit list to
# print, because the two sides share nothing at all.
ALIEN="$WORK/alien"
git init -q -b main "$ALIEN"
git_q "$ALIEN" config user.email t@t; git_q "$ALIEN" config user.name t
echo alien > "$ALIEN/f"; git_q "$ALIEN" add -A; git_q "$ALIEN" commit -qm alien
ALIEN_TIP="$(git_q "$ALIEN" rev-parse HEAD)"
git_q "$REPO" fetch -q "$ALIEN" main:refs/heads/alien 2>/dev/null || true
if git_q "$REPO" cat-file -e "${ALIEN_TIP}^{commit}" 2>/dev/null; then
  mk_binary "$WORK/bin/gc" "1.4.0-1-g${ALIEN_TIP:0:9}"
  r="$(run "$WORK/bin/gc" "$MAIN_TIP")"
  [ "$(rc_of "$r")" = 1 ] || fail "5: unrelated histories must refuse, got: $r"
  case "$(out_of "$r")" in *UNRELATED*) ;; *) fail "5: unrelated histories need their own diagnosis, got: $r" ;; esac
else
  fail "5: fixture setup — could not fetch the unrelated history into the test repo"
fi

# --- 6. a non-git checkout is REPORTED, not refused -------------------------
#     Ancestry is unknowable there, not violated. Refusing would break every
#     legitimate tarball or vendored install, so it passes loudly and the
#     caller's backup and audit line carry the record instead.
mk_binary "$WORK/bin/gc" "1.4.0-838-g${BRANCH_TIP:0:9}"
NOGIT="$WORK/nogit"; mkdir -p "$NOGIT"
out=$("$GUARD" --target "$WORK/bin/gc" --repo "$NOGIT" --source-commit "$MAIN_TIP" 2>&1); rc=$?
[ "$rc" = 0 ] || fail "6: a non-git checkout must not be refused, got rc=$rc: $out"
case "$out" in *WARNING*) ;; *) fail "6: an unverifiable install must be reported loudly, got: $out" ;; esac

# --- 7. structural misuse is a usage error, not a verdict -------------------
"$GUARD" >/dev/null 2>&1; [ "$?" = 2 ] || fail "7: a missing --target must exit 2 (usage)"
"$GUARD" --target "$WORK/bin/gc" --bogus >/dev/null 2>&1; [ "$?" = 2 ] || fail "7: an unknown flag must exit 2 (usage)"

# --- 8. THE MAKEFILE MUST ACTUALLY CALL IT ----------------------------------
# The guard is only half the fix; the other half is that the install path
# invokes it. gc-km00g is precisely a correct guard that one of two paths did
# not run, so asserting the script's behaviour without asserting the wiring
# would reproduce the bead inside its own regression test.
MK="$ROOT/Makefile"
[ -f "$MK" ] || fail "8: Makefile not found at $MK"
install_body="$(awk '/^install: /{f=1} f{print} f&&/^$/{exit}' "$MK")"
case "$install_body" in
  *install-ancestry-guard.sh*) ;;
  *) fail "8: the install target does not invoke install-ancestry-guard.sh" ;;
esac
case "$install_body" in
  *ALLOW_ROLLBACK*) ;;
  *) fail "8: the install target does not pass the deliberate-revert override through" ;;
esac
# Acceptance 2: every path that replaces the live binary records what it
# replaced. Only gc-deploy.sh did.
case "$install_body" in
  *".bak-"*) ;;
  *) fail "8: the install target does not back up the binary it replaces" ;;
esac
case "$install_body" in
  *'version 2>/dev/null'*) ;;
  *) fail "8: the install target does not record the version it replaced" ;;
esac

# --- 9. THE SECOND PATH IN THE SAME TARGET ----------------------------------
# `## install:` says to override INSTALL_DIR "only when building a separate
# artifact". Doing so runs a migration block that DELETES
# $HOME/.local/bin/gc — the canonical live binary — and points it at the
# artifact just built. That is a second, more surprising route to replacing
# what the running city executes, and guarding only $(INSTALL_DIR) would have
# left it open while looking like a complete fix.
case "$install_body" in
  *Symlinked*) ;;
  *) fail "9: the install target no longer contains the symlink migration; this case is testing nothing" ;;
esac
migration="${install_body#*Migrate from old install location}"
case "$migration" in
  *install-ancestry-guard.sh*) ;;
  *) fail "9: the canonical-symlink migration replaces the live binary without invoking the guard" ;;
esac
case "$migration" in
  *".bak-"*) ;;
  *) fail "9: the canonical-symlink migration does not back up the binary it repoints" ;;
esac

# ...and the guard really does refuse that shape, not merely get called. The
# canonical binary is NEWER than what is being installed, which is the exact
# condition the migration would silently discard.
mk_binary "$WORK/canon/gc" "1.4.0-838-g${BRANCH_TIP:0:9}"
r="$(run "$WORK/canon/gc" "$MAIN_TIP")"
[ "$(rc_of "$r")" = 1 ] \
  || fail "9: repointing a canonical binary that carries unmerged work must be REFUSED, got: $r"
case "$(out_of "$r")" in *UNINSTALL*) ;; *) fail "9: say what repointing would cost, got: $r" ;; esac

echo "install-ancestry-guard: all tests passed"
