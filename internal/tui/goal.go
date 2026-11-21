package tui

import (
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/goal"
)

// usageGoal is shown when /goal has no usable subcommand and no objective.
const usageGoal = "usage: /goal <objective> | /goal status | /goal pause | /goal resume | /goal clear"

// handleGoalCommand routes /goal. The objective form starts work, so it needs
// an idle TUI; the control forms are safe mid-turn because they only change the
// session's goal status and never send a turn.
func (m *model) handleGoalCommand(raw string) tea.Cmd {
	if m.goal == nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("/goal is unavailable in this session")})
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		m.showGoalStatus(true)
		return nil
	}
	sub, rest := splitGoalArgs(raw)
	switch sub {
	case "status":
		if rest != "" {
			m.appendBlock(errorBlock{err: fmt.Errorf("/goal status takes no arguments")})
			return nil
		}
		m.showGoalStatus(false)
		return nil
	case "pause":
		m.applyGoalTransition(func() (string, error) {
			s, err := m.goal.Pause(rest)
			if err != nil {
				return "", goalReason(err)
			}
			return fmt.Sprintf("goal %s paused · /goal resume continues it", s.ID), nil
		})
		return nil
	case "resume":
		if !m.applyGoalTransition(func() (string, error) {
			s, err := m.goal.Resume()
			if err != nil {
				return "", goalReason(err)
			}
			return fmt.Sprintf("goal %s resumed with a fresh attempt budget (%d)", s.ID, s.Attempts.Max), nil
		}) {
			return nil
		}
		// Resume is the one command that means "continue now": the goal is
		// active and the user is idle, so the scheduler starts the next turn
		// immediately instead of waiting for a turn that will never come.
		return m.maybeContinueGoal()
	case "clear":
		m.applyGoalTransition(func() (string, error) {
			s, err := m.goal.Clear()
			if err != nil {
				return "", goalReason(err)
			}
			return fmt.Sprintf("goal cleared (%s: %s)", s.ID, goal.Summarize(s.Objective)), nil
		})
		return nil
	}
	return m.startGoal(raw)
}

// splitGoalArgs separates a control subcommand from its remaining text. The
// control words are reserved, so /goal status is never ambiguous with an
// objective that begins with the word "status". An empty subcommand means the
// text is an objective, returned unmodified: a goal is the user's own wording,
// so internal spacing and line breaks must survive.
func splitGoalArgs(raw string) (string, string) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", ""
	}
	switch sub := strings.ToLower(fields[0]); sub {
	case "status", "pause", "resume", "clear":
		_, rest, _ := strings.Cut(strings.TrimLeft(raw, " \t"), fields[0])
		return sub, strings.TrimSpace(rest)
	}
	return "", raw
}

// startGoal creates or replaces the session goal and sends the objective as the
// first ordinary user turn. The goal is saved before that turn so the model can
// never work on a goal the session did not record yet.
func (m *model) startGoal(objective string) tea.Cmd {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		m.appendBlock(errorBlock{err: fmt.Errorf("%s", usageGoal)})
		return nil
	}
	// Remember what disk still holds so a failed save can put it back, whether
	// that was a previous goal or no goal at all.
	previous := m.goal.Snapshot()
	started, err := m.goal.Start(objective)
	if err != nil {
		m.appendBlock(errorBlock{err: goalReason(err)})
		return nil
	}
	if err := m.saveGoal(); err != nil {
		// Nothing was sent, so nothing should linger in memory: an unsaved goal
		// would let the next save resurrect an objective the user never started,
		// and a lost previous goal would be reported as replaced when it is not.
		if restoreErr := m.goal.Restore(previous); restoreErr != nil {
			m.appendBlock(errorBlock{err: fmt.Errorf("save goal: %w; rollback failed: %v", err, restoreErr)})
			return nil
		}
		m.appendBlock(errorBlock{err: fmt.Errorf("save goal: %w (goal not set)", err)})
		return nil
	}
	if started.Replaced != nil {
		m.appendBlock(noticeBlock{text: fmt.Sprintf(
			"replaced goal %s (%s): %s",
			started.Replaced.ID, started.Replaced.Status, goal.Summarize(started.Replaced.Objective))})
	}
	m.appendBlock(noticeBlock{text: fmt.Sprintf(
		"goal %s active · attempts 0/%d · /goal status for details",
		started.State.ID, started.State.Attempts.Max)})
	// The objective is the user's own wording: it must reach the model verbatim,
	// so $name references in it are not expanded into skill instructions.
	return m.submitUserTurnWithSkillExpansion(objective, objective, nil, false)
}

// applyGoalTransition runs a user-owned status change and saves it immediately.
// A failed save is reported as "changed in memory but not saved" rather than
// pretending the transition reached disk. It reports whether the transition is
// both applied and persisted, which is the precondition for acting on it.
func (m *model) applyGoalTransition(transition func() (string, error)) bool {
	message, err := transition()
	if err != nil {
		m.appendBlock(errorBlock{err: err})
		return false
	}
	if err := m.saveGoal(); err != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("save goal: %w (the change is not saved and will be lost on exit)", err)})
		return false
	}
	m.appendBlock(noticeBlock{text: message})
	return true
}

// saveGoal persists a goal transition through the CLI-owned save callback.
// Without a callback the TUI cannot promise the transition survives a restart,
// so it reports that instead of silently keeping it in memory.
func (m *model) saveGoal() error {
	if m.goalPersist == nil {
		return errors.New("this session cannot save goal state")
	}
	return m.goalPersist()
}

func (m *model) showGoalStatus(withUsage bool) {
	snap := m.goal.Snapshot()
	if snap == nil {
		m.appendBlock(goalBlock{})
		if withUsage {
			m.appendBlock(noticeBlock{text: usageGoal})
		}
		return
	}
	m.appendBlock(goalBlock{state: snap})
	if withUsage {
		m.appendBlock(noticeBlock{text: usageGoal})
	}
}

// goalReason strips the package error prefix so the transcript reads as product
// text, while keeping the specific cause (no goal, a rejected transition).
func goalReason(err error) error {
	switch {
	case errors.Is(err, goal.ErrNoGoal):
		return fmt.Errorf("no active goal in this session")
	case errors.Is(err, goal.ErrTransition):
		return fmt.Errorf("%v", err)
	default:
		return err
	}
}

// goalBlock renders the session goal. It is the only place a goal is shown in
// full: status is a deliberate command, never a permanent footer decoration.
type goalBlock struct{ state *goal.State }

func (b goalBlock) render(width int, _ *glamour.TermRenderer) string {
	s := b.state
	if s == nil {
		return accentCard(styMuted.Render("no active goal · set one with /goal <objective>"), colMuted)
	}
	head := []string{
		styAccent.Render("goal") + " " + styMuted.Render(s.ID),
		styLabel.Render(goalStatusLabel(s.Status)) + styMuted.Render(" · attempts "+attemptsLabel(s)),
	}
	body := make([]string, 0, 8)
	body = append(body, styMuted.Render("objective"))
	body = append(body, indentContinuation(wrap(s.Objective, max(width-6, 1))))
	if reason := strings.TrimSpace(s.LastReason); reason != "" {
		body = append(body, styMuted.Render("last reason"))
		body = append(body, indentContinuation(wrap(reason, max(width-6, 1))))
	}
	if s.Blocked.Count > 0 {
		body = append(body, styMuted.Render(fmt.Sprintf("blocked audit: %d report(s) · %s",
			s.Blocked.Count, goal.Summarize(s.Blocked.LastReason))))
	}
	body = append(body, styMuted.Render(fmt.Sprintf("created %s · updated %s",
		formatGoalTime(s.CreatedAt), formatGoalTime(s.UpdatedAt))))

	out := append(head, body...)
	return accentCard(strings.Join(out, "\n"), goalStatusColor(s.Status))
}

func goalStatusLabel(s goal.Status) string {
	switch s {
	case goal.Active:
		return "active"
	case goal.Paused:
		return "paused"
	case goal.Complete:
		return "complete"
	case goal.Blocked:
		return "blocked"
	case goal.BudgetLimited:
		return "budget limited"
	case goal.UsageLimited:
		return "usage limited"
	case goal.Cleared:
		return "cleared"
	}
	return string(s)
}

func goalStatusColor(s goal.Status) color.Color {
	switch s {
	case goal.Active:
		return colOK
	case goal.Complete:
		return colOK
	case goal.Paused, goal.BudgetLimited, goal.UsageLimited:
		return colWarn
	case goal.Blocked:
		return colErr
	}
	return colMuted
}

func attemptsLabel(s *goal.State) string {
	max := s.Attempts.Max
	switch {
	case max <= 0:
		return fmt.Sprintf("%d used", s.Attempts.Used)
	case s.Status == goal.BudgetLimited:
		return fmt.Sprintf("%d/%d (exhausted)", s.Attempts.Used, max)
	default:
		return fmt.Sprintf("%d/%d", s.Attempts.Used, max)
	}
}

func formatGoalTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}
