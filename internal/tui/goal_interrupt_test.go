package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// interruptibleModel builds a model with an active goal and a running turn that
// the interrupt keys can cancel.
func interruptibleModel(t *testing.T, direct bool) (*model, *goal.Manager, *[]string) {
	t.Helper()
	prov := &llmtest.FakeProvider{}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("keep working"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.busy = true
	m.turn = turnStats{direct: direct}
	m.sendCancel = func() {}
	saves := &[]string{}
	m.goalPersist = func() error {
		*saves = append(*saves, string(mgr.Snapshot().Status))
		return nil
	}
	t.Cleanup(func() { m.sendCancel = nil })
	return m, mgr, saves
}

func TestEscapeInterruptPausesAnActiveGoal(t *testing.T) {
	m, mgr, _ := interruptibleModel(t, false)

	m.handleEscape()

	snap := mgr.Snapshot()
	if snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused", snap.Status)
	}
	if snap.LastReason != "turn interrupted" {
		t.Fatalf("last reason = %q", snap.LastReason)
	}
	if !hasNotice(m, "goal") || !hasNotice(m, "/goal resume") {
		t.Fatalf("no pause notice with recovery instructions: %+v", m.blocks)
	}
}

func TestInterruptPersistsThePause(t *testing.T) {
	m, _, saves := interruptibleModel(t, false)

	m.handleEscape()

	if len(*saves) != 1 {
		t.Fatalf("saves = %v, want exactly one pause save", *saves)
	}
	if (*saves)[0] != string(goal.Paused) {
		t.Fatalf("saved status = %q, want paused", (*saves)[0])
	}
}

// Esc is also the picker-dismiss key, so the pause must not fire before the
// user actually cancels a turn.
func TestEscapeWithAnOpenPickerDoesNotPauseTheGoal(t *testing.T) {
	m, mgr, saves := interruptibleModel(t, false)
	m.picker.visible = true

	m.handleEscape()

	if snap := mgr.Snapshot(); snap.Status != goal.Active {
		t.Fatalf("status = %q, want active while only a picker was open", snap.Status)
	}
	if len(*saves) != 0 {
		t.Fatalf("picker dismissal saved the goal: %v", *saves)
	}
}

func TestInterruptingADirectCommandKeepsTheGoal(t *testing.T) {
	m, mgr, saves := interruptibleModel(t, true)

	m.handleEscape()

	if snap := mgr.Snapshot(); snap.Status != goal.Active {
		t.Fatalf("status = %q, want active after a ! command interrupt", snap.Status)
	}
	if len(*saves) != 0 {
		t.Fatalf("direct command interrupt saved the goal: %v", *saves)
	}
}

func TestInterruptOfANonActiveGoalLeavesItAlone(t *testing.T) {
	for _, status := range []goal.Status{goal.Paused, goal.Complete, goal.Blocked, goal.BudgetLimited} {
		t.Run(string(status), func(t *testing.T) {
			m, mgr, saves := interruptibleModel(t, false)
			snap := mgr.Snapshot()
			if err := mgr.Restore(&goal.State{
				ID:        snap.ID,
				Objective: snap.Objective,
				Status:    status,
				Attempts:  snap.Attempts,
			}); err != nil {
				t.Fatalf("Restore: %v", err)
			}

			m.handleEscape()

			if got := mgr.Snapshot(); got.Status != status {
				t.Fatalf("status = %q, want it unchanged at %q", got.Status, status)
			}
			if len(*saves) != 0 {
				t.Fatalf("interrupt of a %s goal saved: %v", status, *saves)
			}
		})
	}
}

func TestInterruptWithoutAGoalOrManagerIsInert(t *testing.T) {
	// No goal in the session.
	m, mgr, _ := interruptibleModel(t, false)
	if _, err := mgr.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	m.handleEscape()
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("goal = %+v", snap)
	}

	// No manager at all: the pre-goal behavior is unchanged.
	bare := makeTestModel()
	canceled := false
	bare.sendCancel = func() { canceled = true }
	bare.busy = true
	bare.handleEscape()
	if !canceled {
		t.Fatal("escape did not cancel the turn without a goal")
	}
}

func TestCanceledTurnDoesNotContinueTheGoal(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("keep working"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.busy = true
	m.busySince = timeNow()

	m.Update(sendResultMsg{err: context.Canceled})

	if m.busy {
		t.Fatal("a canceled turn started a continuation")
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused", snap.Status)
	}
	if snap := mgr.Snapshot(); snap.Attempts.Used != 0 {
		t.Fatalf("attempts = %d, want none consumed", snap.Attempts.Used)
	}
}

// A cancellation that does not come from Esc (a provider shutdown, a canceled
// parent context) follows the same rule: pause instead of continuing.
func TestCancellationFromElsewhereAlsoPauses(t *testing.T) {
	m, mgr, saves := interruptibleModel(t, false)
	m.busySince = timeNow()

	m.Update(sendResultMsg{err: context.Canceled})

	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused", snap.Status)
	}
	if snap := mgr.Snapshot(); snap.LastReason != "turn canceled" {
		t.Fatalf("last reason = %q", snap.LastReason)
	}
	if len(*saves) != 1 {
		t.Fatalf("saves = %v, want one", *saves)
	}
}

// Esc followed by the canceled result must pause once, not twice: the second
// pass sees a goal that is no longer active.
func TestInterruptPausesOnlyOnce(t *testing.T) {
	m, mgr, saves := interruptibleModel(t, false)
	m.busySince = timeNow()

	m.handleEscape()
	m.Update(sendResultMsg{err: context.Canceled})

	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q", snap.Status)
	}
	if snap := mgr.Snapshot(); snap.LastReason != "turn interrupted" {
		t.Fatalf("last reason = %q, want the first interruption reason kept", snap.LastReason)
	}
	if len(*saves) != 1 {
		t.Fatalf("saves = %v, want one pause", *saves)
	}
	if countNotices(m, "paused") != 1 {
		t.Fatalf("pause notices = %d, want 1", countNotices(m, "paused"))
	}
}

func TestCtrlCDuringAGoalTurnPausesBeforeQuitting(t *testing.T) {
	m, mgr, _ := interruptibleModel(t, false)

	cmd := m.requestQuit()

	if cmd != nil {
		t.Fatal("ctrl+c during a busy turn should wait for the save, not quit immediately")
	}
	if !m.quitPending {
		t.Fatal("quit was not marked pending")
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused before exiting", snap.Status)
	}
	if snap := mgr.Snapshot(); snap.LastReason != "quit while the goal was running" {
		t.Fatalf("last reason = %q", snap.LastReason)
	}
}

func TestPausedGoalResumesThroughTheUIAfterAnInterrupt(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	started, err := mgr.Start("keep working")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.busy = true
	m.turn = turnStats{}
	m.sendCancel = func() {}
	m.handleEscape()
	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused", snap.Status)
	}
	m.busy = false
	m.sendCancel = nil

	cmd := m.handleSlashCommand("/goal resume")
	if cmd == nil {
		t.Fatal("/goal resume did not schedule a continuation")
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Active || snap.ID != started.State.ID {
		t.Fatalf("resumed = %+v", snap)
	}
	runCmd(t, m, cmd)
	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want one continuation", len(prov.Calls))
	}
}

func TestPauseSaveFailureIsVisibleAndStillPaused(t *testing.T) {
	m, mgr, _ := interruptibleModel(t, false)
	m.goalPersist = func() error { return errors.New("disk full") }

	m.handleEscape()

	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want the in-memory pause applied", snap.Status)
	}
	found := false
	for _, b := range m.blocks {
		eb, ok := b.(errorBlock)
		if ok && strings.Contains(eb.err.Error(), "not saved") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unsaved warning: %+v", m.blocks)
	}
}

func hasNotice(m *model, substr string) bool { return countNotices(m, substr) > 0 }

func countNotices(m *model, substr string) int {
	n := 0
	for _, b := range m.blocks {
		nb, ok := b.(noticeBlock)
		if ok && strings.Contains(nb.text, substr) {
			n++
		}
	}
	return n
}

// budgetModel builds a model whose goal has a tiny budget, so the handoff is
// reachable without 20 turns.
func budgetModel(t *testing.T, max int) (*model, *goal.Manager, *int) {
	t.Helper()
	m := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: max})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	saves := 0
	m.goal = mgr
	m.goalPersist = func() error {
		saves++
		return nil
	}
	if _, err := mgr.Start("keep working"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return m, mgr, &saves
}

// finishScheduledTurn clears the busy state a scheduled continuation sets, as
// if that turn had run and returned. The TUI would do this in sendResultMsg.
func finishScheduledTurn(t *testing.T, m *model) {
	t.Helper()
	if !m.busy {
		t.Fatal("no turn was scheduled")
	}
	if m.sendCancel != nil {
		m.sendCancel()
	}
	m.busy = false
	m.sendCancel = nil
}

func TestBudgetExhaustionShowsTheHandoff(t *testing.T) {
	m, mgr, saves := budgetModel(t, 1)

	// The first continuation is allowed and spends the whole budget.
	if cmd := m.maybeContinueGoal(); cmd == nil {
		t.Fatal("the first continuation was not allowed")
	}
	if snap := mgr.Snapshot(); snap.Attempts.Used != 1 {
		t.Fatalf("attempts = %d, want 1", snap.Attempts.Used)
	}
	finishScheduledTurn(t, m)

	m.blocks = nil
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a continuation started past the budget")
	}

	handoffs := 0
	for _, b := range m.blocks {
		hb, ok := b.(goalBudgetBlock)
		if !ok {
			continue
		}
		handoffs++
		out := plain(hb.render(100, nil))
		for _, want := range []string{
			"Goal budget reached: 1/1 attempts used.",
			"Last reason: none recorded",
			"/goal resume",
			"/goal clear",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("handoff missing %q:\n%s", want, out)
			}
		}
	}
	if handoffs != 1 {
		t.Fatalf("handoffs = %d, want 1", handoffs)
	}
	if *saves != 1 {
		t.Fatalf("saves = %d, want the budget limit persisted once", *saves)
	}
	if snap := mgr.Snapshot(); snap.Status != goal.BudgetLimited {
		t.Fatalf("status = %q, want budget_limited", snap.Status)
	}
}

func TestBudgetHandoffIncludesTheLastReason(t *testing.T) {
	m, mgr, _ := budgetModel(t, 1)
	started := mgr.Snapshot()
	if _, err := mgr.RequestBlocked(started.ID, "tests still fail in internal/session"); err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if cmd := m.maybeContinueGoal(); cmd == nil {
		t.Fatal("first continuation not allowed")
	}
	finishScheduledTurn(t, m)
	m.blocks = nil
	m.maybeContinueGoal()

	out := plain(lastBlock[goalBudgetBlock](t, m).render(120, nil))
	if !strings.Contains(out, "tests still fail in internal/session") {
		t.Fatalf("handoff lost the last reason:\n%s", out)
	}
}

func TestBudgetHandoffSaveFailureIsVisible(t *testing.T) {
	m, mgr, _ := budgetModel(t, 1)
	m.maybeContinueGoal() // spends the budget
	finishScheduledTurn(t, m)
	m.blocks = nil
	m.goalPersist = func() error { return errors.New("disk full") }

	m.maybeContinueGoal()

	if snap := mgr.Snapshot(); snap.Status != goal.BudgetLimited {
		t.Fatalf("status = %q", snap.Status)
	}
	found := false
	for _, b := range m.blocks {
		eb, ok := b.(errorBlock)
		if ok && strings.Contains(eb.err.Error(), "disk full") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no save error reported: %+v", m.blocks)
	}
}

// The initial /goal turn is not an attempt, and it is the only turn that must
// not count. Both halves are asserted here because the budget's meaning depends
// on the distinction.
func TestInitialGoalTurnDoesNotCountAsAnAttempt(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("done")}}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})

	cmd := m.handleSlashCommand("/goal fix the failing test")
	if cmd == nil {
		t.Fatal("/goal did not start a turn")
	}
	if snap := mgr.Snapshot(); snap.Attempts.Used != 0 {
		t.Fatalf("used = %d before the first turn even ran, want 0", snap.Attempts.Used)
	}
	m.Update(cmd())
	// The initial turn is free, but finishing it schedules the first
	// continuation, and that continuation is what consumes the first attempt.
	if snap := mgr.Snapshot(); snap.Attempts.Used != 1 {
		t.Fatalf("used = %d after the initial turn, want the scheduled continuation to be attempt 1", snap.Attempts.Used)
	}
	if !m.busy {
		t.Fatal("the initial turn did not schedule its continuation")
	}
	found := false
	for _, b := range m.blocks {
		if ub, ok := b.(userBlock); ok && strings.HasPrefix(ub.text, "↳ goal continuation 1/4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the continuation is not labelled as attempt 1: %+v", m.blocks)
	}
}

func TestBudgetLimitedGoalResumesWithAFreshBudget(t *testing.T) {
	m, mgr, _ := budgetModel(t, 2)
	for i := 0; i < 2; i++ {
		if cmd := m.maybeContinueGoal(); cmd == nil {
			t.Fatalf("continuation %d was refused", i+1)
		}
		finishScheduledTurn(t, m)
	}
	m.maybeContinueGoal() // exhausts and shows the handoff
	if snap := mgr.Snapshot(); snap.Status != goal.BudgetLimited {
		t.Fatalf("status = %q, want budget_limited", snap.Status)
	}

	cmd := m.handleSlashCommand("/goal resume")
	if cmd == nil {
		t.Fatal("/goal resume did not restart the goal")
	}
	// Resume schedules the next turn immediately, so by the time it returns the
	// budget is fresh and the first continuation of the new budget is running.
	snap := mgr.Snapshot()
	if snap.Status != goal.Active || snap.Attempts.Max != 2 || snap.Attempts.Used != 1 {
		t.Fatalf("resumed = %+v, want active with 1/2 used", snap)
	}
	if !m.busy {
		t.Fatal("resume did not start a continuation")
	}
	found := false
	for _, b := range m.blocks {
		if ub, ok := b.(userBlock); ok && strings.HasPrefix(ub.text, "↳ goal continuation 1/2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the resumed continuation did not restart the counter: %+v", m.blocks)
	}
}

func TestBudgetLimitedStatusRendersAsExhausted(t *testing.T) {
	m, mgr, _ := budgetModel(t, 1)
	m.maybeContinueGoal()
	finishScheduledTurn(t, m)
	m.blocks = nil
	m.maybeContinueGoal()
	m.handleSlashCommand("/goal status")

	out := plain(lastBlock[goalBlock](t, m).render(100, nil))
	if !strings.Contains(out, "1/1 (exhausted)") {
		t.Fatalf("status block = %s", out)
	}
	if !strings.Contains(out, "budget limited") {
		t.Fatalf("status block missing the budget-limited state: %s", out)
	}
	if snap := mgr.Snapshot(); snap.Status != goal.BudgetLimited {
		t.Fatalf("status = %q", snap.Status)
	}
}
