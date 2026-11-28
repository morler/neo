#!/usr/bin/env bash
# Regenerate fixture/logs/service.log. The log is generated rather than stored so
# the repository keeps a 20-line script instead of a 259 KB file, and so the one
# failed request is reproducible by construction.
#
# The Go fixtures are stored with a .txt suffix (`config.go.txt`) so the Go
# toolchain in this repository does not try to build them; run.sh renames them
# back when it stages a work directory.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$here/fixture/logs"
python3 - "$here/fixture/logs/service.log" <<'PY'
import sys

services = ["auth", "billing", "sync"]
out = []
for i in range(1, 4001):
    svc = services[i % 3]
    n = f"{i:06d}"
    if i == 2871:
        out.append(f"{n} ERROR {svc}: handler failed: dial tcp 10.4.2.9:6379: connect: connection refused")
    elif i == 2872:
        out.append(f"{n} WARN  {svc}: retrying after handler failure")
    else:
        out.append(f"{n} INFO  {svc}: request completed status=200 duration_ms={(i * 7) % 400}")
open(sys.argv[1], "w").write("\n".join(out) + "\n")
PY
wc -lc "$here/fixture/logs/service.log"
