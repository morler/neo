#!/usr/bin/env bash
# Grade one recorded run against the answer its fixture requires (#132).
#
# Success means the final answer carries the evidence a correct answer cannot
# omit, asserted as literal strings. This is deliberately mechanical: it must be
# replayable without judging prose.
set -euo pipefail

base=${1:?usage: grade.sh <base>}
fixture=${2:?usage: grade.sh <base> <fixture>}
final="$(jq -r '.final // ""' "$base.json" 2>/dev/null || echo '')"

pass=1
need() {
  if ! grep -qF -- "$1" <<<"$final"; then
    pass=0
    echo "missing: $1" >&2
  fi
}

case "$fixture" in
  find-const)
    need "8080"
    need "config.go"
    need "root.go"
    ;;
  find-error)
    need "2871"
    need "connection refused"
    need "10.4.2.9"
    need "6379"
    ;;
  narrow-read)
    need "42"
    need "57"
    ;;
  *) echo "unknown fixture: $fixture" >&2; exit 2 ;;
esac

if [ "$pass" = 1 ]; then echo 1; else echo 0; fi
