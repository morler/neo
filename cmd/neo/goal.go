package main

import (
	"fmt"

	"github.com/owainlewis/neo/internal/goal"
)

// chatGoalManager builds the chat session's goal manager. A goal saved with the
// session is restored so resuming continues the same objective; a goal the
// manager rejects is reported as a warning and the session starts without one,
// because losing /goal must not make an otherwise valid session unopenable.
//
// Budget wiring (goals.max_attempts) and saving transitions back into the
// session are separate concerns handled around this manager.
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
