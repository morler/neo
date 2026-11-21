package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

// goalChatRegistry mirrors the interactive chat registry with the goal tool
// included, so prompt tests see what a chat session actually gets.
func goalChatRegistry() *tools.Registry {
	return newRegistry("", "", workflow.Tool{}, goal.Tool{})
}

func TestChatRegistryIncludesTheGoalTool(t *testing.T) {
	if _, ok := goalChatRegistry().Get("goal"); !ok {
		t.Fatal("chat registry is missing the goal tool")
	}
}

func TestHeadlessRegistryHasNoGoalTool(t *testing.T) {
	// Headless runs take no interactive control input, so a goal tool there
	// would invite state the user cannot observe or steer.
	if _, ok := newRegistry(t.TempDir(), t.TempDir()).Get("goal"); ok {
		t.Fatal("headless registry registered the goal tool")
	}
}

func TestGoalCapabilityGuidanceIsScopedToTheRegisteredTool(t *testing.T) {
	cfg := &config.Config{}
	headless, _ := chatSystem(cfg, t.TempDir(), nil, profile.Profile{}, newRegistry("", ""), io.Discard)
	if strings.Contains(headless, "# Standing goal") || strings.Contains(headless, "goal tool") {
		t.Fatalf("headless prompt describes a goal tool it does not have:\n%s", headless)
	}

	chat, blocks := chatSystem(cfg, t.TempDir(), nil, profile.Profile{}, goalChatRegistry(), io.Discard)
	if !strings.Contains(chat, "# Standing goal") {
		t.Fatalf("chat prompt is missing the standing-goal section:\n%s", chat)
	}
	for _, want := range []string{"goal tool", "complete", "blocked", "attempt budget"} {
		if !strings.Contains(chat, want) {
			t.Fatalf("standing-goal section is missing %q:\n%s", want, chat)
		}
	}
	if len(blocks) == 0 || !blocks[0].Cache {
		t.Fatalf("goal guidance belongs in the cacheable base: %+v", blocks)
	}
}

func TestGoalGuidanceStatesTheOwnershipAndEvidenceRules(t *testing.T) {
	chat, _ := chatSystem(&config.Config{}, t.TempDir(), nil, profile.Profile{}, goalChatRegistry(), io.Discard)
	for _, phrase := range []string{
		"Never redefine the goal as a smaller task",
		"verify the objective",
		"same blocker has\nrecurred",
		"belong to the user",
	} {
		if !strings.Contains(chat, phrase) {
			t.Fatalf("goal guidance is missing %q:\n%s", phrase, chat)
		}
	}
}

func TestChatGoalManagerStartsWithoutASavedGoal(t *testing.T) {
	mgr, warning := chatGoalManager(nil, &config.Config{})
	if warning != "" {
		t.Fatalf("warning = %q, want none for a new session", warning)
	}
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("snapshot = %+v, want nil", snap)
	}
}

func TestChatGoalManagerRestoresASavedGoal(t *testing.T) {
	saved := &goal.State{
		ID:        "goal_saved",
		Objective: "finish the migration",
		Status:    goal.Paused,
		Attempts:  goal.Attempts{Used: 2, Max: 20},
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		UpdatedAt: time.Now().UTC(),
	}
	mgr, warning := chatGoalManager(saved, &config.Config{})
	if warning != "" {
		t.Fatalf("warning = %q, want none", warning)
	}
	snap := mgr.Snapshot()
	if snap == nil || snap.ID != "goal_saved" || snap.Status != goal.Paused {
		t.Fatalf("restored = %+v", snap)
	}
	// Restoring must not alias the session's record, so later mutations cannot
	// leak back into the value that was loaded from disk.
	snap.Objective = "tampered"
	saved.Objective = "tampered on the session side"
	if got := mgr.Snapshot().Objective; got != "finish the migration" {
		t.Fatalf("objective = %q, want the restored value", got)
	}
}

func TestChatGoalManagerWarnsAndKeepsTheSessionOpenOnACorruptGoal(t *testing.T) {
	mgr, warning := chatGoalManager(&goal.State{ID: "goal_broken", Status: goal.Active}, &config.Config{})
	if !strings.Contains(warning, "ignoring the saved goal") {
		t.Fatalf("warning = %q, want it to explain the saved goal was ignored", warning)
	}
	if mgr == nil {
		t.Fatal("a corrupt saved goal left the session without a manager")
	}
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("snapshot = %+v, want an empty manager", snap)
	}
}

func TestGoalToolRejectsAnUnknownActionThroughTheRegistry(t *testing.T) {
	reg := goalChatRegistry()
	tool, ok := reg.Get("goal")
	if !ok {
		t.Fatal("goal tool is not registered")
	}
	if _, err := tool.Run(t.Context(), map[string]any{"action": "resume"}); err == nil {
		t.Fatal("the registry-exposed goal tool accepted a user-only action")
	}
}

func TestSaveChatSessionPersistsTheManagerGoal(t *testing.T) {
	ctx := t.Context()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}})
	mgr, warning := chatGoalManager(nil, &config.Config{})
	if warning != "" {
		t.Fatalf("warning = %q", warning)
	}
	started, err := mgr.Start("make go test ./... pass")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := saveChatSession(ctx, store, sess, ag, mgr, "/workspace", ""); err != nil {
		t.Fatalf("saveChatSession: %v", err)
	}
	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal == nil || loaded.Goal.ID != started.State.ID {
		t.Fatalf("saved goal = %+v, want %s", loaded.Goal, started.State.ID)
	}
	if loaded.Goal.Objective != "make go test ./... pass" {
		t.Fatalf("objective = %q", loaded.Goal.Objective)
	}
}

func TestSaveChatSessionTracksEachTransition(t *testing.T) {
	ctx := t.Context()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}})
	mgr, _ := chatGoalManager(nil, &config.Config{})
	started, err := mgr.Start("ship the change")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	save := func() {
		t.Helper()
		if err := saveChatSession(ctx, store, sess, ag, mgr, "/workspace", ""); err != nil {
			t.Fatalf("saveChatSession: %v", err)
		}
	}
	save()

	if _, err := mgr.Pause("user interrupt"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	save()
	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal == nil || loaded.Goal.Status != goal.Paused {
		t.Fatalf("saved status = %+v, want paused", loaded.Goal)
	}

	if _, err := mgr.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := mgr.RequestComplete(started.State.ID, "go test ./... passes"); err != nil {
		t.Fatalf("RequestComplete: %v", err)
	}
	save()
	loaded, err = store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal.Status != goal.Complete || loaded.Goal.LastReason != "go test ./... passes" {
		t.Fatalf("saved goal = %+v, want complete with evidence", loaded.Goal)
	}

	if _, err := mgr.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	save()
	loaded, err = store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal != nil {
		t.Fatalf("cleared goal still persisted: %+v", loaded.Goal)
	}
}

func TestSaveChatSessionWithoutAManagerKeepsTheSavedGoal(t *testing.T) {
	ctx := t.Context()
	store := session.NewStore(t.TempDir())
	mgr, _ := chatGoalManager(nil, &config.Config{})
	if _, err := mgr.Start("keep me"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess, err := store.Create(ctx, session.Metadata{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}})
	if err := saveChatSession(ctx, store, sess, ag, mgr, "/workspace", ""); err != nil {
		t.Fatalf("saveChatSession: %v", err)
	}

	// A caller that never wired a manager must not erase the goal it cannot see.
	if err := saveChatSession(ctx, store, sess, ag, nil, "/workspace", ""); err != nil {
		t.Fatalf("saveChatSession without a manager: %v", err)
	}
	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal == nil || loaded.Goal.Objective != "keep me" {
		t.Fatalf("goal = %+v, want it preserved", loaded.Goal)
	}
}

func TestResumedSessionRestoresTheGoalIntoTheManager(t *testing.T) {
	ctx := t.Context()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := chatGoalManager(nil, &config.Config{})
	started, err := first.Start("finish the migration")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := first.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	ag := agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}})
	if err := saveChatSession(ctx, store, sess, ag, first, "/workspace", ""); err != nil {
		t.Fatalf("saveChatSession: %v", err)
	}

	// Resuming is exactly this: load the record, then rebuild the manager.
	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resumed, warning := chatGoalManager(loaded.Goal, &config.Config{})
	if warning != "" {
		t.Fatalf("resume warning = %q", warning)
	}
	snap := resumed.Snapshot()
	if snap == nil || snap.ID != started.State.ID {
		t.Fatalf("restored = %+v, want %s", snap, started.State.ID)
	}
	if snap.Attempts.Used != 1 || snap.Status != goal.Active {
		t.Fatalf("restored state = %+v, want one attempt already used", snap)
	}
}

func TestChatGoalManagerWarningMentionsTheGoalID(t *testing.T) {
	_, warning := chatGoalManager(&goal.State{ID: "goal_broken", Status: goal.Active}, &config.Config{})
	if !strings.Contains(warning, "goal_broken") {
		t.Fatalf("warning = %q, want it to name the rejected goal", warning)
	}
}

func TestUsageDocumentsTheGoalCommands(t *testing.T) {
	for _, want := range []string{"/goal <objective>", "/goal status", "/goal pause", "/goal resume", "/goal clear", "saved with the session"} {
		if !strings.Contains(usageText, want) {
			t.Fatalf("usage does not contain %q", want)
		}
	}
}

func TestChatGoalManagerUsesTheConfiguredBudget(t *testing.T) {
	cfg := &config.Config{Goals: config.Goals{MaxAttempts: 3}}
	mgr, warning := chatGoalManager(nil, cfg)
	if warning != "" {
		t.Fatalf("warning = %q", warning)
	}
	if got := mgr.Limits().MaxAttempts; got != 3 {
		t.Fatalf("max attempts = %d, want the configured 3", got)
	}
	started, err := mgr.Start("ship it")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.State.Attempts.Max != 3 {
		t.Fatalf("goal budget = %d, want 3", started.State.Attempts.Max)
	}
}

func TestChatGoalManagerFallsBackToTheDefaultBudget(t *testing.T) {
	mgr, _ := chatGoalManager(nil, &config.Config{})
	if got := mgr.Limits().MaxAttempts; got != config.DefaultGoalMaxAttempts {
		t.Fatalf("max attempts = %d, want the default %d", got, config.DefaultGoalMaxAttempts)
	}
}

func TestChatGoalManagerKeepsTheBudgetOnACorruptSavedGoal(t *testing.T) {
	cfg := &config.Config{Goals: config.Goals{MaxAttempts: 7}}
	mgr, warning := chatGoalManager(&goal.State{ID: "goal_broken", Status: goal.Active}, cfg)
	if !strings.Contains(warning, "ignoring the saved goal") {
		t.Fatalf("warning = %q", warning)
	}
	if got := mgr.Limits().MaxAttempts; got != 7 {
		t.Fatalf("fallback manager lost the configured budget: %d", got)
	}
}

func TestRestoredGoalKeepsItsSavedBudgetOverAChangedConfig(t *testing.T) {
	// A session saved with a 5-attempt budget keeps it on resume even if the
	// config now says 20: the budget belongs to the goal that was running.
	saved := &goal.State{
		ID:        "goal_saved",
		Objective: "finish the migration",
		Status:    goal.Active,
		Attempts:  goal.Attempts{Used: 2, Max: 5},
	}
	mgr, warning := chatGoalManager(saved, &config.Config{Goals: config.Goals{MaxAttempts: 20}})
	if warning != "" {
		t.Fatalf("warning = %q", warning)
	}
	snap := mgr.Snapshot()
	if snap.Attempts.Max != 5 {
		t.Fatalf("restored budget = %d, want the saved 5", snap.Attempts.Max)
	}
	if snap.Attempts.Used != 2 {
		t.Fatalf("restored used = %d, want 2", snap.Attempts.Used)
	}
}
