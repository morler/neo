#!/usr/bin/env bash
# Offline verification of grade.sh (#132): a correct answer passes, an answer
# missing the required evidence fails. No network, no model.
set -euo pipefail
ws="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

expect() {
  local name="$1" fx="$2" want="$3"
  local got; got="$("$ws/grade.sh" "$tmp/$name" "$fx" 2>/dev/null || true)"
  if [ "$got" != "$want" ]; then
    echo "FAIL $fx $name: got '$got' want '$want'" >&2; exit 1
  fi
  echo "ok $fx $name -> $got"
}

printf '%s' '{"final":"fixture/service/config.go:8: const DefaultPort = 8080\nfixture/cli/root.go:8: return service.DefaultPort"}' >"$tmp/a.json"
expect a find-const 1
printf '%s' '{"final":"service.go says 9090"}' >"$tmp/b.json"
expect b find-const 0
printf '%s' '{"final":"line 2871: ERROR dial tcp 10.4.2.9:6379: connect: connection refused"}' >"$tmp/c.json"
expect c find-error 1
printf '%s' '{"final":"the log has an error near the end"}' >"$tmp/d.json"
expect d find-error 0
printf '%s' '{"final":"line 57: threshold = 42"}' >"$tmp/e.json"
expect e narrow-read 1
printf '%s' '{"final":"it is 42"}' >"$tmp/f.json"
expect f narrow-read 0
echo "grade.sh: all checks passed"
