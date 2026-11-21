package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

func TestResetConversationClearsConversationState(t *testing.T) {
	m := makeTestModel()
	m.ag = agent.New(agent.Config{
		Model:    "selected-model",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(tools.ReadFile{}),
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: "text", Text: "old conversation"}},
		}},
		Usage: llm.Usage{InputTokens: 11, OutputTokens: 7},
	})
	m.blocks = []block{noticeBlock{text: "old transcript"}}
	m.busy = true
	m.busySince = time.Now()
	m.trackToolCall("t", &toolCallBlock{name: "read_file"})
	m.trackToolCall("call", &toolCallBlock{name: "bash"})
	m.workflow = &workflowBlock{
		title: "Old workflow",
		items: []workflow.Item{{ID: "step", Text: "Old step", Status: workflow.Running}},
	}
	m.turn = turnStats{tools: 2, errors: 1, workflow: true, direct: true, label: "Review"}
	approvalReply := make(chan bool, 1)
	m.approval = &approvalState{req: agent.ApprovalRequest{ToolName: "bash"}, reply: approvalReply}
	canceled := false
	m.sendCancel = func() { canceled = true }
	m.pendingSteering = []string{"old steering"}
	m.queued = &queuedTurn{displayText: "old follow-up", agentText: "old follow-up"}
	m.input.SetValue("old draft")
	m.picker = commandPicker{visible: true, dismissedFor: "/old"}
	m.files.visible = true
	m.files.matches = []string{"old.go"}
	m.files.selected = 1
	m.files.token = filePickerToken{raw: "@old"}
	m.models = modelBrowser{visible: true, query: "old"}
	mgr, err := goal.NewManager(nil, goal.Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Start("old objective"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.goal = mgr

	cleared := m.resetConversation()

	if got := m.ag.Transcript(); len(got) != 0 {
		t.Fatalf("transcript = %#v, want empty", got)
	}
	if got := m.ag.Usage(); got != (llm.Usage{}) {
		t.Fatalf("usage = %+v, want zero", got)
	}
	if len(m.blocks) != 0 || m.viewport.TotalLineCount() != 0 {
		t.Fatalf("rendered transcript was not cleared: blocks=%d lines=%d", len(m.blocks), m.viewport.TotalLineCount())
	}
	if m.busy || !m.busySince.IsZero() || len(m.inflight) != 0 {
		t.Fatalf("active turn state remains: busy=%v since=%v tools=%#v", m.busy, m.busySince, m.inflight)
	}
	if m.inflight != nil {
		t.Fatalf("inflight tool state remains: %#v", m.inflight)
	}
	if m.workflow != nil {
		t.Fatalf("workflow state remains: %#v", m.workflow)
	}
	if m.turn != (turnStats{}) {
		t.Fatalf("turn state remains: %+v", m.turn)
	}
	if m.approval != nil || m.sendCancel != nil || m.pendingSteering != nil || m.queued != nil {
		t.Fatalf("pending activity remains: approval=%#v cancel=%v steering=%#v queued=%#v",
			m.approval, m.sendCancel != nil, m.pendingSteering, m.queued)
	}
	if !canceled {
		t.Fatal("in-flight context was not canceled")
	}
	select {
	case approved := <-approvalReply:
		if approved {
			t.Fatal("pending approval was approved during reset")
		}
	default:
		t.Fatal("pending approval waiter was not released")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("composer = %q, want empty", got)
	}
	if m.picker.visible || m.files.visible || m.files.token != (filePickerToken{}) || m.models.visible {
		t.Fatalf("transient UI remains: picker=%#v files=%#v models=%#v", m.picker, m.files, m.models)
	}
	if cleared == nil || cleared.Objective != "old objective" {
		t.Fatalf("cleared goal = %+v, want the previous objective so the caller can report it", cleared)
	}
	if snap := m.goal.Snapshot(); snap != nil {
		t.Fatalf("goal survived /clear: %+v", snap)
	}
}

func TestResetConversationRetainsLongLivedSettings(t *testing.T) {
	m := makeTestModel()
	m.modelTag = "selected-model"
	m.providerTag = "openai"
	m.cwd = "~/Code/neo"
	m.branch = "feature"
	m.mdStyleName = "light"
	m.skills = []skills.Skill{{Name: "review", Body: "Review it"}}
	m.modelChoices = []ModelChoice{{ID: "selected-model", Name: "Selected"}}
	m.verbose = true
	m.files.root = "/workspace"
	m.files.files = []string{"cached.go"}
	m.files.err = errors.New("cached failure")
	afterSendCalls := 0
	m.afterSend = func() error {
		afterSendCalls++
		return nil
	}
	modelSwitcher := func(string) error { return nil }
	m.modelSwitcher = modelSwitcher
	steer := func(string) bool { return true }
	m.steer = steer
	originalContext := m.ctx
	originalAgent := m.ag

	m.handleSlashCommand("/clear")

	if m.ctx != originalContext || m.ag != originalAgent {
		t.Fatal("reset replaced the model context or agent")
	}
	if got := m.ag.Model(); got != "test" {
		t.Fatalf("agent model = %q, want retained test model", got)
	}
	if m.modelTag != "selected-model" || m.providerTag != "openai" || m.cwd != "~/Code/neo" || m.branch != "feature" {
		t.Fatalf("backend or workspace settings changed: model=%q provider=%q cwd=%q branch=%q",
			m.modelTag, m.providerTag, m.cwd, m.branch)
	}
	if m.mdStyleName != "light" || !m.verbose {
		t.Fatalf("presentation settings changed: style=%q verbose=%v", m.mdStyleName, m.verbose)
	}
	if !reflect.DeepEqual(m.skills, []skills.Skill{{Name: "review", Body: "Review it"}}) ||
		!reflect.DeepEqual(m.modelChoices, []ModelChoice{{ID: "selected-model", Name: "Selected"}}) {
		t.Fatalf("commands or model choices changed: skills=%#v choices=%#v", m.skills, m.modelChoices)
	}
	if m.files.root != "/workspace" || !reflect.DeepEqual(m.files.files, []string{"cached.go"}) || m.files.err != nil {
		t.Fatalf("file picker configuration/cache changed: %#v", m.files)
	}
	if m.modelSwitcher == nil || m.steer == nil {
		t.Fatal("long-lived callbacks were cleared")
	}
	if afterSendCalls != 1 {
		t.Fatalf("afterSend calls = %d, want 1", afterSendCalls)
	}
}

func TestResetConversationIgnoresBufferedActivityFromOldGeneration(t *testing.T) {
	m := makeTestModel()
	oldGeneration := m.conversationGeneration

	m.resetConversation()
	m.handleWorkflowEvent(workflow.Event{
		Generation: oldGeneration,
		Action:     "create",
		State: workflow.State{
			Title: "Old workflow",
			Items: []workflow.Item{{ID: "old", Text: "Old step"}},
		},
	})

	if m.workflow != nil || len(m.blocks) != 0 {
		t.Fatalf("old activity repopulated reset state: workflow=%#v blocks=%#v", m.workflow, m.blocks)
	}
}

func TestOldActivityStaysStaleAfterNewTurnStarts(t *testing.T) {
	m := makeTestModel()
	oldGeneration := m.conversationGeneration
	m.resetConversation()
	newGeneration := m.conversationGeneration
	send := m.submitUserTurn("new turn", "new turn", nil)
	t.Cleanup(func() {
		if m.sendCancel != nil {
			m.sendCancel()
		}
	})
	if send == nil {
		t.Fatal("new turn did not create a send command")
	}

	m.handleWorkflowEvent(workflow.Event{
		Generation: oldGeneration,
		Action:     "create",
		State: workflow.State{
			Title: "Old workflow",
			Items: []workflow.Item{{ID: "old", Text: "Old step"}},
		},
	})

	if m.conversationGeneration == newGeneration || m.conversationGeneration == oldGeneration {
		t.Fatalf("new turn should advance the generation past %d and %d, got %d", oldGeneration, newGeneration, m.conversationGeneration)
	}
	if m.workflow != nil {
		t.Fatalf("old activity was relabeled for new turn: workflow=%#v", m.workflow)
	}
	if len(m.blocks) != 1 {
		t.Fatalf("old activity appended blocks to new turn: %#v", m.blocks)
	}
	if _, ok := m.blocks[0].(userBlock); !ok {
		t.Fatalf("new turn block = %T, want userBlock", m.blocks[0])
	}
}

// /clear must clear an active goal and save that fact: a surviving goal would
// keep the scheduler enqueueing continuations against an emptied transcript.
func TestClearClearsTheActiveGoalAndSavesOnce(t *testing.T) {
	m := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: 4})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("fix the flaky test")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	m.goal = mgr
	m.blocks = append(m.blocks, noticeBlock{text: "keep"})
	saves := 0
	m.afterSend = func() error {
		saves++
		return nil
	}

	cmd := m.handleSlashCommand("/clear")
	if cmd != nil {
		t.Fatal("/clear returned a command")
	}
	if snap := m.goal.Snapshot(); snap != nil {
		t.Fatalf("goal survived /clear: %+v", snap)
	}
	if saves != 1 {
		t.Fatalf("saves = %d, want one save after clearing", saves)
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %+v, want the clearing notice only", m.blocks)
	}
	nb, ok := m.blocks[0].(noticeBlock)
	if !ok {
		t.Fatalf("block = %T, want a notice", m.blocks[0])
	}
	for _, want := range []string{started.State.ID, "fix the flaky test", "/goal pause"} {
		if !strings.Contains(nb.text, want) {
			t.Fatalf("notice %q missing %q", nb.text, want)
		}
	}
}

// Clearing a conversation with no goal stays quiet: no notice, no extra save
// behaviour beyond the existing session save.
func TestClearWithoutAGoalAddsNoNotice(t *testing.T) {
	m := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.goal = mgr
	m.afterSend = func() error { return nil }

	m.handleSlashCommand("/clear")

	if len(m.blocks) != 0 {
		t.Fatalf("blocks = %+v, want none", m.blocks)
	}
}

// A TUI without goal wiring still clears its conversation, exactly as before.
func TestClearWithoutGoalWiringWorks(t *testing.T) {
	m := makeTestModel()
	m.blocks = append(m.blocks, noticeBlock{text: "old"})
	m.handleSlashCommand("/clear")
	if len(m.blocks) != 0 {
		t.Fatalf("blocks = %+v, want the transcript cleared", m.blocks)
	}
}

// The save failure path is unchanged: the error must be visible.
func TestClearWithGoalReportsSaveFailure(t *testing.T) {
	m := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Start("ship it"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.goal = mgr
	saveErr := errors.New("save failed")
	m.afterSend = func() error { return saveErr }

	m.handleSlashCommand("/clear")

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %+v, want the notice plus the save error", m.blocks)
	}
	eb, ok := m.blocks[1].(errorBlock)
	if !ok {
		t.Fatalf("second block = %T, want an error", m.blocks[1])
	}
	if !errors.Is(eb.err, saveErr) {
		t.Fatalf("error = %v, want %v", eb.err, saveErr)
	}
}

// resetConversation is the single owner of conversation-scoped cleanup, so it
// must clear the goal even when called directly rather than through /clear.
func TestResetConversationClearsTheGoalDirectly(t *testing.T) {
	m := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("direct reset")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.goal = mgr

	cleared := m.resetConversation()

	if snap := m.goal.Snapshot(); snap != nil {
		t.Fatalf("goal survived resetConversation: %+v", snap)
	}
	if cleared == nil || cleared.ID != started.State.ID || cleared.Status != goal.Cleared {
		t.Fatalf("cleared = %+v, want the old goal reported as cleared", cleared)
	}
}
