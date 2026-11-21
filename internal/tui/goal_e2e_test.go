package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

// funcProvider answers each call from a function, so a scripted response can
// depend on product state (the goal's id and status) the way a real model would
// learn it from a previous tool result.
type funcProvider struct {
	calls []llm.Request
	fn    func(n int, req llm.Request) llm.Response
}

func (p *funcProvider) Name() string { return "fake" }

func (p *funcProvider) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	n := len(p.calls)
	p.calls = append(p.calls, req)
	resp := p.fn(n, req)
	return &resp, nil
}

// goalE2E drives one chat session the way Bubble Tea does: it executes the
// commands the model schedules, feeds their messages back into Update, and
// expands batches. Every assertion reads user-visible output — transcript
// blocks, rendered status, or the saved session JSON — never manager internals.
type goalE2E struct {
	t     *testing.T
	m     *model
	mgr   *goal.Manager
	prov  *funcProvider
	store *session.Store
	sess  *session.Session
}

// newGoalE2E builds a chat model with the interactive registry: workflow and
// the goal tool. Subagents are omitted because each child would need its own
// provider, and the goal loop is what this test covers.
func newGoalE2E(t *testing.T, maxAttempts int, respond func(n int) llm.Response) *goalE2E {
	t.Helper()
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: maxAttempts})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(t.Context(), session.Metadata{CWD: "/workspace", Model: "test-model"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &funcProvider{fn: func(n int, _ llm.Request) llm.Response { return respond(n) }}

	reg := tools.NewRegistry(
		tools.ReadFile{}, tools.Bash{CWD: t.TempDir()}, workflow.Tool{},
		goal.Tool{Manager: mgr},
	)
	ag := agent.New(agent.Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    reg,
		Messages: sess.Messages,
		Usage:    sess.Usage,
	})

	m := makeTestModel()
	m.ag = ag
	m.goal = mgr
	m.afterSend = func() error { return saveGoalSession(t.Context(), store, sess, ag, mgr) }
	m.goalPersist = m.afterSend

	e := &goalE2E{t: t, m: m, mgr: mgr, prov: prov, store: store, sess: sess}
	t.Cleanup(func() {
		if m.sendCancel != nil {
			m.sendCancel()
		}
	})
	return e
}

// saveGoalSession mirrors cmd/neo's saveChatSession: transcript, usage, and the
// manager's snapshot in one write.
func saveGoalSession(ctx context.Context, store *session.Store, sess *session.Session, ag *agent.Agent, mgr *goal.Manager) error {
	sess.Messages = ag.Transcript()
	sess.Usage = ag.Usage()
	if mgr != nil {
		sess.Goal = mgr.Snapshot()
	}
	return store.Save(ctx, sess)
}

// send runs exactly one scheduled send and processes its result, returning the
// commands the result produced. It deliberately does not run them: the caller
// decides whether the next continuation may start, which is what makes a
// mid-run interrupt testable.
func (e *goalE2E) send(cmd tea.Cmd) tea.Cmd {
	e.t.Helper()
	if cmd == nil {
		e.t.Fatalf("expected a send command")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); ok {
		e.t.Fatalf("send returned a batch, want a single result")
	}
	_, produced := e.m.Update(msg)
	return produced
}

// drain runs commands until the session goes idle, following batches and the
// continuations they schedule. Spinner ticks are dropped: they are redraw
// timing, not work, and rescheduling them would keep the loop alive forever.
func (e *goalE2E) drain(cmd tea.Cmd) {
	e.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 400 {
			e.t.Fatalf("session did not settle after %d commands", steps)
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		switch typed := msg.(type) {
		case nil:
			continue
		case spinner.TickMsg:
			continue
		case tea.BatchMsg:
			queue = append(queue, typed...)
			continue
		}
		_, produced := e.m.Update(msg)
		if produced != nil {
			queue = append(queue, produced)
		}
	}
}

// runGoal types a /goal command and lets the goal loop run to completion.
func (e *goalE2E) runGoal(line string) {
	e.t.Helper()
	e.m.input.SetValue(line)
	e.drain(e.m.handleEnter())
}

// startGoal types a /goal command and runs only its first turn, leaving the
// continuation it scheduled pending. That is the state a user interrupts.
func (e *goalE2E) startGoal(line string) {
	e.t.Helper()
	e.m.input.SetValue(line)
	e.send(e.m.handleEnter())
	if !e.m.busy {
		e.t.Fatal("the first turn did not schedule a continuation")
	}
}

// transcript renders every block, so assertions read what a user would see.
func (e *goalE2E) transcript() string {
	var b strings.Builder
	for _, blk := range e.m.blocks {
		b.WriteString(plain(blk.render(100, nil)))
		b.WriteString("\n")
	}
	return b.String()
}

// promptText flattens the text a provider call received.
func (e *goalE2E) promptText(call int) string {
	e.t.Helper()
	if call >= len(e.prov.calls) {
		e.t.Fatalf("provider call %d never happened (%d calls)", call+1, len(e.prov.calls))
	}
	var b strings.Builder
	for _, msg := range e.prov.calls[call].Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				b.WriteString(block.Text)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// TestGoalE2ECompletesAndStops is the main path: /goal starts a turn, the model
// marks the goal complete through the tool, and nothing runs afterwards.
func TestGoalE2ECompletesAndStops(t *testing.T) {
	var e *goalE2E
	e = newGoalE2E(t, 5, func(n int) llm.Response {
		if n == 0 {
			return llmtest.Text("inspecting the failing test")
		}
		// From the first continuation on, finish the goal once evidence exists.
		snap := e.mgr.Snapshot()
		if snap != nil && snap.Status == goal.Active && n >= 1 {
			return llmtest.ToolUse("call_complete", "goal", map[string]any{
				"action":  "complete",
				"goal_id": snap.ID,
				"reason":  "go test ./internal/tui passes",
			})
		}
		return llmtest.Text("nothing left to do")
	})

	e.runGoal("/goal make the failing session test pass")

	snap := e.mgr.Snapshot()
	if snap == nil || snap.Status != goal.Complete {
		t.Fatalf("goal after the run = %+v, want complete", snap)
	}
	if snap.Attempts.Used != 1 {
		t.Fatalf("attempts = %d, want exactly the one continuation that ran", snap.Attempts.Used)
	}
	if got := e.promptText(0); !strings.Contains(got, "make the failing session test pass") {
		t.Fatalf("first provider call did not carry the objective:\n%s", got)
	}
	// The continuation prompt is a normal user turn, visibly marked.
	if got := e.promptText(1); !strings.Contains(got, "↳ goal continuation") {
		t.Fatalf("continuation call did not carry the continuation prompt:\n%s", got)
	}
	if calls := len(e.prov.calls); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (initial, continuation, tool follow-up)", calls)
	}
	shown := e.transcript()
	for _, want := range []string{"↳ goal continuation 1/5", "make the failing session test pass"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("transcript missing %q:\n%s", want, shown)
		}
	}

	// A completed goal never continues, and the status card agrees.
	if cmd := e.m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a completed goal scheduled a continuation")
	}
	e.m.input.SetValue("/goal status")
	e.drain(e.m.handleEnter())
	if got := e.transcript(); !strings.Contains(got, "complete") {
		t.Fatalf("status does not show completion:\n%s", got)
	}
}

// TestGoalE2EBudgetStopsWithHandoff is termination path A: the model keeps
// working without completing, so the budget ends the run and the handoff
// explains why.
func TestGoalE2EBudgetStopsWithHandoff(t *testing.T) {
	e := newGoalE2E(t, 2, func(int) llm.Response {
		return llmtest.Text("still working on it")
	})

	e.runGoal("/goal finish the refactor")

	shown := e.transcript()
	for _, want := range []string{
		"Goal budget reached: 2/2 attempts used.",
		"/goal resume",
		"/goal clear",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("handoff missing %q:\n%s", want, shown)
		}
	}
	if snap := e.mgr.Snapshot(); snap == nil || snap.Status != goal.BudgetLimited {
		t.Fatalf("goal after the budget = %+v, want budget_limited", snap)
	}
	if calls := len(e.prov.calls); calls != 3 {
		t.Fatalf("provider calls = %d, want the initial turn plus 2 continuations", calls)
	}
	if cmd := e.m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a budget-limited goal scheduled a continuation")
	}

	// The limit reached disk, so a restart does not resume the run.
	onDisk, err := e.store.Load(t.Context(), e.sess.Metadata.ID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if onDisk.Goal == nil || onDisk.Goal.Status != goal.BudgetLimited {
		t.Fatalf("saved goal = %+v, want budget_limited", onDisk.Goal)
	}
	if onDisk.Goal.Attempts.Used != 2 || onDisk.Goal.Attempts.Max != 2 {
		t.Fatalf("saved attempts = %+v, want 2/2", onDisk.Goal.Attempts)
	}

	// /goal resume gives it a fresh budget and starts the next turn.
	e.m.input.SetValue("/goal resume")
	resume := e.m.handleEnter()
	if resume == nil {
		t.Fatal("/goal resume did not restart the goal")
	}
	if snap := e.mgr.Snapshot(); snap == nil || snap.Status != goal.Active {
		t.Fatalf("goal after resume = %+v, want active", snap)
	}
	if snap := e.mgr.Snapshot(); snap.Attempts.Used != 1 {
		t.Fatalf("attempts after resume = %d, want the fresh budget's first continuation", snap.Attempts.Used)
	}
	e.m.sendCancel()
}

// TestGoalE2EInterruptPausesAndResumeRestarts is termination path B: Esc during
// a continuation pauses the goal, and nothing runs until the user resumes.
func TestGoalE2EInterruptPausesAndResumeRestarts(t *testing.T) {
	e := newGoalE2E(t, 5, func(int) llm.Response { return llmtest.Text("working") })

	e.startGoal("/goal get the integration test green")

	e.m.handleEscape()
	e.m.Update(sendResultMsg{err: context.Canceled})

	snap := e.mgr.Snapshot()
	if snap == nil || snap.Status != goal.Paused {
		t.Fatalf("goal after interrupt = %+v, want paused", snap)
	}
	shown := e.transcript()
	if !strings.Contains(shown, "paused") || !strings.Contains(shown, "/goal resume") {
		t.Fatalf("transcript does not explain the pause:\n%s", shown)
	}
	if cmd := e.m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a paused goal scheduled a continuation")
	}

	// Resume starts the next turn immediately, with a fresh counter.
	e.m.input.SetValue("/goal resume")
	resume := e.m.handleEnter()
	if resume == nil {
		t.Fatal("/goal resume did not start a continuation")
	}
	snap = e.mgr.Snapshot()
	if snap == nil || snap.Status != goal.Active {
		t.Fatalf("goal after resume = %+v, want active", snap)
	}
	if snap.Attempts.Used != 1 {
		t.Fatalf("attempts after resume = %d, want 1 of the fresh budget", snap.Attempts.Used)
	}
	e.m.sendCancel()
}

// TestGoalE2ESurvivesResume checks the persistence boundary: the saved session
// reloads into a manager that renders the same status.
func TestGoalE2ESurvivesResume(t *testing.T) {
	e := newGoalE2E(t, 3, func(int) llm.Response { return llmtest.Text("working") })
	// Run one turn and interrupt the continuation, so the saved state is paused
	// with a partially used budget — the state a user actually restarts from.
	e.startGoal("/goal ship the migration")
	e.m.handleEscape()
	e.m.Update(sendResultMsg{err: context.Canceled})

	before := e.mgr.Snapshot()
	if before == nil {
		t.Fatal("no goal before reload")
	}
	e.m.input.SetValue("/goal status")
	e.drain(e.m.handleEnter())
	statusBefore := lastGoalBlockText(t, e.m)

	reloaded, err := e.store.Load(t.Context(), e.sess.Metadata.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Goal == nil || reloaded.Goal.ID != before.ID {
		t.Fatalf("reloaded goal = %+v, want %s", reloaded.Goal, before.ID)
	}
	resumed, err := goal.NewManager(reloaded.Goal, goal.Limits{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	other := makeTestModel()
	other.goal = resumed
	other.handleSlashCommand("/goal status")
	statusAfter := lastGoalBlockText(t, other)

	for _, want := range []string{before.ID, "paused", "objective", before.Objective} {
		if !strings.Contains(statusBefore, want) {
			t.Fatalf("status before reload missing %q:\n%s", want, statusBefore)
		}
		if !strings.Contains(statusAfter, want) {
			t.Fatalf("status after reload missing %q:\n%s", want, statusAfter)
		}
	}
	if statusBefore != statusAfter {
		t.Fatalf("status text changed across a reload:\nbefore:\n%s\nafter:\n%s", statusBefore, statusAfter)
	}
}

// TestGoalE2EWorkflowToolKeepsTheGoalSeparate keeps the two product concepts
// apart: a finished checklist does not finish the goal.
func TestGoalE2EWorkflowToolKeepsTheGoalSeparate(t *testing.T) {
	e := newGoalE2E(t, 4, func(n int) llm.Response {
		switch n {
		case 0:
			return llmtest.ToolUse("wf_create", "workflow", map[string]any{
				"action": "create",
				"title":  "Plan",
				"items": []any{
					map[string]any{"id": "1", "text": "inspect"},
					map[string]any{"id": "2", "text": "fix"},
				},
			})
		case 1:
			return llmtest.ToolUse("wf_done1", "workflow", map[string]any{"action": "complete", "id": "1"})
		case 2:
			return llmtest.ToolUse("wf_done2", "workflow", map[string]any{"action": "complete", "id": "2"})
		default:
			return llmtest.Text("checklist done")
		}
	})

	e.startGoal("/goal fix the failing test")

	if snap := e.mgr.Snapshot(); snap == nil || snap.Status != goal.Active {
		t.Fatalf("workflow activity changed the goal: %+v", snap)
	}
	// The workflow event path is real: a matching event becomes the live plan.
	e.m.handleWorkflowEvent(workflow.Event{
		Generation: e.m.conversationGeneration,
		Action:     "create",
		State: workflow.State{
			Title: "Plan",
			Items: []workflow.Item{{ID: "1", Text: "inspect"}},
		},
	})
	if e.m.workflow == nil {
		t.Fatal("workflow event was ignored")
	}
	if snap := e.mgr.Snapshot(); snap.Status != goal.Active {
		t.Fatalf("finished checklist changed the goal status: %+v", snap)
	}
	e.m.sendCancel()
}

// TestGoalE2EStaleToolUpdateIsRejected covers the replacement boundary: the
// model cannot finish a goal the user already replaced.
func TestGoalE2EStaleToolUpdateIsRejected(t *testing.T) {
	e := newGoalE2E(t, 4, func(int) llm.Response { return llmtest.Text("working") })
	e.startGoal("/goal first objective")
	first := e.mgr.Snapshot()
	e.m.sendCancel()
	e.m.busy = false

	e.startGoal("/goal second objective")
	second := e.mgr.Snapshot()
	if second.ID == first.ID {
		t.Fatal("replacement reused the goal id")
	}

	out, err := goal.Tool{Manager: e.mgr}.Run(t.Context(), map[string]any{
		"action":  "complete",
		"goal_id": first.ID,
		"reason":  "first objective is done",
	})
	if err != nil {
		t.Fatalf("a stale update should be a result, not an error: %v", err)
	}
	if strings.Contains(out, `"ok":true`) {
		t.Fatalf("a stale complete was accepted: %s", out)
	}
	snap := e.mgr.Snapshot()
	if snap.Status != goal.Active || snap.ID != second.ID {
		t.Fatalf("stale update changed the goal: %+v", snap)
	}
	e.m.sendCancel()
}

func lastGoalBlockText(t *testing.T, m *model) string {
	t.Helper()
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if gb, ok := m.blocks[i].(goalBlock); ok {
			return plain(gb.render(100, nil))
		}
	}
	t.Fatal("no goal status block in the transcript")
	return ""
}
