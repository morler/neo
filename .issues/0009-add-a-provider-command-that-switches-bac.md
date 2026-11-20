---
id: 9
title: Add a /provider command that switches backend and persists the choice
status: closed
priority: high
labels:
    - feature
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

The TUI can switch models with /model but cannot change the LLM backend without editing neo.yaml and restarting.

Add a /provider slash command that:
- lists every usable backend (built-ins plus declared custom providers) in a searchable picker, marking the active one
- builds the selected provider through the existing credential checks, so a missing API key or subscription login fails before anything changes
- moves the chat agent and the follow-the-coordinator subagent runner to the new backend and its default (or last used) model
- rewrites the provider: and model: values in the config file Neo actually loaded (~/.neo/config.yaml when running on embedded defaults), leaving the rest of the file untouched
- updates the footer, the /model list, and the session metadata

Acceptance: switching providers works without restart, the next start uses the persisted backend, a failed switch leaves the running backend and the config unchanged.
