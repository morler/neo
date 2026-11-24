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

- the stripped binary must remain at or below 20 MiB;
- the system prompt must remain at or below 1,400 bytes for the core prompt and
  2,600 bytes for the chat prompt, which adds the workflow and delegation
  sections that only exist where those tools do;
- the fixed floor — the tool schemas plus the chat system prompt with the
  built-in skill catalog — must remain at or below 8,500 bytes. It was 8,084
  when the budget was set (5,022 for nine tool definitions, 3,062 for the
  prompt).

The binary budget is checked by `just performance`. The prompt and fixed-floor
budgets are normal Go tests, so CI checks them without running timing
benchmarks. `TestFixedPromptFloorBudget` in `cmd/neo/performance_test.go` also
asserts that two renders of the same session state produce a byte-identical
cacheable prefix, which is what keeps prompt caching worth anything.

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
