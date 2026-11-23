package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPickerKey_EscapeClosesBothBrowsers pins the shared handler's esc branch:
// each browser closes through its own callback.
func TestPickerKey_EscapeClosesBothBrowsers(t *testing.T) {
	modelCase := makeTestModel()
	modelCase.openModelBrowser()
	modelCase.handleModelBrowserKey(keyPress(tea.KeyEscape))
	if modelCase.models.visible {
		t.Fatal("esc did not close the model browser")
	}

	providerCase := makeTestModel()
	providerCase.providerTag = "anthropic"
	providerCase.providerChoices = testProviderChoices()
	providerCase.providerSwitcher = func(string) (ProviderState, error) { return ProviderState{}, nil }
	providerCase.handleSlashCommand("/provider")
	if !providerCase.providers.visible {
		t.Fatal("expected provider browser to open")
	}
	providerCase.handleProviderBrowserKey(keyPress(tea.KeyEscape))
	if providerCase.providers.visible {
		t.Fatal("esc did not close the provider browser")
	}
}

// TestPickerKey_BackspaceDropsQueryAndKeepsCursorInRange covers the delete
// branch: it removes one rune, clears a stale error, and re-clamps the cursor.
func TestPickerKey_BackspaceDropsQueryAndKeepsCursorInRange(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
		{ID: "gpt-4o", Name: "GPT-4o"},
	})
	m.openModelBrowser()
	for _, r := range "gpt" {
		m.handleModelBrowserKey(keyPress(r))
	}
	if m.models.query != "gpt" {
		t.Fatalf("query = %q, want gpt", m.models.query)
	}
	// A stale error must be cleared by the next edit.
	m.models.err = errFake{}
	m.models.selected = 5

	m.handleModelBrowserKey(keyPress(tea.KeyBackspace))

	if got := m.models.query; got != "gp" {
		t.Fatalf("query = %q, want gp", got)
	}
	if m.models.err != nil {
		t.Fatalf("err = %v, want nil", m.models.err)
	}
	if m.models.selected >= len(m.filteredModels()) {
		t.Fatalf("cursor %d left outside %d visible choices", m.models.selected, len(m.filteredModels()))
	}
}

// errFake is a stand-in error for the stale-error assertion above.
type errFake struct{}

func (errFake) Error() string { return "stale" }

// TestPickerKey_CtrlCRequestsQuit checks the shared ctrl+c branch fires for
// both browsers instead of being swallowed by the picker.
func TestPickerKey_CtrlCRequestsQuit(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.openModelBrowser()
	m.handleModelBrowserKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !m.quitPending {
		t.Fatal("ctrl+c did not request quit from the model browser")
	}

	m2 := makeTestModel()
	m2.busy = true
	m2.providers = providerBrowser{visible: true}
	m2.handleProviderBrowserKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !m2.quitPending {
		t.Fatal("ctrl+c did not request quit from the provider browser")
	}
}
