package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPickerKey_EscapeClosesBothBrowsers pins the shared handler's esc branch:
// each browser closes through its own callback.
func TestPickerKey_EscapeClosesBothBrowsers(t *testing.T) {
	modelCase := makeTestModel()
	modelCase.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
	})
	modelCase.openModelBrowser()
	if !modelCase.models.visible {
		t.Fatal("model browser did not open")
	}
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

// TestPickerKey_CtrlDRequestsQuit covers the second half of the quit condition:
// ctrl+d quits exactly like ctrl+c instead of being read as text input.
func TestPickerKey_CtrlDRequestsQuit(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.openModelBrowser()
	m.handleModelBrowserKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	if !m.quitPending {
		t.Fatal("ctrl+d did not request quit from the model browser")
	}

	m2 := makeTestModel()
	m2.busy = true
	m2.providers = providerBrowser{visible: true}
	m2.handleProviderBrowserKey(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	if !m2.quitPending {
		t.Fatal("ctrl+d did not request quit from the provider browser")
	}
}

// TestPickerKey_PendingQuitSwallowsLaterKeys pins the early return: once a quit
// is pending, later keys must not close or mutate the browser underneath it.
func TestPickerKey_PendingQuitSwallowsLaterKeys(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
	})
	m.busy = true
	m.openModelBrowser()
	if !m.models.visible {
		t.Fatal("model browser did not open")
	}
	m.handleModelBrowserKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !m.quitPending {
		t.Fatal("ctrl+c did not request quit")
	}

	m.handleModelBrowserKey(keyPress(tea.KeyEscape))

	if !m.models.visible {
		t.Fatal("esc closed the browser while a quit was pending")
	}
	if m.models.query != "" {
		t.Fatalf("query = %q, want empty", m.models.query)
	}
}

// TestPickerKey_UpMovesSelectionBackwards covers the up branch: the cursor wraps
// to the last item when it moves back from the first.
func TestPickerKey_UpMovesSelectionBackwards(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
		{ID: "gpt-4o", Name: "GPT-4o"},
	})
	m.openModelBrowser()
	items := len(m.filteredModels())
	if items < 2 {
		t.Fatalf("need at least two choices, got %d", items)
	}
	if m.models.selected != 0 {
		t.Fatalf("selected = %d, want 0 before moving", m.models.selected)
	}

	m.handleModelBrowserKey(keyPress(tea.KeyUp))

	if got, want := m.models.selected, items-1; got != want {
		t.Fatalf("selected = %d, want %d after up", got, want)
	}
}

// TestPickerKey_BackspaceOnEmptyQueryIsANoop covers the false branch of the
// query guard: with nothing to delete the query and cursor stay put.
func TestPickerKey_BackspaceOnEmptyQueryIsANoop(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
	})
	m.openModelBrowser()

	m.handleModelBrowserKey(keyPress(tea.KeyBackspace))

	if m.models.query != "" {
		t.Fatalf("query = %q, want empty", m.models.query)
	}
	if m.models.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.models.selected)
	}
}

// TestSelectionHelpersHandleEmptyLists pins the items == 0 guards in both pure
// helpers, which the browser paths never reach because a picker with no
// choices does not open.
func TestSelectionHelpersHandleEmptyLists(t *testing.T) {
	t.Run("moveSelection", func(t *testing.T) {
		selected := 7
		moveSelection(0, &selected, 1)
		if selected != 0 {
			t.Fatalf("selected = %d, want 0", selected)
		}
	})
	t.Run("clampSelection", func(t *testing.T) {
		selected := 7
		clampSelection(0, &selected)
		if selected != 0 {
			t.Fatalf("selected = %d, want 0", selected)
		}
	})
	t.Run("clampSelectionPullsBackFromPastTheEnd", func(t *testing.T) {
		selected := 5
		clampSelection(2, &selected)
		if selected != 1 {
			t.Fatalf("selected = %d, want 1", selected)
		}
	})
	t.Run("clampSelectionPullsBackFromNegative", func(t *testing.T) {
		selected := -3
		clampSelection(2, &selected)
		if selected != 0 {
			t.Fatalf("selected = %d, want 0", selected)
		}
	})
}

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

// TestBrowserViewsRenderTheSharedLayout pins what the shared renderer draws for
// both browsers, so the two call sites stay wired to it.
func TestBrowserViewsRenderTheSharedLayout(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("gpt-5.2", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2", Description: "flagship"},
		{ID: "gpt-4o", Name: "GPT-4o"},
	})
	m.modelTag = "gpt-5.2"
	m.openModelBrowser()
	m.models.query = "gpt"

	view := m.modelBrowserView()
	for _, want := range []string{"Select a model", "gpt-5.2", "GPT-5.2", "(current)", "flagship", "1 / 2", "enter", "browse"} {
		if !strings.Contains(view, want) {
			t.Fatalf("model view missing %q:\n%s", want, view)
		}
	}

	m.providerTag = "anthropic"
	m.providerChoices = []ProviderChoice{
		{ID: "anthropic", Name: "Anthropic", Description: "claude"},
		{ID: "openai", Name: "OpenAI"},
	}
	m.providers = providerBrowser{visible: true}

	providerView := m.providerBrowserView()
	for _, want := range []string{"Select a provider", "anthropic", "Anthropic", "(current)", "claude", "1 / 2", "switch"} {
		if !strings.Contains(providerView, want) {
			t.Fatalf("provider view missing %q:\n%s", want, providerView)
		}
	}
}

// TestBrowserViewsRenderEmptyAndLoadingStates covers the placeholder rows the
// shared renderer chooses between.
func TestBrowserViewsRenderEmptyAndLoadingStates(t *testing.T) {
	m := makeTestModel()
	m.models = modelBrowser{visible: true}
	if got := m.modelBrowserView(); !strings.Contains(got, "No models match.") {
		t.Fatalf("empty model view = %q", got)
	}
	m.models.loading = true
	if got := m.modelBrowserView(); !strings.Contains(got, "Loading…") || !strings.Contains(got, "loading…") {
		t.Fatalf("loading model view = %q", got)
	}

	m.providers = providerBrowser{visible: true}
	if got := m.providerBrowserView(); !strings.Contains(got, "No providers match.") {
		t.Fatalf("empty provider view = %q", got)
	}
}
