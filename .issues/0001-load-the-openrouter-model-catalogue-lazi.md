---
id: 1
title: Load the OpenRouter model catalogue lazily
status: closed
priority: high
labels:
    - performance
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

Starting interactive chat with provider openrouter blocks the TUI on a synchronous GET /models request (5s timeout, no cache) before the first frame. Defer the fetch until the model picker opens, keep the static default visible meanwhile, and never write the catalogue warning to stderr from inside the TUI.
