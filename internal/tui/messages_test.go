package tui

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// TestUpdateRoutesWindowSize covers the resize handler through Update: the
// viewport adopts the new size and the model keeps it for layout.
func TestUpdateRoutesWindowSize(t *testing.T) {
	m := makeTestModel()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	if m.width != 100 || m.height != 40 {
		t.Fatalf("size = %dx%d, want 100x40", m.width, m.height)
	}
	if got := m.viewport.Width(); got != 100 {
		t.Fatalf("viewport width = %d, want 100", got)
	}
}

// TestUpdateSpinnerTickStopsWhenIdle pins the tick handler's else branch: a
// tick that lands after the turn ended must clear the spin flag and schedule
// no follow-up tick, or the spinner would run forever.
func TestUpdateSpinnerTickStopsWhenIdle(t *testing.T) {
	m := makeTestModel()
	m.spinning = true
	m.busy = false

	_, cmd := m.Update(spinner.TickMsg{})

	if m.spinning {
		t.Fatal("spinning stayed true after an idle tick")
	}
	if cmd != nil {
		t.Fatal("idle tick scheduled another command")
	}
}

// TestUpdateSpinnerTickKeepsRunningWhileBusy pins the other half: while a turn
// runs the tick chain continues.
func TestUpdateSpinnerTickKeepsRunningWhileBusy(t *testing.T) {
	m := makeTestModel()
	m.spinning = true
	m.busy = true

	m.Update(spinner.TickMsg{})

	if !m.spinning {
		t.Fatal("spinning was cleared while a turn was running")
	}
}

// TestUpdateBrowserKeyReturnsEarly covers the earlyWith path: a visible browser
// consumes the key and Update returns its command without running the tail.
func TestUpdateBrowserKeyReturnsEarly(t *testing.T) {
	m := makeTestModel()
	m.modelChoices = normalizeModelChoices("test", []ModelChoice{{ID: "gpt-5.2", Name: "GPT-5.2"}})
	m.busy = true
	m.openModelBrowser()
	if !m.models.visible {
		t.Fatal("model browser did not open")
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))

	if m.models.visible {
		t.Fatal("esc did not close the browser through Update")
	}
	if m.spinning {
		t.Fatal("an overlay key started the spinner")
	}
}

// TestUpdateApprovalKeyReturnsEarly covers the approval branch of handleKeyMsg:
// with an approval open, the key goes to the approval and the model is not
// otherwise mutated.
func TestUpdateApprovalKeyReturnsEarly(t *testing.T) {
	m := makeTestModel()
	m.approval = &approvalState{reply: make(chan bool, 1)}

	m.Update(keyPress('n'))

	if m.approval == nil {
		t.Fatal("approval was closed by a bare dismissal key")
	}
}
