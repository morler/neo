package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// SessionChoice is one saved session in the /sessions browser. This package
// never reads the session store itself: the caller supplies the list and the
// loadability check, so the TUI stays unaware of the on-disk format.
type SessionChoice struct {
	ID          string
	Title       string
	Description string
}

type sessionBrowser struct {
	visible  bool
	query    string
	selected int
	items    []SessionChoice
	err      error
}

// openSessionBrowser lists the saved sessions. Listing reads the local index,
// so it runs inline and the browser opens complete instead of showing a
// loading state for one small file.
func (m *model) openSessionBrowser() tea.Cmd {
	if m.sessionLister == nil {
		m.appendBlock(noticeBlock{text: "session: " + m.sessionID})
		return nil
	}
	items, err := m.sessionLister(m.ctx)
	if err != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("list sessions: %w", err)})
		return nil
	}
	m.sessions = sessionBrowser{visible: true, items: normalizeSessionChoices(items)}
	m.ensureSessionSelection()
	return nil
}

func (m *model) closeSessionBrowser() {
	m.sessions = sessionBrowser{}
	m.refreshViewport()
}

func (m *model) handleSessionBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.handlePickerKey(msg, pickerTarget{
		query:    &m.sessions.query,
		selected: &m.sessions.selected,
		err:      &m.sessions.err,
		close:    m.closeSessionBrowser,
		move:     m.moveSessionSelection,
		ensure:   m.ensureSessionSelection,
		choose:   m.switchToSelectedSession,
	})
	// Choosing a session leaves the TUI: the caller resumes the target once
	// this program has exited, so no runtime handle is ever swapped in place.
	if m.quitting {
		return m, tea.Quit
	}
	return m, nil
}

// switchToSelectedSession asks to leave for the chosen session. The current
// session is saved first and the target must still load, so a failed save or a
// deleted session keeps the browser open on the running conversation instead of
// losing either one.
func (m *model) switchToSelectedSession() {
	items := m.filteredSessions()
	if len(items) == 0 {
		return
	}
	choice := items[m.sessions.selected]
	if choice.ID == m.sessionID {
		m.closeSessionBrowser()
		m.appendBlock(noticeBlock{text: "session: " + choice.ID + " is already current"})
		return
	}
	if m.sessionOpener == nil {
		m.sessions.err = fmt.Errorf("switching sessions is unavailable in this TUI")
		return
	}
	if err := m.persistSession(); err != nil {
		m.sessions.err = fmt.Errorf("save current session: %w", err)
		return
	}
	if err := m.sessionOpener(choice.ID); err != nil {
		m.sessions.err = err
		return
	}
	m.resumeSessionID = choice.ID
	m.quitting = true
}

func (m *model) moveSessionSelection(delta int) {
	moveSelection(len(m.filteredSessions()), &m.sessions.selected, delta)
}

func (m *model) ensureSessionSelection() {
	clampSelection(len(m.filteredSessions()), &m.sessions.selected)
}

func (m *model) filteredSessions() []SessionChoice {
	query := strings.ToLower(strings.TrimSpace(m.sessions.query))
	out := make([]SessionChoice, 0, len(m.sessions.items))
	for _, choice := range m.sessions.items {
		if query == "" || sessionChoiceMatches(choice, query) {
			out = append(out, choice)
		}
	}
	return out
}

func sessionChoiceMatches(choice SessionChoice, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		choice.ID,
		choice.Title,
		choice.Description,
	}, "\n"))
	return strings.Contains(haystack, query)
}

// normalizeSessionChoices drops unusable rows and titles an untitled session
// the way `neo sessions` prints it.
func normalizeSessionChoices(choices []SessionChoice) []SessionChoice {
	out := make([]SessionChoice, 0, len(choices))
	seen := map[string]bool{}
	for _, choice := range choices {
		choice.ID = strings.TrimSpace(choice.ID)
		if choice.ID == "" || seen[choice.ID] {
			continue
		}
		if strings.TrimSpace(choice.Title) == "" {
			choice.Title = "(untitled)"
		}
		out = append(out, choice)
		seen[choice.ID] = true
	}
	return out
}

func (m *model) sessionBrowserView() string {
	items := m.filteredSessions()
	choices := make([]pickerChoice, 0, len(items))
	for _, item := range items {
		choices = append(choices, pickerChoice{id: item.ID, name: item.Title, description: item.Description})
	}
	return m.renderPicker(pickerView{
		title:      "Switch session",
		query:      m.sessions.query,
		items:      choices,
		selected:   m.sessions.selected,
		currentID:  m.sessionID,
		idColumn:   sessionBrowserIDColumn,
		listHeight: pickerListHeight(m.height, 6),
		emptyNote:  "No sessions match.",
		enterLabel: "switch",
		err:        m.sessions.err,
	})
}
