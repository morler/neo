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
  script: false

output:
  verbose: false
```

The compaction window applies consistently to the coordinator and child
agents. Children that follow the coordinator use the active model after a
`/model` switch while retaining this configured window. Omitting the setting
keeps the built-in 200,000-token default for both. The trigger measures the
prompt size the provider reported for the previous request, so it accounts for
the system prompt and tool definitions rather than the transcript alone.

The trigger is 70% of that window: 140,000 tokens at the default, and 700,000
for a 1M-token model, so a large window is not compacted as if it were a small
one. After a compaction, the transcript must also grow by another 20% before it
is compacted again, which stops a summary from re-firing on the next turn just
because the kept tail is still near the trigger. The 70% ratio and the 20%
growth floor are not config keys. Two settings choose the window itself, and the
more specific one wins:

| Where | Scope |
| --- | --- |
| `providers.<name>.models[].context_window_tokens` | one model of a custom provider |
| `compaction.context_window_tokens` | every model in the file |

The model-level key exists because a window belongs to a model. A config that
serves a 40K local model and a 200K hosted one cannot share a single trigger: at
the 200,000 default the local model's prompt is refused long before 140,000, and
at 40,192 every hosted model would be compacted at 28,134. Resolution is by
(provider, model), so it follows a `/model` switch, and a child that runs on a
different model gets that model's window rather than the coordinator's.

The embedded source, including annotated provider examples, is
[`internal/config/defaults/neo.yaml`](https://github.com/owainlewis/neo/blob/main/internal/config/defaults/neo.yaml).

## Goals

`goals.max_attempts` bounds how long `/goal` keeps going on its own. An attempt
is one automatic continuation turn; the turn started by `/goal <objective>` is
not counted, and neither are the turns you type yourself.

```yaml
goals:
  max_attempts: 20
```

| Field | Default | Effect |
| --- | --- | --- |
| `max_attempts` | `20` | Continuation turns one goal may start. When they are used up the goal becomes `budget_limited` and stops with a handoff message. `/goal resume` gives it a fresh budget; `/goal clear` removes it. |

Omitting the section, or setting `max_attempts: 0`, uses the default. A negative
value is rejected when the config loads, naming `goals.max_attempts` and the
file it came from. There is no per-turn token or wall-clock budget in this
version: an attempt is the only unit, so the limit is predictable and cheap to
reason about.

A restored goal keeps the budget it was saved with, so resuming an old session
does not silently change how much work it has left. When the budget is used up
the handoff names the last recorded reason and the two ways forward:

```text
Goal budget reached: 20/20 attempts used.
Last reason: tests still fail in internal/session.
Use /goal resume to continue or /goal clear to stop.
```

`/goal resume` resets the counter to zero and starts the next continuation
immediately when nothing else is happening. See [Goals](goals.md) for the full
command surface and status model.

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

The top-level `provider` selects the backend for a session. In the TUI, `/model` lists models for that provider and switches the model and compactor for the current session. A successful switch also rewrites `model:` in the config file Neo loaded (`~/.neo/config.yaml` when the session runs on embedded defaults), leaving `provider:` and every other line as it was, so the next launch starts on the same model. A write failure restores the previous model and reports the error in the picker.

`/provider` switches the running session to another backend. It lists the built-in providers and every name declared under `providers:`, marks the active one, and validates credentials before changing anything. A successful switch moves the chat agent, and a subagent runner that follows the coordinator, to the new provider and to the model last used with it (or its `default_model`), updates the footer and `/model` list, saves the session metadata, and rewrites `provider:` and `model:` in the config file Neo loaded. When Neo is running on embedded defaults that file is `~/.neo/config.yaml`, created on demand from the embedded source; the rest of the file, including annotated examples, is left as it was. A failed switch leaves both the running backend and the file untouched.

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
| `models` | no | Static list for the `/model` picker. It is the immediate fallback: opening `/model` also fetches `GET <endpoint>/models` for the live catalogue, and those ids replace the static entries (matching ids keep the configured name and description). A failed fetch keeps the static list and shows the error in the picker. Each entry may also carry `context_window_tokens`, which overrides the global compaction window for that model; an id from the live catalogue that is not declared here falls back to the global key. |
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
- The live catalogue is read from `<endpoint>/models`, derived from the
  chat-completions endpoint, and accepts both `{"data":[{"id":...}]}` and a bare
  JSON array. It is optional: a gateway without that route leaves the static
  list in place and reports the fetch error inside `/model`.
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

The compaction window is resolved per model, in this order:

1. `providers.<name>.models[].context_window_tokens`, when set for that model.
2. A window the backend reported in its own catalogue, when it reports one.
3. `compaction.context_window_tokens` (200,000 in the embedded defaults).

Discovery is best effort and deliberately optional. Neo reads the same
`<endpoint>/models` response the picker already fetches and looks for a window
under the names seen in the wild: `context_length` (OpenRouter and most
gateways), `max_model_len` (vLLM), `context_window`/`max_context_length`, a
numeric string instead of a number, or llama.cpp's `meta.n_ctx`. Anything else —
a missing field, a value of the wrong type, an unreadable body — means "not
discovered", never an error.

The fetch runs in the background, so startup and a `/model` switch never wait on
it. A chat session refreshes its summarizer when the answer arrives, and
`neo run` waits for it within its own deadline, because a headless run is a
single batch where a later answer would be useless. When discovery fails the
configured or default window is used unchanged.

Two consequences worth knowing. Discovery never overrides a value you wrote, and
it is never persisted: it is a runtime observation about a server Neo does not
control, so it cannot be mistaken for configuration. OpenAI's own `/v1/models`
reports only ids and owners, so a built-in provider keeps the global default.

`ContextWindow` in `internal/config` returns the window together with its
source (`config`, `discovered`, or `default`), so a caller can explain why
compaction fires where it does.

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
| `script` | `true` | Register the JavaScript `script` tool: the model writes code that can call `read_file`, `grep`, `glob`, `write_file`, `edit_file`, and in code mode `run()`. Set it to `false` for a session with no code execution. In chat, `/codemode on\|off` toggles it and writes the choice here. In code mode it leaves only the coordinating tools, so `neo run` refuses to start with this setting, since a headless session has no coordinating tools and would advertise none; use `codemode: classic` or turn `script` back on for headless runs. |
| `codemode` | `codemode` | The surface shape, read once at startup. `codemode` (default): every file and command action is written as code inside one `script` tool, which carries `run()`, `console`/`print`, and one interpreter per session; `agent`, `goal` and `workflow` stay available because they coordinate rather than act. `classic`: keep the separate `bash`, `read_file`, `write_file`, `edit_file`, `grep` and `glob` tools. Any other value is rejected at startup rather than treated as the default, so a typo cannot silently change the surface. Measured 2026-11-25 on one debugging task, five runs each: code mode finished 5/5 with 6 requests and 17,135 input tokens (median), classic 5/5 with 8 and 23,406, and tool errors were 0–2 against 1–2. #91 records the measurement and why merging read/write into one call is still rare. |

Interrupting a running goal turn with `esc` or quitting with `ctrl+c` pauses the
goal instead of letting it continue, so a canceled turn never loops. `/goal
resume` starts it again with a fresh attempt budget.

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
