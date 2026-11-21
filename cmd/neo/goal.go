package main

import (
	"fmt"

	"github.com/owainlewis/neo/internal/goal"
)

// chatGoalManager builds the chat session's goal manager. A goal saved with the
// session is restored so resuming continues the same objective; a goal the
// manager rejects is reported as a warning and the session starts without one,
// because an unreadable goal must not make an otherwise valid session
// unopenable.
//
// The attempt budget comes from goals.max_attempts (issue #15); until that
// config lands this uses the package default.
func chatGoalManager(saved *goal.State) (*goal.Manager, string) {
	mgr, err := goal.NewManager(saved, goal.Limits{})
	if err != nil {
		return mustGoalManager(), fmt.Sprintf("warning: ignoring the saved goal: %v\n", err)
	}
	return mgr, ""
}

// mustGoalManager is the empty fallback for an unreadable saved goal.
func mustGoalManager() *goal.Manager {
	mgr, err := goal.NewManager(nil, goal.Limits{})
	if err != nil {
		// NewManager(nil, …) cannot fail: there is no saved state to validate.
		panic(fmt.Sprintf("goal: empty manager: %v", err))
	}
	return mgr
}
