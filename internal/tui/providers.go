package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ProviderChoice is one selectable backend in the /provider picker.
type ProviderChoice struct {
	ID          string
	Name        string
	Description string
}

// ProviderState describes the backend after a successful provider switch: the
// TUI needs the model and its catalogue so the footer and /model list follow
// the new backend.
type ProviderState struct {
	Provider string
	Model    string
	Choices  []ModelChoice
	Loader   func(context.Context) ([]ModelChoice, error)
}

// ProviderSwitcher switches the running chat to provider and returns the
// resulting backend. It must finish the switch before returning; an error
// leaves the previous backend running.
type ProviderSwitcher func(provider string) (ProviderState, error)

type providerBrowser struct {
	visible  bool
	query    string
	selected int
	err      error
}

func normalizeProviderChoices(choices []ProviderChoice) []ProviderChoice {
	out := make([]ProviderChoice, 0, len(choices))
	seen := map[string]bool{}
	for _, choice := range choices {
		choice.ID = strings.TrimSpace(choice.ID)
		if choice.ID == "" || seen[choice.ID] {
			continue
		}
		if strings.TrimSpace(choice.Name) == "" {
			choice.Name = choice.ID
		}
		out = append(out, choice)
		seen[choice.ID] = true
	}
	return out
}

func (m *model) openProviderBrowser() tea.Cmd {
	if len(m.providerChoices) == 0 || m.providerSwitcher == nil {
		m.appendBlock(noticeBlock{text: "provider: " + m.providerTag})
		return nil
	}
	m.providers = providerBrowser{visible: true}
	m.ensureProviderSelection()
	return nil
}

func (m *model) closeProviderBrowser() {
	m.providers = providerBrowser{}
	m.refreshViewport()
}

func (m *model) handleProviderBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" || key == "ctrl+d" {
		return m, m.requestQuit()
	}
	if m.quitPending {
		return m, nil
	}
	switch key {
	case "esc":
		m.closeProviderBrowser()
	case "up":
		m.moveProviderSelection(-1)
	case "down":
		m.moveProviderSelection(1)
	case "backspace", "delete":
		if m.providers.query != "" {
			m.providers.query = dropLastRune(m.providers.query)
			m.providers.err = nil
			m.ensureProviderSelection()
		}
	case "enter":
		m.selectCurrentProvider()
	default:
		s := msg.String()
		if utf8.RuneCountInString(s) == 1 && s >= " " && s != "\x7f" {
			m.providers.query += s
			m.providers.err = nil
			m.ensureProviderSelection()
		}
	}
	return m, nil
}

func (m *model) moveProviderSelection(delta int) {
	items := m.filteredProviders()
	if len(items) == 0 {
		m.providers.selected = 0
		return
	}
	n := len(items)
	m.providers.selected = (m.providers.selected + delta + n) % n
}

func (m *model) ensureProviderSelection() {
	items := m.filteredProviders()
	if len(items) == 0 {
		m.providers.selected = 0
		return
	}
	if m.providers.selected >= len(items) {
		m.providers.selected = len(items) - 1
	}
	if m.providers.selected < 0 {
		m.providers.selected = 0
	}
}

// selectCurrentProvider switches backends. A failed switch keeps the picker
// open with the error so the user can pick another provider, and leaves the
// running backend untouched.
func (m *model) selectCurrentProvider() {
	items := m.filteredProviders()
	if len(items) == 0 {
		return
	}
	choice := items[m.providers.selected]
	state, err := m.providerSwitcher(choice.ID)
	if err != nil {
		m.providers.err = err
		return
	}
	m.providerTag = state.Provider
	m.modelTag = state.Model
	m.modelChoices = normalizeModelChoices(state.Model, state.Choices)
	m.modelChoicesLoader = state.Loader
	m.modelChoicesLoaded = false
	m.modelChoicesLoading = false
	m.models = modelBrowser{}
	if err := m.persistSession(); err != nil {
		m.providers.err = err
		return
	}
	m.blocks = append(m.blocks, noticeBlock{text: "provider: " + backendLabel(state.Provider, state.Model)})
	m.closeProviderBrowser()
}

func (m *model) filteredProviders() []ProviderChoice {
	query := strings.ToLower(strings.TrimSpace(m.providers.query))
	out := make([]ProviderChoice, 0, len(m.providerChoices))
	for _, choice := range m.providerChoices {
		if query == "" || providerChoiceMatches(choice, query) {
			out = append(out, choice)
		}
	}
	return out
}

func providerChoiceMatches(choice ProviderChoice, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		choice.ID,
		choice.Name,
		choice.Description,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func (m *model) providerBrowserView() string {
	items := m.filteredProviders()
	header := styAccent.Render("Select a provider")
	query := m.providers.query
	if query == "" {
		query = styMuted.Render("Type to search")
	} else {
		query = styTool.Render(query)
	}

	listHeight := m.height - 6
	if listHeight < 3 {
		listHeight = 3
	}
	start := 0
	if m.providers.selected >= listHeight {
		start = m.providers.selected - listHeight + 1
	}
	end := min(len(items), start+listHeight)

	var lines []string
	for i := start; i < end; i++ {
		choice := items[i]
		selected := i == m.providers.selected
		prefix := "  "
		style := lipgloss.NewStyle()
		if selected {
			prefix = styTool.Render("›") + " "
			style = lipgloss.NewStyle().Background(colCardBg)
		}
		name := choice.Name
		if choice.ID == m.providerTag {
			name += " " + styMuted.Render("(current)")
		}
		line := prefix + styTool.Render(padRight(choice.ID, 18)) + "  " + name
		if choice.Description != "" && choice.Description != choice.ID {
			line += "  " + styMuted.Render(choice.Description)
		}
		lines = append(lines, style.Width(max(1, m.width-2)).Render(truncate(line, max(1, m.width-2))))
	}
	if len(lines) == 0 {
		lines = append(lines, styMuted.Render("No providers match."))
	}

	status := fmt.Sprintf("%d / %d", min(m.providers.selected+1, len(items)), len(items))
	if len(items) == 0 {
		status = "0 / 0"
	}
	footer := strings.Join([]string{
		styTool.Render("enter") + styMuted.Render(" switch"),
		styTool.Render("esc") + styMuted.Render(" exit"),
		styTool.Render("↑/↓") + styMuted.Render(" browse"),
	}, "    ")

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(query)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(lines, "\n"))
	if m.providers.err != nil {
		sb.WriteString("\n\n")
		sb.WriteString(styErr.Render("! " + m.providers.err.Error()))
	}
	used := strings.Count(sb.String(), "\n")
	for used < m.height-3 {
		sb.WriteString("\n")
		used++
	}
	sb.WriteString(styDim.Render(strings.Repeat("─", max(1, m.width-1))))
	sb.WriteString("\n")
	sb.WriteString(footer)
	if m.width > lipgloss.Width(footer)+lipgloss.Width(status)+2 {
		sb.WriteString(strings.Repeat(" ", m.width-lipgloss.Width(footer)-lipgloss.Width(status)-2))
		sb.WriteString(styMuted.Render(status))
	}
	return sb.String()
}
