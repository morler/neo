#!/usr/bin/env bash
# Offline verification of reduce.sh (#132). Feeds a synthetic run through the
# reducer and asserts every field. No network and no model, so the instrument is
# checked independently of what a model does.
set -euo pipefail

ws="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/s.json" <<'EOF'
{"ok":true,"requests":3,"usage":{"input_tokens":100,"output_tokens":40,"cache_creation_tokens":20,"cache_read_tokens":7},"tool_calls":2,"tool_errors":0,"final":"answer text"}
EOF
echo 0 >"$tmp/s.exit"
cat >"$tmp/s.log" <<'EOF'
{"level":"DEBUG","msg":"provider request","payload":{"bytes":900}}
{"level":"DEBUG","msg":"provider request","payload":{"bytes":1100}}
{"level":"DEBUG","msg":"tool call","name":"script","args":{"source":"x"}}
{"level":"DEBUG","msg":"script sub-call","name":"grep","done":false,"error":false}
{"level":"DEBUG","msg":"script sub-call","name":"grep","done":true,"error":false}
{"level":"DEBUG","msg":"tool result","name":"script","output":{"bytes":50}}
{"level":"DEBUG","msg":"tool call","name":"read_file","args":{"path":"a"}}
{"level":"DEBUG","msg":"tool result","name":"read_file","output":{"bytes":150}}
{"level":"DEBUG","msg":"script sub-call","name":"run","done":true,"error":false,"args":{"argv":["./fixture/outline","fixture/processor.go"]}}
{"level":"DEBUG","msg":"script sub-call","name":"run","done":true,"error":false,"args":{"argv":["go","test","./..."]}}
EOF

got="$("$ws/reduce.sh" "$tmp/s" r1 find-const full)"

check() {
  local expr="$1" want="$2"
  local have; have="$(jq -r "$expr" <<<"$got")"
  if [ "$have" != "$want" ]; then
    echo "FAIL $expr: got '$have' want '$want'" >&2
    echo "$got" | jq . >&2
    exit 1
  fi
}
check '.label' 'r1'
check '.fixture' 'find-const'
check '.arm' 'full'
check '.ok' 'true'
check '.requests' '3'
check '.input_tokens' '100'
check '.output_tokens' '40'
check '.cache_creation_tokens' '20'
check '.cache_read_tokens' '7'
check '.prompt_tokens' '127'
check '.final_bytes' '11'
check '.tool_result_bytes' '200'
check '.tool_result_count' '2'
check '.max_tool_result_bytes' '150'
check '.payload_bytes' '2000'
check '.requests_logged' '2'
check '.top_reads' '1'
check '.sub_reads' '1'
check '.script_calls' '1'
check '.runs' '2'
check '.outline_calls' '0'
check '.exit' '0'

# A missing log and a missing json must reduce to zeros rather than fail, so a
# crashed run is recorded instead of aborting the batch.
rm -f "$tmp/s.json" "$tmp/s.log"
got="$("$ws/reduce.sh" "$tmp/s" r2 find-const evidence)"
check '.ok' 'false'
check '.requests' '0'
check '.tool_result_bytes' '0'

echo "reduce.sh: all checks passed"
