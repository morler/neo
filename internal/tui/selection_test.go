package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// selectableModel returns a model with a three-row transcript pane, so a drag
// map from terminal cells to transcript cells can be asserted without a
// terminal.
func selectableModel() *model {
	m := makeTestModel()
	m.width = 40
	m.viewport.SetWidth(20)
	m.viewport.SetHeight(3)
	m.viewport.SetContent(strings.Join([]string{
		"alpha beta",
		"gamma delta",
		"epsilon",
	}, "\n"))
	m.viewport.GotoTop()
	return m
}

func TestSelectionPointMapsTerminalCellsToTranscriptRows(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.viewport.SetContent(strings.Join([]string{
		"zero", "one", "two", "three", "four", "five",
	}, "\n"))
	m.viewport.SetYOffset(2)

	point, ok := m.selectionPoint(3, 1)
	if !ok {
		t.Fatal("selectionPoint rejected a cell inside the pane")
	}
	if point != (selPoint{row: 3, col: 3}) {
		t.Fatalf("selectionPoint = %+v, want row 3 col 3", point)
	}
}

func TestSelectionPointRejectsCellsOutsideThePane(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"left of pane", -1, 0},
		{"above pane", 0, -1},
		{"right of pane", m.viewport.Width(), 0},
		{"below pane", 0, m.viewport.Height()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := m.selectionPoint(tc.x, tc.y); ok {
				t.Fatalf("selectionPoint(%d, %d) = ok, want rejected", tc.x, tc.y)
			}
		})
	}
}

func TestSelectionTextCopiesTheCoveredCellsOnly(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		"alpha beta",
		"gamma delta",
		"epsilon",
	}, "\n")

	// From the 'p' of alpha across to the first 'm' of gamma. The release cell
	// is included, so gamma contributes its first three cells.
	got := selectionText(content, selPoint{row: 0, col: 3}, selPoint{row: 1, col: 2})
	if want := "ha beta\ngam"; got != want {
		t.Fatalf("selectionText = %q, want %q", got, want)
	}
}

func TestSelectionTextJoinsWideCharactersByCellWidth(t *testing.T) {
	t.Parallel()

	content := "日本語テスト"
	// Each CJK character is two cells, so cells 2..4 are 本語 (the start cell is
	// included and the end cell's character is included whole).
	got := selectionText(content, selPoint{row: 0, col: 2}, selPoint{row: 0, col: 5})
	if want := "本語"; got != want {
		t.Fatalf("selectionText = %q, want %q", got, want)
	}
}

func TestSelectionTextTrimsTrailingPaddingButKeepsIndent(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		"   indented line    ",
		"next              ",
	}, "\n")
	got := selectionText(content, selPoint{row: 0, col: 0}, selPoint{row: 1, col: 17})
	if want := "   indented line\nnext"; got != want {
		t.Fatalf("selectionText = %q, want %q", got, want)
	}
}

func TestSelectionTextStopsAtTheReleaseCell(t *testing.T) {
	t.Parallel()

	// Releasing on a cell copies that cell, so pressing and releasing on it
	// selects one character rather than nothing.
	got := selectionText("hello", selPoint{row: 0, col: 1}, selPoint{row: 0, col: 1})
	if want := "e"; got != want {
		t.Fatalf("selectionText = %q, want %q", got, want)
	}
}

func TestSelectionEmptyOnlyForAClick(t *testing.T) {
	t.Parallel()

	click := transcriptSelection{anchor: selPoint{row: 1, col: 2}, focus: selPoint{row: 1, col: 2}}
	if !click.empty() {
		t.Fatal("a press and release on one cell is not empty")
	}

	drag := transcriptSelection{anchor: selPoint{row: 1, col: 2}, focus: selPoint{row: 1, col: 1}}
	if drag.empty() {
		t.Fatal("a drag backwards over one cell is not empty")
	}
}

func TestNormalizedOrdersABackwardsDrag(t *testing.T) {
	t.Parallel()

	s := transcriptSelection{anchor: selPoint{row: 4, col: 9}, focus: selPoint{row: 1, col: 2}}
	start, end := s.normalized()
	if start != (selPoint{row: 1, col: 2}) || end != (selPoint{row: 4, col: 9}) {
		t.Fatalf("normalized = %+v..%+v, want row1col2..row4col9", start, end)
	}
}

func TestHighlightSelectionInvertsOnlyCoveredCells(t *testing.T) {
	t.Parallel()

	pane := "one two\nthree\nfour"
	out := highlightSelection(pane, selPoint{row: 0, col: 1}, selPoint{row: 0, col: 2}, 0)
	if got := ansi.Strip(out); got != ansi.Strip(pane) {
		t.Fatalf("highlight changed the text: %q, want %q", got, ansi.Strip(pane))
	}
	if got := selectedSpans(t, out); len(got) != 1 || got[0] != "ne" {
		t.Fatalf("selected spans = %q, want [ne] from %q", got, out)
	}
	if got := selectedSpans(t, strings.Split(out, "\n")[1]); len(got) != 0 {
		t.Fatalf("highlight leaked onto an uncovered row: %q", got)
	}
}

// selectionOpen is the escape prefix stySelection emits on its own. It is a
// prefix rather than a whole render because StyleRanges merges adjacent cells
// into one span, which makes stySelection.Render("ab") != Render("a")+Render("b").
func selectionOpen(t *testing.T) string {
	t.Helper()
	open, _, ok := strings.Cut(stySelection.Render("x"), "x")
	if !ok || open == "" {
		t.Fatalf("selection style emitted no escape prefix: %q", stySelection.Render("x"))
	}
	return open
}

// selectedSpans returns the text painted with the selection style, in order.
func selectedSpans(t *testing.T, s string) []string {
	t.Helper()
	open := selectionOpen(t)
	var spans []string
	for _, rest := range strings.Split(s, open)[1:] {
		text, _, _ := strings.Cut(rest, "\x1b[m")
		spans = append(spans, text)
	}
	return spans
}

func TestHighlightSelectionUsesTheViewportOffset(t *testing.T) {
	t.Parallel()

	// The pane shows transcript row 2 on its first line, so a selection on row 2
	// must highlight the first rendered line.
	pane := "first\nsecond"
	out := highlightSelection(pane, selPoint{row: 2, col: 0}, selPoint{row: 2, col: 1}, 2)
	if got := ansi.Strip(out); got != ansi.Strip(pane) {
		t.Fatalf("text changed: %q", got)
	}
	firstLine, secondLine, _ := strings.Cut(out, "\n")
	if got := selectedSpans(t, firstLine); len(got) != 1 || got[0] != "fi" {
		t.Fatalf("first pane row not highlighted for transcript row 2: %q from %q", got, out)
	}
	if got := selectedSpans(t, secondLine); len(got) != 0 {
		t.Fatalf("second pane row highlighted for a one-row selection: %q", got)
	}
}

func TestMouseDragCopiesTheSelectedText(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 1, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 4, Y: 1, Button: tea.MouseLeft}))
	res := m.handleMsg(tea.MouseReleaseMsg(tea.Mouse{X: 4, Y: 1, Button: tea.MouseLeft}))

	if len(res.cmds) != 1 {
		t.Fatalf("release produced %d commands, want one copy", len(res.cmds))
	}
	msg, ok := res.cmds[0]().(clipboardResultMsg)
	if !ok {
		t.Fatalf("copy command returned %T, want clipboardResultMsg", res.cmds[0]())
	}
	if msg.text != "gamma" {
		t.Fatalf("copied %q, want %q", msg.text, "gamma")
	}
	// The highlight is the visible receipt of what was copied, so it outlives
	// the release until the next interaction.
	if m.selection == nil {
		t.Fatal("release dropped the highlight that confirms the copy")
	}
	m.Update(keyPress('a'))
	if m.selection != nil {
		t.Fatal("a keypress left the highlight behind")
	}
}

func TestMouseClickWithoutDragCopiesNothing(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 1, Button: tea.MouseLeft}))
	res := m.handleMsg(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: 1, Button: tea.MouseLeft}))

	if len(res.cmds) != 0 {
		t.Fatalf("click produced %d commands, want none", len(res.cmds))
	}
	if m.selection != nil {
		t.Fatal("click left a selection behind")
	}
}

func TestMouseDragPastThePaneClampsToTheNearestRow(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 19, Y: 99, Button: tea.MouseLeft}))
	res := m.handleMsg(tea.MouseReleaseMsg(tea.Mouse{X: 19, Y: 99, Button: tea.MouseLeft}))

	msg, ok := res.cmds[0]().(clipboardResultMsg)
	if !ok {
		t.Fatal("release produced no copy command")
	}
	if want := "alpha beta\ngamma delta\nepsilon"; msg.text != want {
		t.Fatalf("copied %q, want %q", msg.text, want)
	}
}

func TestShiftDragStaysWithTheTerminal(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft, Mod: tea.ModShift}))
	if m.selection != nil {
		t.Fatal("shift+drag started an in-app selection")
	}
}

func TestClickOutsideThePaneCopiesNothing(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 30, Y: 0, Button: tea.MouseLeft}))
	if m.selection != nil {
		t.Fatal("click right of the transcript started a selection")
	}
}

func TestSelectionIsClearedWhenTheTranscriptChanges(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		apply func(m *model)
	}{
		{name: "wheel", apply: func(m *model) { m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp})) }},
		{name: "resize", apply: func(m *model) { m.Update(tea.WindowSizeMsg{Width: 60, Height: 20}) }},
		{name: "new turn", apply: func(m *model) { m.clearSelection() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := selectableModel()
			m.selection = &transcriptSelection{anchor: selPoint{row: 0, col: 0}, focus: selPoint{row: 0, col: 3}}
			tc.apply(m)
			if m.selection != nil {
				t.Fatalf("%s left a stale selection", tc.name)
			}
		})
	}
}

func TestBrowsersDoNotStartATranscriptSelection(t *testing.T) {
	t.Parallel()

	m := selectableModel()
	m.models.visible = true
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	if m.selection != nil {
		t.Fatal("a click over the model browser started a transcript selection")
	}
}

func TestCopySelectionFallsBackToOSC52WhenNoHelperWorks(t *testing.T) {
	t.Parallel()

	msg, ok := copySelectionWith("hello", func(string) error {
		return errors.New("no clipboard helper on PATH")
	})().(clipboardResultMsg)
	if !ok {
		t.Fatal("copy did not report a result")
	}
	if msg.via != "osc52" {
		t.Fatalf("via = %q, want osc52 fallback", msg.via)
	}
	if msg.chars != 5 {
		t.Fatalf("chars = %d, want 5", msg.chars)
	}
}

func TestCopySelectionReportsTheLocalPath(t *testing.T) {
	t.Parallel()

	var written string
	msg, ok := copySelectionWith("héllo", func(text string) error {
		written = text
		return nil
	})().(clipboardResultMsg)
	if !ok {
		t.Fatal("copy did not report a result")
	}
	if msg.via != "local" {
		t.Fatalf("via = %q, want local", msg.via)
	}
	if written != "héllo" {
		t.Fatalf("wrote %q, want %q", written, "héllo")
	}
	// Runes, not bytes: an accented character must not be counted twice.
	if msg.chars != 5 {
		t.Fatalf("chars = %d, want 5", msg.chars)
	}
}

func TestCopySelectionSkipsBlankText(t *testing.T) {
	t.Parallel()

	if msg := copySelectionWith("   ", func(string) error { return nil })(); msg != nil {
		t.Fatalf("blank copy returned %T, want nil", msg)
	}
}

func TestFinishClipboardCopySetsTheClipboardOnlyForOSC52(t *testing.T) {
	t.Parallel()

	local := makeTestModel()
	if res := local.finishClipboardCopy(clipboardResultMsg{chars: 3, text: "abc", via: "local"}); len(res.cmds) != 0 {
		t.Fatalf("local copy produced %d commands, want none", len(res.cmds))
	}
	if local.copyNotice != "copied 3 characters" {
		t.Fatalf("notice = %q", local.copyNotice)
	}

	remote := makeTestModel()
	res := remote.finishClipboardCopy(clipboardResultMsg{chars: 3, text: "abc", via: "osc52"})
	if len(res.cmds) != 1 {
		t.Fatalf("osc52 copy produced %d commands, want one", len(res.cmds))
	}
}

func TestViewHighlightsAnActiveSelection(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.width = 60
	m.height = 20
	m.blocks = []block{textBlock{text: "selectable output"}}
	m.md = nil
	m.layout()
	m.refreshViewport()

	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 9, Y: 0, Button: tea.MouseLeft}))

	view := m.View().Content
	if got := selectedSpans(t, view); len(got) != 1 || got[0] != "selectable" {
		t.Fatalf("view highlighted %q, want [selectable]", got)
	}
}

func TestStatusLineShowsTheCopyNoticeWhileIdle(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.copyNotice = "copied 12 characters"
	if got := plain(m.statusLine()); !strings.Contains(got, "copied 12 characters") {
		t.Fatalf("status line = %q", got)
	}

	// The confirmation is transient: the next key returns the line to "ready"
	// instead of leaving a stale notice on screen for the rest of the session.
	m.Update(keyPress('a'))
	if got := plain(m.statusLine()); strings.Contains(got, "copied") {
		t.Fatalf("status line kept a stale copy notice: %q", got)
	}
}
