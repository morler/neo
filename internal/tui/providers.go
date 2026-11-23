package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	return m.handlePickerKey(msg, pickerTarget{
		query:    &m.providers.query,
		selected: &m.providers.selected,
		err:      &m.providers.err,
		close:    m.closeProviderBrowser,
		move:     m.moveProviderSelection,
		ensure:   m.ensureProviderSelection,
		choose:   m.selectCurrentProvider,
	})
}

func (m *model) moveProviderSelection(delta int) {
	moveSelection(len(m.filteredProviders()), &m.providers.selected, delta)
}

func (m *model) ensureProviderSelection() {
	clampSelection(len(m.filteredProviders()), &m.providers.selected)
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
	choices := make([]pickerChoice, 0, len(items))
	for _, item := range items {
		choices = append(choices, pickerChoice{id: item.ID, name: item.Name, description: item.Description})
	}
	return m.renderPicker(pickerView{
		title:      "Select a provider",
		query:      m.providers.query,
		items:      choices,
		selected:   m.providers.selected,
		currentID:  m.providerTag,
		idColumn:   providerBrowserIDColumn,
		listHeight: pickerListHeight(m.height, 6),
		emptyNote:  "No providers match.",
		enterLabel: "switch",
		err:        m.providers.err,
	})
}
