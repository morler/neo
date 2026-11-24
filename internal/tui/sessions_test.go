package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testSessionChoices() []SessionChoice {
	return []SessionChoice{
		{ID: "sess_current", Title: "current work", Description: "gpt-5 · ~/repo · 2026-11-24 10:00"},
		{ID: "sess_other", Title: "other work", Description: "claude-opus-5 · ~/other · 2026-11-23 09:00"},
	}
}

// sessionTestModel wires /sessions to a fixed list and records what the picker
// asked the caller to open.
func sessionTestModel(t *testing.T) (*model, *[]string) {
	t.Helper()
	m := makeTestModel()
	m.sessionID = "sess_current"
	opened := []string{}
	m.sessionLister = func(context.Context) ([]SessionChoice, error) {
		return testSessionChoices(), nil
	}
	m.sessionOpener = func(id string) error {
		opened = append(opened, id)
		return nil
	}
	return m, &opened
}

func TestSlashCommand_SessionsOpensBrowserAndMarksCurrent(t *testing.T) {
	m, _ := sessionTestModel(t)

	m.handleSlashCommand("/sessions")

	if !m.sessions.visible {
		t.Fatal("expected the session browser to open")
	}
	out := plain(m.View().Content)
	for _, want := range []string{"Switch session", "sess_current", "current work", "sess_other", "other work"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session browser missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "(current)") {
		t.Fatalf("session browser did not mark the running session:\n%s", out)
	}
}

func TestSessionBrowser_FiltersByTitleAndID(t *testing.T) {
	m, _ := sessionTestModel(t)
	m.handleSlashCommand("/sessions")

	for _, r := range "other" {
		m.handleSessionBrowserKey(keyPress(r))
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "sess_other") {
		t.Fatalf("filter dropped the matching session:\n%s", out)
	}
	if strings.Contains(out, "sess_current") {
		t.Fatalf("filter kept the non-matching session:\n%s", out)
	}

	m.sessions.query = ""
	m.ensureSessionSelection()
	for _, r := range "sess_current" {
		m.handleSessionBrowserKey(keyPress(r))
	}
	if !strings.Contains(plain(m.View().Content), "current work") {
		t.Fatal("filtering by session id did not match the title row")
	}
}

func TestSessionBrowser_SelectingCurrentSessionIsANoOp(t *testing.T) {
	m, opened := sessionTestModel(t)
	saves := 0
	m.afterSend = func() error {
		saves++
		return nil
	}
	m.handleSlashCommand("/sessions")

	m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if len(*opened) != 0 {
		t.Fatalf("opened %v, want no switch for the current session", *opened)
	}
	if saves != 0 {
		t.Fatalf("saved %d times, want no save for the current session", saves)
	}
	if m.sessions.visible {
		t.Fatal("expected the browser to close")
	}
	if m.quitting || m.resumeSessionID != "" {
		t.Fatalf("quitting %t / target %q, want a no-op", m.quitting, m.resumeSessionID)
	}
	var notice string
	for _, b := range m.blocks {
		if n, ok := b.(noticeBlock); ok {
			notice = n.text
		}
	}
	if !strings.Contains(notice, "already current") {
		t.Fatalf("last notice = %q, want an already-current message", notice)
	}
}

func TestSessionBrowser_SwitchesAfterSavingAndLeavesTheTUI(t *testing.T) {
	m, opened := sessionTestModel(t)
	saves := 0
	m.afterSend = func() error {
		saves++
		return nil
	}
	m.handleSlashCommand("/sessions")

	m.handleSessionBrowserKey(keyPress(tea.KeyDown))
	_, cmd := m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if saves != 1 {
		t.Fatalf("saved %d times, want exactly one save before switching", saves)
	}
	if len(*opened) != 1 || (*opened)[0] != "sess_other" {
		t.Fatalf("opened %v, want the selected session", *opened)
	}
	if m.resumeSessionID != "sess_other" {
		t.Fatalf("target = %q, want sess_other", m.resumeSessionID)
	}
	if !m.quitting {
		t.Fatal("expected the TUI to leave for the chosen session")
	}
	assertQuitCommand(t, cmd)
}

func TestSessionBrowser_SaveFailureKeepsTheCurrentSession(t *testing.T) {
	m, opened := sessionTestModel(t)
	m.afterSend = func() error { return errors.New("disk full") }
	m.handleSlashCommand("/sessions")

	m.handleSessionBrowserKey(keyPress(tea.KeyDown))
	m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if len(*opened) != 0 {
		t.Fatalf("opened %v, want no switch after a failed save", *opened)
	}
	if m.quitting || m.resumeSessionID != "" {
		t.Fatalf("quitting %t / target %q, want the current session kept", m.quitting, m.resumeSessionID)
	}
	if !m.sessions.visible {
		t.Fatal("expected the browser to stay open for a retry")
	}
	if m.sessions.err == nil || !strings.Contains(m.sessions.err.Error(), "disk full") {
		t.Fatalf("browser error = %v, want the save failure", m.sessions.err)
	}
}

func TestSessionBrowser_DeletedTargetReportsAndKeepsTheConversation(t *testing.T) {
	m, opened := sessionTestModel(t)
	m.sessionOpener = func(id string) error {
		*opened = append(*opened, id)
		return errors.New("session not found: " + id)
	}
	m.handleSlashCommand("/sessions")

	m.handleSessionBrowserKey(keyPress(tea.KeyDown))
	m.handleSessionBrowserKey(keyPress(tea.KeyEnter))

	if m.quitting || m.resumeSessionID != "" {
		t.Fatalf("quitting %t / target %q, want the current session kept", m.quitting, m.resumeSessionID)
	}
	if m.sessions.err == nil || !strings.Contains(m.sessions.err.Error(), "session not found") {
		t.Fatalf("browser error = %v, want the missing-session error", m.sessions.err)
	}
	if !strings.Contains(plain(m.View().Content), "session not found") {
		t.Fatal("browser did not render the load error")
	}
}

func TestSessionBrowser_IsIdleOnlyAndListedInHelp(t *testing.T) {
	m, _ := sessionTestModel(t)
	help := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	if !strings.Contains(help, "/sessions") {
		t.Fatalf("help is missing /sessions:\n%s", help)
	}
	if !builtinSlashCommand("/sessions") {
		t.Fatal("/sessions is not registered as a built-in command")
	}

	m.busy = true
	m.handleSlashCommand("/sessions")
	if m.sessions.visible {
		t.Fatal("expected /sessions to be refused while a turn runs")
	}
	last, ok := m.blocks[len(m.blocks)-1].(errorBlock)
	if !ok || !strings.Contains(last.err.Error(), "unavailable while a turn is running") {
		t.Fatalf("refusal block = %#v, want the idle-only message", m.blocks[len(m.blocks)-1])
	}
}

func TestSessionBrowser_ListFailureIsReportedWithoutOpening(t *testing.T) {
	m := makeTestModel()
	m.sessionID = "sess_current"
	m.sessionLister = func(context.Context) ([]SessionChoice, error) {
		return nil, errors.New("index unreadable")
	}

	m.handleSlashCommand("/sessions")

	if m.sessions.visible {
		t.Fatal("expected no browser when the list fails")
	}
	last, ok := m.blocks[len(m.blocks)-1].(errorBlock)
	if !ok || !strings.Contains(last.err.Error(), "index unreadable") {
		t.Fatalf("last block = %#v, want the list error", m.blocks[len(m.blocks)-1])
	}
}

func TestNormalizeSessionChoicesTitlesAndDeduplicates(t *testing.T) {
	got := normalizeSessionChoices([]SessionChoice{
		{ID: " a ", Title: ""},
		{ID: "a", Title: "duplicate"},
		{ID: "", Title: "no id"},
		{ID: "b", Title: "named"},
	})

	if len(got) != 2 {
		t.Fatalf("choices = %#v, want two rows", got)
	}
	if got[0].ID != "a" || got[0].Title != "(untitled)" {
		t.Fatalf("first = %#v, want a trimmed id and the untitled label", got[0])
	}
	if got[1].ID != "b" || got[1].Title != "named" {
		t.Fatalf("second = %#v, want the named session", got[1])
	}
}
