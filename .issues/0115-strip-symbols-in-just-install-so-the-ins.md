---
id: 115
title: Strip symbols in just install so the installed binary is the release shape
status: closed
priority: low
labels:
    - chore
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

just install ran go install with only -X main.Version, so the binary it put on PATH was 34,582,663 B (32.98 MiB) on linux/amd64 even after just build and the release artefacts were stripped by #114. That is the binary a contributor actually runs.

Pass -s -w, taking it to 25,014,535 B (23.86 MiB), identical to just build. The discovery, GOBIN resolution, and PATH warning are unchanged.

Acceptance: just install produces a binary with no symtab or debug_info section, a working version stamp, and a size equal to just build. qlty check passes.

Measured with GOBIN pointed at a temporary directory: 32.98 MiB before, 23.86 MiB after.
