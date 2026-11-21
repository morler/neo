package goal

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T, limits Limits) (*Manager, *State) {
	t.Helper()
	mgr, err := NewManager(nil, limits)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("make the failing session test pass")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return mgr, &started.State
}

func TestNewManagerStartsEmpty(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("Snapshot = %+v, want nil for a session without a goal", snap)
	}
	if _, err := mgr.Pause("x"); !errors.Is(err, ErrNoGoal) {
		t.Fatalf("Pause without a goal = %v, want ErrNoGoal", err)
	}
}

func TestStartCreatesActiveGoalWithBudgetAndContract(t *testing.T) {
	mgr, err := NewManager(nil, Limits{MaxAttempts: 5, BlockedThreshold: 3})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("  fix the flaky test in internal/session  ")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := started.State
	if started.Replaced != nil {
		t.Fatalf("Replaced = %+v, want nil for the first goal", started.Replaced)
	}
	if !strings.HasPrefix(s.ID, "goal_") {
		t.Fatalf("id = %q, want a goal_ prefix", s.ID)
	}
	if s.Objective != "fix the flaky test in internal/session" {
		t.Fatalf("objective = %q, want trimmed text", s.Objective)
	}
	if s.Status != Active || !s.Continues() {
		t.Fatalf("status = %q, want active", s.Status)
	}
	if s.Attempts != (Attempts{Used: 0, Max: 5}) {
		t.Fatalf("attempts = %+v, want 0/5", s.Attempts)
	}
	if s.Contract.Outcome != s.Objective {
		t.Fatalf("contract outcome = %q, want the objective", s.Contract.Outcome)
	}
	if !strings.Contains(strings.Join(s.Contract.StopWhen, "\n"), "3 times in a row") {
		t.Fatalf("contract stop rules missing the blocked threshold: %+v", s.Contract.StopWhen)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", s)
	}
}

func TestStartRejectsEmptyObjective(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Start(" \n\t "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Start with a blank objective = %v, want ErrInvalid", err)
	}
	if snap := mgr.Snapshot(); snap != nil {
		t.Fatalf("Snapshot = %+v after a rejected start, want nil", snap)
	}
}

func TestStartReplacesTheExistingGoal(t *testing.T) {
	mgr, first := newTestManager(t, Limits{MaxAttempts: 4})
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	if _, err := mgr.RequestBlocked(first.ID, "needs credentials"); err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}

	started, err := mgr.Start("ship the migration")
	if err != nil {
		t.Fatalf("Start replacement: %v", err)
	}
	if started.Replaced == nil || started.Replaced.ID != first.ID {
		t.Fatalf("Replaced = %+v, want the previous goal %s", started.Replaced, first.ID)
	}
	if started.State.ID == first.ID {
		t.Fatal("replacement reused the previous goal id")
	}
	if started.State.Attempts.Used != 0 || started.State.Blocked.Count != 0 {
		t.Fatalf("replacement inherited counters: attempts=%+v blocked=%+v", started.State.Attempts, started.State.Blocked)
	}
	if snap := mgr.Snapshot(); snap == nil || snap.ID != started.State.ID {
		t.Fatalf("Snapshot = %+v, want the replacement", snap)
	}
}

func TestSnapshotIsAnIsolatedCopy(t *testing.T) {
	mgr, _ := newTestManager(t, Limits{})
	snap := mgr.Snapshot()
	snap.Objective = "tampered"
	snap.Contract.StopWhen[0] = "tampered"
	snap.Blocked.Count = 99

	fresh := mgr.Snapshot()
	if fresh.Objective == "tampered" || fresh.Blocked.Count == 99 {
		t.Fatalf("Snapshot shares state with the manager: %+v", fresh)
	}
	if fresh.Contract.StopWhen[0] == "tampered" {
		t.Fatalf("Snapshot shares the contract slice with the manager: %+v", fresh.Contract.StopWhen)
	}
}

func TestUserTransitions(t *testing.T) {
	t.Run("pause and resume", func(t *testing.T) {
		mgr, first := newTestManager(t, Limits{MaxAttempts: 2})
		if _, err := mgr.ConsumeAttempt(); err != nil {
			t.Fatalf("ConsumeAttempt: %v", err)
		}
		paused, err := mgr.Pause("user interrupt")
		if err != nil {
			t.Fatalf("Pause: %v", err)
		}
		if paused.Status != Paused || paused.Continues() {
			t.Fatalf("status = %q, want paused", paused.Status)
		}
		if paused.LastReason != "user interrupt" {
			t.Fatalf("last reason = %q", paused.LastReason)
		}
		if paused.ID != first.ID {
			t.Fatalf("pause changed the goal id: %q", paused.ID)
		}
		if _, err := mgr.ConsumeAttempt(); !errors.Is(err, ErrTransition) {
			t.Fatalf("ConsumeAttempt while paused = %v, want ErrTransition", err)
		}
		resumed, err := mgr.Resume()
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if resumed.Status != Active || resumed.Attempts.Used != 0 {
			t.Fatalf("resumed state = %+v, want active with a fresh budget", resumed)
		}
		if resumed.LastReason != "" {
			t.Fatalf("resume kept the pause reason %q", resumed.LastReason)
		}
	})

	t.Run("pause keeps recorded evidence when no reason is given", func(t *testing.T) {
		mgr, first := newTestManager(t, Limits{})
		if _, err := mgr.RequestBlocked(first.ID, "tests still fail in internal/session"); err != nil {
			t.Fatalf("RequestBlocked: %v", err)
		}
		paused, err := mgr.Pause("  ")
		if err != nil {
			t.Fatalf("Pause: %v", err)
		}
		if paused.Status != Paused {
			t.Fatalf("status = %q, want paused", paused.Status)
		}
		if paused.Blocked.LastReason != "tests still fail in internal/session" {
			t.Fatalf("pause dropped the blocker evidence: %+v", paused.Blocked)
		}
	})

	t.Run("pause requires active", func(t *testing.T) {
		mgr, first := newTestManager(t, Limits{})
		if _, err := mgr.RequestComplete(first.ID, "tests pass"); err != nil {
			t.Fatalf("RequestComplete: %v", err)
		}
		if _, err := mgr.Pause("late"); !errors.Is(err, ErrTransition) {
			t.Fatalf("Pause on a complete goal = %v, want ErrTransition", err)
		}
	})

	t.Run("resume rejects active and complete", func(t *testing.T) {
		mgr, _ := newTestManager(t, Limits{})
		if _, err := mgr.Resume(); !errors.Is(err, ErrTransition) {
			t.Fatalf("Resume on an active goal = %v, want ErrTransition", err)
		}
		snap := mgr.Snapshot()
		if _, err := mgr.RequestComplete(snap.ID, "done"); err != nil {
			t.Fatalf("RequestComplete: %v", err)
		}
		if _, err := mgr.Resume(); !errors.Is(err, ErrTransition) {
			t.Fatalf("Resume on a complete goal = %v, want ErrTransition", err)
		}
	})

	t.Run("clear removes the goal", func(t *testing.T) {
		mgr, first := newTestManager(t, Limits{})
		final, err := mgr.Clear()
		if err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if final.Status != Cleared || final.ID != first.ID {
			t.Fatalf("cleared snapshot = %+v, want the old goal reported as cleared", final)
		}
		if snap := mgr.Snapshot(); snap != nil {
			t.Fatalf("Snapshot after clear = %+v, want nil", snap)
		}
		if _, err := mgr.Clear(); !errors.Is(err, ErrNoGoal) {
			t.Fatalf("second Clear = %v, want ErrNoGoal", err)
		}
		restarted, err := mgr.Start("new objective")
		if err != nil {
			t.Fatalf("Start after clear: %v", err)
		}
		if restarted.Replaced != nil {
			t.Fatalf("Start after clear reported a replacement: %+v", restarted.Replaced)
		}
	})
}

func TestModelUpdatesRequireTheCurrentActiveGoal(t *testing.T) {
	mgr, first := newTestManager(t, Limits{})

	if _, err := mgr.RequestComplete("", "done"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("complete without a goal id = %v, want ErrInvalid", err)
	}
	if _, err := mgr.RequestComplete("goal_other", "done"); !errors.Is(err, ErrStaleGoal) {
		t.Fatalf("complete with a stale id = %v, want ErrStaleGoal", err)
	}
	if _, err := mgr.RequestBlocked("goal_other", "no network"); !errors.Is(err, ErrStaleGoal) {
		t.Fatalf("blocked with a stale id = %v, want ErrStaleGoal", err)
	}
	if _, err := mgr.RequestComplete(first.ID, "   "); !errors.Is(err, ErrReason) {
		t.Fatalf("complete without a reason = %v, want ErrReason", err)
	}
	if _, err := mgr.RequestBlocked(first.ID, ""); !errors.Is(err, ErrReason) {
		t.Fatalf("blocked without a reason = %v, want ErrReason", err)
	}
	if snap := mgr.Snapshot(); snap.Status != Active {
		t.Fatalf("rejected updates changed the status to %q", snap.Status)
	}

	started, err := mgr.Start("replacement")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.RequestComplete(first.ID, "done"); !errors.Is(err, ErrStaleGoal) {
		t.Fatalf("complete against a replaced goal = %v, want ErrStaleGoal", err)
	}
	if _, err := mgr.RequestComplete(started.State.ID, "go test ./... passes"); err != nil {
		t.Fatalf("RequestComplete: %v", err)
	}
	if snap := mgr.Snapshot(); snap.Status != Complete || snap.LastReason != "go test ./... passes" {
		t.Fatalf("completed state = %+v", snap)
	}
}

func TestTerminalGoalsRejectModelUpdates(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, mgr *Manager, id string)
	}{
		{
			name: "complete",
			setup: func(t *testing.T, mgr *Manager, id string) {
				if _, err := mgr.RequestComplete(id, "verified"); err != nil {
					t.Fatalf("RequestComplete: %v", err)
				}
			},
		},
		{
			name: "blocked",
			setup: func(t *testing.T, mgr *Manager, id string) {
				for i := 0; i < DefaultBlockedThreshold; i++ {
					if _, err := mgr.RequestBlocked(id, "needs a database"); err != nil {
						t.Fatalf("RequestBlocked: %v", err)
					}
				}
			},
		},
		{
			name: "budget limited",
			setup: func(t *testing.T, mgr *Manager, id string) {
				for {
					res, err := mgr.ConsumeAttempt()
					if err != nil {
						t.Fatalf("ConsumeAttempt: %v", err)
					}
					if !res.Allowed {
						return
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, first := newTestManager(t, Limits{MaxAttempts: 1})
			tt.setup(t, mgr, first.ID)
			if _, err := mgr.RequestComplete(first.ID, "again"); !errors.Is(err, ErrTransition) {
				t.Fatalf("complete after terminal state = %v, want ErrTransition", err)
			}
			if _, err := mgr.RequestBlocked(first.ID, "again"); !errors.Is(err, ErrTransition) {
				t.Fatalf("blocked after terminal state = %v, want ErrTransition", err)
			}
			if _, err := mgr.ConsumeAttempt(); !errors.Is(err, ErrTransition) {
				t.Fatalf("continue after terminal state = %v, want ErrTransition", err)
			}
		})
	}
}

func TestBlockedAuditThreshold(t *testing.T) {
	mgr, first := newTestManager(t, Limits{BlockedThreshold: 2})

	firstTry, err := mgr.RequestBlocked(first.ID, "  Needs   API credentials ")
	if err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if firstTry.Applied || firstTry.Count != 1 {
		t.Fatalf("first blocker report = %+v, want a single unapplied count", firstTry)
	}
	if firstTry.State.Status != Active {
		t.Fatalf("status after one blocker report = %q, want active", firstTry.State.Status)
	}

	// A different blocker restarts the audit rather than accumulating.
	other, err := mgr.RequestBlocked(first.ID, "needs network access")
	if err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if other.Applied || other.Count != 1 {
		t.Fatalf("different blocker = %+v, want the audit restarted at 1", other)
	}

	// Case and spacing differences are the same blocker.
	sameAgain, err := mgr.RequestBlocked(first.ID, "needs NETWORK   access")
	if err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if !sameAgain.Applied || sameAgain.Count != 2 {
		t.Fatalf("repeated blocker = %+v, want applied at 2", sameAgain)
	}
	if sameAgain.State.Status != Blocked {
		t.Fatalf("status = %q, want blocked", sameAgain.State.Status)
	}
	if sameAgain.State.LastReason != "needs NETWORK   access" {
		t.Fatalf("last reason = %q, want the latest wording", sameAgain.State.LastReason)
	}

	// Resume clears the audit so a new blocker needs a fresh threshold.
	resumed, err := mgr.Resume()
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Blocked.Count != 0 || resumed.Blocked.Signature != "" {
		t.Fatalf("resume kept the blocked audit: %+v", resumed.Blocked)
	}
	afterResume, err := mgr.RequestBlocked(resumed.ID, "needs NETWORK access")
	if err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if afterResume.Applied || afterResume.Count != 1 {
		t.Fatalf("blocker after resume = %+v, want a fresh single count", afterResume)
	}
}

func TestBlockedThresholdDefaultsWhenUnset(t *testing.T) {
	mgr, first := newTestManager(t, Limits{})
	result, err := mgr.RequestBlocked(first.ID, "no credentials")
	if err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if result.Threshold != DefaultBlockedThreshold {
		t.Fatalf("threshold = %d, want %d", result.Threshold, DefaultBlockedThreshold)
	}
}

func TestConsumeAttemptBudget(t *testing.T) {
	mgr, first := newTestManager(t, Limits{MaxAttempts: 2})
	for used := 1; used <= 2; used++ {
		res, err := mgr.ConsumeAttempt()
		if err != nil {
			t.Fatalf("ConsumeAttempt %d: %v", used, err)
		}
		if !res.Allowed || res.State.Attempts.Used != used {
			t.Fatalf("attempt %d = %+v", used, res)
		}
	}
	exhausted, err := mgr.ConsumeAttempt()
	if err != nil {
		t.Fatalf("ConsumeAttempt past the budget: %v", err)
	}
	if exhausted.Allowed {
		t.Fatal("consumed an attempt past the budget")
	}
	if exhausted.State.Status != BudgetLimited || exhausted.State.Continues() {
		t.Fatalf("state = %+v, want budget_limited", exhausted.State)
	}
	if exhausted.State.Attempts.Used != 2 {
		t.Fatalf("used = %d, want the budget is preserved at 2", exhausted.State.Attempts.Used)
	}
	if exhausted.State.ID != first.ID {
		t.Fatal("budget limit replaced the goal id")
	}

	resumed, err := mgr.Resume()
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Status != Active || resumed.Attempts.Used != 0 {
		t.Fatalf("resumed = %+v, want a fresh budget", resumed)
	}
}

func TestConsumeAttemptKeepsLastReasonForHandoff(t *testing.T) {
	mgr, first := newTestManager(t, Limits{MaxAttempts: 1})
	if _, err := mgr.RequestBlocked(first.ID, "tests still fail in internal/session"); err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	res, err := mgr.ConsumeAttempt()
	if err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	if res.Allowed {
		t.Fatal("budget allowed a second attempt")
	}
	if res.State.Blocked.LastReason != "tests still fail in internal/session" {
		t.Fatalf("handoff lost the last reason: %+v", res.State.Blocked)
	}
}

func TestRestoreFromSessionState(t *testing.T) {
	mgr, first := newTestManager(t, Limits{MaxAttempts: 3, BlockedThreshold: 2})
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	saved := mgr.Snapshot()

	restored, err := NewManager(saved, Limits{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got := restored.Snapshot()
	if got == nil || got.ID != first.ID {
		t.Fatalf("restored = %+v, want the saved goal", got)
	}
	if got.Objective != saved.Objective || got.Status != saved.Status || got.Attempts != saved.Attempts {
		t.Fatalf("restored fields drifted:\n got %+v\nwant %+v", got, saved)
	}
	if !got.CreatedAt.Equal(saved.CreatedAt) || !got.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("restored timestamps drifted: %+v", got)
	}
	if got.Contract.Verification == "" || len(got.Contract.StopWhen) == 0 {
		t.Fatalf("restored contract lost its defaults: %+v", got.Contract)
	}
}

func TestRestoreRejectsCorruptState(t *testing.T) {
	valid := State{
		ID:        "goal_restored",
		Objective: "ship it",
		Status:    Active,
		Attempts:  Attempts{Max: 5},
	}
	tests := []struct {
		name   string
		mutate func(*State)
		want   error
	}{
		{"missing id", func(s *State) { s.ID = "" }, ErrInvalid},
		{"missing objective", func(s *State) { s.Objective = " " }, ErrInvalid},
		{"unknown status", func(s *State) { s.Status = "running" }, ErrInvalid},
		{"cleared status", func(s *State) { s.Status = Cleared }, ErrInvalid},
		{"negative attempts", func(s *State) { s.Attempts.Used = -1 }, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid
			tt.mutate(&in)
			if _, err := NewManager(&in, Limits{}); !errors.Is(err, tt.want) {
				t.Fatalf("NewManager = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRestoreRepairsBudgetFields(t *testing.T) {
	in := State{ID: "goal_old", Objective: "ship it", Status: Active, Attempts: Attempts{Used: 9}, Blocked: BlockedAudit{Count: -3}}
	mgr, err := NewManager(&in, Limits{MaxAttempts: 4})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got := mgr.Snapshot()
	if got.Attempts.Max != 4 {
		t.Fatalf("max = %d, want the configured 4", got.Attempts.Max)
	}
	if got.Attempts.Used != 4 {
		t.Fatalf("used = %d, want it clamped to the budget", got.Attempts.Used)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("missing timestamps were not filled: %+v", got)
	}
	if got.Blocked.Count < 0 {
		t.Fatalf("blocked count = %d", got.Blocked.Count)
	}
}

func TestTransitionsWithoutAGoal(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	calls := map[string]func() error{
		"pause":    func() error { _, err := mgr.Pause("x"); return err },
		"resume":   func() error { _, err := mgr.Resume(); return err },
		"clear":    func() error { _, err := mgr.Clear(); return err },
		"continue": func() error { _, err := mgr.ConsumeAttempt(); return err },
		"complete": func() error { _, err := mgr.RequestComplete("goal_x", "done"); return err },
		"blocked":  func() error { _, err := mgr.RequestBlocked("goal_x", "stuck"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrNoGoal) {
				t.Fatalf("%s without a goal = %v, want ErrNoGoal", name, err)
			}
		})
	}
}

func TestStatusValues(t *testing.T) {
	for _, s := range []Status{Active, Paused, Complete, Blocked, BudgetLimited, UsageLimited, Cleared} {
		if !validStatus(s) {
			t.Fatalf("validStatus(%q) = false", s)
		}
	}
	if !(State{Status: Active}).Continues() {
		t.Fatal("active goal does not continue")
	}
	for _, s := range []Status{Paused, Complete, Blocked, BudgetLimited, UsageLimited, Cleared} {
		if (State{Status: s}).Continues() {
			t.Fatalf("%q reports Continues() = true", s)
		}
	}
	if validStatus("running") {
		t.Fatal("validStatus accepted an unknown status")
	}
}

// TestManagerIsRaceFree exercises concurrent snapshots against transitions.
// Iteration-bounded rather than time-bounded so the test cannot flake on a
// slow machine, and only reported failures are race reports plus a missing
// snapshot, never a mutation.
func TestManagerIsRaceFree(t *testing.T) {
	mgr, _ := newTestManager(t, Limits{MaxAttempts: 10_000})
	const readers = 4
	const iterations = 200
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				snap := mgr.Snapshot()
				if snap == nil || snap.ID == "" {
					t.Error("snapshot returned no goal or an empty id")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			res, err := mgr.ConsumeAttempt()
			if err != nil || !res.Allowed {
				t.Errorf("ConsumeAttempt = %+v, %v", res, err)
				return
			}
			if i%10 != 0 {
				continue
			}
			if _, err := mgr.Pause("tick"); err != nil {
				t.Errorf("Pause: %v", err)
				return
			}
			if _, err := mgr.Resume(); err != nil {
				t.Errorf("Resume: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
