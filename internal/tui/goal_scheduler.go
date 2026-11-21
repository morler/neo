package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/goal"
)

// maybeContinueGoal starts the next automatic continuation turn when the goal
// is still open and nothing else is happening. It is called only after a turn
// or a user command finishes, never from a background ticker: a goal advances
// because the previous turn ended, so there is no timer to race with input.
//
// The preconditions are the whole user-control story of Phase 1. Anything that
// suggests the user is about to act — a non-empty composer, an open picker, a
// pending approval, a queued follow-up, an exit in progress — suppresses the
// continuation. The goal stays active and the next turn end checks again.
func (m *model) maybeContinueGoal() tea.Cmd {
	if m.goal == nil {
		return nil
	}
	snap := m.goal.Snapshot()
	if snap == nil || !snap.Continues() {
		return nil
	}
	if !m.goalIdle() {
		return nil
	}

	res, err := m.goal.ConsumeAttempt()
	if err != nil {
		return nil
	}
	if !res.Allowed {
		// The budget ran out. The next issue (#20) renders the handoff; here the
		// contract is only that no further turn starts.
		return nil
	}

	// The visible text carries the marker, so the transcript identifies each
	// automatic turn without a second notice line repeating it.
	prompt := goal.ContinuationPrompt(res.State, m.goal.Limits())
	display := goal.ContinuationDisplay(res.State)
	return m.submitGoalContinuation(display, prompt)
}

// goalIdle reports whether an automatic continuation may start without
// interrupting the user. Every condition is a reason the user might be about to
// act, so a false answer is always safe: the goal simply waits for the next
// turn end.
func (m *model) goalIdle() bool {
	switch {
	case m.busy, m.quitting, m.quitPending:
		return false
	case m.approval != nil:
		return false
	case m.queued != nil:
		return false
	case m.picker.visible, m.files.visible:
		return false
	case m.models.visible, m.providers.visible:
		return false
	}
	return strings.TrimSpace(m.input.Value()) == ""
}

// submitGoalContinuation sends a continuation turn. It mirrors
// submitUserTurnWithSkillExpansion but never expands skills: the prompt is
// generated text, and an objective that happens to contain "$name" must not be
// rewritten into a skill body on every automatic turn.
func (m *model) submitGoalContinuation(displayText, promptText string) tea.Cmd {
	m.workflow = nil
	m.clearInflight()
	m.conversationGeneration++
	m.appendBlock(userBlock{text: displayText})
	m.busy = true
	m.busySince = time.Now()
	m.turn = turnStats{}
	m.setDotColor(colDotThinking)
	return m.startSend(displayText, promptText, nil)
}
