# Configuration

Neo loads the first available config:

1. `./neo.yaml`
2. `~/.neo/config.yaml`
3. Embedded defaults from `internal/config/defaults/neo.yaml`

First hit wins. Config files are not merged.

## Default Config

This is the complete configuration surface. Copy it to `~/.neo/config.yaml` or
`./neo.yaml` and change only the values you need.

```yaml
# LLM backend: anthropic, openai, openrouter, google, or a name declared
# under providers: (see Custom Providers below).
provider: anthropic

# OpenAI only: api_key uses OPENAI_API_KEY; subscription uses `neo login`.
# openai_auth: api_key

model: claude-opus-5

# Optional. When omitted, subagents follow the active provider and model.
# subagents:
#   provider: anthropic
#   model: <claude-model-id>

# Optional interactive confirmations. Empty by default.
# tool_approvals:
#   - git
#   - rm -rf
#   - write_file

# Transcripts compact once the provider reports a prompt at 70% of this size.
compaction:
  context_window_tokens: 200000

features:
  agents_file: true
  skills: true
  prompt_caching: true

output:
  verbose: false
```

The compaction window applies consistently to the coordinator and child
agents. Children that follow the coordinator use the active model after a
`/model` switch while retaining this configured window. Omitting the setting
keeps the built-in 200,000-token default for both. The trigger measures the
prompt size the provider reported for the previous request, so it accounts for
the system prompt and tool definitions rather than the transcript alone.

The embedded source, including annotated provider examples, is
[`internal/config/defaults/neo.yaml`](https://github.com/owainlewis/neo/blob/main/internal/config/defaults/neo.yaml).

## Provider Selection

| Config | Auth source | Provider adapter |
| --- | --- | --- |
| `provider: anthropic` | `ANTHROPIC_API_KEY` | `internal/llm/anthropic` |
| `provider: openai` with `openai_auth: api_key` | `OPENAI_API_KEY` | `internal/llm/openai.Client` |
| `provider: openai` with `openai_auth: subscription` | ChatGPT/Codex device-code credentials from `~/.neo/auth.json` | `internal/llm/openai.CodexClient` |
| `provider: openrouter` | `OPENROUTER_API_KEY` | `internal/llm/openrouter` |
| `provider: google` | `GOOGLE_API_KEY` | `internal/llm/google` |
| `provider: <custom>` | `providers.<custom>.api_key_env`, or none | `internal/llm/custom` over `internal/llm/chatcompletions` |

When `provider: openai` omits `model`, both API-key and subscription auth default
to `gpt-6.1-sol`. Explicit model settings and saved-session models are unchanged.

Subscription credentials are created with `neo login` and removed with `neo logout`. The docs describe only where credentials live and which flow uses them; token values are never generated into developer docs.

The top-level `provider` selects the backend for a session. In the TUI, `/model` lists models for that provider and switches the model and compactor for the current session without rewriting configuration. Start a new session with a different `provider` value to change backends.

## Custom Providers

Anyone running an OpenAI-compatible endpoint can declare it instead of waiting
for a built-in adapter. The endpoint must speak the Chat Completions API; Neo
has no Anthropic-compatible custom backend.

```yaml
provider: local
model: qwen3-coder

providers:
  local:
    endpoint: http://127.0.0.1:8000/v1
    api_key_env: LOCAL_LLM_API_KEY
    default_model: qwen3-coder
    models:
      - id: qwen3-coder
        name: Qwen3 Coder
        description: Local coder model
```

| Field | Required | Meaning |
| --- | --- | --- |
| `endpoint` | yes | Base URL of the backend, such as `https://host/v1`. Neo appends `/chat/completions`; an endpoint that already ends with that path is used unchanged. |
| `api_key_env` | no | Environment variable holding the key. Omit to send no `Authorization` header at all, which is the normal case for a local runtime. |
| `default_model` | no | Used when `model` is unset and the session records none. |
| `models` | no | Static list for the `/model` picker. Neo never fetches a catalogue from a custom backend, so an omitted list leaves the picker showing only the current model. |
| `api` | no | Wire protocol. Only `openai_compatible` exists, and it is the default. |

Resolution order is fixed: the four built-in names always win, then `providers`
entries are matched by name. A `providers` key that reuses a built-in name is
rejected rather than silently shadowing it. Both `provider` and
`subagents.provider` accept custom names.

Validation happens at startup, so mistakes surface before the first model call:

- Names are trimmed and must be non-empty and unique after trimming.
- `endpoint` must be an absolute `http` or `https` URL with a host.
- `api_key_env` must be a valid environment-variable name and cannot be shared
  by two providers; each backend reads its own variable.
- `models` entries need an `id`; duplicate ids keep the first occurrence.
- An unknown protocol in `api` is rejected.
- A configured `api_key_env` whose variable is unset fails immediately with the
  variable name, instead of surfacing as a 401 mid-conversation.

An unknown provider name fails with the list of usable names and a pointer to
the `providers` section, so a typo does not require reading these docs.

`neo doctor` reports the same state without contacting the endpoint: the
`provider` check shows `<name> (custom)`, and the `credentials` check either
names the missing `api_key_env` variable, confirms it is set, or states that the
endpoint needs no key. Its `model` check shows the model the chat would start
with, so a custom provider whose `model` and `default_model` are both missing is
visible before the first turn.

The compaction window still comes from `compaction.context_window_tokens`. Neo
cannot discover a custom model's context size, so set it explicitly for models
whose window differs from the 200,000-token default.

Note the security boundary: `endpoint` with a plain `http` scheme sends the API
key, and every prompt, in clear text over the network. Use `https` for anything
beyond a loopback address. Neo's logs redact the values of every configured
`api_key_env`, alongside the built-in provider keys.

## Feature Flags

Each feature flag is tri-state in Go: absent means use the built-in default, while explicit `false` disables that capability.

| Flag | Default | Effect |
| --- | --- | --- |
| `agents_file` | `true` | Load AGENTS.md into the chat system prompt. Project files are read through a rooted workspace boundary. Relative and absolute project symlinks load only when their resolved targets stay inside the workspace; escaping files are skipped with a warning. Safe project files and the explicit user-global `~/.neo/AGENTS.md` remain loaded. |
| `skills` | `true` | Discover skills and expand $name references or /name slash invocations. |
| `prompt_caching` | `true` | Mark the stable system prompt prefix as cacheable when the provider supports it. |

## Output

`output.verbose` is tri-state, same as feature flags: absent or `false` means concise mode (the default).

| Setting | Default | Effect |
| --- | --- | --- |
| `output.verbose: false` | (default) | Show live in-flight activity and concise completed receipts (e.g. a file read or command run). Errors, failures, and direct `!` command output always render in full. |
| `output.verbose: true` | | Restore full tool call/result cards, including complete file contents and command output. |

## Skills

Skills are files, not config. Neo always provides `/design`, `/plan`,
`/build`, and `/review`; add or replace one with
`.neo/skills/<name>/SKILL.md` in the project or `~/.neo/skills/<name>/SKILL.md`
globally. The `features.skills` flag controls discovery of those files; the
built-ins stay available either way. A `phases` key in `neo.yaml` is rejected
with a message pointing at the skill layout. See [Skills](skills.md).

## Tool Approvals

`tool_approvals` is an optional top-level list. It is empty by default and
applies only to interactive coordinator calls and direct `!` commands.

```yaml
tool_approvals:
  - git
  - rm -rf
  - write_file
```

Each entry matches both an exact tool name and the start of a Bash command.
Matching is literal and case-sensitive. The next command character must be
whitespace or the end, so `git` matches `git status` but not `github`.

Entries are trimmed when loaded. Empty entries are rejected and exact
duplicates keep their first position. Neo does not parse shell chains,
wrappers, aliases, variables, scripts, or substitutions.

This is optional user-interface friction, not a security boundary. It is not
passed to `neo run` or child agents. The VM or sandbox must control filesystem,
process, network, credentials, and external-service access.

The old `permissions:` section has been removed. Neo rejects it with migration
guidance instead of silently granting broader access.
