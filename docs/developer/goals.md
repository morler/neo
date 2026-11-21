# Goals

`/goal` gives the current session one standing objective. While it is active and
you are idle, Neo keeps sending itself continuation turns until the goal is
finished, blocked, paused, cleared, or out of budget.

Use it when the work needs several "keep going" prompts that you would otherwise
type by hand.

```text
/goal <objective>
/goal status
/goal pause
/goal resume
/goal clear
```

## What A Goal Is Not

| Thing | What it does | Why it is separate |
| --- | --- | --- |
| Goal | Durable control state: should Neo continue? | It outlives a turn and survives resume. |
| Workflow | The visible checklist for the current work. | A finished checklist does not finish a goal. |
| Skill | Reusable instructions invoked with `/name` or `$name`. | A skill changes how one turn is done; it never decides whether to continue. |

## Setting A Goal

```text
› /goal make go test ./... pass in internal/session
· goal goal_9f2c1a7b3d4e5f60 active · attempts 0/20 · /goal status for details
› make go test ./... pass in internal/session
```

The objective becomes the first ordinary user turn, so the session title and the
transcript read like the task you asked for. The goal is written to the session
file before that turn is sent: the model can never work on a goal the session
did not record yet.

`/goal` at any time with no argument shows the status card plus the usage line.
`/goal status` shows the card alone: objective, status, attempt budget, blocked
audit (when something was reported), the last recorded reason, and the timestamps.

Setting a goal while one is active replaces it: the replacement gets a new ID, a
fresh attempt budget, and an empty blocked audit, and a notice names the goal it
replaced. Tool updates that still carry the old ID are rejected.

## Statuses

```text
/goal <objective>
        │
        ▼
      active ──── goal tool complete ────▶ complete   (terminal)
        │
        ├──── goal tool blocked ──────────▶ blocked
        │                                    │
        ├──── /goal pause, esc, ctrl+c ────▶ paused
        │                                    │
        ├──── attempt budget used up ──────▶ budget_limited
        │                                    │
        └───────────── /goal resume ◀────────┘   (paused, blocked, budget_limited)

any of active / paused / blocked / budget_limited ──── /goal clear ────▶ no goal
```

`complete` is terminal; the only way forward from it is `/goal <objective>` with
a new objective. `paused`, `blocked`, and `budget_limited` all return to
`active` through `/goal resume`, which also resets the attempt counter and the
blocked audit.

| Status | Meaning | Automatic continuation |
| --- | --- | --- |
| `active` | Work may continue. | Yes, when idle |
| `paused` | The user stopped it; the goal is kept. | No |
| `complete` | The model reported verified completion. | No |
| `blocked` | The same blocker recurred past the threshold. | No |
| `budget_limited` | The attempt budget ran out. | No |
| `usage_limited` | Reserved for token or wall-clock limits; nothing produces it yet. | No |

`complete` is terminal: a completed goal cannot be resumed, only replaced by a
new `/goal`.

## Who Owns What

Neo splits goal control so the model cannot quietly extend its own run:

| Actor | May do |
| --- | --- |
| Model (`goal` tool) | `get`, `complete` (with evidence), `blocked` (after the audit threshold) |
| User | `/goal <objective>`, `status`, `pause`, `resume`, `clear`, `esc`, `ctrl+c` |
| System | Move an active goal to `budget_limited` when attempts run out |

The `goal` tool has no action that could pause, resume, clear, or spend budget.

## The Continuation Turn

When a turn ends with an active goal and nothing else is happening, Neo starts
another turn itself. The transcript marks it:

```text
↳ goal continuation 3/20 · make go test ./... pass in internal/session
```

That visible line is the `display_text` stored with the message; the model
receives a longer prompt built by `internal/goal`. The prompt restates the full
objective inside `<goal>` tags, says the objective is user-provided task context
rather than higher-priority instruction, restates the verification rule from the
goal's contract, forbids redefining success as a smaller task, explains that
`blocked` needs the same blocker more than once, and names the current attempt
out of the budget.

Continuations are ordinary user turns. The core agent loop (`internal/agent`)
knows nothing about goals, so running out of goals never changes how a turn is
executed.

### When Neo Does Not Continue

A continuation starts only if all of these hold:

- the goal is `active`
- no turn is running
- the composer is empty
- no slash or file picker is open
- no model or provider browser is open
- no approval is pending
- no follow-up is queued
- no quit is pending

If you start typing before it fires, it does not fire. Your next turn runs first,
and the check happens again when that turn ends. A failed turn or a failed save
also skips the continuation: a goal does not advance on state the session could
not record.

Neo never keeps a goal alive on a timer. Continuation is triggered by the end of
a turn or by `/goal resume`, so there is nothing running while you are idle.

## Budget

An attempt is one automatic continuation turn. The turn started by
`/goal <objective>` is not counted, and neither are the turns you type yourself.

```yaml
goals:
  max_attempts: 20   # default; omit the section for the same 20
```

When the last attempt is spent, the goal becomes `budget_limited` and Neo shows
the handoff instead of continuing:

```text
Goal budget reached: 20/20 attempts used.
Last reason: tests still fail in internal/session.
Use /goal resume to continue or /goal clear to stop.
```

`Last reason` is the most recent thing the goal recorded: completion evidence, a
pause reason, or the latest blocker — including one that never reached the
blocked threshold, which is often exactly what you need to see.

`/goal resume` resets the counter to zero and starts the next continuation
immediately when nothing else is happening. A resumed session keeps the budget it
was saved with, so a config change does not silently give an old goal a different
amount of room.

## Blocked

The model can ask for `blocked`, but `internal/goal` enforces an audit: the same
blocker, compared by lowercased and whitespace-normalized text, must be reported
twice in a row. A different blocker restarts the count, and the first report
returns a result telling the model to keep working.

This exists because a single transient failure — a flaky command, a slow network,
one bad test run — is not a reason to end a goal.

## `/clear`, Interrupts, And Quitting

- `/clear` removes the goal along with the transcript, saves the session, and
  says what it dropped. A goal without its evidence would keep working on an
  empty conversation; use `/goal pause` if you want to keep the goal without
  continuing it.
- `esc` during a goal turn pauses the goal before the turn is canceled, so the
  cancel cannot be followed by an immediate continuation.
- `ctrl+c` does the same as part of quitting. The pause is saved even though the
  process is leaving.
- Interrupting a direct `!` shell command does not touch the goal: it was not an
  agent turn.
- A goal that is already paused, complete, blocked, or budget limited keeps its
  status when a turn is interrupted.

## Where Goals Live

| Path | Role |
| --- | --- |
| `internal/goal` | State, serialized transitions, attempt budget, blocked audit, the `goal` tool, continuation prompt text. Imports no other Neo package. |
| `internal/session` | Persists the optional `goal` object with the transcript. |
| `internal/tui` | Slash commands, status card, handoff, idle scheduling, interrupt handling. |
| `internal/config` | `goals.max_attempts`. |
| `cmd/neo` | Builds the manager from the saved session and the config, and saves every transition. |

## What Phase 1 Does Not Do

- No separate judge model. Completion is self-reported with a required evidence
  reason. The tradeoff is that the working model grades its own work; the
  continuation prompt, the evidence requirement, the blocked audit, and your own
  review are what keep that honest. A judge can be added later if self-reported
  completion proves too weak.
- No `/loop` or scheduled automation. A goal is not a cron job; it only advances
  while the session is open.
- No goal files, YAML/TOML definitions, or shareable goal assets.
- No token or wall-clock budget. Attempts are the only unit, so the limit is
  predictable and cheap to reason about.
- No `/goal draft`, `/subgoal`, or `/goal wait`. The contract fields exist in the
  saved state and are restated to the model, but only `/goal <objective>` sets
  them.

## Related

- [Configuration](config.md) for `goals.max_attempts`.
- [Sessions](sessions.md) for the saved `goal` field.
- [Tools](tools.md) for the `goal` tool's input and results.
- `docs/design/goal-runner.md` in the repository for the full rationale and the
  alternatives that were considered. The design directory is not published on
  the docs site.
