package tui

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// pickerTarget points at one browser's query, cursor, and error fields so the
// shared key handler works for both the /model and /provider pickers without
// knowing which one it drives. The callbacks carry the parts that genuinely
// differ: what closing, moving, clamping, and choosing mean for that picker.
type pickerTarget struct {
	query    *string
	selected *int
	err      *error

	close  func()
	move   func(delta int)
	ensure func()
	choose func()
}

// handlePickerKey applies one key press to a picker. Both browsers respond to
// the same keys, so the behavior lives here once instead of twice.
func (m *model) handlePickerKey(msg tea.KeyMsg, t pickerTarget) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" || key == "ctrl+d" {
		return m, m.requestQuit()
	}
	if m.quitPending {
		return m, nil
	}
	switch key {
	case "esc":
		t.close()
	case "up":
		t.move(-1)
	case "down":
		t.move(1)
	case "backspace", "delete":
		if *t.query != "" {
			*t.query = dropLastRune(*t.query)
			*t.err = nil
			t.ensure()
		}
	case "enter":
		t.choose()
	default:
		s := msg.String()
		if utf8.RuneCountInString(s) == 1 && s >= " " && s != "\x7f" {
			*t.query += s
			*t.err = nil
			t.ensure()
		}
	}
	return m, nil
}

// moveSelection wraps the cursor by delta, keeping it inside the list.
func moveSelection(items int, selected *int, delta int) {
	if items == 0 {
		*selected = 0
		return
	}
	*selected = (*selected + delta + items) % items
}

// clampSelection pulls the cursor back inside the list after the query
// narrowed it.
func clampSelection(items int, selected *int) {
	if items == 0 {
		*selected = 0
		return
	}
	if *selected >= items {
		*selected = items - 1
	}
	if *selected < 0 {
		*selected = 0
	}
}
