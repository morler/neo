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

For the same reason the tool cannot grow into a `bash` replacement: process
execution cannot be confined by a runtime that itself runs unconfined. That is
answered separately in [Script as a Bash
replacement](script-vs-bash.md), and it is why the whitelist above is not a
sign of an incomplete feature.

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
rather than assumed. `internal/tools/moejs_test.go` pins each one, so an engine
upgrade that changes a semantic fails there instead of surfacing as a confusing
tool result; the comment on each test says why the behaviour is load-bearing.

- `CompileScript` plus `Runtime.RunScript` evaluates a classic script and yields
  the last expression's value.
- A classic script has no top-level `await`; it is a syntax error.
- `Compile` plus `Hook` plus `Call` returns a Promise object rather than a
  value. Since every host function is synchronous, that path buys nothing.

The engine costs **+5.83 MiB** of binary size, taking a stripped build from
17.13 MiB to 22.96 MiB. That is a recorded product decision, not an accident:
the budget task raises the checked ceiling from its current 20 MiB and writes
the reason beside the number.

### Engines considered and rejected

Three other Go script engines were evaluated as replacements for moejs.
All three are rejected, and the reasons are recorded here so the question is
not reopened from memory.

The measurements below are binary size on one machine, Go 1.27.4,
`-trimpath -ldflags='-s -w'`, against a 1.43 MiB hello-world baseline. The
behavioural notes were checked by running each engine, not by reading its
README.

**d5/tengo** (MIT, v3.0.0 tag, zero external dependencies, cheapest at
+0.8 MiB). Rejected on four semantics the tool depends on:

- No completion value. A bare `1 + 41` leaves the global table empty; the
  host has to rewrite the source to `__res__ := (...)` and read that back.
  `scriptWrapper` would not survive as written.
- No methods on strings, arrays, or maps, and no `split`/`join`/`filter`/
  `map`/`take` builtins. The most common model-written line in this tool is a
  `.split("\n").filter(...)` chain, and it is not expressible.
- A host function that returns a Go `error` ends the whole script. Today a
  failed tool call is a throw the script may catch; under tengo the statements
  after it never run.
- No `try`/`catch`. Only `error(x)` values and `is_error`.

**deepnoodle-ai/risor** (Apache-2.0). Rejected on version state and language
shape. The actively developed line is v3, which is alpha and **not published as
a Go module at all** — `proxy.golang.org/github.com/deepnoodle-ai/risor/go/v3`
lists no versions and `v3.0.0-alpha.1` returns 404 — so a host can only import
v2. v2 has no `for` and no `while`: its own migration guide says to use
`map`/`filter`/`reduce` instead. A tool whose purpose is batching several
dependent steps has no way to write the loop. v2 also has no `json` module and
no `list.push`. Its expression-level API (`Eval` with `WithEnv`,
`WithTimeout`, `WithMaxSteps`) is otherwise clean, and v3 is worth re-reading
if it ever ships a module.

**refaktor/rye** (BSD-3-Clause). Rejected on a reproducible crash and on
language traps. Creating two engines concurrently aborts the process with
`fatal error: concurrent map writes` from `evaldo.RegisterBuiltins2` via
`embed.New()`; two of three attempts crashed, the third happened to pass.
The cause is a package-level `evaldo.BuiltinNames` that the source itself
describes as a hanging global. It also has no `context.Context` integration,
no memory limit, no general Rye-to-Go struct conversion, and a REBOL-style
reader where opwords share one precedence and associate right, so
`low + high` has to be parenthesised. Its cost is +7.6 MiB.

The comparison that decided the question is not binary size. moejs is the only
one of the four that gives the model JavaScript, and JavaScript is the language
the model already writes correctly. `docs/performance.md` records a run that
spent three failed calls learning this tool's conventions; a language with no
prior in the model raises that number rather than lowering it. That cost is
paid on every turn, while the 5 MiB saved is paid once.

Replacing the engine would also not change the answer in
[Script as a Bash replacement](script-vs-bash.md) §3: process execution cannot
be confined by a runtime that itself runs unconfined, and no choice of engine
changes that.

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
- **Not stateful across calls, except in code mode.** In classic mode a
  session's script starts fresh: no REPL, no retained globals, no way for one
  script to hand a value to the next except through its output. Code mode keeps
  one interpreter per session so `globalThis.x = …` survives, because it is the
  surface the session acts through and it has no other way to build up work.
  Local declarations are still per call: a kept runtime rejects a repeated
  `const`, and every second script writes one. The state is never persisted, so
  `/clear`, resuming or a restart starts empty, and the prompt says so. See
  [Performance](../performance.md) for what the mode measured.
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
