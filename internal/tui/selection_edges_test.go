package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

// TestDragFarRightCopiesFullRowsButStopsAtRowEnd checks a common gesture: drag
// to the right edge and down, expecting whole lines. The viewport pads rows, so
// clamping the column must copy the text, not the padding.
func TestDragFarRightCopiesFullRowsButStopsAtRowEnd(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 500, Y: 0, Button: tea.MouseLeft}))
	res := m.handleMsg(tea.MouseReleaseMsg(tea.Mouse{X: 500, Y: 0, Button: tea.MouseLeft}))
	msg, ok := res.cmds[0]().(clipboardResultMsg)
	if !ok {
		t.Fatal("no copy result")
	}
	if msg.text != "alpha beta" {
		t.Fatalf("copied %q, want %q without padding", msg.text, "alpha beta")
	}
}

// TestDragAboveThePaneClampsToTheFirstRow keeps a drag that starts or ends in
// the composer from selecting a negative row.
func TestDragAboveThePaneClampsToTheFirstRow(t *testing.T) {
	t.Parallel()

	// The pane is already at the top, so the focus row clamps to row 0 and the
	// drag becomes a backwards one from column 2 on row 1 to column 2 on row 0.
	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 1, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 2, Y: -40, Button: tea.MouseLeft}))
	res := m.handleMsg(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: -40, Button: tea.MouseLeft}))
	msg, ok := res.cmds[0]().(clipboardResultMsg)
	if !ok {
		t.Fatal("no copy result")
	}
	if want := "pha beta\ngam"; msg.text != want {
		t.Fatalf("copied %q, want %q", msg.text, want)
	}
}

// TestRightClickDoesNotSelect keeps the context-menu button out of the drag
// path, so a future right-click menu cannot start a selection.
func TestRightClickDoesNotSelect(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 1, Y: 1, Button: tea.MouseRight}))
	if m.selection != nil {
		t.Fatal("right click started a selection")
	}
}

// TestDragPastTheTopEdgeScrollsAndKeepsTheSelectionAnchored lets a drag reach
// text that was off screen when the press happened. The anchor is stored in
// transcript rows, so scrolling must extend the selection rather than move it.
func TestDragPastTheTopEdgeScrollsAndKeepsTheSelectionAnchored(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.viewport.SetWidth(20)
	m.viewport.SetHeight(3)
	m.viewport.SetContent(strings.Join([]string{
		"line zero", "line one", "line two", "line three", "line four", "line five",
	}, "\n"))
	m.viewport.GotoBottom()
	offsetBefore := m.viewport.YOffset()
	if offsetBefore == 0 {
		t.Fatal("fixture must be scrolled to test scrolling during a drag")
	}

	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 1, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 0, Y: -1, Button: tea.MouseLeft}))

	if got := m.viewport.YOffset(); got != offsetBefore-1 {
		t.Fatalf("y offset = %d, want %d after dragging past the top", got, offsetBefore-1)
	}
	if m.selection == nil {
		t.Fatal("dragging past the edge ended the selection")
	}
	if m.selection.anchor.row != offsetBefore+1 {
		t.Fatalf("anchor row = %d, want %d: scrolling moved the anchor", m.selection.anchor.row, offsetBefore+1)
	}
}
