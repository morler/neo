package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testProviderChoices() []ProviderChoice {
	return []ProviderChoice{
		{ID: "anthropic", Name: "anthropic", Description: "Anthropic API key"},
		{ID: "local", Name: "local", Description: "custom OpenAI-compatible backend"},
	}
}

func TestSlashCommand_ProviderOpensPickerWithActiveMarked(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.providerChoices = testProviderChoices()
	m.providerSwitcher = func(string) (ProviderState, error) { return ProviderState{}, nil }

	m.handleSlashCommand("/provider")

	if !m.providers.visible {
		t.Fatal("expected the provider picker to open")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "Select a provider") || !strings.Contains(out, "local") {
		t.Fatalf("provider picker did not render choices: %s", out)
	}
	if !strings.Contains(out, "anthropic") || !strings.Contains(out, "(current)") {
		t.Fatalf("provider picker did not mark the active provider: %s", out)
	}

	for _, r := range "loc" {
		m.handleProviderBrowserKey(keyPress(r))
	}
	out = plain(m.View().Content)
	if !strings.Contains(out, "local") || strings.Contains(out, "Anthropic API key") {
		t.Fatalf("provider picker did not filter by query: %s", out)
	}
}

func TestProviderBrowser_SwitchesAndUpdatesFooterAndModels(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.providerChoices = testProviderChoices()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "claude-opus-5"}})
	m.modelChoicesLoader = nil
	saveCalls := 0
	m.afterSend = func() error {
		saveCalls++
		return nil
	}
	m.providerSwitcher = func(name string) (ProviderState, error) {
		if name != "local" {
			t.Fatalf("switcher called with %q", name)
		}
		return ProviderState{
			Provider: "local",
			Model:    "qwen3-coder",
			Choices:  []ModelChoice{{ID: "qwen3-coder", Name: "Qwen3 Coder"}},
		}, nil
	}

	m.handleSlashCommand("/provider")
	m.handleProviderBrowserKey(keyPress(tea.KeyDown))
	m.handleProviderBrowserKey(keyPress(tea.KeyEnter))

	if m.providers.visible {
		t.Fatal("expected the provider picker to close after a successful switch")
	}
	if m.providerTag != "local" || m.modelTag != "qwen3-coder" {
		t.Fatalf("visible backend = %s/%s", m.providerTag, m.modelTag)
	}
	if m.ag.Model() != "test" {
		// The fake switcher does not touch the agent; the TUI must not either.
		t.Fatalf("agent model = %q, want the switcher to own the agent", m.ag.Model())
	}
	if saveCalls != 1 {
		t.Fatalf("saveCalls = %d, want 1", saveCalls)
	}
	if got := plain(m.footerLine()); !strings.Contains(got, "local/qwen3-coder") {
		t.Fatalf("footer does not show the new backend: %q", got)
	}
	if len(m.modelChoices) != 1 || m.modelChoices[0].ID != "qwen3-coder" {
		t.Fatalf("model choices did not follow the provider: %#v", m.modelChoices)
	}
	out := plain(m.viewportContent())
	if !strings.Contains(out, "provider: local/qwen3-coder") {
		t.Fatalf("switch notice missing: %s", out)
	}
}

func TestProviderBrowser_SwitchFailureKeepsBackendAndShowsError(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.providerChoices = testProviderChoices()
	m.providerSwitcher = func(string) (ProviderState, error) {
		return ProviderState{}, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	m.handleSlashCommand("/provider")
	m.handleProviderBrowserKey(keyPress(tea.KeyDown))
	m.handleProviderBrowserKey(keyPress(tea.KeyEnter))

	if !m.providers.visible {
		t.Fatal("expected the picker to stay open after a failed switch")
	}
	if m.providers.err == nil || !strings.Contains(m.providers.err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected the switch error, got %v", m.providers.err)
	}
	if m.providerTag != "anthropic" || m.modelTag != "test" {
		t.Fatalf("backend changed after a failed switch: %s/%s", m.providerTag, m.modelTag)
	}
	if out := plain(m.View().Content); !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("picker does not show the switch error: %s", out)
	}
}

func TestProviderBrowser_SaveErrorKeepsPickerOpen(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.providerChoices = testProviderChoices()
	saveErr := errors.New("save failed")
	m.afterSend = func() error { return saveErr }
	m.providerSwitcher = func(string) (ProviderState, error) {
		return ProviderState{Provider: "local", Model: "qwen3-coder"}, nil
	}

	m.handleSlashCommand("/provider")
	m.handleProviderBrowserKey(keyPress(tea.KeyDown))
	m.handleProviderBrowserKey(keyPress(tea.KeyEnter))

	if !m.providers.visible {
		t.Fatal("expected the picker to stay open when the session save fails")
	}
	if !errors.Is(m.persistenceErr, saveErr) {
		t.Fatalf("persistence error = %v, want %v", m.persistenceErr, saveErr)
	}
}

func TestProviderBrowser_WithoutSwitcherShowsCurrentProvider(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.handleSlashCommand("/provider")
	if m.providers.visible {
		t.Fatal("expected no picker without choices")
	}
	out := plain(m.viewportContent())
	if !strings.Contains(out, "provider: anthropic") {
		t.Fatalf("notice missing: %s", out)
	}
}

func TestProviderBrowse_RequiresIdleTurn(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.providerChoices = testProviderChoices()
	m.providerSwitcher = func(string) (ProviderState, error) { return ProviderState{}, nil }

	m.handleSlashCommand("/provider")
	if m.providers.visible {
		t.Fatal("expected /provider to be rejected during a turn")
	}
	eb, ok := m.blocks[len(m.blocks)-1].(errorBlock)
	if !ok || !strings.Contains(eb.err.Error(), "unavailable while a turn is running") {
		t.Fatalf("expected the busy error, got %#v", m.blocks[len(m.blocks)-1])
	}
}

func TestSlashCommand_ProviderListedInHelp(t *testing.T) {
	m := makeTestModel()
	help := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	if !strings.Contains(help, "/provider") {
		t.Fatalf("help should advertise /provider: %s", help)
	}
	if !builtinSlashCommand("/provider") {
		t.Fatal("/provider should be a built-in command")
	}
}
