package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/logx"
)

// Mouse selection over the transcript.
//
// Neo reports cell-motion mouse events so the wheel scrolls the transcript, and
// the alt screen hides the terminal's own scrollback. The cost is that a plain
// drag never reaches the terminal: it arrives here as a press, motion, and
// release, so text could only be selected with shift held. This file gives the
// drag meaning inside the program: the covered cells are painted with the
// selection style while dragging and written to the clipboard on release.
// Shift+drag still goes to the terminal, for users who want the terminal's own
// selection.
//
// A selection is stored in absolute transcript rows, not in pane rows, so the
// highlight stays on the same text if the transcript scrolls during the drag.
// Only what is on screen is copied: the viewport renders fixed-width rows, so a
// selection maps onto rendered cells rather than onto the logical text.

// selPoint is one cell inside the transcript: an absolute row and a display
// column. The column counts cells, not bytes or runes, so a wide CJK character
// occupies two columns.
type selPoint struct {
	row, col int
}

// transcriptSelection is the drag in progress. The cell under the press and the
// cell under the pointer are both part of the selection, which is how a text
// selection behaves in an editor: press on a character and release on the next
// one to copy both.
type transcriptSelection struct {
	anchor selPoint
	focus  selPoint
}

// normalized orders the ends of the drag, which may run right-to-left or
// bottom-to-top.
func (s transcriptSelection) normalized() (selPoint, selPoint) {
	start, end := s.anchor, s.focus
	if end.row < start.row || (end.row == start.row && end.col < start.col) {
		return end, start
	}
	return start, end
}

// empty reports a press and release on the same cell. A click is not a copy: it
// must not replace the clipboard or leave a one-cell highlight behind.
func (s transcriptSelection) empty() bool {
	start, end := s.normalized()
	return start == end
}

// selectionCellRange returns the half-open column range this selection covers
// on row, and whether the row is covered at all. The highlight and the copied
// text both go through it, so they cannot drift apart.
func selectionCellRange(row int, start, end selPoint, width int) (int, int, bool) {
	if row < start.row || row > end.row {
		return 0, 0, false
	}
	left, right := 0, width
	if row == start.row {
		left = start.col
	}
	if row == end.row {
		right = end.col + 1
	}
	left = clampInt(left, 0, width)
	right = clampInt(right, 0, width)
	if right <= left {
		return 0, 0, false
	}
	return left, right, true
}

// selectionText renders the selected cells as plain text. It copies what is on
// screen: trailing padding is trimmed from every row, leading indentation is
// kept, and a selection that ends mid-row stops at that cell.
func selectionText(content string, start, end selPoint) string {
	if content == "" {
		return ""
	}
	rows := strings.Split(content, "\n")
	last := len(rows) - 1
	start.row = clampInt(start.row, 0, last)
	end.row = clampInt(end.row, 0, last)
	start.col = max(start.col, 0)
	end.col = max(end.col, 0)
	if start.row == end.row && start.col > end.col {
		start.col, end.col = end.col, start.col
	}

	var parts []string
	for row := start.row; row <= end.row; row++ {
		plain := ansi.Strip(rows[row])
		left, right, ok := selectionCellRange(row, start, end, ansi.StringWidth(plain))
		if !ok {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, strings.TrimRight(ansi.Cut(plain, left, right), " "))
	}
	return strings.Join(parts, "\n")
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// highlightSelection paints the selected cells of a rendered pane. offset is
// the absolute transcript row shown on the pane's first line, which is the
// viewport's Y offset.
func highlightSelection(pane string, start, end selPoint, offset int) string {
	lines := strings.Split(pane, "\n")
	for i, line := range lines {
		width := ansi.StringWidth(ansi.Strip(line))
		left, right, ok := selectionCellRange(offset+i, start, end, width)
		if !ok {
			continue
		}
		lines[i] = lipgloss.StyleRanges(line, lipgloss.NewRange(left, right, stySelection))
	}
	return strings.Join(lines, "\n")
}

// transcriptPane renders the viewport with any mouse selection inverted. It is
// the one place the pane is drawn, so the highlight cannot be skipped on a code
// path that renders the view differently.
func (m *model) transcriptPane() string {
	pane := m.viewport.View()
	if m.selection == nil {
		return pane
	}
	start, end := m.selection.normalized()
	return highlightSelection(pane, start, end, m.viewport.YOffset())
}

// clearSelection drops the highlight without copying. Any event that changes
// what the highlighted cells mean calls it: a new turn, /clear, a resize, or a
// scroll that would leave the highlight pointing at unrelated text.
func (m *model) clearSelection() {
	if m.selection != nil {
		m.selection = nil
	}
}

// selectionPoint maps a terminal cell onto the transcript. It reports false for
// anything outside the pane, which keeps a click on the composer, status line,
// or footer from starting a selection.
func (m *model) selectionPoint(x, y int) (selPoint, bool) {
	if x < 0 || y < 0 || x >= m.viewport.Width() || y >= m.viewport.Height() {
		return selPoint{}, false
	}
	return selPoint{row: m.viewport.YOffset() + y, col: x}, true
}

// selectionPointClamped keeps a drag that leaves the pane attached to the
// nearest transcript cell instead of ending the selection. Clamping the column
// is what lets a drag past the right edge copy whole rows.
func (m *model) selectionPointClamped(x, y int) selPoint {
	top := m.viewport.YOffset()
	return selPoint{
		row: clampInt(top+max(y, 0), top, top+max(m.viewport.Height()-1, 0)),
		col: max(x, 0),
	}
}

// transcriptSelectable reports whether a mouse event belongs to the transcript
// rather than an overlay. A browser owns the screen, and a shift-drag is the
// terminal's own selection, which the program must not intercept.
func (m *model) transcriptSelectable(mod tea.KeyMod) bool {
	if m.width == 0 || m.quitting || m.quitPending {
		return false
	}
	if m.providers.visible || m.models.visible || m.sessions.visible {
		return false
	}
	return mod&tea.ModShift == 0
}

func (m *model) handleMouseClick(msg tea.MouseClickMsg) updateResult {
	m.clearApprovalSelection()
	m.selection = nil
	if msg.Button != tea.MouseLeft || !m.transcriptSelectable(msg.Mod) {
		return updateResult{}
	}
	point, ok := m.selectionPoint(msg.X, msg.Y)
	if !ok {
		return updateResult{}
	}
	m.selection = &transcriptSelection{anchor: point, focus: point}
	return updateResult{}
}

func (m *model) handleMouseMotion(msg tea.MouseMotionMsg) updateResult {
	// Motion only arrives between a press and a release while a button is held,
	// so the presence of a selection is the state to trust. Terminals disagree
	// on which button a motion event reports; requiring a specific one would
	// end the drag on some of them.
	if m.selection == nil {
		return updateResult{}
	}
	if !m.transcriptSelectable(msg.Mod) {
		m.selection = nil
		return updateResult{}
	}
	// Dragging past the top or bottom edge scrolls one row per motion event, so
	// a selection can reach text that was outside the pane when the drag began.
	// The anchor is stored in transcript rows, so scrolling never moves it.
	switch {
	case msg.Y < 0:
		m.viewport.ScrollUp(1)
	case msg.Y >= m.viewport.Height():
		m.viewport.ScrollDown(1)
	}
	m.selection.focus = m.selectionPointClamped(msg.X, msg.Y)
	return updateResult{}
}

// handleMouseRelease ends the drag and copies what it covers. A release without
// a drag copies nothing: clearing a selection must not overwrite the clipboard.
func (m *model) handleMouseRelease(msg tea.MouseReleaseMsg) updateResult {
	m.clearApprovalSelection()
	selection := m.selection
	if selection == nil {
		return updateResult{}
	}
	selection.focus = m.selectionPointClamped(msg.X, msg.Y)
	if selection.empty() {
		m.selection = nil
		return updateResult{}
	}
	// The highlight stays until the next click, key, or scroll: it is the only
	// visible confirmation of what was copied, and it shows the exact cells.
	m.selection = selection
	start, end := selection.normalized()
	text := selectionText(m.viewport.GetContent(), start, end)
	if strings.TrimSpace(text) == "" {
		return updateResult{}
	}
	return updateResult{cmds: []tea.Cmd{copySelection(text)}}
}

// clipboardResultMsg reports a finished copy so the status line can confirm
// it. Runes, not bytes, are counted: "copied 12 characters" should not read as
// 36 for a line of CJK.
type clipboardResultMsg struct {
	chars int
	text  string
	via   string
}

// copySelection writes text to the clipboard. A local clipboard helper is tried
// first because its exit status can be checked. When none is installed — over
// SSH, in a container, or on a terminal with no helper — the copy falls back to
// OSC52, which asks the terminal itself to set the clipboard.
func copySelection(text string) tea.Cmd {
	return copySelectionWith(text, writeLocalClipboard)
}

// copySelectionWith is copySelection with the writer supplied, so a test can
// exercise both clipboard paths without touching the developer's clipboard.
func copySelectionWith(text string, write func(string) error) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		chars := utf8.RuneCountInString(text)
		if err := write(text); err == nil {
			logx.Debug("tui clipboard", "via", localClipboardTool(), "bytes", len(text))
			return clipboardResultMsg{chars: chars, text: text, via: "local"}
		}
		logx.Debug("tui clipboard", "via", "osc52", "bytes", len(text))
		return clipboardResultMsg{chars: chars, text: text, via: "osc52"}
	}
}

// finishClipboardCopy confirms the copy in the status line and, for the OSC52
// fallback, returns the command that hands the text to the terminal. OSC52 has
// no reply, so "sent" is as much as the program can know; reporting a failure
// there would show a false error on every terminal that accepts it.
func (m *model) finishClipboardCopy(msg clipboardResultMsg) updateResult {
	m.copyNotice = fmt.Sprintf("copied %d characters", msg.chars)
	var cmds []tea.Cmd
	if msg.via == "osc52" {
		cmds = append(cmds, tea.SetClipboard(msg.text))
	}
	return updateResult{cmds: cmds}
}

// localClipboardTool returns the first installed tool that can set the
// clipboard, in the order the platform prefers.
func localClipboardTool() string {
	for _, name := range []string{"wl-copy", "xclip", "xsel", "pbcopy"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// writeLocalClipboard sets the system clipboard through the first available
// helper. An error means there is none, which sends the copy down the OSC52
// path.
func writeLocalClipboard(text string) error {
	tool := localClipboardTool()
	if tool == "" {
		return errors.New("no clipboard helper on PATH")
	}
	var cmd *exec.Cmd
	switch tool {
	case "xclip":
		cmd = exec.Command(tool, "-in", "-selection", "clipboard")
	case "xsel":
		cmd = exec.Command(tool, "--input", "--clipboard")
	case "wl-copy", "pbcopy":
		cmd = exec.Command(tool)
	default:
		return fmt.Errorf("unsupported clipboard helper %q", tool)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
