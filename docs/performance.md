# Performance baseline

Neo tracks a small local baseline so contributors can notice meaningful growth
without turning variable timings into flaky CI gates.

Run the release-shaped build and stable microbenchmarks from the repository
root:

```bash
just performance
```

The command reports:

- stripped, `CGO_ENABLED=0` binary size for the current OS and architecture;
- base system prompt construction time, allocations, and prompt bytes;
- representative workflow rendering time and allocations;
- built-in tool catalogue construction time and allocations.

It runs each benchmark five times by default. Use `BENCH_COUNT=10 just
performance` when investigating a suspected regression. Startup, filesystem,
terminal, and provider network timings are omitted because they vary too much
between hosts to be useful gates.

Three deterministic size measurements have deliberately generous regression
budgets:

- the stripped binary must remain at or below 28 MiB. It was 17.13 MiB before
  the embedded JavaScript engine landed and 22.96 MiB after, of which the
  engine is 5.83 MiB; the remaining room is ordinary growth;
- the system prompt must remain at or below 1,400 bytes for the core prompt and
  3,000 bytes for the chat prompt, which adds the workflow, delegation, goal,
  and script sections that only exist where those tools do;
- the fixed floor — the tool schemas plus the chat system prompt with the
  built-in skill catalog — must remain at or below 8,500 bytes. It is 8,084
  (5,022 for nine tool definitions, 3,062 for the prompt);
- the same floor with the opt-in script tool enabled must remain at or below
  10,000 bytes. It is 9,796 (5,971 and 3,825).

Re-measure the binary before adding a dependency: the ceiling is generous on
purpose, and a second engine-sized cost would use it up.

The binary budget is checked by `just performance`. The prompt and fixed-floor
budgets are normal Go tests, so CI checks them without running timing
benchmarks. `TestFixedPromptFloorBudget` in `cmd/neo/performance_test.go` also
asserts that two renders of the same session state produce a byte-identical
cacheable prefix, which is what keeps prompt caching worth anything.

## Token Baseline

Bytes are not a bill. The tables in this section compare what Neo *sends*, so a
change can be judged before and after; only a provider's `llm.Usage` says what a
request actually cost, because tokenizers differ and cache hits are priced
separately.

All byte-to-token figures here use the same estimate as
`internal/compact.EstimateTokens`: characters divided by four. It is a planning
number, not a contract.

### Fixed overhead

Measured on 2026-11-24 with the chat tool registry and the built-in skill
catalog, which is what an interactive session sends before any conversation
exists:

| Component | Bytes | ~Tokens |
| --- | ---: | ---: |
| 9 tool definitions (`chatRegistry().Specs()`, JSON) | 5,022 | 1,255 |
| System prompt with the built-in skill catalog | 3,062 | 765 |
| **Fixed floor** | **8,084** | **~2,020** |

These are the default surface, which has no script tool. Turning the tool on
adds 949 bytes of schema and 763 bytes of prompt, for a floor of 9,796; that is
`scriptFloorByteBudget` (10,000), checked separately by `TestScriptFloorBudget`.
The prompt section grew when it was rewritten around task shapes with a worked
example, which is what an opt-in tool has to do to be chosen at all. It grew
again by 56 bytes on 2026-11-25 when the judgement moved from file count to
dependency (`TestScriptCapabilityNamesDependencyNotVolume`); that is the same
section restated, not a new feature.

The scripted arm's schema moved 851 → 949 on 2026-11-25 when bash's description
gained one sentence pointing at the script tool (#87), and its prompt moved
682 → 763 the same day when the batching rule was qualified against a script
being one serial call (#89). Both appear only in that arm: a session without the
tool cannot act on either, so it pays for neither.

The schema grew by 147 bytes on 2026-11-24 when the description was corrected:
it had claimed a script could not write, long after write_file and edit_file
were reachable, and a model that reads a narrower capability than it has stops
choosing the tool. `TestScriptSpecMatchesGlobals` now fails if the text and the
whitelist drift apart again.

These figures are the basis of `fixedFloorByteBudget` (8,500) and the 1,400 /
3,000 prompt budgets in `cmd/neo/performance_test.go`; if one moves
intentionally, move both or the docs and the gate disagree.

### The script tool's measured cost and benefit

Measured 2026-11-24 on `openrouter` / `particle-ai/particle-1`, three fixed
tasks, one run each, against a build with and without the tool registered:

| Task | requests | input tokens |
| --- | ---: | ---: |
| count TODOs across files | same | **+8.7%** |
| conditional single-line edit | same | **+6.7%** |
| locate one line in a 2,001-line file | same | **+10.6%** |

Request counts did not fall. Input rose by the tool's fixed cost — then 704 bytes
of schema and 439 bytes of prompt, re-sent on every request — and nothing offset
it: in all three free-form runs the model chose `bash` and `grep` and never
called the script tool. A run with an explicit instruction to use it did, but
spent three failed calls discovering the calling convention.

So the numbers here say what the tool costs, not what it is worth. Until a
measurement shows a task where it earns the fixed cost back, treat it as a
capability that must be asked for rather than one that pays for itself.

### Re-measured after the description and prompt were corrected

The same three tasks, three runs each, re-measured 2026-11-24 after the schema
description was corrected and the prompt rewritten around task shapes with a
worked example. `NEO_LOG` was on, so a `script` call is counted from the log
rather than inferred from the final answer. Medians:

| Task | requests (off → on) | input tokens (off → on) | runs that called `script` |
| --- | --- | --- | ---: |
| count TODOs across files | 3 → 3 | 6,344 → 7,213 (**+13.7%**) | **3 / 3** |
| conditional single-line edit | 4 → 4 | 9,362 → 9,068 (−3.1%) | 0 / 3 |
| locate one line in a 2,001-line file | 3 → 3 | 6,319 → 7,219 (**+14.2%**) | 0 / 3 |

The first row is the change the correction was for: the model now chooses the
tool every time on a counting task, and its scripts do the counting
(`grep`, then `split`/`filter`, returning a count) instead of reading the list
back and judging it. The other two rows did not move, which is the intended
shape — a single conditional edit and a single line lookup are the cases the
prompt tells it not to script.

Request counts still do not fall. Counting across three files already took
three requests without the tool, so there was no round trip for a script to
save; the +13.7% is the fixed cost of having the tool registered, now visible
in a run that uses it. The honest summary is narrow: the tool is chosen when a
task matches the shapes the prompt names, and it still costs prompt bytes on
every request whether or not it is chosen.

### Why a script competes with a narrowed command, not a whole file

Measured 2026-11-25 (#83) on one task built to favour the tool: read a
2,001-line file, find the line holding a unique marker, change only the word
after the marker, leave every other line byte-identical. Nine runs on
`commandcode` / `deepseek/deepseek-v4.1-flash`, one binary, only
`features.script` flipped.

| | off (n=3) | on (n=6) |
| --- | ---: | ---: |
| requests (median) | 5 | 6 |
| input tokens (median) | 11,331 | 16,769 |
| runs that called `script` | 0 / 3 | 2 / 6 |
| top-level `read_file` calls | 0 | 0 |

The number that matters is the last one. `read_file` was never called, in any
run of either arm: the model narrowed the window with `sed -n '1499,1503p'` and
`grep -n`, so the cost `docs/design/js-batch-tool.md` §1 attributes to reading a
large file never arrived. The script is therefore not competing with "pull 2,001
lines into the conversation"; it is competing with one already-narrow `bash`
command, and it loses on bytes because its own fixed cost is re-sent every turn.

Where it was chosen, the scripts were not what the design predicts. One run
wrote four of them, reached 8 requests and 26,813 input tokens, and its first
script only replaced a `grep` before the edit happened at the top level — read,
branch, and write were not merged into one call. So the dependency shape raises
the selection rate but the model uses the tool as a faster `grep`, not as a way
to save a round trip.

Two consequences recorded here rather than discovered later:

- The noise floor on this model is wide. The `on` arm spans 5–8 requests and
  13,768–26,813 input tokens across six runs, which is larger than any wording
  change could produce. A re-measurement at n=3 cannot decide keep-or-narrow on
  request or token counts; it needs either many more runs or a non-random
  signal (did the chosen script merge read, branch, and write?).
- A task a `sed` one-liner cannot solve is still unmeasured. This fixture was
  small enough that no script was needed.

The byte figures in #80 treated 1,455 bytes as roughly 2,000 tokens. At four
characters per token that is about 364. The qualitative conclusion there stands;
the conversion should not be reused.

### Measuring a real run

A run's actual cost comes from the provider, not from an estimate:

```bash
go build ./...
neo run --json 'Read the file `cmd/neo/main.go` and list its functions.'
```

The summary on stdout carries `requests` and `usage`. The numbers below are
from one example run and are shown for their shape, not as a baseline:

```json
{
  "requests": 2,
  "usage": {
    "input_tokens": 4200,
    "output_tokens": 310,
    "cache_creation_tokens": 8300,
    "cache_read_tokens": 0
  }
}
```

Read it as four disjoint prompt parts plus output: `input_tokens`,
`cache_creation_tokens`, and `cache_read_tokens` sum to the whole prompt (see
`Usage.PromptTokens()`), and the cache hit rate is `cache_read_tokens` over that
sum. `requests` counts every provider call the run made, including a compaction
summary, because that call is billed like any other.

To compare two revisions, run the same prompt against the same provider and
model on both, twice each, and record the four usage fields plus `requests`.
Report the second run of each: the first pays cache creation, the second
reflects what a session costs once its prefix is cached.

Keep the run figures in the change that produced them rather than in this file:
they depend on the provider, the model, and the prompt. A measurement taken
against a different one of those is not comparable, so record all three
alongside the numbers.

The fixed-overhead table above is different: it comes from the code alone, so it
is updated here when the prompt or the tool set intentionally changes.

### Per-request detail

`neo run` reports totals only. Per-request detail is `ag.Usage()` diffed around
a single turn, which is what a `/usage` reading gives interactively: `/usage`
shows the same four figures for the session, so the difference between two
readings is what one turn cost.

## Baseline

The baseline below was captured on Go 1.25.12 on Darwin/arm64. Compare like with
like: the same Go version, OS, architecture, and an otherwise idle machine.
To reproduce it when another Go version is active, run
`GOTOOLCHAIN=go1.25.12 just performance`.

| Measurement | Baseline |
| --- | ---: |
| Stripped binary | 16,273,714 bytes |
| Base prompt size | 2,013 bytes |
| Base prompt | ~2,480 ns/op |
| Base prompt allocation | 6,973 B/op, 15 allocs/op |
| Workflow render | ~249,000 ns/op |
| Workflow render allocation | 24,277 B/op, 664 allocs/op |
| Tool catalogue | ~3,980 ns/op |
| Tool catalogue allocation | 10,032 B/op, 70 allocs/op |

Timing and allocation numbers are informational. There are no timing thresholds
in CI. The stable size budgets above are the only regression gates.

## Updating the baseline

Update this table only for an intentional change or a repeatedly reproduced
regression. Run `just performance` at least twice with the Go version declared
by `go.mod`, use the median result from the reported samples, and include the
before/after output in the pull request. If an intentional change exceeds a
size budget, update the corresponding constant in `scripts/performance.sh` or
`cmd/neo/performance_test.go` and explain why in the pull request. Do not update
a baseline or budget to hide a one-off noisy result.
