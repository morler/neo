#!/usr/bin/env bash
# Grade one recorded run against the answer its fixture requires (#133).
#
# Both fixtures ask for facts that cannot be guessed: a file and line for a
# behaviour, or the exact range of a named method. The check is literal and
# applied to both arms identically.
set -euo pipefail

base=${1:?usage: grade.sh <base> <fixture>}
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
  unknown)
    # The attempt budget is the Attempts field on Config; the retry delay grows
    # in backoffFor. Both answers need the file and the line.
    need "Attempts"
    need "config.go"
    need "backoffFor"
    need "retry.go"
    ;;
  known)
    # reconcile occupies 643-660 and its signature spans six lines.
    need "reconcile"
    need "643"
    need "660"
    need "force bool"
    need "[]string, error"
    ;;
  *) echo "unknown fixture: $fixture" >&2; exit 2 ;;
esac

if [ "$pass" = 1 ]; then echo 1; else echo 0; fi
