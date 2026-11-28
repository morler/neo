#!/usr/bin/env bash
# Reduce one recorded run (#133) to the figures the issue asks for.
#
# Inputs: a run base path "<base>" with .json, .exit and .log alongside it, and
# its work directory, which holds outline-invocations.log when the arm used the
# probe. Output: one JSON object on stdout. No network, no model: check.sh
# verifies this against a synthetic sample.
set -euo pipefail

base=${1:?usage: reduce.sh <base>}
label=${2:-}
fixture=${3:-}
arm=${4:-}

run_json='{}'
if [ -f "$base.json" ]; then
  run_json="$(jq -c . "$base.json" 2>/dev/null || echo '{}')"
fi
exit_code=0
if [ -f "$base.exit" ]; then exit_code="$(cat "$base.exit")"; fi

log_json='{"tool_result_bytes":0,"tool_result_count":0,"max_tool_result_bytes":0,"payload_bytes":0,"requests_logged":0,"top_reads":0,"sub_reads":0,"sub_writes":0,"sub_runs":0,"script_calls":0,"runs":0}'
if [ -f "$base.log" ] && [ -s "$base.log" ]; then
  log_json="$(jq -s '
    {
      tool_result_bytes: ([ .[] | select(.msg=="tool result") | (.output.bytes // 0) ] | add // 0),
      tool_result_count: ([ .[] | select(.msg=="tool result") ] | length),
      max_tool_result_bytes: ([ .[] | select(.msg=="tool result") | (.output.bytes // 0) ] | max // 0),
      payload_bytes: ([ .[] | select(.msg=="provider request") | (.payload.bytes // 0) ] | add // 0),
      requests_logged: ([ .[] | select(.msg=="provider request") ] | length),
      top_reads: ([ .[] | select(.msg=="tool call") | select(.name=="read_file" or .name=="grep" or .name=="glob") ] | length),
      sub_reads: ([ .[] | select(.msg=="script sub-call") | select(.done==true) | select(.name=="read_file" or .name=="grep" or .name=="glob") ] | length),
      sub_writes: ([ .[] | select(.msg=="script sub-call") | select(.done==true) | select(.name=="write_file" or .name=="edit_file") ] | length),
      sub_runs: ([ .[] | select(.msg=="script sub-call") | select(.done==true) | select(.name=="run") ] | length),
      script_calls: ([ .[] | select(.msg=="tool call") | select(.name=="script") ] | length),
      runs: ([ .[] | select(.msg=="script sub-call") | select(.done==true) | select(.name=="run") ] | length)
    }' "$base.log" 2>/dev/null || echo '{}')"
fi

# A run() sub-call is logged without its argv, so the probe records its own
# invocations. Count them from the work directory the run used.
outline_calls=0
# run.sh names the work directory with dashes (work-<fixture>-<arm>-<label>)
# while the run base uses dots (<fixture>.<arm>.<label>), so translate the stem.
stem="$(basename "$base")"
stem="${stem//./-}"
record="$(dirname "$base")/../work-$stem/outline-invocations.log"
if [ -f "$record" ]; then
  outline_calls="$(wc -l <"$record" | tr -d '[:space:]')"
fi

jq -n \
  --argjson run "$run_json" \
  --argjson log "$log_json" \
  --arg label "$label" --arg fixture "$fixture" --arg arm "$arm" \
  --argjson exit "$exit_code" --argjson outline_calls "$outline_calls" \
  '$run as $r | {
    label: $label, fixture: $fixture, arm: $arm, exit: $exit,
    ok: ($r.ok // false), error: ($r.error // ""),
    requests: ($r.requests // 0),
    input_tokens: ($r.usage.input_tokens // 0),
    output_tokens: ($r.usage.output_tokens // 0),
    cache_creation_tokens: ($r.usage.cache_creation_tokens // 0),
    cache_read_tokens: ($r.usage.cache_read_tokens // 0),
    prompt_tokens: (($r.usage.input_tokens // 0) + ($r.usage.cache_creation_tokens // 0) + ($r.usage.cache_read_tokens // 0)),
    tool_calls: ($r.tool_calls // 0), tool_errors: ($r.tool_errors // 0),
    final: ($r.final // ""), final_bytes: (($r.final // "") | length)
  } + $log + {outline_calls: $outline_calls}'
