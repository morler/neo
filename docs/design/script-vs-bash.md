# Script as a Bash Replacement - Design Note

> **Status:** Decided, nothing implemented. This note records why the option was
> rejected so the question is not reopened without new evidence. See
> [Sandbox and Tool Approvals](../developer/guides/permissions.md) for the
> policy that already answers it, and [Embedded JS batch
> tool](../design/js-batch-tool.md) for what the `script` tool is for.

## 1. The Question

A recurring proposal: make `script` the only way to run code, so that `bash` can
be removed. The argument has three parts — shell commands are error-prone,
shell commands are unsafe, and a JavaScript runtime inside Neo could be
sandboxed, so the sandbox would then make the tool safe.

This note answers one question and only one: **can an embedded JavaScript
runtime replace `bash` while keeping the sandbox property?**

The answer is no, and the reason is not about the engine. Section 3 states it;
section 6 lists what would change the answer.

## 2. What the Engine Actually Provides

Checked against `github.com/MoeForge/moejs` v0.1.1, the version in `go.mod`.

**File system confinement (`node` package).** Real, tested, and the reason the
package exists. Its own doc comment says it "is meant for sandboxes, where a
program reads and perhaps writes the files of a workspace and nothing else."

- `FSOptions.Root` is a host directory; `Mount` is the absolute path JavaScript
  sees. A path that leaves the root fails with `EACCES`.
- On Linux amd64 and arm64, resolution goes through `openat2` with
  `RESOLVE_BENEATH` in one call; elsewhere `os.Root`.
- `Write: false` makes every mutating call fail with `EROFS`.
- `Allow(op, path)` sees each access first, with symlinks resolved, `op` being
  `"read"` or `"write"`.
- One recorded caveat: the directory-descriptor cache leaves a window of up to a
  millisecond in which another process can move a directory out of the root and
  back. `NoDescriptorCache` closes it at the cost of three system calls per stat.

**Network denial (`webapi` package).** Default deny — with no `Client`, every
`fetch` rejects — plus an `Allow` hook per request and redirect, and
`webapi.AllowHosts` for a host list. The docs warn that a nil `Allow` allows
loopback and the cloud metadata address `169.254.169.254`.

**Process execution.** Not present. Not disabled — absent. Searching the whole
module for `os/exec`, `syscall.Exec`, `StartProcess`, and `ForkExec` finds no
non-test use. The `node` package serves exactly three specifiers — `node:fs`,
`node:fs/promises`, and `node:buffer` — and `TODO.md` mentions no plan for
`child_process`.

So the engine gives a file-system sandbox and no way to run a command. Those are
precisely the two halves that decide this question, in the wrong combination.

## 3. Why Process Execution Cannot Be Sandboxed In-Process

This is the whole argument, and it is independent of which embedded runtime is
chosen.

`FSOptions.Root` confines **path resolution performed by the Go process**. A
child process is a different process. Once a script can start one, that child
has its own creds, its own working directory, and its own path resolution, so it
can leave the root, read the environment, open a socket, or start another
process. The confinement stops at the `fork` boundary by construction; there is
nothing for it to apply to.

The only ways to contain what a child process does are outside that process:

- the kernel, through namespaces, seccomp, Landlock, or `chroot`, driven by a
  container runtime or a wrapper;
- a virtual machine;
- a separate user or machine.

None of those can be built by a JavaScript engine, because the engine itself
runs unsandboxed inside the process it would have to confine.

Neo's own docs already reached this conclusion from the other direction.
[`permissions.md`](../developer/guides/permissions.md) records that Neo "does not
classify risk" because shell commands can be expressed through wrappers,
aliases, variables, and substitutions, so a classifier "can look reassuring
without reliably containing an agent." Classifying is the weaker version of the
same mistake: trying to decide, from inside, what an outward-facing action will
do.

Two consequences follow, and both are load-bearing:

1. **Injecting an `exec` host function is not a feature, it is a bypass.** moejs
   lets the host install any `NativeFunc` as a global, so Neo could add `exec` in
   an afternoon. The result would have every `bash` risk plus a JavaScript
   interpreter, a second output path to bound, a second timeout, and no
   approvals classifier that can see inside it.
   [`js-batch-tool.md`](js-batch-tool.md) §2 already refuses a second shell on the
   narrower ground that an unclassifiable tool makes approvals decorative.
2. **The sandbox cannot make `script` stronger than `bash`.** Under a container
   both are equally contained. Outside one, both are equally uncontained, and
   `script` is worse because its executing code is less understood.

## 4. What Was Measured

The safety argument aside, the token and round-trip argument does not hold
either. [Performance](../performance.md) records nine runs on a 2,001-line file with one
binary, flipping only `features.script`:

- `read_file` was called **zero** times in either arm. The model narrowed the
  window with `sed -n '1499,1503p'` and `grep -n`.
- The `off` arm used 5 requests and 11,331 input tokens at the median; the `on`
  arm used 6 and 16,769.
- Where `script` was chosen, one run wrote four of them and reached 8 requests
  and 26,813 input tokens, and its first script only replaced a `grep`.

So `script` is not competing with "pull a whole file into the context". It is
competing with one already-narrow shell command, and it loses on bytes because
its fixed cost is re-sent on every turn. Replacing `bash` would therefore raise
cost on the tasks measured, not lower it.

This does not say `script` is useless. It says its value is batching dependent
steps, which is what the tool documents, and that value does not depend on
`bash` going away.

## 5. The Two Options That Remain

Neither is a replacement, and the distinction matters when recording a decision.

**Option A — keep `bash`, move the boundary out.**

Contain the whole of Neo in a container or VM. That is what
[`permissions.md`](../developer/guides/permissions.md) and
[`architecture.md`](../developer/architecture.md) already assume, and it is the
only arrangement in which running `git`, tests, and builds is both possible and
bounded. Cost: an external runtime, which `MISSION.md` lists as an accepted
non-goal for Neo itself but not for its environment.

**Option B — drop `bash`, keep a file-only session.**

Register `read_file`, `grep`, `glob`, `write_file`, `edit_file`, and optionally
`script`, and nothing that starts a process. This is capability selection, not a
permission model, which is the distinction `supervisor.go` already draws for
inspect subagents: they are "limited through capability selection rather than
permissions."

Option B is the only route to a sandboxed session that Neo can implement without
an external container. It is genuinely safer than today, because the process
boundary it removes is the one that cannot be policed. It is also not a
replacement: no git, no tests, no builds, no package managers.

A smaller step that needs no new engine work is a `features.bash: false` switch,
since `Registry.Filter` already supports dropping a tool by name. That is worth
doing on its own merits and is independent of this note.

**What is not an option** is a third arrangement in which a script runs commands
and stays confined. Section 3 is the reason, and no choice of engine changes it.

## 6. What Would Change This Answer

The decision is not a matter of taste, so it should be revisitable on evidence.
Any one of these would reopen it:

- moejs, or another embedded runtime, ships process execution with a confinement
  mechanism that survives `fork`, and the mechanism is not "the host must
  already be in a container." That would make the engine part of the boundary
  rather than a layer above it.
- A measurement shows a task where a script saves round trips or tokens against
  `bash` by a margin larger than its fixed cost, on a task a single shell command
  could not solve. The current fixture is too small: a `sed` one-liner solves it.
- A platform appears where containers are unavailable but a script-only session
  is still useful. That would make Option B the default rather than a setting,
  which is a product decision rather than a security one.

Absent one of those, the answer stays: an embedded JavaScript runtime can be
sandboxed for files and network, and cannot be sandboxed for process execution.

## 7. What This Note Does Not Decide

- Whether to add `features.bash: false`. It is independent, and worth doing
  whenever a user asks for a file-only session.
- Whether `script` should adopt moejs's `node` package for real path confinement
  instead of Neo's own whitelist. That trade is real — the whitelist bounds the
  surface to five tools, while `node:fs` bounds paths inside one root — and it
  would require an event loop, which
  [`js-batch-tool.md`](js-batch-tool.md) §4 rejected for a different
  reason. It should be decided on its own evidence.
- Which sandbox to recommend. `permissions.md` lists the properties that matter —
  filesystem, processes, network, credentials, external services — and a sandbox
  that only isolates the filesystem leaves the rest exposed.
