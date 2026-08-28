#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
version_script="$script_dir/version-from-git.sh"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/gc-version-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

new_repo() {
  repo=$1
  mkdir -p "$repo"
  git -C "$repo" init -q
  git -C "$repo" config user.name test
  git -C "$repo" config user.email test@example.com
  printf 'one\n' >"$repo/file"
  git -C "$repo" add file
  git -C "$repo" commit -qm initial
}

assert_version() {
  expected=$1
  repo=$2
  actual=$("$version_script" "$repo")
  if [ "$actual" != "$expected" ]; then
    printf 'version for %s = %s, want %s\n' "$repo" "$actual" "$expected" >&2
    exit 1
  fi
}

new_repo "$tmp/release"
git -C "$tmp/release" tag v1.4.0
assert_version 1.4.0 "$tmp/release"

printf 'two\n' >>"$tmp/release/file"
git -C "$tmp/release" commit -qam second
short=$(git -C "$tmp/release" rev-parse --short=9 HEAD)
assert_version "1.4.0-1-g$short" "$tmp/release"

new_repo "$tmp/untagged"
assert_version dev "$tmp/untagged"

new_repo "$tmp/prerelease"
git -C "$tmp/prerelease" tag v1.5.0-rc1
assert_version 1.5.0-rc1 "$tmp/prerelease"

printf 'version-from-git tests passed\n'
