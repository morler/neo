# #133 · Go source outlines before adding an index capability

Measured 2026-11-28. The question: does a Go source skeleton plus a narrow read
beat grep plus a narrow read, once the extra round trip and the extra prompt
sentence are charged to the skeleton?

## What is compared

Two arms, two fixtures, one binary, one provider, one model, one effort. The
task and the required answer are identical inside a fixture; the only difference
between arms is whether the prompt says the outline probe exists.

| | grep | outline |
| --- | --- | --- |
| Prompt | states the task | states the task **plus** 260 bytes naming `./fixture/outline`, its output shape, and how to turn a range into `read_file(offset, limit)` |
| Everything else | identical fixture, provider, model, effort, tool surface, binary | identical |

Naming the probe costs bytes on **every** request of the arm, exactly as a
shipped tool's schema and description would. That is deliberate: #133 asks for
the skeleton's cost to be counted, not to be waved away.

## The probe

`outline/` is a separate Go module using only `go/parser` and `go/token`. It
prints one line per declaration:

    path:start-end<TAB>kind<TAB>name<TAB>signature

It prints no function bodies. On the fixture's `processor.go` that is **2,803
bytes for 28 declarations** covering 670 lines — the whole-file read it replaces
is 28,345 bytes in one tool result.

It is deliberately not registered as a Neo tool. Answering "is a skeleton worth
it" does not justify shipping one, and the experiment needs no product change to
run.

## Ranges are usable (the first acceptance criterion)

`outline/range_test.go` is the reason to trust the numbers above. For every
declaration it slices `start-end` back out of the file, re-parses the slice, and
requires the same declaration to come out. It runs over the repository's own Go
sources, so a multi-line signature and a method are both exercised:

| check | result |
| --- | --- |
| declarations probed | **1,327** across `internal/` and `cmd/` |
| ranges that re-parse to the same declaration | 1,327 / 1,327 |
| multi-line declarations seen | non-zero (asserted; the test fails if none) |
| a file that does not parse | returns an error, never a partial index |

The check is structural, not textual: it parses the slice instead of comparing
bytes, because the question is whether the range is a complete declaration the
model can narrow into.

## Fixtures

| fixture | shape | required answer |
| --- | --- | --- |
| `unknown` | 9 files, 908 lines; the attempt budget and the retry-delay growth are not named in the prompt | file + line + exact line for both, and the names `Attempts`, `backoffFor` |
| `known` | `processor.go`, 670 lines, 28 declarations, one method with a six-line signature | `reconcile`'s range (643–660) and its full signature |

`known` is the shape where an outline has the best possible starting point: the
target is named, so one skeleton call can locate it. It is not the shape the
issue expects the gain on, which is `unknown`.

## Running it

```bash
go build -o docs/measurements/133-go-source-outlines/neo ./cmd/neo
cd docs/measurements/133-go-source-outlines
./build-outline.sh              # builds fixture/.outline-bin, installs the wrapper
./check.sh && ./grade_check.sh  # offline: reducer and grader, no model, no network
./run.sh all all r1             # 2 fixtures x 2 arms
```

`fixture/outline` is a wrapper that appends each invocation to
`outline-invocations.log` before exec'ing `.outline-bin`. A `run()` sub-call is
logged without its `argv`, so without the wrapper the harness could not tell an
outline call from a `go build`; counting them from the answer would be inference,
not measurement.

## Environment

`provider: commandcode`, `model: deepseek/deepseek-v4.1-flash`,
`reasoning_effort: low`, code mode with `features.script: true`, binary
`nightly-20261127-da511a0-2-gcd84837`. Five runs per arm per fixture (20 runs).

**The backend reports no cache figures**: every run has
`cache_creation_tokens = 0` and `cache_read_tokens = 0`. The prompt column
below is the whole prompt, not the billed part.

## Results

Medians over five runs, ranges in parentheses. Tool bytes are what the model
received from every tool call; input is the whole prompt the provider reported.

| fixture | arm | success | requests | tool bytes | input | output | outline calls |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| known | grep | 5/5 | 4 (3–6) | 5,340 (1,187–27,025) | 14,302 (6,631–19,804) | 664 | 0 |
| known | outline | 5/5 | 4 (3–7) | **3,543** (2,231–5,518) | **12,474** (8,297–19,474) | 418 | 1,1,1,1,1 |
| unknown | grep | 5/5 | **5** (4–6) | 30,957 (5,002–64,988) | **20,217** (12,710–57,312) | 844 | 0 |
| unknown | outline | 5/5 | 7 (5–8) | **10,304** (8,245–13,068) | 25,081 (15,798–34,490) | 1,202 | 3,1,1,2,1 |

Correctness was 20/20: every run in every arm produced the required evidence.
No run failed the task, in either arm, on either fixture.

### The known-symbol fixture: a real but small win

Outlines cut median tool bytes by a third (5,340 → 3,543) and prompt tokens by
13% (14,302 → 12,474), with **identical request counts** (median 4, and the
outline arm's range extends one higher). The mechanism is visible in the logs:
the grep arm sometimes read the whole 670-line file (27,025 and 13,292 bytes in
two of five runs) while the outline arm never did — it read 2,231–5,518 bytes.

That is the shape the skeleton is for, and it is also the limit of the result: it
saves **bytes**, not round trips, and the grep arm's cost is erratic rather than
uniformly high. On a task where the model already narrows its grep correctly, the
saving disappears.

### The unknown-location fixture: the skeleton loses

`unknown` is where #133 expects the gain, and it is where the arms are furthest
apart — in favour of grep:

- requests: median **5 vs 7**, and the outline arm's best run (5) only ties the
  grep median;
- prompt tokens: median **20,217 vs 25,081**, rising to 34,490 at the outline
  arm's worst.

The extra round trip is the cause. `outline` needs the file list first, so a run
pays for `glob` + `outline` + narrow reads where the grep arm often pays for one
`grep` and one narrow read. Charging the 260-byte prompt sentence on every one
of those requests makes the gap wider, not narrower.

The outline arm was more consistent and cheaper in tool bytes (8,245–13,068
against 5,002–64,988), which is a real quality difference — but the issue's
criterion is end-to-end usage, and tool bytes are not a bill.

## Conclusion: do not add an index capability

The pre-registered rule is that the capability is only suggested when task
success holds **and** end-to-end usage falls. Success held (20/20). End-to-end
usage did not fall:

- `known`: tool bytes −34% and prompt −13%, at an unchanged median request
  count (4 vs 4; the outline arm spans 3–7 against 3–6). A real saving in bytes,
  not in round trips, on the fixture with the cheapest possible starting point.
- `unknown`: requests +40% and prompt +24% against grep. The skeleton's own
  round trip costs more than the reads it saves.

An index capability is a permanent cost: a second parser path to maintain, a
tool on the default surface, and prompt bytes on every request whether or not the
model uses it. The data supports that cost only on the narrow shape where the
symbol is already named and the model would otherwise be tempted to read the
whole file — and grep already covers that shape, just less consistently.

Recorded as **not introduced**. No product code, tool surface, prompt, or
configuration changed.

What the measurement does support, and what a later attempt should build on: the
ranges are trustworthy (1,327/1,327 re-parse), the probe costs 2,803 bytes for
670 lines, and the win appears in *stability* (8KB–13KB instead of 1KB–65KB),
not in the median. If a future fixture shows a task where the model must page
through several large files before it knows which one matters, the skeleton is
the shape to retry — that is the case this fixture set approximated but did not
force.

## Two things that bit this harness

Both are recorded because the next measurement will hit them too.

- **A nested `go.mod` decides what `qlty check` scans.** `outline/go.mod` and
  `fixture/go.mod` first declared `go 1.21`, and `qlty check --all` went from 1
  issue to 147: 146 `osv-scanner` findings, all against `stdlib@1.21.99`. The
  module version is not cosmetic — a nested module pins the stdlib that the
  security scan evaluates. Both now declare the repository's own version.
- **`run()` sub-calls are logged without their `argv`.** The harness therefore
  cannot tell an outline call from `go build` after the fact, and counting them
  from the answer would be inference. `fixture/outline` is a wrapper that
  appends to `outline-invocations.log` before exec'ing `.outline-bin`.

## Caveats

- One model, one provider, one effort, five runs per arm. The `unknown` gap is
  larger than any plausible noise floor; the `known` win is not, and is stated
  as small for that reason.
- The backend reports no cache figures.
- `unknown`'s grep arm logged 10 tool errors against the outline arm's 4. Those
  are failed reads (typically a whole-file read past `MaxReadBytes`), not task
  failures, and they are part of why its variance is wide — a real property of
  the arm, not an artefact to correct away.
- The fixtures are synthetic. `processor.go` is large but not adversarial; a file
  10× longer with deeper nesting would plausibly widen both arms' spread.
- `grade.sh` checks that the required facts are present, not that the answer is
  elegant. The same rubric applied to both arms.
