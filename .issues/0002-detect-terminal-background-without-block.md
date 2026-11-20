---
id: 2
title: Detect terminal background without blocking startup
status: closed
priority: high
labels:
    - performance
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

internal/tui/newModel calls lipgloss.HasDarkBackground synchronously before Bubble Tea starts. The query waits 2s on stdin and 2s on stdout unless the terminal answers DA1, delaying the first frame by up to 4s. Default to dark and request the background colour through Bubble Tea, applying the light style when BackgroundColorMsg arrives.
