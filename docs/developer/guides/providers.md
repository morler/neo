# Providers

## The Simple Idea

A provider is the adapter between Neo and a model API. Neo speaks its own small internal language; providers translate that into Anthropic, OpenAI, OpenRouter, or Gemini requests, or into the OpenAI-compatible Chat Completions calls a custom backend exposes.

## The Problem

Different model APIs use different request shapes, response shapes, tool-call formats, auth methods, retry behavior, and token accounting. OpenRouter uses an OpenAI-compatible Chat Completions shape while OpenAI itself uses the newer Responses API in Neo.

Users also run their own endpoints — Ollama, vLLM, LiteLLM, or an internal gateway — that will never get a dedicated adapter in this repository.

Neo should not bake any one provider into the agent loop.

## How Neo Solves It

Neo defines one provider interface:

```go
type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (*Response, error)
}
```

The core loop sends an `llm.Request`. The provider returns an `llm.Response`. Everything provider-specific stays behind the adapter.

## Streaming

`Complete` is blocking by design and always returns a finished response, but the
Anthropic adapter uses the streaming endpoint underneath and reassembles the
events in `stream.go`. Nothing is emitted as it arrives: Neo renders completed
blocks, so streaming here is a transport decision, not a UI one.

Two reasons for it:

- No fixed client deadline. A `http.Client.Timeout` caps how long one generation
  may take regardless of progress. The Anthropic client sets no timeout and lets
  the caller's context bound the request; every caller has one (Ctrl-C in chat,
  `--timeout` headless).
- The Messages API requires streaming above a `max_tokens` threshold, so raising
  `defaultMaxTokens` will not need another transport change.

Adapters must propagate the error from reading a response body. Discarding it
turns a cancelled request into an apparently successful empty body, which then
surfaces as a decode failure rather than the cancellation that happened.

## Current Providers

| Provider config | Auth | Adapter |
| --- | --- | --- |
| `provider: anthropic` | `ANTHROPIC_API_KEY` | `internal/llm/anthropic` |
| `provider: openai` + `openai_auth: api_key` | `OPENAI_API_KEY` | `internal/llm/openai.Client` |
| `provider: openai` + `openai_auth: subscription` | `neo login` device-code credentials | `internal/llm/openai.CodexClient` |
| `provider: openrouter` | `OPENROUTER_API_KEY` | `internal/llm/openrouter` |
| `provider: google` | `GOOGLE_API_KEY` | `internal/llm/google` |
| `provider: <custom>` declared under `providers:` | `api_key_env`, or no auth at all | `internal/llm/custom` over `internal/llm/chatcompletions` |

When an API-key provider is selected without its key, Neo names the required
environment variable, links to the provider's key-creation page, shows an
`export` command with a placeholder value, and suggests `neo doctor` to check
the local setup. This guidance does not apply to OpenAI subscription auth,
which uses `neo login` instead.

## Custom Providers

The four built-in adapters exist because their APIs differ. A custom backend is
the opposite case: it speaks the same Chat Completions shape OpenRouter already
uses, so `internal/llm/custom` only fills in a client rather than translating a
new protocol.

```go
client, err := custom.New(custom.Spec{
    Name:         "local",
    Endpoint:     "http://127.0.0.1:8000/v1", // /chat/completions is appended
    APIKeyEnv:    "LOCAL_LLM_API_KEY",        // "" means no Authorization header
    DefaultModel: "qwen3-coder",
})
```

That constructor returns a `*chatcompletions.Client`, so custom backends
inherit the same retry policy, error shaping, and idle-read protection as every
other Chat Completions path. Nothing in `internal/agent` knows the difference
between a custom provider and a built-in one.

Two deliberate limits:

- The protocol is OpenAI Chat Completions only. An Anthropic-compatible gateway
  is not supported, and adding one would mean a real adapter, not a config key.
- The model list is static, taken from `providers.<name>.models`. Neo does not
  poll a custom endpoint for a catalogue, so `/model` shows what the config
  declares plus the model already in use.

`custom.Spec` is defined in the llm layer rather than reusing `config.CustomProvider`
so `internal/llm` keeps no dependency on `internal/config`. The CLI maps one to
the other at the composition boundary.

## How Models Are Chosen

The config `model` value is passed through to the provider. If omitted, Neo chooses a provider-aware default.

In the TUI, `/model` opens a model picker. It changes the active model for the current session, saves that session metadata, and rewrites `model:` in the config file Neo loaded (only `model:`; the active provider stays as it is), so the next launch starts on the same model. If that write fails, the previous model is restored and the picker shows the error.

`/provider` opens a provider picker. Choosing a provider builds it through the same code path as startup, so a missing API key or an incomplete `neo login` fails the switch with the provider's own error and the running backend stays where it was. On success the switch applies to the coordinator and to a subagent runner that follows it, and Neo rewrites `provider:` and `model:` in the config file it loaded. When that config came from embedded defaults, the rewritten file is created at `~/.neo/config.yaml`; everything else in the file is preserved, including comments and custom provider declarations.

## What To Be Careful About

- Provider adapters should translate, not decide product behavior.
- Retry and response parsing belong in provider packages.
- The core agent loop should not care whether a response came from Anthropic, OpenAI, OpenRouter, or Google, or from a custom backend.
- A custom `endpoint` using plain `http` sends the API key in clear text; that is the user's choice to make, but it must stay documented.
- Subscription/Codex auth is experimental and should be documented carefully.

## Where To Look

- `internal/llm/provider.go`: provider-neutral types.
- `internal/llm/anthropic`: Anthropic adapter.
- `internal/llm/openai`: OpenAI adapters.
- `internal/llm/chatcompletions`: reusable OpenAI-compatible Chat Completions translation.
- `internal/llm/openrouter`: OpenRouter adapter wiring.
- `internal/llm/custom`: builds a chat-completions client from a configured custom provider.
- `internal/llm/google`: Google Gemini adapter.
- `internal/auth`: subscription credential storage and refresh.
- `cmd/neo/main.go`: provider selection.
