---
id: 113
title: Discover skills from the shared ~/.agents/skills directory
status: closed
priority: medium
labels:
    - feature
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

Neo currently discovers user-global skills only from ~/.neo/skills. Add ~/.agents/skills/<name>/SKILL.md as a shared, tool-agnostic user-level source so skills written for other agent tools work without copying.

Priority, lowest to highest: built-ins < ~/.agents/skills < ~/.neo/skills < <repo>/.neo/skills. The Neo-specific directory must still beat the shared one, and the project directory must beat both. A missing directory is skipped as today.

Update the Load doc comment, docs/developer/skills.md, ARCHITECTURE.md, the embedded neo.yaml comment, docs/developer/config.md, website quick-start, README, and add tests covering discovery, override order, and an unavailable HOME.
