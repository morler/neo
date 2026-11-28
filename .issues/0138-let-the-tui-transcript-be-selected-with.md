---
id: 138
title: Let the TUI transcript be selected with the mouse and copied
status: closed
priority: medium
labels:
    - tui
    - feature
created: "2026-11-28"
updated: "2026-11-28"
closed: "2026-11-28"
---

Neo's TUI turns on cell-motion mouse reporting so the wheel scrolls the transcript. The alt screen plus that mode means a plain drag goes to the program, not the terminal, so output text can only be selected with shift held — users report that as "cannot select copy".

Add in-app drag selection over the transcript viewport.

## Acceptance

- Press left button inside the transcript, drag, release: the covered text is highlighted while dragging and written to the system clipboard on release.
- Copy uses OSC52 (works over SSH) with a local-tool fallback (`wl-copy`, `xclip`, `xsel`, `pbcopy`).
- A click without drag clears the selection and copies nothing.
- Starting a turn, `/clear`, or resizing clears a stale selection.
- Wheel scrolling still works; shift+drag remains the terminal-native path.
- Tests cover hit testing, selection text extraction (including wide CJK characters), clipboard fallback ordering, and the failure notice.
