package tui

import (
	"strings"
	"testing"
)

// TestMouseCoordinatesMatchRenderedTranscriptRows pins the one assumption the
// drag path makes about layout: row 0 of the terminal is row 0 of the
// transcript pane. If the View ever puts anything above the pane, a drag would
// select text the user did not point at.
func TestMouseCoordinatesMatchRenderedTranscriptRows(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.width = 60
	m.height = 20
	m.blocks = nil
	m.viewport.SetContent("")
	for _, line := range []string{"row zero", "row one", "row two", "row three"} {
		m.blocks = append(m.blocks, textBlock{text: line})
	}
	m.md = nil
	m.layout()
	m.refreshViewport()

	view := plain(m.View().Content)
	lines := strings.Split(view, "\n")
	if len(lines) < 4 {
		t.Fatalf("view has %d lines, want at least 4:\n%s", len(lines), view)
	}
	// The viewport pads every row to the pane width, so compare the text.
	if got := strings.TrimRight(lines[0], " "); got != "row zero" {
		t.Fatalf("line 0 = %q, want the first transcript row", got)
	}
	// Blocks are separated by a blank row, so find the rendered row by its text
	// instead of assuming an index. What matters is that terminal row N indexes
	// pane row N: selecting on that row must copy that row's text.
	index := -1
	for i, line := range lines {
		if strings.TrimRight(line, " ") == "row two" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("no rendered row holds %q;\nfull view:\n%s", "row two", view)
	}

	point, ok := m.selectionPoint(0, index)
	if !ok {
		t.Fatalf("selectionPoint rejected a cell on rendered row %d", index)
	}
	if point.row != m.viewport.YOffset()+index {
		t.Fatalf("point row = %d, want %d", point.row, m.viewport.YOffset()+index)
	}
	start := point
	end := selPoint{row: point.row, col: 2}
	if got, want := selectionText(m.viewport.GetContent(), start, end), "row"; got != want {
		t.Fatalf("copied %q at the pointed-at row, want %q", got, want)
	}
}
