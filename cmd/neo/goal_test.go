package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/profile"
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
	mgr, warning := chatGoalManager(nil)
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
	mgr, warning := chatGoalManager(saved)
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
	mgr, warning := chatGoalManager(&goal.State{ID: "goal_broken", Status: goal.Active})
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
