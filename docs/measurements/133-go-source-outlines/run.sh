#!/usr/bin/env bash
# #133 measurement harness: outline+narrow-read against grep+narrow-read.
#
#   ./run.sh <fixture|all> <arm|all> <label>
#
# Fixtures: "unknown" (a symbol whose location is not known) and "known" (a
# symbol named in the prompt). Arms differ only in how the model is told to
# locate code; the task and the answer required are identical.
set -euo pipefail

ws="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
neo="$ws/neo"
outdir="$ws/runs"
mkdir -p "$outdir"

fixture=${1:-all}; arm=${2:-all}; label=${3:-r1}

prompt_for() {
  case "$1:$2" in
    unknown:grep)
      echo 'This workspace is a Go module under fixture/. Something sets an attempt budget and something else grows the retry delay with the attempt number. Find both, and report the file, line number, and exact line for each.' ;;
    unknown:outline)
      echo 'This workspace is a Go module under fixture/. Something sets an attempt budget and something else grows the retry delay with the attempt number. Find both, and report the file, line number, and exact line for each.

You may run ./fixture/outline with one or more .go files as arguments. It prints one line per declaration as "path:start-end<TAB>kind<TAB>name<TAB>signature", so a range can be read with read_file offset=start limit=(end-start+1). It prints no function bodies.' ;;
    known:grep)
      echo 'In fixture/processor.go, find the declare of the method named reconcile and report its full signature and the line range it occupies.' ;;
    known:outline)
      echo 'In fixture/processor.go, find the declare of the method named reconcile and report its full signature and the line range it occupies.

You may run ./fixture/outline with one or more .go files as arguments. It prints one line per declaration as "path:start-end<TAB>kind<TAB>name<TAB>signature", so a range can be read with read_file offset=start limit=(end-start+1). It prints no function bodies.' ;;
    *)
      echo "unknown fixture/arm: $1/$2" >&2; return 1 ;;
  esac
}

run_one() {
  local fx="$1" a="$2"
  local base="$outdir/${fx}.${a}.${label}"
  local work="$ws/work-${fx}-${a}-${label}"
  rm -rf "$work"; mkdir -p "$work"
  cp -r "$ws/fixture" "$work/fixture"
  # The fixture's Go sources are stored as *.go.txt so this repository's
  # "go test ./..." does not build them; the workspace the model sees is real.
  for f in "$work"/fixture/*.go.txt; do
    [ -e "$f" ] || continue
    mv "$f" "${f%.txt}"
  done
  local p; p="$(prompt_for "$fx" "$a")"
  local code=0
  ( cd "$work" && NEO_LOG="$base.log" NEO_LOG_VERBOSE="" "$neo" run --json --effort "${EFFORT:-low}" --timeout "${TIMEOUT:-8m}" "$p" >"$base.json" 2>"$base.err" ) || code=$?
  echo "$code" >"$base.exit"
  "$ws/reduce.sh" "$base" "$label" "$fx" "$a" >"$base.summary.json"
  local grade; grade="$("$ws/grade.sh" "$base" "$fx" 2>/dev/null || echo 0)"
  jq -r --argjson g "$grade" '[.fixture,.arm,.label,(.ok|tostring),(.requests|tostring),(.tool_result_bytes|tostring),(.final_bytes|tostring),(.input_tokens|tostring),(.cache_read_tokens|tostring),(.output_tokens|tostring),(.tool_errors|tostring),($g|tostring)]|@tsv' "$base.summary.json"
}

fixtures=(); arms=()
if [ "$fixture" = all ]; then fixtures=(unknown known); else fixtures=("$fixture"); fi
if [ "$arm" = all ]; then arms=(grep outline); else arms=("$arm"); fi

for fx in "${fixtures[@]}"; do
  for a in "${arms[@]}"; do
    echo "== $fx / $a / $label" >&2
    run_one "$fx" "$a"
  done
done
