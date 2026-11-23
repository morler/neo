package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

type ModelChoice struct {
	ID          string
	Name        string
	Description string
}

type modelBrowser struct {
	visible  bool
	query    string
	selected int
	loading  bool
	err      error
}

func dropLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

func normalizeModelChoices(currentModel string, choices []ModelChoice) []ModelChoice {
	var out []ModelChoice
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
	currentModel = strings.TrimSpace(currentModel)
	if currentModel != "" && !seen[currentModel] {
		out = append([]ModelChoice{{
			ID:          currentModel,
			Name:        currentModel,
			Description: "Current configured model",
		}}, out...)
	}
	return out
}

func backendLabel(provider, model string) string {
	if strings.TrimSpace(provider) == "" {
		return model
	}
	return provider + "/" + model
}

func (m *model) openModelBrowser() tea.Cmd {
	if len(m.modelChoices) == 0 && m.modelChoicesLoader == nil {
		m.appendBlock(noticeBlock{text: "model: " + m.modelTag})
		return nil
	}
	loading := m.modelChoicesLoader != nil && !m.modelChoicesLoaded
	m.models = modelBrowser{visible: true, loading: loading || m.modelChoicesLoading}
	m.ensureModelSelection()
	if !loading || m.modelChoicesLoading {
		return nil
	}
	m.modelChoicesLoading = true
	return m.loadModelChoices()
}

// loadModelChoices fetches the provider's live catalogue in a command, so the
// browser opens with its static fallback instead of waiting on the network.
func (m *model) loadModelChoices() tea.Cmd {
	loader := m.modelChoicesLoader
	ctx := m.ctx
	return func() tea.Msg {
		choices, err := loader(ctx)
		return modelChoicesMsg{choices: choices, err: err}
	}
}

func (m *model) closeModelBrowser() {
	m.models = modelBrowser{}
	m.refreshViewport()
}

func (m *model) handleModelBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handlePickerKey(msg, pickerTarget{
		query:    &m.models.query,
		selected: &m.models.selected,
		err:      &m.models.err,
		close:    m.closeModelBrowser,
		move:     m.moveModelSelection,
		ensure:   m.ensureModelSelection,
		choose:   m.selectCurrentModel,
	})
}

func (m *model) moveModelSelection(delta int) {
	moveSelection(len(m.filteredModels()), &m.models.selected, delta)
}

func (m *model) ensureModelSelection() {
	clampSelection(len(m.filteredModels()), &m.models.selected)
}

func (m *model) selectCurrentModel() {
	items := m.filteredModels()
	if len(items) == 0 {
		return
	}
	choice := items[m.models.selected]
	if m.modelSwitcher != nil {
		if err := m.modelSwitcher(choice.ID); err != nil {
			m.models.err = err
			return
		}
	} else {
		m.ag.SetModel(choice.ID)
	}
	m.modelTag = choice.ID
	if err := m.persistSession(); err != nil {
		m.models.err = err
		return
	}
	m.blocks = append(m.blocks, noticeBlock{text: "model: " + backendLabel(m.providerTag, choice.ID)})
	m.closeModelBrowser()
}

func (m *model) filteredModels() []ModelChoice {
	query := strings.ToLower(strings.TrimSpace(m.models.query))
	out := make([]ModelChoice, 0, len(m.modelChoices))
	for _, choice := range m.modelChoices {
		if query == "" || modelChoiceMatches(choice, query) {
			out = append(out, choice)
		}
	}
	return out
}

func modelChoiceMatches(choice ModelChoice, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		choice.ID,
		choice.Name,
		choice.Description,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func (m *model) modelBrowserView() string {
	items := m.filteredModels()
	choices := make([]pickerChoice, 0, len(items))
	for _, item := range items {
		choices = append(choices, pickerChoice{id: item.ID, name: item.Name, description: item.Description})
	}
	return m.renderPicker(pickerView{
		title:       "Select a model",
		query:       m.models.query,
		items:       choices,
		selected:    m.models.selected,
		currentID:   m.modelTag,
		idColumn:    modelBrowserIDColumn,
		listHeight:  pickerListHeight(m.height, 7),
		loading:     m.models.loading,
		loadingNote: "Fetching model catalog…",
		emptyNote:   "No models match.",
		enterLabel:  "select",
		err:         m.models.err,
	})
}
