# Neo Developer Docs

These docs are intended for both humans and Neo itself. Update them alongside behavior changes.

Start here when changing Neo:

- [Architecture](architecture.md) explains the module boundaries.
- [CLI](cli.md) documents commands and startup flow.
- [Configuration](config.md) documents config loading and feature flags.
- [Sessions](sessions.md) documents persisted conversations.
- [Goals](goals.md) documents `/goal`, continuation turns, and the attempt budget.
- [Tools](tools.md) documents the built-in tool surface.
- [Skills](skills.md) documents built-in and user-defined slash prompts.
- [Prompt caching](prompt-caching.md) documents structured system prompt blocks.
- [Code quality tooling](code-quality.md) documents qlty, supercov, and their environment.
- [Performance](../performance.md) records the binary, prompt, and token baselines
  and how to measure them.
- [Token budget design](../design/token-budget.md) explains the four rules behind
  the fixed-floor budget, the frozen prefix, and the compaction trigger; it also
  records which switches were deliberately not added.
- [Embedded JS batch tool design](../design/js-batch-tool.md) records why Neo runs
  model-written JavaScript through the `script` tool, which tools it may call,
  what it deliberately does not do, and what the engine costs in binary size.
- [Teaching guides](guides/index.md) explain the core ideas in plain language.
