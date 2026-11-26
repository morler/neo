---
id: 116
title: Discover skills whose directory is a symlink
status: closed
priority: high
labels:
    - bug
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

internal/skills.loadDir skips any entry whose DirEntry.IsDir() is false. DirEntry.IsDir uses lstat semantics, so a skill entry that is a symlink to a directory is dropped even though ~/.agents/skills/<name>/SKILL.md resolves fine. Skills are commonly symlinked into ~/.agents/skills from another checkout, so real user skills never appear in the catalog or the slash picker.

Acceptance: Load discovers a skill whose entry under a skills directory is a symlink to a directory containing SKILL.md; a dangling symlink and a symlink to a non-skill directory are skipped without failing the load; name and override precedence are unchanged. Update the Load doc comment and docs/developer/skills.md, and add tests.
