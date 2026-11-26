---
id: 117
title: Fix the data race on Config.discovered between background catalogue discovery and subagent construction
status: closed
priority: high
labels:
    - bug
    - config
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

`internal/config` has no synchronisation at all (`grep -n 'sync\.|Mutex|RWMutex' internal/config/*.go` returns nothing), yet `Config.discovered` is written from a background goroutine and read from the subagent path on the main goroutine.

## Evidence

Write (background goroutine, started at chat startup and at every `/model` switch):
- `cmd/neo/provider.go:551` — `go func()` inside `requestContextWindowDiscovery`
- `cmd/neo/provider.go:564` — `cfg.SetDiscoveredContextWindow(name, m.ID, m.ContextWindowTokens)`
- `internal/config/config.go:184-190` — builds and mutates `c.discovered` with no lock

Read (main goroutine):
- `internal/config/config.go:196-205` — `DiscoveredContextWindow` reads `c.discovered` with no lock
- `internal/config/config.go:171` + `:240` — `ContextWindowFor` -> `ContextWindow` -> `DiscoveredContextWindow`
- `cmd/neo/subagent.go:58-60` — `WindowFor` closure calls `cfg.ContextWindowFor(provider, workerModel)`; invoked from `internal/subagent/runner.go:131-132` every time a child agent is built

## Reproduction

A probe test in the package (`internal/config`, removed after the run) with one goroutine calling `SetDiscoveredContextWindow` and another calling `ContextWindowFor`:

```
$ go test -race -run TestDiscoveredRaceProbe ./internal/config/
WARNING: DATA RACE
Write at 0x00c00023e0d8 by goroutine 10:
  config.(*Config).SetDiscoveredContextWindow()  config.go:186
Previous read at 0x00c00023e0d8 by goroutine 9:
  config.(*Config).DiscoveredContextWindow()    config.go:198
  config.(*Config).ContextWindow()              config.go:240
  config.(*Config).ContextWindowFor()           config.go:171
```

## Impact

Go's runtime aborts the whole process with `fatal error: concurrent map read and map write` when a map is read and written at the same time — this is not a recoverable panic and not merely a `-race` warning. The window is real: discovery is asynchronous by design, so a user who starts a subagent (`agent` tool) while the background catalogue fetch is in flight hits exactly this pair. It only affects custom providers (`cfg.IsCustomProvider`), which narrows the blast radius but not the crash.

## Acceptance criteria

- `internal/config` synchronises `discovered` (a `sync.RWMutex` around the map, or an atomic snapshot) so `SetDiscoveredContextWindow` and `DiscoveredContextWindow` are safe to call concurrently.
- A `-race` test in `internal/config` runs a writer and a reader concurrently and passes; today the equivalent probe fails.
- `go test -race ./...` and `go vet ./...` pass.
- No new lock is taken on the hot path in a way that can deadlock with `Compact` (which already takes `Summarizer.mu`).

## Notes

Found by a full-repository review at `899b7b5`. `-race` is already part of CI (`.github/workflows/ci.yml` runs `go test -race ./...`), so a test added here would guard the fix.
