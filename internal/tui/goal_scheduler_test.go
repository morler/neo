package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// errTestTurn stands in for a failed agent or save result.
var errTestTurn = errors.New("turn failed")

// timeNow keeps the scheduler tests independent of the wall clock in assertions.
func timeNow() time.Time { return time.Now() }

// runCmd executes one scheduled send and returns the model after its result is
// processed. The result may itself schedule the next continuation, which stays
// pending exactly as Bubble Tea would leave it.
func runCmd(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	m.Update(cmd())
}

func TestGoalSchedulerContinuesAnActiveGoal(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first turn"),
		llmtest.Text("second turn"),
	}}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("make the failing test pass"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	next := m.maybeContinueGoal()
	if next == nil {
		t.Fatal("no continuation after an idle turn with an active goal")
	}
	if snap := mgr.Snapshot(); snap.Attempts.Used != 1 {
		t.Fatalf("attempts = %d, want 1 after scheduling one continuation", snap.Attempts.Used)
	}
	runCmd(t, m, next)

	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.Calls))
	}
	continuation := prov.Calls[0].Messages[len(prov.Calls[0].Messages)-1]
	text := continuation.Content[len(continuation.Content)-1].Text
	if !strings.HasPrefix(text, "↳ goal continuation") {
		t.Fatalf("continuation prompt does not start with the marker:\n%s", text)
	}
	for _, want := range []string{"<goal>", "make the failing test pass", "complete", "blocked"} {
		if !strings.Contains(text, want) {
			t.Fatalf("continuation prompt is missing %q:\n%s", want, text)
		}
	}
}

func TestGoalSchedulerShowsAVisibleMarkerPerContinuation(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first turn"),
		llmtest.Text("second turn"),
	}}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("finish the migration"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	next := m.maybeContinueGoal()
	if next == nil {
		t.Fatal("no continuation")
	}
	// Inspect the transcript before the scheduled turn runs: Bubble Tea would
	// start it immediately, and its own completion schedules the next marker.
	var markers int
	for _, b := range m.blocks {
		ub, ok := b.(userBlock)
		if ok && strings.HasPrefix(ub.text, "↳ goal continuation") {
			markers++
			if !strings.Contains(ub.text, "1/4") {
				t.Fatalf("continuation marker missing the attempt counter: %q", ub.text)
			}
		}
	}
	if markers != 1 {
		t.Fatalf("transcript has %d continuation markers, want 1", markers)
	}

	runCmd(t, m, next)
	transcript := m.ag.Transcript()
	if len(transcript) < 2 {
		t.Fatalf("transcript = %+v", transcript)
	}
	if got := transcript[0].DisplayText; !strings.HasPrefix(got, "↳ goal continuation 1/4") {
		t.Fatalf("display text = %q, want the marker stored for replay", got)
	}
}

func TestGoalSchedulerRespectsUserPresence(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *model)
	}{
		{"typing", func(m *model) { m.input.SetValue("h") }},
		{"slash picker", func(m *model) { m.picker.visible = true }},
		{"file picker", func(m *model) { m.files.visible = true }},
		{"model browser", func(m *model) { m.models.visible = true }},
		{"provider browser", func(m *model) { m.providers.visible = true }},
		{"approval", func(m *model) { m.approval = &approvalState{} }},
		{"queued follow-up", func(m *model) { m.queued = &queuedTurn{displayText: "next"} }},
		{"busy", func(m *model) { m.busy = true }},
		{"quitting", func(m *model) { m.quitting = true }},
		{"quit pending", func(m *model) { m.quitPending = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, mgr, _ := goalTestModel(t, nil)
			started, err := mgr.Start("keep working")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			tt.setup(m)

			if cmd := m.maybeContinueGoal(); cmd != nil {
				t.Fatal("scheduler started a turn while the user was present")
			}
			snap := mgr.Snapshot()
			if snap.Attempts.Used != 0 {
				t.Fatalf("attempts = %d, want none consumed", snap.Attempts.Used)
			}
			if snap.ID != started.State.ID || snap.Status != goal.Active {
				t.Fatalf("goal changed while waiting: %+v", snap)
			}
		})
	}
}

func TestGoalSchedulerStopsForNonActiveGoals(t *testing.T) {
	for _, status := range []goal.Status{
		goal.Paused, goal.Complete, goal.Blocked, goal.BudgetLimited, goal.UsageLimited,
	} {
		t.Run(string(status), func(t *testing.T) {
			m, mgr, _ := goalTestModel(t, nil)
			started, err := mgr.Start("keep working")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := mgr.Restore(&goal.State{
				ID:        started.State.ID,
				Objective: started.State.Objective,
				Status:    status,
				Attempts:  started.State.Attempts,
			}); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if cmd := m.maybeContinueGoal(); cmd != nil {
				t.Fatalf("scheduler continued a %s goal", status)
			}
		})
	}
}

func TestGoalSchedulerStopsWithoutAGoalOrManager(t *testing.T) {
	m, _, _ := goalTestModel(t, nil)
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("scheduler started a turn with no goal")
	}

	bare := makeTestModel()
	bare.goal = nil
	if cmd := bare.maybeContinueGoal(); cmd != nil {
		t.Fatal("scheduler started a turn without a manager")
	}
}

func TestGoalSchedulerStopsWhenTheBudgetIsExhausted(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	started, err := mgr.Start("keep working")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Use the whole budget of 4.
	for i := 0; i < 4; i++ {
		res, err := mgr.ConsumeAttempt()
		if err != nil || !res.Allowed {
			t.Fatalf("ConsumeAttempt %d = %+v, %v", i+1, res, err)
		}
	}
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("scheduler started a turn past the attempt budget")
	}
	snap := mgr.Snapshot()
	if snap.Status != goal.BudgetLimited {
		t.Fatalf("status = %q, want budget_limited", snap.Status)
	}
	if snap.ID != started.State.ID {
		t.Fatalf("budget limit changed the goal id: %q", snap.ID)
	}
}

func TestGoalSchedulerStopsAfterPause(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	if _, err := mgr.Start("keep working"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.handleSlashCommand("/goal pause")
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("scheduler continued a paused goal")
	}
	if cmd := m.handleSlashCommand("/goal resume"); cmd == nil {
		t.Fatal("/goal resume did not start the next continuation")
	}
}

// A continuation must not be skill-expanded: an objective containing "$name"
// would otherwise be rewritten on every automatic turn.
func TestGoalContinuationDoesNotExpandSkills(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok"), llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)
	m.skills = nil
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("run $design over the docs"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	next := m.maybeContinueGoal()
	if next == nil {
		t.Fatal("no continuation")
	}
	runCmd(t, m, next)

	calls := prov.Calls
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	last := calls[0].Messages[len(calls[0].Messages)-1]
	text := last.Content[len(last.Content)-1].Text
	if strings.Contains(text, "[skill:") {
		t.Fatalf("continuation prompt was skill-expanded:\n%s", text)
	}
	if !strings.Contains(text, "$design") {
		t.Fatalf("continuation prompt lost the literal objective:\n%s", text)
	}
}

// The scheduler must not run when the previous turn failed or could not be
// saved: a goal should never advance on state the session did not record.
func TestGoalSchedulerSkipsAfterAFailedOrUnsavedTurn(t *testing.T) {
	for _, tt := range []struct {
		name        string
		err         error
		saveErr     error
		saveAttempt bool
	}{
		{name: "agent error", err: errTestTurn},
		{name: "save error", saveErr: errTestTurn, saveAttempt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
			m, mgr, _ := goalTestModel(t, prov)
			m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
			if _, err := mgr.Start("keep working"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			m.busy = true
			m.Update(sendResultMsg{err: tt.err, saveErr: tt.saveErr, saveAttempted: tt.saveAttempt})
			if m.busy {
				t.Fatal("a failed turn started a continuation")
			}
			if snap := mgr.Snapshot(); snap.Attempts.Used != 0 {
				t.Fatalf("attempts = %d, want none consumed after a failed turn", snap.Attempts.Used)
			}
		})
	}
}

func TestGoalSchedulerStartsAfterASuccessfulTurnEnd(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok"), llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	if _, err := mgr.Start("keep working"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.busy = true
	m.busySince = timeNow()

	m.Update(sendResultMsg{})

	if !m.busy {
		t.Fatal("a successful turn did not start the continuation")
	}
	if snap := mgr.Snapshot(); snap.Attempts.Used != 1 {
		t.Fatalf("attempts = %d, want 1", snap.Attempts.Used)
	}
	if m.sendCancel != nil {
		m.sendCancel()
	}
}
