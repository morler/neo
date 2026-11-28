#!/usr/bin/env bash
# Offline verification of grade.sh (#133): a correct answer passes, one that
# misses the required evidence fails. No network, no model.
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

printf '%s' '{"final":"fixture/config.go:12: Attempts int\nfixture/retry.go:17: func backoffFor(...)"}' >"$tmp/a.json"
expect a unknown 1
printf '%s' '{"final":"backoff lives somewhere in the retry logic"}' >"$tmp/b.json"
expect b unknown 0
printf '%s' '{"final":"fixture/processor.go:643-660 method reconcile func (p *Processor) reconcile( left []string, right []string, attempt int, force bool, ) ([]string, error)"}' >"$tmp/c.json"
expect c known 1
printf '%s' '{"final":"reconcile is in processor.go"}' >"$tmp/d.json"
expect d known 0
echo "grade.sh: all checks passed"
