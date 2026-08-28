#!/bin/sh
set -eu

repo=${1:-.}
described=$(git -C "$repo" describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --long --abbrev=9 2>/dev/null || true)
if [ -z "$described" ]; then
  printf 'dev\n'
  exit 0
fi

version=${described#v}
case "$version" in
  *-0-g*) version=${version%-0-g*} ;;
esac
printf '%s\n' "$version"
