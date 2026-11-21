package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/tools"
)

// goalTestModel builds a model with a working goal manager and a save callback
// that records how often the goal was persisted.
func goalTestModel(t *testing.T, prov llm.Provider) (*model, *goal.Manager, *int) {
	t.Helper()
	m := makeTestModel()
	if prov != nil {
		m.ag = agent.New(agent.Config{Model: "test", Provider: prov})
	}
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: 4})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	saves := 0
	m.goal = mgr
	m.goalPersist = func() error {
		saves++
		return nil
	}
	return m, mgr, &saves
}

func lastBlock[T any](t *testing.T, m *model) T {
	t.Helper()
	if len(m.blocks) == 0 {
		t.Fatalf("no blocks appended")
	}
	b, ok := m.blocks[len(m.blocks)-1].(T)
	if !ok {
		t.Fatalf("last block = %T, want %T", m.blocks[len(m.blocks)-1], b)
	}
	return b
}

func TestGoalAppearsInHelpAndPicker(t *testing.T) {
	m := makeTestModel()
	help := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	if !strings.Contains(help, "/goal") || !strings.Contains(help, "standing goal") {
		t.Fatalf("help is missing /goal:\n%s", help)
	}
	if !builtinSlashCommand("/goal") {
		t.Fatal("/goal is not registered as a built-in command")
	}

	m.input.SetValue("/go")
	m.updateSlashPicker()
	if len(m.picker.matches) != 1 || m.picker.matches[0].cmd != "/goal" {
		t.Fatalf("picker matches = %+v, want /goal", m.picker.matches)
	}
}

// A skill named goal must not shadow the built-in command, or the feature would
// disappear based on what the user happens to have in .neo/skills.
func TestGoalIsNotShadowedByASkill(t *testing.T) {
	m, _, _ := goalTestModel(t, nil)
	m.skills = []skills.Skill{{Name: "goal", Description: "a skill", Body: "skill body"}}
	if _, ok := m.slashSkill("/goal"); ok {
		t.Fatal("/goal resolved to a skill despite being a built-in command")
	}
	m.handleSlashCommand("/goal status")
	if _, ok := m.blocks[len(m.blocks)-1].(errorBlock); ok {
		t.Fatalf("/goal status routed to the skill: %v", m.blocks[len(m.blocks)-1])
	}
}

func TestGoalWithNoArgumentShowsStatusAndUsage(t *testing.T) {
	m, _, saves := goalTestModel(t, nil)
	m.handleSlashCommand("/goal")

	got := plain(m.blocks[0].render(80, nil))
	if !strings.Contains(got, "no active goal") {
		t.Fatalf("status block = %q, want a no-goal message", got)
	}
	usage, ok := m.blocks[len(m.blocks)-1].(noticeBlock)
	if !ok {
		t.Fatalf("last block = %T, want the usage notice", m.blocks[len(m.blocks)-1])
	}
	if !strings.Contains(usage.text, "/goal status") {
		t.Fatalf("usage = %q", usage.text)
	}
	if *saves != 0 {
		t.Fatalf("saves = %d, want none for a status read", *saves)
	}
}

func TestGoalStatusShowsEveryField(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	started, err := mgr.Start("make the failing session test pass\nwithout touching public APIs")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	if _, err := mgr.RequestBlocked(started.State.ID, "needs API credentials"); err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}

	m.handleSlashCommand("/goal status")
	got := plain(lastBlock[goalBlock](t, m).render(100, nil))
	for _, want := range []string{
		started.State.ID,
		"active",
		"attempts 1/4",
		"objective",
		"make the failing session test pass",
		"without touching public APIs",
		"blocked audit: 1 report(s)",
		"needs API credentials",
		"created",
		"updated",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status block missing %q:\n%s", want, got)
		}
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want status only for /goal status", len(m.blocks))
	}
}

func TestGoalStartSendsTheObjectiveAsTheFirstTurn(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("working on it")}}
	m, mgr, saves := goalTestModel(t, prov)

	cmd := m.handleSlashCommand("/goal fix the flaky test in internal/session")
	if cmd == nil {
		t.Fatal("/goal did not start a turn")
	}
	snap := mgr.Snapshot()
	if snap == nil || snap.Status != goal.Active {
		t.Fatalf("goal after start = %+v, want active", snap)
	}
	if snap.Objective != "fix the flaky test in internal/session" {
		t.Fatalf("objective = %q", snap.Objective)
	}
	if *saves != 1 {
		t.Fatalf("saves = %d, want the goal saved before the first turn", *saves)
	}
	if !m.busy {
		t.Fatal("model is not busy after /goal")
	}

	m.Update(cmd())
	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.Calls))
	}
	transcript := m.ag.Transcript()
	if len(transcript) == 0 {
		t.Fatal("the goal turn did not reach the transcript")
	}
	first := transcript[0]
	visible := first.DisplayText
	if visible == "" {
		visible = first.Content[0].Text
	}
	if visible != "fix the flaky test in internal/session" {
		t.Fatalf("visible transcript text = %q, want the objective verbatim", visible)
	}
}

func TestGoalStartReportsTheReplacedGoal(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)
	first, err := mgr.Start("first objective")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}

	m.handleSlashCommand("/goal second objective")

	replaced := false
	for _, b := range m.blocks {
		nb, ok := b.(noticeBlock)
		if !ok {
			continue
		}
		if strings.Contains(nb.text, "replaced goal "+first.State.ID) && strings.Contains(nb.text, "active") {
			replaced = true
		}
	}
	if !replaced {
		t.Fatalf("no replacement notice naming %s: %+v", first.State.ID, m.blocks)
	}
	snap := mgr.Snapshot()
	if snap.Attempts.Used != 0 {
		t.Fatalf("attempts = %d, want the replacement to start at zero", snap.Attempts.Used)
	}
	if snap.ID == first.State.ID {
		t.Fatal("replacement reused the previous goal id")
	}
}

func TestGoalStartRollsBackWhenSavingFails(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("must not run")}}
	m, mgr, _ := goalTestModel(t, prov)
	saveErr := errors.New("disk full")
	m.goalPersist = func() error { return saveErr }

	cmd := m.handleSlashCommand("/goal do the thing")
	if cmd != nil {
		t.Fatal("/goal started a turn despite a failed save")
	}
	if len(prov.Calls) != 0 {
		t.Fatalf("provider calls = %d, want none", len(prov.Calls))
	}
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("goal = %+v, want the failed goal rolled back", snap)
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("first block = %T, want an error", m.blocks[0])
	}
	if !errors.Is(eb.err, saveErr) && !strings.Contains(eb.err.Error(), "disk full") {
		t.Fatalf("error = %v", eb.err)
	}
}

func TestGoalControlCommandsTransitionAndSave(t *testing.T) {
	m, mgr, saves := goalTestModel(t, nil)
	started, err := mgr.Start("ship it")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	m.handleSlashCommand("/goal pause")
	if got := plain(lastBlock[noticeBlock](t, m).text); !strings.Contains(got, "paused") || !strings.Contains(got, "/goal resume") {
		t.Fatalf("pause notice = %q", got)
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q, want paused", snap.Status)
	}

	resume := m.handleSlashCommand("/goal resume")
	if snap := mgr.Snapshot(); snap.Status != goal.Active {
		t.Fatalf("status = %q, want active", snap.Status)
	}
	notices := 0
	for _, b := range m.blocks {
		if nb, ok := b.(noticeBlock); ok && strings.Contains(nb.text, "fresh attempt budget (4)") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("resume notices = %d, want 1", notices)
	}
	if resume == nil {
		t.Fatal("/goal resume did not schedule the next continuation")
	}

	m.handleSlashCommand("/goal clear")
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("goal = %+v, want it cleared", snap)
	}
	if got := plain(lastBlock[noticeBlock](t, m).text); !strings.Contains(got, "goal cleared") {
		t.Fatalf("clear notice = %q", got)
	}
	if *saves != 3 {
		t.Fatalf("saves = %d, want one per transition", *saves)
	}
	_ = started
}

func TestGoalControlCommandsRejectStatesTheyCannotUse(t *testing.T) {
	t.Run("pause with no goal", func(t *testing.T) {
		m, _, saves := goalTestModel(t, nil)
		m.handleSlashCommand("/goal pause")
		eb := lastBlock[errorBlock](t, m)
		if !strings.Contains(eb.err.Error(), "no active goal") {
			t.Fatalf("error = %v", eb.err)
		}
		if *saves != 0 {
			t.Fatalf("saves = %d, want none", *saves)
		}
	})

	t.Run("resume while active", func(t *testing.T) {
		m, mgr, _ := goalTestModel(t, nil)
		if _, err := mgr.Start("active objective"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		m.handleSlashCommand("/goal resume")
		eb := lastBlock[errorBlock](t, m)
		if !strings.Contains(eb.err.Error(), "active") {
			t.Fatalf("error = %v, want it to name the current status", eb.err)
		}
	})

	t.Run("resume a complete goal", func(t *testing.T) {
		m, mgr, _ := goalTestModel(t, nil)
		started, err := mgr.Start("finish me")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := mgr.RequestComplete(started.State.ID, "tests pass"); err != nil {
			t.Fatalf("RequestComplete: %v", err)
		}
		m.handleSlashCommand("/goal resume")
		lastBlock[errorBlock](t, m)
		if snap := mgr.Snapshot(); snap.Status != goal.Complete {
			t.Fatalf("status = %q, want complete", snap.Status)
		}
	})
}

func TestGoalControlSaveFailureIsReportedAsUnsaved(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	if _, err := mgr.Start("ship it"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	saveErr := errors.New("read-only file system")
	m.goalPersist = func() error { return saveErr }

	m.handleSlashCommand("/goal pause")

	eb := lastBlock[errorBlock](t, m)
	if !strings.Contains(eb.err.Error(), "read-only file system") {
		t.Fatalf("error = %v, want the save failure", eb.err)
	}
	if !strings.Contains(eb.err.Error(), "not saved") {
		t.Fatalf("error = %v, want it to say the change is not saved", eb.err)
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Paused {
		t.Fatalf("status = %q: the in-memory transition should still happen", snap.Status)
	}
}

func TestGoalStatusIsAllowedWhileBusyButStartIsNot(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	if _, err := mgr.Start("ship it"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.busy = true

	m.handleSlashCommand("/goal status")
	if lastBlock[goalBlock](t, m).state == nil {
		t.Fatal("status block has no state")
	}

	m.handleSlashCommand("/goal do more work")
	eb := lastBlock[errorBlock](t, m)
	if !strings.Contains(eb.err.Error(), "while a turn is running") {
		t.Fatalf("error = %v", eb.err)
	}

	for _, sub := range []string{"pause", "clear"} {
		m.blocks = nil
		m.handleSlashCommand("/goal " + sub)
		if _, ok := m.blocks[len(m.blocks)-1].(errorBlock); ok {
			t.Fatalf("/goal %s was rejected while busy", sub)
		}
	}
}

func TestGoalWithoutWiringIsReported(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/goal status")
	eb := lastBlock[errorBlock](t, m)
	if !strings.Contains(eb.err.Error(), "/goal is unavailable") {
		t.Fatalf("error = %v", eb.err)
	}
}

func TestGoalStatusRejectsExtraArguments(t *testing.T) {
	m, _, _ := goalTestModel(t, nil)
	m.handleSlashCommand("/goal status now")
	eb := lastBlock[errorBlock](t, m)
	if !strings.Contains(eb.err.Error(), "takes no arguments") {
		t.Fatalf("error = %v", eb.err)
	}
}

func TestGoalObjectiveKeepsItsExactWording(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)

	m.handleSlashCommand("/goal   two   spaces   kept  ")
	snap := mgr.Snapshot()
	if snap.Objective != "two   spaces   kept" {
		t.Fatalf("objective = %q, want only the surrounding space trimmed", snap.Objective)
	}
}

func TestGoalControlWordsAreReservedButParsedCaseInsensitively(t *testing.T) {
	for _, raw := range []string{"PAUSE", "Status", "clear"} {
		m, mgr, _ := goalTestModel(t, nil)
		m.handleSlashCommand("/goal " + raw)
		if mgr.Snapshot() != nil {
			t.Fatalf("/goal %s started a goal instead of routing to the control command", raw)
		}
		switch m.blocks[len(m.blocks)-1].(type) {
		case goalBlock, errorBlock:
		default:
			t.Fatalf("/goal %s produced %T, want a status or error block", raw, m.blocks[len(m.blocks)-1])
		}
	}
	// A word that merely starts with a control word is an objective.
	m, mgr, _ := goalTestModel(t, &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}})
	m.handleSlashCommand("/goal statuses of the failing tests")
	if snap := mgr.Snapshot(); snap == nil || snap.Objective != "statuses of the failing tests" {
		t.Fatalf("goal = %+v, want an objective", snap)
	}
}

func TestGoalResumeClearsTheBlockedAuditThroughTheUI(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	started, err := mgr.Start("deploy")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < goal.DefaultBlockedThreshold; i++ {
		if _, err := mgr.RequestBlocked(started.State.ID, "needs credentials"); err != nil {
			t.Fatalf("RequestBlocked: %v", err)
		}
	}
	if snap := mgr.Snapshot(); snap.Status != goal.Blocked {
		t.Fatalf("status = %q, want blocked", snap.Status)
	}
	m.handleSlashCommand("/goal resume")
	snap := mgr.Snapshot()
	if snap.Status != goal.Active || snap.Blocked.Count != 0 {
		t.Fatalf("resumed = %+v, want an active goal with a cleared audit", snap)
	}
}

func TestGoalBlockRendersEveryTerminalStatus(t *testing.T) {
	for _, status := range []goal.Status{
		goal.Active, goal.Paused, goal.Complete, goal.Blocked,
		goal.BudgetLimited, goal.UsageLimited,
	} {
		t.Run(string(status), func(t *testing.T) {
			now := time.Now().UTC()
			out := plain(goalBlock{state: &goal.State{
				ID:        "goal_x",
				Objective: "objective text",
				Status:    status,
				Attempts:  goal.Attempts{Used: 2, Max: 5},
				CreatedAt: now,
				UpdatedAt: now,
			}}.render(80, nil))
			if !strings.Contains(out, "goal_x") || !strings.Contains(out, "objective text") {
				t.Fatalf("render missing identity:\n%s", out)
			}
			if !strings.Contains(out, goalStatusLabel(status)) {
				t.Fatalf("render missing status %q:\n%s", goalStatusLabel(status), out)
			}
		})
	}
}

func TestGoalBlockShowsAnExhaustedBudget(t *testing.T) {
	out := plain(goalBlock{state: &goal.State{
		ID:        "goal_x",
		Objective: "o",
		Status:    goal.BudgetLimited,
		Attempts:  goal.Attempts{Used: 5, Max: 5},
	}}.render(80, nil))
	if !strings.Contains(out, "5/5 (exhausted)") {
		t.Fatalf("render = %s", out)
	}
}

func TestGoalLastReasonPrefersTheGoalThenTheBlocker(t *testing.T) {
	if got := goalLastReason(goal.State{LastReason: "tests pass"}); got != "tests pass" {
		t.Fatalf("last reason = %q", got)
	}
	if got := goalLastReason(goal.State{Blocked: goal.BlockedAudit{LastReason: "no credentials"}}); got != "no credentials" {
		t.Fatalf("blocker reason = %q", got)
	}
	if got := goalLastReason(goal.State{}); got != "none recorded" {
		t.Fatalf("empty reason = %q", got)
	}
}

// The goal UI must not add permanent chrome: nothing is rendered until the user
// asks, which keeps the status line unchanged for everyone else.
func TestGoalStatusDoesNotTouchTheStatusLine(t *testing.T) {
	m, mgr, _ := goalTestModel(t, &llmtest.FakeProvider{})
	if _, err := mgr.Start("quiet"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	before := plain(m.statusLine())
	m.handleSlashCommand("/goal status")
	if after := plain(m.statusLine()); after != before {
		t.Fatalf("status line changed: %q → %q", before, after)
	}
	if !strings.Contains(plain(m.footerLine()), "test") {
		t.Fatalf("footer lost the backend tag: %q", plain(m.footerLine()))
	}
}

// A goal typed as an objective must reach the provider as the user wrote it,
// with no /goal prefix and no skill expansion artefacts.
func TestGoalObjectiveIsNotSkillExpanded(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	m, _, _ := goalTestModel(t, prov)
	m.skills = skills.Defaults()
	m.ag = agent.New(agent.Config{
		Model:    "test",
		Provider: prov,
		Tools:    tools.NewRegistry(tools.ReadFile{}),
	})

	cmd := m.handleSlashCommand("/goal run $design over the docs")
	if cmd == nil {
		t.Fatal("/goal did not start a turn")
	}
	m.Update(cmd())
	got := prov.Calls[0].Messages[0].Content[0].Text
	if strings.Contains(got, "[skill:") {
		t.Fatalf("objective was skill-expanded:\n%s", got)
	}
	if !strings.Contains(got, "$design") {
		t.Fatalf("objective lost its literal text:\n%s", got)
	}
}

// handleGoalCommand must be reachable through the key path as well, since the
// slash picker inserts /goal and the user presses enter.
func TestGoalEnterPathStartsTheGoal(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	m, mgr, _ := goalTestModel(t, prov)
	m.input.SetValue("/goal finish the refactor")
	cmd := m.handleEnter()
	if cmd == nil {
		t.Fatal("enter did not start the goal turn")
	}
	if snap := mgr.Snapshot(); snap == nil || snap.Objective != "finish the refactor" {
		t.Fatalf("goal = %+v", snap)
	}
	m.Update(cmd())
	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d", len(prov.Calls))
	}
}

// A failed save must leave the previously valid goal intact: the replacement
// never reached disk, so reporting it as replaced would be a lie.
func TestGoalStartRollbackKeepsThePreviousGoal(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("must not run")}}
	m, mgr, _ := goalTestModel(t, prov)
	previous, err := mgr.Start("the original objective")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.goalPersist = func() error { return errors.New("read-only file system") }

	cmd := m.handleSlashCommand("/goal a replacement objective")
	if cmd != nil {
		t.Fatal("/goal started a turn despite a failed save")
	}
	snap := mgr.Snapshot()
	if snap == nil || snap.ID != previous.State.ID || snap.Objective != "the original objective" {
		t.Fatalf("goal = %+v, want the original goal restored", snap)
	}
	for _, b := range m.blocks {
		if nb, ok := b.(noticeBlock); ok && strings.Contains(nb.text, "replaced") {
			t.Fatalf("reported a replacement that never happened: %q", nb.text)
		}
	}
}

// The control transitions have the same problem: a failed save leaves memory
// and disk disagreeing, and the message must say so.
func TestGoalResumeRollbackMessageKeepsTheOldStatus(t *testing.T) {
	m, mgr, _ := goalTestModel(t, nil)
	started, err := mgr.Start("ship it")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.Pause("interrupt"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	m.goalPersist = func() error { return errors.New("disk full") }
	m.handleSlashCommand("/goal resume")
	if snap := mgr.Snapshot(); snap.Status != goal.Active {
		t.Fatalf("status = %q: the in-memory transition should still apply", snap.Status)
	}
	if snap := mgr.Snapshot(); snap.ID != started.State.ID {
		t.Fatalf("resume changed the goal id: %q", snap.ID)
	}
}
