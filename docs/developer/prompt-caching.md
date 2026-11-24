# Prompt Caching

Neo can pass the system prompt as ordered `llm.SystemBlock` values.

The flattened `Request.System` string remains available for providers that do not support structured system prompts. Providers that do support caching should prefer `Request.SystemBlocks` when present.

## Current Block Layout

1. Static base instructions plus the skill catalog. This block is marked cacheable when `features.prompt_caching` is enabled.
2. Dynamic environment section: working directory, repository root, platform, date. Not cacheable.
3. Dynamic AGENTS.md project context. Not cacheable.

The goal is to cache stable instructions without letting the per-session tail evict that prefix.

## Conversation Breakpoint

The system prefix is only a few thousand tokens. By mid-session the transcript
dominates the request, so the Anthropic adapter also marks the last content
block of the last message. The cached prefix is then tools + system + the whole
conversation, and each turn only writes the messages added since the last one.

One rolling breakpoint is enough. A cache entry outlives the request that
created it, so the previous turn's entry is still matched as a prefix after the
breakpoint has moved past it.

`wireMessages` decides whether to place it from the system blocks rather than a
separate request field, since `features.prompt_caching` governs both and one
signal keeps them from drifting apart. Other providers do not place a
conversation breakpoint; nor do subagents or the compactor, which call
`Complete` with a plain `System` string and no blocks.

## Token Accounting

`llm.Usage` partitions the prompt across `InputTokens`, `CacheCreationTokens`,
and `CacheReadTokens`: they never overlap, and `Usage.PromptTokens()` is their
sum. Providers whose APIs report the cached count as a subset of the total
(OpenAI Responses, Google) subtract it in their adapter. Compaction depends on
this, so a mistake here makes a cached session compact at the wrong size.

`/usage` is the user-facing view of these figures, and its cache hit rate
divides `CacheReadTokens` by the same `PromptTokens()` sum. A reporting change
that used a different denominator would disagree with compaction about how full
the prompt is.

## Prefix Stability Contract

A cache hit needs the request to start with exactly the bytes it started with
last time. Everything below the breakpoint can change; everything above it must
not.

Frozen for the life of a session:

- the tool list, its order, and each tool's schema text;
- the base system block (`SystemBlocks[0]`, marked `Cache`);
- every message already sent, verbatim.

Allowed to change the prefix, because they start a new cache lineage:

- `/clear`, which drops the transcript;
- `/provider` and `/model`, which change the backend the prefix is cached for;
- compaction, which rewrites the oldest messages into one summary.

`cmd/neo/performance_test.go` enforces the contract: `TestFixedPromptFloorBudget`
renders the same session state twice and fails if the base block or the tool
schemas differ, and it budgets their combined size. Anything that would make
the cached prefix depend on time, call count, or filesystem order breaks that
test rather than quietly costing every turn.
