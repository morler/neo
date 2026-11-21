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
