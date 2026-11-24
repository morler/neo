# Embedded JS Batch Tool - Design Spec

> **Status:** Implemented, off by default. The contracts live in the tasks that
> implemented them, not here: this note records why the tool exists and where
> its edges are. See [Tools](../developer/tools.md) for how to turn it on.

## 1. The Problem

An agent reads a file, looks at the output, then writes the next call. That is
fine for one step. It stops being fine when the step after a read cannot be
decided until the read is visible, because the whole read has to travel back to
the model as context before the next call can be written.

Three costs follow:

1. **An extra round trip per dependent step.** A read, a conditional edit, and a
   check cost three provider requests even when the model already knew the plan
   before the first one.
2. **The same bytes are sent twice.** The file content that the model reads
   enters the transcript and is re-sent on every later turn of that conversation.
3. **Filtering happens in the model's head.** "How many places match, and is it
   exactly one?" is answered by asking the model to count, which is both tokens
   and a correctness risk.

Neo already runs several tool calls from one response, concurrently where the
tool allows it and serially where it does not. What it cannot express is a call
whose *arguments* depend on an earlier call's result. That is the gap this
design fills.

## 2. The Shape

One tool, `script`. The model writes JavaScript; the runtime executes it; the
last expression's value is the tool output.

The script is not a shell and not a plugin. It is a way to turn several known
steps into one request. A script that reads three files, keeps the one line
that matters, and returns it costs one provider round trip and puts one line in
the transcript.

The tool is **off by default**. A measurement on three fixed tasks found that
the model chose `bash` and `grep` every time, so a default session paid 1,143
bytes of prompt per request for a capability it never used. Turning it on is
therefore a decision: `--script`, `features.script: true`, or `/codemode on`,
and the last one writes the choice back to the config. The measurements are in
[Performance](../performance.md).

The tools available inside a script are a **closed whitelist**, chosen by Neo,
not by the model. The list starts with reads and grows only through the
approval task, which is where write access is decided.

`bash` is not on the list and never will be. Neo does not parse shell commands,
so it cannot tell a read from a write, and a tool it cannot classify is a tool
whose approvals are decoration.

`import` is not available. A script is one unit of work, not a place to load
code from elsewhere.

## 3. The Runtime

[moejs](https://github.com/MoeForge/moejs) is a JavaScript engine written in Go.

- **Pure Go, so the binary stays one file with no cgo.** It links statically,
  and Neo still has no daemon, no plugin runtime, and no external process to
  supervise.
- **Apache-2.0**, which composes with Neo's MIT license. Redistributing the
  binary needs the notice file the dependency task adds, and nothing more.
- **Interruptible.** A `context` deadline stops a running script through
  `Runtime.Interrupt`, so a script that loops forever ends like any other
  cancelled tool call.
- **No file or network access of its own.** The engine reads nothing. Anything a
  script can touch, Neo put there.

Semantics that the implementation depends on, verified against the engine
rather than assumed:

- `CompileScript` plus `Runtime.RunScript` evaluates a classic script and yields
  the last expression's value.
- A classic script has no top-level `await`; it is a syntax error.
- `Compile` plus `Hook` plus `Call` returns a Promise object rather than a
  value. Since every host function is synchronous, that path buys nothing.

The engine costs **+5.83 MiB** of binary size, taking a stripped build from
17.13 MiB to 22.96 MiB. That is a recorded product decision, not an accident:
the budget task raises the checked ceiling from its current 20 MiB and writes
the reason beside the number.

## 4. What This Is Not

The edges matter more than the feature.

- **Not a plugin ecosystem.** There is one tool with one script per call. Scripts
  are not installed, discovered, versioned, or shared.
- **Not a second task state.** It does not plan, track, or report progress. The
  workflow checklist and the goal already own that.
- **Not a change to the agent loop.** `internal/agent` stays policy-free. The
  script tool is an ordinary tool: it receives context, runs, and returns a
  string. Approval, observation, and limits are composed around it the same way
  they are composed around `bash`.
- **Not stateful across calls.** A script starts fresh. There is no REPL, no
  retained globals, and no way for one script to hand a value to the next except
  through its output.
- **Not async.** Support for `async`/`await` would mean an event loop, promise
  settling, and a second cancellation model. The value of a script here is
  batching known steps, which synchronous host functions already provide.

## 5. Licensing

Neo is MIT. moejs is Apache-2.0. The two are compatible: the dependency is
linked, its notice travels with the binary, and Neo's own license is unchanged.

This is deliberately different from the AGPL situation considered earlier. An
AGPL dependency would make the whole distributed binary AGPL and end Neo's MIT
distribution. Apache-2.0 does not.

Where this design borrows an idea — batching dependent steps into one request is
not novel — it borrows the idea only. No code, prompt text, schema text, or test
case from another project is copied in. The one project whose behavior informed
this note is AGPL, so the rule there is stricter than usual: read the docs,
write the implementation from scratch.

## 6. Open Contracts

These are specified by the tasks that build them, and this note does not restate
them, because two copies of a spec drift:

- Which tools the whitelist contains, and when write access is added (#71).
- How a script's internal calls appear in the terminal and in subagent output
  (#68, #69).
- Where approval is enforced, and why it cannot be enforced at the `script`
  boundary (#70, #71).
- The error shapes for syntax errors, thrown values, timeouts, memory limits, and
  truncated output (#65, #67).
- The prompt text and the measured cost it adds to the fixed floor (#73).
