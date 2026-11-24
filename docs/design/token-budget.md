# Neo Token Budget

> **Status:** Proposed. No behavior below ships yet; the table maps each piece
> to its issue.

Neo re-sends the system prompt, every tool definition, and the whole transcript
on every turn. Input cost therefore grows with the square of the conversation.
This note records the four rules that keep that cost down, what Neo spends
today, and the seven tasks that follow from it.

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

Largest definitions: `agent` 808 B, `goal` 775 B, `grep` 645 B, `workflow`
536 B. A project `AGENTS.md` adds an uncached tail on top.

Two facts worth noting:

- `cmd/neo/performance_test.go` enforces 1,400 bytes for the core prompt and
  2,600 for the chat prompt, but **nothing gates the tool schemas**, which are
  the larger half.
- `docs/performance.md` still states a single 2,048-byte budget, which predates
  the split into two.

## Tasks

| Issue | Task | Why it matters |
| --- | --- | --- |
| #47 | Correct the stale 2,048-byte budget in `performance.md` | One-line doc fix, do it now |
| #41 | Count an inline image as a fixed reserve | `maxImageBytes` is 5 MB; base64 at 4 chars/token estimates ~1.7M tokens, so one screenshot blows the 140k trigger |
| #36 | Show session usage with `/usage` | Minimum observable: nobody can currently tell whether caching works |
| #38 | Gate the fixed floor and the frozen prefix in one test | Turns rules 1 and 3 into a failing test instead of a convention |
| #42 | Trigger compaction by a share of the window | Constant 140k under-uses a 1M window and can re-fire right after a compaction |
| #43 | Repeated-call breaker: warn at 8, stop at 12 | `MaxTurns` is 500; a looping model burns turns until then |
| #44 | Truncation suggests the next call | `read_file` says "narrow with offset/limit" without saying how |

Dependency order: #47 alone; #41 → #42 → #43; #36 → #38 → #44; #43 and #44
feed #46 (docs, after behavior lands) and #45 (baseline, after measurement).

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
