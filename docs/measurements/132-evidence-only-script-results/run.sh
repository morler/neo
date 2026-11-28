#!/usr/bin/env bash
# #132 measurement harness: run one fixture in one arm, then reduce it.
#
#   ./run.sh <fixture|all> <arm|all> <label>
#
# Arms: "full" asks for the findings quoted verbatim, "evidence" asks for the
# conclusion only. The fixture, provider, model, effort, and binary are identical
# between arms; the final sentence of the prompt is the only difference, which is
# what isolates how much a script returns into the context.
set -euo pipefail

ws="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
neo="$ws/neo"
outdir="$ws/runs"
mkdir -p "$outdir"

fixture=${1:-all}; arm=${2:-all}; label=${3:-r1}

prompt_for() {
  case "$1:$2" in
    find-const:full)
      echo 'In the workspace under fixture/, find where the service port constant is defined and where it is used. Quote the exact file, line number, and full line for each location, and include the surrounding lines you relied on.' ;;
    find-const:evidence)
      echo 'In the workspace under fixture/, find where the service port constant is defined and where it is used. Answer with only the file, line number, and value for each location, one per line. Do not paste file contents.' ;;
    find-error:full)
      echo 'The log at fixture/logs/service.log has 4000 lines. Find the one failed request. Quote the failing line and enough surrounding lines to understand it, exactly as they appear in the file.' ;;
    find-error:evidence)
      echo 'The log at fixture/logs/service.log has 4000 lines. Find the one failed request. Answer with only the line number, the error text, and the host and port that refused the connection. Do not paste log contents.' ;;
    narrow-read:full)
      echo 'In fixture/config/limits.conf, find the line that sets the threshold. Quote that line and the ten lines around it exactly as they appear.' ;;
    narrow-read:evidence)
      echo 'In fixture/config/limits.conf, find the line that sets the threshold. Answer with only the line number and the value.' ;;
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
  # Go fixtures are stored as *.go.txt so this repository's "go test ./..." does
  # not try to build them; the fixture the model sees is the real .go tree.
  for f in "$work"/fixture/*/*.go.txt; do
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
if [ "$fixture" = all ]; then fixtures=(find-const find-error narrow-read); else fixtures=("$fixture"); fi
if [ "$arm" = all ]; then arms=(full evidence); else arms=("$arm"); fi

for fx in "${fixtures[@]}"; do
  for a in "${arms[@]}"; do
    echo "== $fx / $a / $label" >&2
    run_one "$fx" "$a"
  done
done
