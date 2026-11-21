---
id: 11
title: Fetch the live model catalogue for custom providers when /model opens
status: closed
priority: high
labels:
    - bug
    - tui
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## Problem

After /provider switches to a declared custom OpenAI-compatible backend, /model never requests the backend's model list. cmd/neo/provider.go modelChoicesLoader only returns a loader for openrouter, so custom providers fall back to the static providers.<name>.models list (or just the current model).

## Expected

Opening /model on a custom provider lazily fetches GET <endpoint-without-/chat/completions>/models with the configured api_key_env credential, using the existing 5s time-box, and shows the live list. The static models list stays as the immediate fallback and on fetch errors; a load error is reported in the picker rather than blocking chat startup.

## Acceptance

- /model on a custom provider issues one GET /models request when the picker opens.
- Live ids replace the static fallback; configured names/descriptions are kept for matching ids.
- A non-OK or malformed response keeps the fallback visible and shows the error in the picker.
- Built-in providers (anthropic/google/openai static lists) are unchanged; openrouter still fetches lazily.
- Unit tests in internal/llm/custom and cmd/neo; docs/CHANGELOG updated.
