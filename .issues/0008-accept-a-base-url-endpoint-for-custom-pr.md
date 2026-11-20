---
id: 8
title: Accept a base-URL endpoint for custom providers
status: closed
priority: high
labels:
    - feature
    - config
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

Custom providers currently require the full /chat/completions URL. Accept an OpenAI-SDK-style base URL (e.g. https://host/v1) and append /chat/completions during config normalization, while leaving an already-complete URL untouched. Update docs, examples, and tests.
