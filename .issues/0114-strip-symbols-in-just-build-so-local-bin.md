---
id: 114
title: Strip symbols in just build so local binaries match the release shape
status: closed
priority: low
labels:
    - chore
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

just build produced a 34.5 MB linux/amd64 binary because it passed only -X main.Version, while .goreleaser.yaml has always built with -s -w -trimpath. The difference is .debug_info plus the symbol table, about 9.6 MB, so a local build was not the shape users download.

Add -s -w to build, add a build-debug recipe that keeps DWARF for panic traces and Delve, extend clean to both binaries, and ignore neo.debug. Update ARCHITECTURE.md and the website install page.

Acceptance: just build yields a stripped binary with no debug_info or symtab section and a working version stamp; just build-debug yields an unstripped neo.debug; just clean removes both; qlty check passes.

Measured on linux/amd64: 34,582,663 B (32.98 MiB) before, 25,014,535 B (23.86 MiB) after.
