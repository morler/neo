# #132 · Evidence-only script results against full output

Measured 2026-11-28. The question: when a `script` returns its findings, is it
cheaper when it returns only the evidence the answer needs, than when it returns
the full window or log it read?

## What is compared

Two arms, one fixture set, one binary, one provider, one model, one effort:

| | full | evidence |
| --- | --- | --- |
| Prompt tail | "quote the failing line and enough surrounding lines… exactly as they appear" | "answer with only the line number, the error text, and the host and port… do not paste log contents" |
| Everything else | identical: fixture bytes, provider, model, effort, tool surface, `neo` binary | identical |

The prompt's final sentence is the only difference, so any difference in tool
result bytes, final bytes, requests, or prompt tokens comes from how much the
model returns into the context — which is exactly what the issue isolates.

This is deliberately **not** a comparison against a no-script baseline. #83, #86,
#91, #97, #101, #103 already cover tool selection and the fixed cost of the tool;
those found no round-trip saving and no merge-rate gain. This experiment starts
from the assumption that the script is already in use and asks a narrower
question: is there anything to gain from returning less.

## Fixtures

`make-log.sh` regenerates `fixture/logs/service.log` deterministically; the
generated file is gitignored. The repository holds 20 lines of generator rather
than a 259 KB artifact, and the one failed request is reproducible by
construction.

| fixture | shape | answer |
| --- | --- | --- |
| `find-const` | 6 files, a Go constant defined once and used once, plus a decoy constant and prose that mentions ports | `config.go` + `8080`, `root.go` + `DefaultPort` |
| `find-error` | 4,000-line log, one `ERROR` at line 2871, a retry warning at 2872 | `2871` + `connection refused` + `10.4.2.9` + `6379` |
| `narrow-read` | 120-line config, one `threshold = 42` at line 57 | `57` + `42` |

`narrow-read` is the negative control the issue asks for: the answer sits at a
known line, so a correct run does not have to pull anything large into the
context, and an evidence-only instruction has almost nothing to save.

The grader (`grade.sh`) is mechanical: the final answer must contain the literal
strings a correct answer cannot omit. It does not judge prose. `grade_check.sh`
pins the grader against six synthetic answers, so a passing run means the
evidence is present, not that the answer was impressive.

The reducer (`reduce.sh`) is verified offline by `check.sh` against a synthetic
request/result log. No model and no network are involved in either check, which
is what the issue's "non-model tests use a stub" requirement asks for.

## Running it

```bash
go build -o docs/measurements/132-evidence-only-script-results/neo ./cmd/neo
cd docs/measurements/132-evidence-only-script-results
./make-log.sh                 # regenerate the 4,000-line log
./check.sh && ./grade_check.sh
./run.sh all all r1            # 3 fixtures x 2 arms, one run each
```

`run.sh` copies the fixture into a fresh work directory per run, sets
`NEO_LOG` so a run is reduced from the log rather than inferred from the answer,
and writes `results/<fixture>.<arm>.<label>.summary.json`. The summary records
what `neo run --json` reports (`requests`, the four usage fields, tool calls and
errors) plus what the log holds (tool result bytes, provider payload bytes,
top-level vs sub-call reads, script calls).

## Environment

`provider: commandcode`, `model: deepseek/deepseek-v4.1-flash`,
`reasoning_effort: low`, code mode with `features.script: true`, binary
`nightly-20261127-da511a0-2-gcd84837`. Five runs per arm per fixture (30 runs).

**The backend reports no cache figures.** Every run has
`cache_creation_tokens = 0` and `cache_read_tokens = 0`, so this report cannot
say how much of the prompt was served from cache, and it does not estimate it.
That is a limitation of the backend, not an absence of caching in Neo.

## Results

Medians over five runs; ranges in parentheses. "tool bytes" is the sum of every
tool result the model received (from `NEO_LOG`), not the final answer.

| fixture | arm | success | requests | tool bytes | final bytes | prompt tokens |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| find-const | full | 5/5 | 5 (3–6) | 66,879 (2,281–67,928) | 1,562 | 26,530 (6,790–44,528) |
| find-const | evidence | 5/5 | 4 (3–6) | 26,325 (2,446–68,366) | 301 | 23,759 (9,210–49,902) |
| find-error | full | 5/5 | 7 (5–9) | 6,832 (3,125–34,807) | 999 | 26,644 (18,084–54,402) |
| find-error | evidence | 5/5 | 4 (2–6) | 986 (287–1,114) | 351 | 8,859 (3,827–14,318) |
| narrow-read | full | 5/5 | 2 (2–3) | 902 (827–1,030) | 973 | 4,115 (4,016–6,415) |
| narrow-read | evidence | 5/5 | 3 (2–3) | 5,073 (109–5,570) | 80 | 5,732 (5,349–7,372) |

Every run scored 1 on the grader: 30/30. No run failed, and no run in either arm
needed an answer-level retry.

### The long-log fixture separates, but not the way the hypothesis predicts

`find-error` is the one where the medians separate most: returning only the
evidence moved median tool bytes from 6,832 to 986 (−86%), prompt tokens from
26,644 to 8,859 (−67%), and requests from 7 to 4. Success did not move: 5/5 in
both arms.

The ranges are not clean, though. The full arm spans 3,125–34,807 tool bytes and
the evidence arm 287–1,114, so four of five full runs sit above every evidence
run but one full run (3,125) does not. The direction is consistent and large; the
sample cannot call it a step change, and this report does not.

That is a real reduction in what the model re-sends on every later request, and
it is the fixture the issue calls "long log location". It is also the only one
with a reduction this size.

### The cross-file fixture does not separate

`find-const` shows the same direction in medians (tool bytes 66,879 → 26,325,
final bytes 1,562 → 301) but the ranges overlap almost completely, and in two of
five evidence runs the model read every file in the fixture anyway
(66,372 and 68,366 bytes) despite being told not to paste file contents. The
median is driven by three runs that searched narrowly and two that dumped the
fixture. This is not a stable effect, and reporting it as one would be wrong.

### The negative control reverses

`narrow-read` is why this report does not recommend a product change. The
evidence arm used **more** tool bytes (median 5,073 vs 902) and **more** requests
(median 3 vs 2) than the full arm, while producing a shorter answer. The model
read the whole 120-line file to answer a question it could have answered from one
line, in three of five evidence runs, because "answer with only the line number
and the value" says nothing about how to find it. The full arm's instruction to
quote surrounding lines pushed it toward a narrow read.

That is the shape the issue asks to report separately: fewer final bytes, but
more context and more round trips.

## What the run shapes say

Two behaviours recur and both are outside what a prompt sentence controls:

1. **Raw reads are not pageable.** `find-error` runs that used
   `read_file({raw: true})` hit `selection exceeds the byte cap; retry with
   offset=1013, limit=506` and then had to page or switch to `grep`. The log
   fixture's 259 KB is past `MaxReadBytes`, so a raw whole-file read is refused
   by design. The evidence arm avoided this by grepping; the full arm sometimes
   did not. This is a *discovery cost per attempt*, not a difference between the
   arms — but it is the mechanism behind several of the full arm's extra
   requests.
2. **The model answers in the arm's requested form almost always, and reads the
   same way in both.** In `find-const`, both arms converged on
   `glob` + `read_file` over every file. In `narrow-read`, the full arm's
   "quote ten lines" produced a narrow read and the evidence arm's "report the
   value" produced a whole-file read. The instruction shapes *how much the model
   writes*, not reliably *how much it reads*.

## Conclusion: do not add a product change

The pre-registered rule in the issue is that a product change is only suggested
when task success does not drop **and** end-to-end usage drops. Success held, but
end-to-end usage did not drop consistently:

- one fixture (`find-error`) drops by 67–86% in the medians, but the ranges overlap
  at their edges, so it is one consistent effect rather than a demonstrated one;
- one (`find-const`) drops in median only, with overlapping ranges, and two of
  five runs read everything anyway;
- one (`narrow-read`) reverses: more tool bytes, more requests.

A product change would have to be a prompt or tool-surface rule stating when to
return less. The data says such a rule is worth its bytes only on the long-log
shape, and there is no evidence it can be written without flipping the negative
control. Recorded as not introduced.

What the measurement does support is narrower and already true: Neo's script
tool *can* return only evidence, and when the model chooses to, the saving on a
large-log task is large. The gap is selection, not capability, and #83/#86/#91
already recorded that prompt wording does not move selection reliably on this
model.

## Reproducibility check

Two runs labelled `verify` were added after the report was written, from the
committed harness against the same binary and fixture, to confirm the steps in
"Running it" still work from a clean checkout. They are in `results/` and are not
part of the five-run sample: `narrow-read` / full scored 1 with 2,185 tool bytes
and 5 requests, evidence scored 1 with 5,073 tool bytes and 2 requests. The
negative control's direction reproduced (evidence read more than full); the
absolute figures did not, which is the same noise floor the rest of this report
declines to over-read.

## Caveats

- One model, one provider, one effort, five runs per arm. The earlier arms in
  `docs/performance.md` recorded a noise floor wide enough that n=5 cannot
  decide a small effect; this report therefore only claims the one large effect
  and explicitly declines to claim the two ambiguous ones.
- The backend reports no cache creation or read tokens, so the prompt-token
  column is the whole prompt, not the billed part.
- The fixtures are synthetic and small. `find-const`'s 6-file workspace is far
  below the size at which dumping a tree stops being viable; a larger tree would
  likely strengthen the full arm's cost, not the evidence arm's advantage.
- The grader checks facts, not completeness: an answer that names line 2871 and
  the endpoint passes even if it omits the retry warning. It was accepted because
  the same rubric was applied to both arms.
