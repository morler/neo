package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
)

func TestModelBrowser_LoadsCatalogueOnlyWhenOpened(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = nil
	m.modelChoicesLoader = func(context.Context) ([]ModelChoice, error) {
		return []ModelChoice{{ID: "dynamic", Name: "Dynamic Model"}}, nil
	}

	cmd := m.handleSlashCommand("/model")
	if cmd == nil {
		t.Fatal("expected /model to return a catalogue load command")
	}
	if !m.models.visible || !m.models.loading {
		t.Fatal("expected the browser to open while the catalogue loads")
	}

	m.Update(cmd())

	if m.models.loading {
		t.Fatal("expected loading to finish")
	}
	found := false
	for _, choice := range m.modelChoices {
		if choice.ID == "dynamic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model choices = %#v, want dynamic", m.modelChoices)
	}
	if got := plain(m.View().Content); !strings.Contains(got, "dynamic") {
		t.Fatalf("loaded model missing from browser: %s", got)
	}
}

func TestModelBrowser_LoadErrorKeepsFallbackAndShowsError(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "fallback", Name: "Fallback"}})
	m.modelChoicesLoader = func(context.Context) ([]ModelChoice, error) {
		return nil, errors.New("offline")
	}

	cmd := m.handleSlashCommand("/model")
	if cmd == nil {
		t.Fatal("expected /model to return a catalogue load command")
	}
	m.Update(cmd())

	if !m.models.visible {
		t.Fatal("expected the browser to stay open after a load error")
	}
	if m.models.err == nil || !strings.Contains(m.models.err.Error(), "offline") {
		t.Fatalf("expected load error, got %v", m.models.err)
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "fallback") || !strings.Contains(out, "offline") {
		t.Fatalf("browser did not keep the fallback and show the error: %s", out)
	}
}

func TestBackgroundColorMsgSwitchesMarkdownStyle(t *testing.T) {
	m := makeTestModel()
	md, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(0))
	if err != nil {
		t.Fatal(err)
	}
	m.md = md
	m.mdStyleName = "dark"
	m.mdWidth = 0
	m.refreshViewport()

	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}})

	if m.mdStyleName != "light" {
		t.Fatalf("md style = %q, want light", m.mdStyleName)
	}
	if m.md == md {
		t.Fatal("expected the markdown renderer to be rebuilt for the light style")
	}
}

func TestInitRequestsBackgroundColor(t *testing.T) {
	m := makeTestModel()
	if m.Init() == nil {
		t.Fatal("expected Init to request the terminal background color")
	}
}

func TestSlashCommand_ModelOpensSearchableBrowser(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2", Description: "flagship"},
		{ID: "gpt-4o", Name: "GPT-4o", Description: "fast"},
	})

	m.handleSlashCommand("/model")

	if !m.models.visible {
		t.Fatal("expected model browser to open")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "Select a model") || !strings.Contains(out, "gpt-5.2") {
		t.Fatalf("model browser did not render choices: %s", out)
	}

	for _, r := range "4o" {
		m.handleModelBrowserKey(keyPress(r))
	}
	out = plain(m.View().Content)
	if !strings.Contains(out, "gpt-4o") || strings.Contains(out, "gpt-5.2") {
		t.Fatalf("model browser did not filter by query: %s", out)
	}
}

func TestModelBrowser_EnterSelectsModelAndSaves(t *testing.T) {
	m := makeTestModel()
	m.persistenceErr = fmt.Errorf("stale save failure")
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "gpt-5.2", Name: "GPT-5.2"},
	})
	saveCalls := 0
	m.afterSend = func() error {
		saveCalls++
		return nil
	}

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if m.models.visible {
		t.Fatal("expected model browser to close after selection")
	}
	if got := m.ag.Model(); got != "test" {
		t.Fatalf("selected wrong model: got %q want test", got)
	}
	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))
	if got := m.ag.Model(); got != "gpt-5.2" {
		t.Fatalf("agent model = %q, want gpt-5.2", got)
	}
	if m.modelTag != "gpt-5.2" {
		t.Fatalf("modelTag = %q, want gpt-5.2", m.modelTag)
	}
	if saveCalls != 2 {
		t.Fatalf("saveCalls = %d, want 2", saveCalls)
	}
	if m.persistenceErr != nil {
		t.Fatalf("persistence error after successful model save = %v, want nil", m.persistenceErr)
	}
	out := plain(m.viewportContent())
	if !strings.Contains(out, "model: gpt-5.2") {
		t.Fatalf("selection notice missing: %s", out)
	}
}

func TestModelBrowser_SaveErrorKeepsBrowserOpen(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "gpt-5.2"}})
	saveErr := fmt.Errorf("save failed")
	m.afterSend = func() error { return saveErr }

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if !m.models.visible {
		t.Fatal("expected model browser to stay open on save error")
	}
	if m.models.err == nil || !strings.Contains(m.models.err.Error(), "save failed") {
		t.Fatalf("expected save error, got %v", m.models.err)
	}
	if !errors.Is(m.persistenceErr, saveErr) {
		t.Fatalf("persistence error = %v, want %v", m.persistenceErr, saveErr)
	}
}

func TestModelBrowser_UsesSwitcher(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "test"},
		{ID: "claude-sonnet-4-6"},
	})
	var selected string
	m.modelSwitcher = func(model string) error {
		selected = model
		return nil
	}

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if selected != "claude-sonnet-4-6" {
		t.Fatalf("selected = %q", selected)
	}
	if m.providerTag != "anthropic" || m.modelTag != "claude-sonnet-4-6" {
		t.Fatalf("visible backend = %s/%s", m.providerTag, m.modelTag)
	}
	if got := plain(m.footerLine()); !strings.Contains(got, "anthropic/claude-sonnet-4-6") {
		t.Fatalf("footer does not show provider and model: %q", got)
	}
}

func TestModelBrowser_SwitchFailureKeepsCurrentBackend(t *testing.T) {
	m := makeTestModel()
	m.providerTag = "anthropic"
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{
		{ID: "test"},
		{ID: "claude-sonnet-4-6"},
	})
	m.modelSwitcher = func(string) error { return fmt.Errorf("switch failed") }

	m.handleSlashCommand("/model")
	m.handleModelBrowserKey(keyPress(tea.KeyDown))
	m.handleModelBrowserKey(keyPress(tea.KeyEnter))

	if !m.models.visible || m.models.err == nil {
		t.Fatal("failed switch should keep the picker open with an error")
	}
	if m.providerTag != "anthropic" || m.modelTag != "test" {
		t.Fatalf("model changed after failed switch: %s/%s", m.providerTag, m.modelTag)
	}
}
