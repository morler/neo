---
id: 120
title: Give inspect subagents their read-only tools under code mode, where they currently receive none
status: in-progress
priority: high
labels:
    - bug
    - tools
created: "2026-11-26"
updated: "2026-11-26"
---

Inspect-mode subagents receive an **empty tool registry** under the default code-mode surface, so they cannot read, search, or inspect anything. They still answer, which is what makes the failure silent.

## Evidence

`internal/subagent/supervisor.go:75-90` — `toolsFor` asks for the read-only names regardless of surface:

```go
func (s *Supervisor) toolsFor(mode AgentMode) []string {
    if mode == AgentModeInspect {
        return append([]string(nil), inspectAgentTools...)   // read_file, grep, glob
    }
    if s.codeModeEnabled() {
        return []string{scriptToolName}                      // script only
    }
    ...
}
```

`internal/subagent/runner.go:148-166` — under code mode the registry is built from `script` alone. There is no `read_file`, `grep`, or `glob` entry for `Filter` to keep:

```go
if r.CodeMode {
    codeFiles := append([]tools.Tool{
        tools.Grep{Root: r.Root, Absolute: true},
        tools.Glob{Root: r.Root, Absolute: true},
    }, tools.NewFileTools()...)
    code := tools.NewScript(codeFiles)
    code.CodeMode = true
    base = []tools.Tool{code}                                // <- named "script"
} else {
    base = append([]tools.Tool{tools.Bash{...}}, files...)
}
all := tools.NewRegistry(base...)
return all.Filter(opts.Tools)   // Filter keeps only requested names present in all
```

`internal/tools/tool.go:85-96` — `Filter` drops every requested name that is absent from the registry and returns an empty registry when nothing survives.

Code mode is the **default** surface (`internal/config/config.go:276` — `CodeModeCodemode = "codemode"`, and `docs/developer/tools.md:18` — "The default surface is code mode"). The code-mode branch of `toolsFor` was added in `ad63f36 feat!: make code mode the default tool surface`, whose commit message states children get the same surface as the coordinator. Inspect mode was not covered.

## Reproduction

A probe test in `internal/subagent` (removed after the run) comparing all four combinations:

```
codeMode=true  mode=inspect asked=[read_file grep glob]                        got=[]
codeMode=true  mode=work    asked=[script]                                     got=[script]
codeMode=false mode=inspect asked=[read_file grep glob]                        got=[glob grep read_file]
codeMode=false mode=work    asked=[bash read_file write_file edit_file grep glob script] got=[bash edit_file glob grep read_file script write_file]
```

Observed end to end on this checkout (`model: deepseek/deepseek-v4.1-flash`, default code mode) with three inspect calls:

- A prompt with no tool need answered normally (`PONG`).
- `Read the file .../MISSION.md and reply with only its first heading line` replied `I'll read the file and return the first heading line.` — a promise, no read, no answer.
- A prompt naming the tool explicitly emitted a malformed tool call as **plain text**: literal `<|DSML| calls>` / `<|DSML| invoke name="read_file">` markup. With an empty spec list there is no tool for the model to call, so the helper model invents its own dialect.

## Impact

Delegated read-only investigation — the shipped workflow advertised by the `agent` tool's own description ("For independent investigations, issue several mode=inspect calls together") — produces confident-sounding reports with no evidence behind them. Because the child exits `ok=true` with output text, the coordinator has no signal that nothing was read. This is exactly the failure `MISSION.md` forbids: "Make state transitions and failures visible rather than silently recovering or discarding work", and it contradicts two guarantees:

- `docs/developer/tools.md:28` — "Inspect children receive only `read_file`, `grep`, and `glob`."
- `docs/developer/tools.md:137-138` — "Search applies no path confinement of its own... The sandbox is the boundary" (a boundary that assumes the tools exist at all).

It affects every default-configured session, not an edge case: code mode is the default, and the `agent` tool is registered in interactive chat.

## Why the existing tests miss it

`internal/subagent/runner_test.go:200-211` (`TestInspectModeNeverGetsScript`) is the test that looks like it covers this. It builds `&AgentRunner{Root: dir, Script: true}` — `CodeMode` is left `false`, so it always exercises the classic branch, where the whitelist does resolve. Its assertion `slices.Equal(names, []string{"glob", "grep", "read_file"})` would fail under code mode.

`internal/subagent/supervisor_test.go:282-286` asserts on `sup.toolsFor(...)` output — the *requested* names — not on the registry a child actually receives, so it cannot see the mismatch either.

## Relationship to the headless guard

`cmd/neo/main.go:909-912` already refuses a headless run that would advertise zero tools, naming both ways out. The same reasoning applies here and the team has already accepted it as an error state worth failing on; the subagent path has no equivalent check.

## Acceptance criteria

- An inspect child under code mode receives `read_file`, `grep`, and `glob`, matching `docs/developer/tools.md:28`.
- The inspect whitelist stays read-only: no `script`, no `bash`, no write tools. `TestInspectModeNeverGetsScript` keeps passing and is extended to run with `CodeMode: true` as well as `false`.
- A test asserts on the registry a child *actually receives* (the `Filter` result), for both modes under both surfaces — the four-way table above becomes a table-driven test.
- A child that ends up with an empty registry is either impossible by construction or reported as an error rather than returning `ok=true`, in the spirit of `cmd/neo/main.go:909`.
- `go test -race ./internal/subagent/ ./internal/tools/ ./cmd/neo/` passes.

## Notes

Found while auditing this repository at `899b7b5`. The malformed `<|DSML|>` text is a symptom of the empty spec list, not a separate parser defect: `internal/llm/chatcompletions/client.go:314-322` correctly reads `tool_calls` from the wire, and no code in this repository emits or parses that markup (`grep -rn 'DSML'` returns nothing). A provider that returns tool calls only in a private in-band format would be a separate finding, and this one would need re-checking once the spec list is non-empty.
