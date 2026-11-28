# #134 · A tool-result budget for compaction summary requests

Measured 2026-11-28. The question: when compaction builds its summary request,
should the old tool results be sent verbatim, or replaced by a bounded preview?

## Why it is worth asking

Compaction exists to make the transcript smaller. Today it re-sends the old
segment **verbatim** on the summary request, so a 136 KB log is billed once more
on the very call that is supposed to reduce cost. On the fixture below that makes
the summary request **141,241 bytes / 28,276 input tokens** for a transcript
whose only interesting content is one line.

## What was measured

Two arms, one fixture, one model. The only variable is whether the summarizer
receives the tool results verbatim or as a head+tail preview.

| | control | bounded |
| --- | --- | --- |
| Summary request | the old segment verbatim | each tool result above 2,000 bytes becomes head + marker + tail |
| Transcript | untouched | untouched; `Preview` copies |
| Everything else | same fixture, provider, model, system prompt, instruction | same |

The prototype is `budget/`, a normal package in this repository with no product
wiring: `internal/compact` is unchanged. If the measurement had supported the
budget, the plan would have been a call inside `Summarizer.summarize`.

## The fixture

`budget/Fixture()` is eight messages, exported so the tests and the probe
command cannot drift apart:

- a user constraint that must survive: *"do not change the retry budget"*;
- a 4,000-line log whose only failure is at line 2871 —
  `ERROR handler failed: dial tcp 10.4.2.9:6379: connect: connection refused` —
  followed by a retry warning and a final `exit status 1`;
- a parallel tool result that passed: `ok internal/compact 0.02s`;
- a recent user question that must stay in the retained tail.

That is the shape the issue asks for: a large log, parallel tool results, and a
recent unanswered input.

## Offline verification (no network, no model)

`go test ./docs/measurements/134-compaction-tool-result-budget/budget/` — 10
tests, all passing:

| invariant | test |
| --- | --- |
| only tool results shrink; every message, role, block type and ID survives | `TestPreviewShrinksOnlyToolResults` |
| tool_use/tool_result pairing and order stay valid | `TestPairingAndOrderSurvive` |
| the source transcript is not mutated | `TestSourceIsNotMutated` |
| a result under the budget is byte-identical | `TestSmallResultsAreUntouched` |
| budget 0 is the control arm | `TestZeroBudgetIsTheControlArm` |
| head and tail survive, the marker counts the dropped bytes, UTF-8 is valid | `TestMarkerNamesTheDroppedBytes` |
| an explicit head/tail split is honoured | `TestHeadAndTailSplitIsHonoured` |
| the summary request actually carries the log today | `TestSummaryRequestCarriesTheLargeLogVerbatim` |
| the summary request gets smaller and stays well-formed | `TestPreviewBudgetReducesTheSummaryRequest` |
| the retained recent messages are identical to the control | `TestRecentSegmentIsIdenticalToTheControl` |

In the offline test the same figures are 141,214 → 3,297, because the test
fixture's log has no timestamp prefix. The wire size is measured by marshalling `chatcompletions.BuildRequest`, not
estimated from character counts.

One subtlety the tests pinned down: with `KeepRecent` small the safe split lands
*before* the log, so the log is retained verbatim and the preview is never
exercised. The fixture is eight messages so `SafeSplitPoint` lands at 5, which
summarizes message 4 (the log) and keeps 5–7. A naive fixture would have
measured nothing and reported a clean zero difference.

## Results

### The summary request shrinks by 98%

| | control | bounded (2,000 B/result) |
| --- | ---: | ---: |
| summary request body | 141,241 B | **3,324 B** |
| input tokens (live run) | **28,276** | **687** |
| output tokens (live run) | 550 / 482 / 649 | 501 / 343 / 402 |
| summary length | 1,739 / 1,512 / 1,398 B | 1,160 / 710 / 1,299 B |

Live runs: `commandcode` / `deepseek/deepseek-v4.1-flash`, three runs per arm,
same fixture, three runs summarised in `runs/`. The bounded arm's prompt is
98% smaller and its request body is 43× smaller.

### But the summary loses the only failure

This is the result that decides the issue. Every control summary names the
refused endpoint; **no** bounded summary does:

| run | keeps `10.4.2.9` / `connection refused` | keeps the retry-budget constraint |
| --- | --- | --- |
| control r1/r2/r3 | **yes / yes / yes** | yes / yes / yes |
| bounded r1 | no | yes |
| bounded r2 | no | yes |
| bounded r3 | no | yes |

The control arm's r1 summary states the finding plainly:

> the only actual failure is loss of connectivity to the Redis instance at
> 10.4.2.9:6379; everything else in that run reported success

The bounded arm's r3 summary, on the same input, states the opposite:

> a failure whose actual cause is not visible in the retained output since the
> log was dominated by successful request lines and the result was truncated.

The head of that log is 4,000 consecutive `status=200` lines and the tail is
an exit status. The one line that mattered was in the middle, which is exactly
where a head+tail preview cuts. The preview is not lossy in an abstract sense —
it removed the answer.

### And it makes the continuation pay more

Every bounded summary's open items ask for the log to be read again, differently:

> re-running with log output filtered or reduced since the INFO spam obscures the
> real failure

> the repeated status=200 noise should be suppressed or filtered rather than read
> in full

So the budget saved 27,589 input tokens on the summary request and created a
follow-up read to recover what it dropped. That is the case the issue names
explicitly: "若需补查导致总消耗上升，记录为不引入".

## Conclusion: do not introduce the budget

The pre-registered rule is that it is enabled only when the input drops clearly
**and** continuation quality does not regress. The input drop is real and large
(−98%). Continuation quality regressed on the one thing a summary of a failing
run exists to carry: the cause of the failure. 3/3 bounded summaries lost it,
3/3 control summaries kept it.

A tool-result budget is therefore not a neutral size/cost trade. On this shape it
trades away the single line that made the transcript worth summarizing, and it
does so because of how logs are built — the interesting event is in the middle,
surrounded by noise at both ends, and head+tail keeps exactly the noise.

Recorded as **not introduced**. No change to `internal/compact`, the trigger
threshold, the retained segment, the providers, or the public config.

### What a later attempt should try instead

The failure mode suggests the fix is not "keep less" but "keep differently":

- **Keep by classification, not by position.** Lines matching `ERROR|FATAL|FAIL|WARN`,
  a non-zero exit status, and the command itself are the substance; `status=200`
  repeats are not. A preview that keeps matches and a few neighbours would have
  kept line 2871 and dropped 3,999 lines of noise.
- **Keep the conclusion, drop the log.** The summary prompt already says "drop
  repeated logs once their conclusion is captured". Doing that in code, rather
  than asking the model to, is the missing piece.
- **Cap by result count, not bytes.** Two parallel results were fine here; the
  problem was one result's interior, not the number of them.

Any of those needs a fixture where the answer is *not* in the middle of the noise
as well, because a classifier that keeps all errors will just as easily keep
4,000 error lines.

## Caveats

- One model, one provider, three runs per arm. The loss was 3/3 against 3/3 kept
  with identical inputs, which is a categorical difference, not a mean.
- The backend reports no cache figures, so the two arms' prompt costs are
  compared whole.
- The probe measures the request the production adapter would send by marshalling
  it; it does not send the control arm's 136 KB log anywhere else.
- `budget/` is a measurement package under `docs/measurements/`, imported by
  nothing in `internal/`. It is not a proposal to ship this code.
