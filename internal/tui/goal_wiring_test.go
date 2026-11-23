package tui

import (
	"errors"
	"testing"

	"github.com/owainlewis/neo/internal/goal"
)

func TestGoalManagerAndPersistAreWiredIntoTheModel(t *testing.T) {
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	saves := 0
	persist := func() error {
		saves++
		return nil
	}
	opts := Options{}
	WithGoalManager(mgr)(&opts)
	WithGoalPersist(persist)(&opts)
	if opts.GoalManager != mgr {
		t.Fatal("WithGoalManager did not store the manager")
	}
	if opts.GoalPersist == nil {
		t.Fatal("WithGoalPersist did not store the callback")
	}

	m := makeTestModel()
	m.goal = opts.GoalManager
	m.goalPersist = opts.GoalPersist
	if m.goal == nil || m.goalPersist == nil {
		t.Fatal("model did not keep the goal wiring")
	}
	if _, err := m.goal.Start("keep going"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.goalPersist(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if saves != 1 {
		t.Fatalf("saves = %d, want 1", saves)
	}
	if snap := m.goal.Snapshot(); snap == nil || snap.Objective != "keep going" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// A TUI built without a manager must not acquire goal behaviour by accident:
// every later feature (#17-#21) gates on these two fields.
func TestModelWithoutGoalWiringIsInert(t *testing.T) {
	m := makeTestModel()
	if m.goal != nil {
		t.Fatalf("fresh model has a goal manager: %v", m.goal)
	}
	if m.goalPersist != nil {
		t.Fatal("fresh model has a goal persist callback")
	}
	if m.ag == nil {
		t.Fatal("test model has no agent")
	}
}

func TestGoalPersistErrorsReachTheCaller(t *testing.T) {
	saveErr := errors.New("save failed")
	opts := Options{}
	WithGoalPersist(func() error { return saveErr })(&opts)
	if err := opts.GoalPersist(); !errors.Is(err, saveErr) {
		t.Fatalf("persist error = %v, want %v", err, saveErr)
	}
}

// The options must not disturb the existing after-send wiring: both callbacks
// describe the same save, and a caller that sets only AfterSend keeps working.
func TestGoalOptionsLeaveAfterSendUntouched(t *testing.T) {
	calls := 0
	opts := Options{}
	WithAfterSend(func() error { calls++; return nil })(&opts)
	WithGoalPersist(func() error { calls++; return nil })(&opts)
	m := makeTestModel()
	m.afterSend = opts.AfterSend
	if err := m.persistSession(); err != nil {
		t.Fatalf("persistSession: %v", err)
	}
	if calls != 1 {
		t.Fatalf("afterSend calls = %d, want 1", calls)
	}
}

// newModel must carry the goal wiring through the full constructor, which is
// what a real chat session uses.
func TestNewModelCarriesGoalWiring(t *testing.T) {
	base := makeTestModel()
	mgr, err := goal.NewManager(nil, goal.Limits{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	saves := 0
	m := newModel(t.Context(), base.ag, "test", "dev", "", nil, Options{
		GoalManager: mgr,
		GoalPersist: func() error { saves++; return nil },
	})
	if m.goal != mgr {
		t.Fatal("newModel dropped the goal manager")
	}
	if m.goalPersist == nil {
		t.Fatal("newModel dropped the goal persist callback")
	}
	if err := m.goalPersist(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if saves != 1 {
		t.Fatalf("saves = %d, want 1", saves)
	}
}
