package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Layout constants for the two browser views. The ID column width differs on
// purpose: model ids are long enough to crowd the name beside them, provider
// ids are short, so each view reserves its own. Everything else is shared.
const (
	modelBrowserIDColumn    = 30
	providerBrowserIDColumn = 18

	pickerMinListHeight = 3
	pickerSideMargin    = 2
	pickerBottomRows    = 3
	pickerRuleInset     = 1
	pickerStatusPadding = 2
	// sessionBrowserIDColumn fits a generated sess_<16 hex> id.
	sessionBrowserIDColumn = 24
)

// pickerChoice is one row shared by both browsers: an identifier in a fixed
// column, a display name, and an optional description.
type pickerChoice struct {
	id          string
	name        string
	description string
}

// pickerView holds everything the shared browser layout needs. The two
// browsers differ only in these values — the title, the identifier column
// width, the empty and loading wording, and the label on the select key.
type pickerView struct {
	title       string
	query       string
	items       []pickerChoice
	selected    int
	currentID   string
	idColumn    int
	listHeight  int
	loading     bool
	loadingNote string
	emptyNote   string
	enterLabel  string
	err         error
}

// renderPicker draws a browser screen: title, query, the visible rows, an
// optional error, then a rule with the position and the key hints.
func (m *model) renderPicker(v pickerView) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(styAccent.Render(v.title))
	sb.WriteString("\n\n")
	sb.WriteString(pickerQueryLine(v.query))
	sb.WriteString("\n")
	if v.loadingNote != "" && v.loading {
		sb.WriteString(styMuted.Render(v.loadingNote))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(strings.Join(pickerLines(v, m.width), "\n"))
	if v.err != nil {
		sb.WriteString("\n\n")
		sb.WriteString(styErr.Render("! " + v.err.Error()))
	}
	used := strings.Count(sb.String(), "\n")
	for used < m.height-pickerBottomRows {
		sb.WriteString("\n")
		used++
	}
	sb.WriteString(styDim.Render(strings.Repeat("─", max(1, m.width-pickerRuleInset))))
	sb.WriteString("\n")
	status := pickerStatus(v)
	footer := pickerFooter(v.enterLabel)
	sb.WriteString(footer)
	if m.width > lipgloss.Width(footer)+lipgloss.Width(status)+pickerStatusPadding {
		sb.WriteString(strings.Repeat(" ", m.width-lipgloss.Width(footer)-lipgloss.Width(status)-pickerStatusPadding))
		sb.WriteString(styMuted.Render(status))
	}
	return sb.String()
}

// pickerListHeight reserves chrome rows above and below the list and keeps the
// result usable on a short terminal.
func pickerListHeight(height, chrome int) int {
	return max(pickerMinListHeight, height-chrome)
}

// pickerQueryLine renders the search field, or a muted placeholder when it is
// empty.
func pickerQueryLine(query string) string {
	if query == "" {
		return styMuted.Render("Type to search")
	}
	return styTool.Render(query)
}

// pickerLines renders the rows currently in view, scrolled so the selection
// stays visible, or a placeholder when there is nothing to show.
func pickerLines(v pickerView, width int) []string {
	start := 0
	if v.selected >= v.listHeight {
		start = v.selected - v.listHeight + 1
	}
	end := min(len(v.items), start+v.listHeight)

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, pickerLine(v, v.items[i], i == v.selected, width))
	}
	if len(lines) > 0 {
		return lines
	}
	if v.loading {
		return []string{styMuted.Render("Loading…")}
	}
	return []string{styMuted.Render(v.emptyNote)}
}

// pickerLine renders one row: a cursor, the identifier in its column, the name
// with a current-value marker, and the description when it adds anything.
func pickerLine(v pickerView, choice pickerChoice, selected bool, width int) string {
	prefix := "  "
	style := lipgloss.NewStyle()
	if selected {
		prefix = styTool.Render("›") + " "
		style = lipgloss.NewStyle().Background(colCardBg)
	}
	name := choice.name
	if choice.id == v.currentID {
		name += " " + styMuted.Render("(current)")
	}
	line := prefix + styTool.Render(padRight(choice.id, v.idColumn)) + "  " + name
	if choice.description != "" && choice.description != choice.id {
		line += "  " + styMuted.Render(choice.description)
	}
	line = truncate(line, max(1, width-pickerSideMargin))
	return style.Width(max(1, width-pickerSideMargin)).Render(line)
}

// pickerStatus reports the cursor position within the list.
func pickerStatus(v pickerView) string {
	if len(v.items) == 0 {
		if v.loading {
			return "loading…"
		}
		return "0 / 0"
	}
	return fmt.Sprintf("%d / %d", min(v.selected+1, len(v.items)), len(v.items))
}

// pickerFooter renders the key hints. Only the select verb differs between the
// browsers.
func pickerFooter(enterLabel string) string {
	return strings.Join([]string{
		styTool.Render("enter") + styMuted.Render(" "+enterLabel),
		styTool.Render("esc") + styMuted.Render(" exit"),
		styTool.Render("↑/↓") + styMuted.Render(" browse"),
	}, "    ")
}

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
