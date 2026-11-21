package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

const (
	// ActionGet returns the current goal without changing it.
	ActionGet = "get"
	// ActionComplete reports verified completion.
	ActionComplete = "complete"
	// ActionBlocked reports a recurring blocker.
	ActionBlocked = "blocked"
)

// Tool is the model-visible goal tool. It is registered for the whole chat
// session whether or not a goal exists, so the tool list never changes
// mid-session and "no active goal" is an ordinary result rather than an error.
//
// The model can only read a goal and ask to finish it. Pausing, resuming,
// clearing, and the attempt budget belong to the user and the scheduler, which
// is why this tool deliberately exposes no such actions.
type Tool struct {
	Manager *Manager
}

func (Tool) Name() string { return "goal" }

func (Tool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "goal",
		Description: `Read or finish this session's standing goal.
get: the goal id, objective, status, attempt budget, and stopping rules.
complete: only after verifying the objective. Needs the goal_id from get and a reason naming the evidence.
blocked: only when the same blocker keeps recurring. Needs the goal_id and the blocker.
The user owns pause, resume, clear, and the attempt budget; this tool cannot change them.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{ActionGet, ActionComplete, ActionBlocked},
				},
				"goal_id": map[string]any{
					"type":        "string",
					"description": "Current goal id from action=get; required for complete and blocked",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Evidence for complete, or the blocker for blocked",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t Tool) Run(_ context.Context, input map[string]any) (string, error) {
	action, ok := input["action"].(string)
	if !ok || strings.TrimSpace(action) == "" {
		return "", fmt.Errorf("goal: missing required input: action")
	}
	switch action = strings.TrimSpace(action); action {
	case ActionGet:
		return t.get()
	case ActionComplete, ActionBlocked:
		return t.update(action, input)
	default:
		return "", fmt.Errorf("goal: unknown action %q", action)
	}
}

// goalToolResult is the JSON shape every action returns. Failures that describe
// goal state (no goal, a stale id, a terminal status, an unmet blocked
// threshold) are results with ok=false rather than Go errors, so the model
// reads them as product state instead of a broken tool.
type goalToolResult struct {
	OK           bool         `json:"ok"`
	Action       string       `json:"action"`
	GoalID       string       `json:"goal_id,omitempty"`
	Objective    string       `json:"objective,omitempty"`
	Status       Status       `json:"status,omitempty"`
	Attempts     *Attempts    `json:"attempts,omitempty"`
	Verification string       `json:"verification,omitempty"`
	Constraints  []string     `json:"constraints,omitempty"`
	Boundaries   []string     `json:"boundaries,omitempty"`
	StopWhen     []string     `json:"stop_when,omitempty"`
	Blocked      *blockedView `json:"blocked,omitempty"`
	LastReason   string       `json:"last_reason,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	Error        string       `json:"error,omitempty"`
	Next         string       `json:"next,omitempty"`
}

type blockedView struct {
	Count      int    `json:"count"`
	Threshold  int    `json:"threshold"`
	LastReason string `json:"last_reason,omitempty"`
}

func (t Tool) get() (string, error) {
	s := t.snapshot()
	if s == nil {
		return encodeGoalToolResult(goalToolResult{
			Action: ActionGet,
			Error:  ErrNoGoal.Error(),
			Next:   "The user has not set a goal for this session; work on the request normally.",
		})
	}
	attempts := s.Attempts
	view := goalToolResult{
		OK:           true,
		Action:       ActionGet,
		GoalID:       s.ID,
		Objective:    s.Objective,
		Status:       s.Status,
		Attempts:     &attempts,
		Verification: s.Contract.Verification,
		Constraints:  s.Contract.Constraints,
		Boundaries:   s.Contract.Boundaries,
		StopWhen:     s.Contract.StopWhen,
		LastReason:   s.LastReason,
		Next:         nextStep(s.Status),
	}
	if s.Blocked.Count > 0 {
		view.Blocked = &blockedView{
			Count:      s.Blocked.Count,
			Threshold:  DefaultBlockedThreshold,
			LastReason: s.Blocked.LastReason,
		}
		if t.Manager != nil {
			view.Blocked.Threshold = t.Manager.Limits().BlockedThreshold
		}
	}
	return encodeGoalToolResult(view)
}

func (t Tool) update(action string, input map[string]any) (string, error) {
	goalID, err := requiredGoalInput(action, input, "goal_id")
	if err != nil {
		return "", err
	}
	reason, err := requiredGoalInput(action, input, "reason")
	if err != nil {
		return "", err
	}
	if t.Manager == nil {
		return encodeGoalToolResult(goalToolResult{
			Action: action,
			GoalID: goalID,
			Error:  ErrNoGoal.Error(),
			Next:   "The user has not set a goal for this session; work on the request normally.",
		})
	}

	switch action {
	case ActionComplete:
		s, err := t.Manager.RequestComplete(goalID, reason)
		if err != nil {
			return t.failure(action, goalID, err)
		}
		return encodeGoalToolResult(goalToolResult{
			OK:     true,
			Action: action,
			GoalID: s.ID,
			Status: s.Status,
			Reason: reason,
			Next:   "The goal is complete. Summarize the evidence for the user and stop working on it.",
		})
	default:
		res, err := t.Manager.RequestBlocked(goalID, reason)
		if err != nil {
			return t.failure(action, goalID, err)
		}
		view := goalToolResult{
			Action: action,
			GoalID: res.State.ID,
			Status: res.State.Status,
			Blocked: &blockedView{
				Count:      res.Count,
				Threshold:  res.Threshold,
				LastReason: res.State.Blocked.LastReason,
			},
		}
		if res.Applied {
			view.OK = true
			view.Reason = reason
			view.Next = "The goal is blocked. Explain the blocker to the user and stop; only /goal resume can continue it."
			return encodeGoalToolResult(view)
		}
		view.Error = fmt.Sprintf("the same blocker has been reported %d of %d times", res.Count, res.Threshold)
		view.Next = "Keep working. Report the same blocker again only if it still applies after another real attempt."
		return encodeGoalToolResult(view)
	}
}

// failure turns a manager rejection into a readable result with the current
// status and an actionable next step.
func (t Tool) failure(action, goalID string, err error) (string, error) {
	view := goalToolResult{
		Action: action,
		GoalID: goalID,
		Error:  err.Error(),
	}
	if s := t.snapshot(); s != nil {
		view.GoalID = s.ID
		view.Status = s.Status
		view.Next = nextStep(s.Status)
	}
	if view.Next == "" {
		view.Next = "Call action=get for the current goal state."
	}
	return encodeGoalToolResult(view)
}

func (t Tool) snapshot() *State {
	if t.Manager == nil {
		return nil
	}
	return t.Manager.Snapshot()
}

// nextStep tells the model what the current status permits. The status strings
// match the ones /goal status prints, so the transcript reads consistently.
func nextStep(s Status) string {
	switch s {
	case Active:
		return "Keep working toward the objective; verify it with evidence before calling complete."
	case Paused:
		return "Automatic continuation is paused. Wait for the user to resume or clear the goal."
	case Complete:
		return "This goal is already complete. Do not update it again."
	case Blocked:
		return "This goal is blocked. Only the user can resume or clear it."
	case BudgetLimited:
		return "The attempt budget is exhausted. Only the user can resume or clear the goal."
	case UsageLimited:
		return "Usage limits stopped this goal. Wait for the user."
	case Cleared:
		return "The goal was cleared. There is no goal to update."
	}
	return ""
}

// requiredGoalInput reads a required string input, rejecting missing, non-string,
// and blank values with a message that says how to recover.
func requiredGoalInput(action string, input map[string]any, key string) (string, error) {
	v, ok := input[key]
	if !ok {
		return "", goalInputError(action, key, "is missing")
	}
	s, ok := v.(string)
	if !ok {
		return "", goalInputError(action, key, "must be a string")
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", goalInputError(action, key, "must not be empty")
	}
	return s, nil
}

func goalInputError(action, key, problem string) error {
	err := fmt.Errorf("goal %s: input %s %s", action, key, problem)
	if key == "goal_id" {
		return fmt.Errorf("%w; call action=get for the current goal id", err)
	}
	return fmt.Errorf("%w; complete needs the verification evidence and blocked needs the blocker", err)
}

func encodeGoalToolResult(r goalToolResult) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("goal: encode result: %w", err)
	}
	return string(b), nil
}
