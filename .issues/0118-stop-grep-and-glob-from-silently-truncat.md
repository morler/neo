---
id: 118
title: Stop grep and glob from silently truncating when one result line exceeds 64 KiB
status: closed
priority: high
labels:
    - bug
    - tools
created: "2026-11-26"
updated: "2026-11-27"
closed: "2026-11-27"
---

`readLines` in `internal/tools/search.go` never checks `scanner.Err()`. When ripgrep emits a single line longer than the scanner's buffer cap, `bufio.Scanner` stops permanently and every line after it is dropped — and the tool reports success.

## Evidence

- `internal/tools/search.go:218-231` — `readLines` has no `scanner.Err()` check; it returns `lines, total` as if the stream ended cleanly.
- `internal/tools/search.go:220` — `scanner.Buffer(make([]byte, 0, 64*1024), MaxOutputBytes)`, and `MaxOutputBytes = 64 * 1024` (`internal/tools/tool.go:15`).
- `internal/tools/search.go:166-213` — `runSearch` consumes that result, and only inspects `cmd.Wait()`; a scanner error is invisible to it.

The two other `bufio.Scanner` call sites in the repository do check: `internal/llm/anthropic/stream.go:71` and `internal/llm/openai/codex.go:161`. This one is the outlier.

## Reproduction

A standalone copy of `readLines` (same code, same buffer settings) fed one 70 KiB line between two short lines:

```
lines=1 total=1
  [0] len=17 "short line before"
```

Expected 3 lines. The 70 KiB line and everything after it vanished, `total` stayed at 1, and no error was returned.

## Impact

Minified JavaScript, bundled JSON, lock files, and single-line generated sources all exceed 64 KiB per line. `grep` over a repository containing one of them silently returns a *partial* result set — and when the over-long line is the first match, returns `no matches` for a pattern that genuinely matches. The model then concludes the symbol does not exist and acts on a false negative. The failure is silent in both the tool result and `NEO_LOG`, so neither the user nor the model can notice it.

`glob` is unaffected by long lines in practice (paths are short), but it shares `readLines`, so the fix covers both.

## Acceptance criteria

- `readLines` returns the scanner error, and `runSearch` surfaces it as a tool error naming the limit, e.g. `grep: a result line exceeded the 64 KiB limit; narrow the pattern`.
- A test covers a match line longer than `MaxOutputBytes` and asserts a non-nil error rather than a truncated success.
- Behavior for ordinary results is unchanged: `go test ./internal/tools/` passes, including the existing `search_empty_test.go` and truncation tests.

## Notes

Found by a full-repository review at `899b7b5`. Reproduced with the copy above, not by calling ripgrep against the repository, so the reproduction does not depend on any particular file.
