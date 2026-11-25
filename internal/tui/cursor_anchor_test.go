package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestViewCursorFollowsTheComposer covers #107: fcitx5 put its candidate window
// in the bottom-right corner because makeView never set tea.View.Cursor. With no
// cursor declared, the terminal leaves it where the last frame stopped drawing,
// so an input method has no anchor near the text being typed.
//
// The assertions are geometric rather than textual on purpose: the defect is a
// coordinate, so the test has to check the coordinate lands on the composer and
// moves with the insertion point.
func TestViewCursorFollowsTheComposer(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("hello")
	m.input.CursorEnd()

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("the view declares no cursor, so an input method cannot place its candidate window")
	}
	startY, startX := view.Cursor.Y, view.Cursor.X

	// The cursor must sit inside the rendered frame, on the composer row rather
	// than on the transcript, the status line or the footer.
	rows := strings.Split(plain(view.Content), "\n")
	if startY < 0 || startY >= len(rows) {
		t.Fatalf("cursor row %d is outside the %d-row frame", startY, len(rows))
	}
	if !strings.Contains(rows[startY], "hello") {
		t.Fatalf("cursor row %d does not hold the composer text: %q", startY, rows[startY])
	}
	// And it must be on the default surface, not in the corner bubbletea leaves
	// it in when the view declares nothing.
	if startX <= 0 {
		t.Fatalf("cursor column %d is not past the composer prompt", startX)
	}

	// Typing more must move it: a frozen anchor would also be "inside the row".
	m.input.SetValue("hello world")
	m.input.CursorEnd()
	moved := m.View().Cursor
	if moved == nil {
		t.Fatal("cursor disappeared after more input")
	}
	if moved.X <= startX {
		t.Fatalf("cursor column %d did not advance past %d", moved.X, startX)
	}
}

// TestViewCursorIsAbsentWhenTheComposerIsNotFocused pins the other half: the
// anchor is only meaningful for a focused composer. An approval bar replaces the
// composer and a browser covers the screen, so a cursor there would point at
// something the user is not typing into.
func TestViewCursorIsAbsentWhenTheComposerIsNotFocused(t *testing.T) {
	m := makeTestModel()
	m.input.Blur()
	if cur := m.View().Cursor; cur != nil {
		t.Fatalf("blurred composer still reported a cursor at %+v", cur.Position)
	}

	m.input.Focus()
	m.width = 0
	if cur := m.View().Cursor; cur != nil {
		t.Fatalf("unknown width still reported a cursor at %+v", cur.Position)
	}
}

// TestComposerCursorSitsInsideTheInputBar checks the offsets against the
// rendered block instead of hardcoding padding: if styInputBar ever gains a
// border, the anchor follows it rather than drifting off the row.
func TestComposerCursorSitsInsideTheInputBar(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("x")
	m.input.CursorEnd()

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("no cursor")
	}
	rows := strings.Split(plain(view.Content), "\n")

	// The cursor belongs on the composer's text row: the one carrying the
	// textarea's prompt, between the bar's top and bottom padding. Checking the
	// prompt rather than a row index keeps the test independent of what is
	// stacked below the composer (the footer, a picker).
	pos := view.Cursor.Position
	if pos.Y < 0 || pos.Y >= len(rows) {
		t.Fatalf("cursor row %d is outside the %d-row frame", pos.Y, len(rows))
	}
	textRow := rows[pos.Y]
	if !strings.Contains(textRow, "› x") {
		t.Fatalf("cursor row %d is not the composer text row: %q", pos.Y, textRow)
	}
	// The composer's text always starts with the bar's left padding, so a cursor
	// at or left of it would be in the padding rather than after the text.
	if pos.X <= styInputBar.GetPaddingLeft() {
		t.Fatalf("cursor column %d is not past the composer's left padding", pos.X)
	}
	if pos.X > lipgloss.Width(textRow) {
		t.Fatalf("cursor column %d is past the end of row %q", pos.X, textRow)
	}
}
