#!/usr/bin/env bash
# Build the outline probe beside the fixture (#133).
#
# The probe is a separate module so it never becomes a Neo dependency. The
# workspace exposes fixture/outline as a wrapper that records each invocation,
# because a run() sub-call is logged without its argv.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
(cd "$here/outline" && go build -trimpath -o "$here/fixture/.outline-bin" .)
chmod +x "$here/fixture/outline"
echo "built $here/fixture/.outline-bin"
