package main

import (
	"fmt"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
)

// chatGoalManager builds the chat session's goal manager with the attempt
// budget from goals.max_attempts. A goal saved with the session is restored so
// resuming continues the same objective; a goal the manager rejects is reported
// as a warning and the session starts without one, because an unreadable goal
// must not make an otherwise valid session unopenable.
func chatGoalManager(saved *goal.State, cfg *config.Config) (*goal.Manager, string) {
	limits := goal.Limits{MaxAttempts: cfg.GoalMaxAttempts()}
	mgr, err := goal.NewManager(saved, limits)
	if err != nil {
		return mustGoalManager(limits), fmt.Sprintf("warning: ignoring the saved goal: %v\n", err)
	}
	return mgr, ""
}

// mustGoalManager is the empty fallback for an unreadable saved goal. It keeps
// the same budget, so a rejected goal does not change how the next one behaves.
func mustGoalManager(limits goal.Limits) *goal.Manager {
	mgr, err := goal.NewManager(nil, limits)
	if err != nil {
		// NewManager(nil, …) cannot fail: there is no saved state to validate.
		panic(fmt.Sprintf("goal: empty manager: %v", err))
	}
	return mgr
}
