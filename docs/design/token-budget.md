# Neo Token Budget

> **Status:** Implemented. The rules below describe shipped behavior; the task
> table records what each piece changed and which issue it came from.

Neo re-sends the system prompt, every tool definition, and the whole transcript
on every turn. Input cost therefore grows with the square of the conversation.
This note records the four rules that keep that cost down, what Neo spends
today, and the work that followed from it.

Adapted from the killi project (`../killi/docs/decisions.md`, decisions 9, 10,
26, 28, 36, 43, 48), which measures 36.6k input tokens per task at 84% cached.

## Rules

In priority order. When they conflict, the earlier one wins.

1. **The cached prefix never breaks.** A stable prefix beats a smaller prompt.
   The tool list, the base system block, and already-sent history do not change
   mid-session. Anything that must be said goes at the end of the conversation.
2. **Measure before cutting.** A reduction is justified by a recorded
   before/after, not by intuition. Cutting tool output to save tokens is wrong
   if it costs an extra round trip.
3. **The budget is explicit and tested.** The prompt plus always-present tool
   definitions stays under a checked ceiling, so growth fails review.
4. **Product policy stays out of the agent loop.** Accounting and reporting live
   at the CLI boundary. `internal/agent` stays policy-free, per `MISSION.md`.

## What Neo Spends Today

Measured on this checkout (2026-11-24), character counts divided by 4 — the same
estimate `compact.EstimateTokens` uses.

| Component | Bytes | ~Tokens |
| --- | ---: | ---: |
| 9 tool definitions (chat registry) | 5,022 | 1,255 |
| System prompt with built-in skill catalog | 3,062 | 765 |
| **Fixed floor** | **8,084** | **~2,020** |

The opt-in script tool adds 1,533 bytes when enabled (851 schema, 682 prompt).

Largest definitions: `agent` 808 B, `goal` 775 B, `grep` 645 B, `workflow`
536 B. A project `AGENTS.md` adds an uncached tail on top.

What gates these numbers:

- `cmd/neo/performance_test.go` enforces 1,400 bytes for the core prompt, 3,000
  for the chat prompt, 8,500 for the fixed floor (tool schemas plus chat prompt,
  measured here as 8,084), and 10,000 for that floor with the script tool on. The same test renders the cacheable prefix
  twice and fails if the bytes differ.
- `docs/performance.md` records the same three budgets and the token baseline,
  so the doc and the gate cannot drift apart quietly.

## Tasks

All landed:

| Issue | Change | Why it mattered |
| --- | --- | --- |
| #36 | `/usage` reports the session's tokens and cache hit rate | Minimum observable: nobody could tell whether caching worked |
| #37 | `neo run --json` reports `requests` and `usage` | The same figures for scripted runs, including compaction calls |
| #38 | One test gates the fixed floor and the frozen prefix | Turns rules 1 and 3 into a failing test instead of a convention |
| #41 | An inline image counts as a fixed `ImageTokenReserve` | One screenshot estimated as ~100k tokens and started a compaction loop |
| #42 | Trigger is 70% of the window, with a 20% growth floor after each compaction | A fixed 140k under-used a 1M window and could re-fire immediately |
| #43 | Repeat guard: reminders at 3, 5, 8; the 12th call is refused | `MaxTurns` is 500; a looping model burned every remaining turn |
| #44 | Truncation names the next call | "Narrow with offset/limit" did not say where or by how much |
| #45 | Token baseline and measurement protocol in `performance.md` | Every later change can cite one basis for comparison |
| #47 | The stale single 2,048-byte budget is gone | The doc now describes all three budgets |

## Deferred On Purpose

Reviewed out because the evidence is missing or the machinery has no caller.
Pick one up when a measurement names it.

| Dropped | Reason |
| --- | --- |
| `--json-stream` per-request records | `--json` totals answer "was this expensive"; per-turn detail is a `Usage()` diff when needed |
| `tools.max_output_bytes` per-tool limits | Defaults do not change, so nobody would set it |
| `compaction.trigger_ratio` / `growth_floor` config | `context_window_tokens` already covers it; behavior changes should not arrive as knobs |
| Usage segment in the TUI footer | Decoration; `/usage` covers the need on demand |
| `cache_control` pass-through | Only matters behind Bedrock-style gateways; no such case here yet |
| `prompt_cache_key` | OpenAI prefix caching does not need it; it is a routing hint with unmeasured value |
| `agent.EventUsage` | Existed only to feed `--json-stream` |

## Measurement Protocol

Every change records its evidence in the issue; user-visible changes also touch
`CHANGELOG.md`.

```bash
go build ./...
go test ./... -cover
just performance
```

For provider-facing changes, add a stub-backed probe that prints
`{input, cache_creation, cache_read, output}` per request. Never call real
providers in tests.

## Non-Goals

- No automatic model downgrade or provider-side routing.
- No mid-session tool list changes to "save" tokens — that breaks rule 1.
- No semantic or vector compaction, no embedding store (`MISSION.md` rules out a
  database or daemon).
- No aggregate telemetry. Accounting is local and per session.
- No token budget that stops a `/goal` run; the attempt budget already bounds it.
