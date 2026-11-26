# Tools

Neo exposes a small built-in tool surface to the model.

| Tool | Surface | Description |
| --- | --- | --- |
| `agent` | Interactive chat | Spawn a fresh subagent with a self-contained prompt. `mode: "work"` is writable and serial; `mode: "inspect"` is read-only and parallel-safe. |
| `bash` | Chat and headless | Run a shell command via `/bin/bash -c`. Returns bounded combined stdout and stderr, retaining the start and end when truncated. Optional `timeout` in seconds. |
| `edit_file` | Chat and headless | Replace exactly one occurrence of `old_string` with `new_string`. Fails if the old text is missing, appears more than once, or the file changed since the agent last read it. `old_string` matches the file's raw text, so the line-number gutter `read_file` adds must be stripped first. |
| `glob` | Chat and headless | List workspace files matching a glob pattern, honouring `.gitignore`. Supports `**` for recursive matches. Returns one path per line. |
| `goal` | Interactive chat | Read or finish the session's standing goal. `action: "get"` reports the goal id, objective, status, attempt budget, and stopping rules; `complete` and `blocked` need that goal id plus a reason. The tool cannot pause, resume, clear, or change the budget. |
| `grep` | Chat and headless | Search the workspace with a regular expression, honouring `.gitignore`. Returns matching lines as `path:line:text`. |
| `read_file` | Chat and headless | Read a file from disk, prefixing each line with its 1-indexed number and a tab so the numbers match what `grep` reports. Returns up to `tools.MaxOutputBytes` (64 KiB); use `offset` and `limit` to page through larger files. Pass `raw: true` to omit the gutter and get the file's own text, which is what `edit_file` matches, so a script can read and edit in one call without stripping line numbers by hand. |
| `script` | Default surface | Run JavaScript that can call a whitelisted tool set, returning its last expression's value. On the code-mode surface (the default) it is how every file and command action happens: globals include `run(argv, timeoutSeconds)` for programs, `console`/`print`, and one interpreter kept for the session so `globalThis.x = …` survives between calls, while local declarations stay per call. `run` returns `{stdout, stderr, code}` and a non-zero code is data, not an error. On the classic surface it is the opt-in batch tool with only the file globals. Either way the whitelist is `read_file`, `grep`, `glob`, `write_file`, `edit_file`; `bash` is never included and there is no `import`. A bare string argument binds to the tool's one required field, so `glob(\"*\")` works as well as `glob({pattern: \"*\"})`. Each scripted call passes the same approval matcher a top-level call would. |
| `workflow` | Interactive chat | Create or update the visible workflow checklist. Neo attaches tool and subagent activity to the active item automatically. |
| `write_file` | Chat and headless | Write content to a file, creating parent directories. Overwrites the file if it exists. |

The default surface is **code mode**: the model acts by writing code in the
`script` tool, which carries `run()` for programs, a console, and one interpreter
per session, and keeps `agent`, `goal` and `workflow` because those coordinate
rather than act. `features.codemode: classic` restores the separate per-action
tools in the table above.

Process execution loses its confirmation hook in either surface: `tool_approvals`
can match `bash` but cannot see inside `run()`, which is the same position
`permissions.md` takes — the environment contains, the prompt only reminds.

Independent inspect calls issued in one model response can run concurrently.
Inspect children receive only `read_file`, `grep`, and `glob`. Work-mode children
also receive `script` when the session has it enabled (`features.script`,
`--script`, or `/codemode on`), so delegated file work can batch dependent steps
the way the coordinator can; inspect children never do, because their whitelist
is read-only by construction and a script reaches `write_file` through the same
globals. A child's script shares the file tools a later edit consults, so a read
inside a script counts as the child having seen the file.

## Goal ownership

The `goal` tool is always registered in interactive chat, even in a session with
no goal, so the model's tool list never changes mid-conversation. Its results are
JSON with `ok`, and failures caused by goal state — no goal, a stale
`goal_id`, a terminal status, a blocked report below the audit threshold — are
`ok: false` results with a `next` instruction rather than Go errors. Missing or
malformed input is a tool error, like every other tool.

The model may only read the goal and request `complete` or `blocked`. `active`,
`paused`, `budget_limited`, and `cleared` belong to the user and the idle
scheduler, so the tool has no action that could resume, pause, clear, or spend
budget. `complete` requires a reason naming the evidence; `blocked` requires the
same blocker to recur past the audit threshold (`internal/goal`).

`neo run` does not register the tool: headless runs take no interactive control
input, and a goal they could not continue would only be dead state.

```json
{"ok": true, "action": "get", "goal_id": "goal_1a2b", "status": "active",
 "attempts": {"used": 1, "max": 20}, "stop_when": ["…"]}
{"ok": false, "action": "blocked", "error": "the same blocker has been reported 1 of 2 times",
 "next": "Keep working. Report the same blocker again only if it still applies after another real attempt."}
```

## Output size

`tools.MaxOutputBytes` (64 KiB, roughly 16k tokens) is the single limit on what
one tool call contributes to the transcript. `bash` truncates its own output
head-and-tail at that size so a failing command's trailing error survives;
`read_file` refuses above it and asks the model to page; the agent applies the
same cap as a backstop for any tool that does not bound itself. A single line
longer than the limit cannot be read by `read_file`, since pagination cuts on
line boundaries; use `bash` for that.

## Script error hints

`script` reports the failure class (`script has a syntax error`, `script threw`,
`script stopped`, `script exceeded its memory limit`) and then, only where the
source confirms the cause, one sentence saying what to write instead:

- A syntax error whose source contains `await` gets the synchronous rewrite,
  because the engine reports `await 1` as `Unexpected number` and that says
  nothing about scripts being synchronous.
- A `script threw` whose message is `edit_file` rejecting `old_string not found`
  gets the line-number gutter note: `read_file` prefixes each line with its
  number and a tab, while `edit_file` matches raw text.

The hints are deliberately narrow. A hint attached to the wrong cause sends the
model after a bug that is not in the file, which costs more than the silence it
replaced, so a plain syntax error or an unrelated throw stays unadorned. They
cost bytes only on the failure path, so `TestFixedPromptFloorBudget` and
`TestScriptFloorBudget` do not move when one is added.

## Command timeouts

`bash` kills a command that outruns its deadline, along with its process group,
and returns an error naming the duration. The host sets the default
(`tools.DefaultBashTimeout`, 2 minutes; the chat host gives subagents 5). The
model can pass `timeout` in seconds to change it for a single call — usually to
raise it for a cold build or a full test suite — up to `tools.MaxBashTimeout`
(10 minutes), or the host default if that is longer, so asking for more time
never yields less than saying nothing.

A request above the ceiling is clamped rather than rejected, so an over-eager
number still runs the command instead of wasting a turn. A value that is not a
positive number is an error, as is one too small to survive the conversion to a
duration: falling back to the default would kill the long command anyway, with a
message that never mentions the ignored argument, and a zero deadline would
cancel the command before it started.

The command's own deadline is not the only one it can hit. `neo run` bounds the
whole session (10 minutes by default, `--timeout` to change it), and when that
expires first the error says so rather than blaming the requested timeout. It
also means a `timeout` near the ceiling is only reachable in interactive chat or
with a raised headless budget. Which deadline fired is read when the command is
stopped, not after it is reaped, so a slow reap that outlasts the run budget
still reports the command's own timeout.

## Stale edits

`read_file` records each file's modification time and size. `edit_file` refuses
when they no longer match and tells the model to read the file again, which
catches a change the model could not observe: the user saving in an editor, a
`git checkout`, or a concurrent `work`-mode subagent. A file the agent has never
read is not stale and edits normally — the guard is for invisible changes, not
for model error. `write_file` and `edit_file` re-record after writing so the
agent's own writes are never mistaken for external ones.

`tools.NewFileTools` constructs `read_file`, `write_file`, and `edit_file`
sharing one record. Build a fresh set per agent so a subagent's reads never
satisfy the coordinator's edits. The check is a stat comparison, not a lock: a
writer racing between the check and the write still wins.

## Search

`grep` and `glob` shell out to [ripgrep](https://github.com/BurntSushi/ripgrep),
which honours `.gitignore`, skips binaries, and is far faster than a hand-rolled
walk. `--no-require-git` is passed so ignore rules apply whether or not the
workspace is a git checkout.

Hidden files are searched (`--hidden`), because `.github/workflows` and the like
are ordinary project files; `.git/` is excluded explicitly, since
`--no-require-git` turns off ripgrep's own handling of it.

Output is read incrementally and ripgrep is stopped once `max_matches` lines
have been collected. A broad pattern over a large workspace can produce far more
than will ever be returned, and buffering it first would let a model-chosen
pattern decide how much memory Neo uses. The consequence is that truncation is
reported without a total: counting the rest means reading it.

`max_matches` bounds output lines, not matches. With `context_lines` set, the
context counts toward it.

If `rg` is not on `PATH`, both tools return an error naming the missing
dependency. It deliberately does not tell the model to use `bash`: the
coordinator will reach for it anyway, and an inspect subagent has no shell, so
suggesting one would only waste a turn. Inspect subagents therefore lose search
entirely on a machine without ripgrep and will say so.

There is no Go fallback: a second implementation would mean two sets of ignore
rules and two output shapes. `neo doctor` reports ripgrep as a warning, not a
failure, for the same reason.

They stay separate tools rather than folding into `bash` because they are
classified parallel-safe, which `bash` cannot be without interpreting shell
commands, and because inspect-mode subagents need read-only search without a
shell.

Search applies no path confinement of its own, matching `read_file`,
`write_file`, and `edit_file`. The sandbox is the boundary.

Cancelling a search kills the ripgrep process and returns an error with no
partial output.

## Execution and confirmations

Neo relies on its VM or sandbox for security boundaries. Interactive users can
set `tool_approvals` to confirm exact tool names or Bash command prefixes. The
list is empty by default and is not applied to headless or child agents.

## Repeated Calls

A model that keeps issuing the same call with the same arguments wastes a full
provider request and a full tool execution each turn. The agent counts calls by
tool name plus a hash of their arguments, within one user turn only, and acts on
the count:

| Identical call number | Behavior |
| --- | --- |
| 3, 5, 8 | The tool runs, and a short `<system-reminder>` is appended after its result. |
| 12 | The tool does not run. The turn ends with an error naming the tool and the count. |

The reminder is appended at the end of the conversation. It never touches the
system prompt, the tool definitions, or earlier messages, so the cached prefix
stays byte-identical (see [Prompt caching](prompt-caching.md)). The counts reset
at the start of every user turn, because the same call in a later turn is not a
loop. State-changing tools such as `workflow` and `goal` are counted like any
other: they cost a request and a turn too.

This is not a replacement for `MaxTurns` (500). `MaxTurns` is the outer safety
valve for a loop that keeps changing shape; this guard is the inner one for a
loop that repeats the same call and can be named. A turn stopped here reports
`repeated tool call` and records the refusal in the transcript as a failed tool
result, so it is distinguishable from a cancellation.

## Truncation

Every tool result is bounded by `MaxOutputBytes` (64 KiB, roughly 16k tokens).
Two tools say what to do when a result hits the cap, and the wording matters
because a model will copy it:

- `read_file` fails with `read_file: selection exceeds the byte cap; retry with
  offset=N, limit=M`. `N` is the first line that did not fit, so the retry
  resumes rather than restarting; `M` is the caller's own limit when it set one,
  or half the number of lines that overflowed. The advice is derived from the
  real truncation point, not from a second byte budget, so a suggested call is
  known to fit.
- `bash` keeps its head-and-tail marker (`omitted N bytes; showing first A and
  last B bytes`) and adds what to change in the command: `| tail -n 200` or
  `| grep pattern`. It cannot suggest a different call because the output is
  already gone by the time truncation happens.

`grep` and `glob` have their own `max_matches` limits and are not affected.
