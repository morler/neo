package goal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// runTool invokes the goal tool and decodes its JSON result, which is what the
// model sees. Tests assert on that result rather than on manager internals.
func runTool(t *testing.T, tool Tool, input map[string]any) goalToolResult {
	t.Helper()
	out, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run(%v) returned error: %v", input, err)
	}
	var res goalToolResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("Run(%v) returned invalid JSON: %v\n%s", input, err, out)
	}
	return res
}

func TestToolSpecMatchesTheAcceptedInput(t *testing.T) {
	spec := Tool{}.Spec()
	if spec.Name != "goal" {
		t.Fatalf("name = %q, want goal", spec.Name)
	}
	props, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %+v", spec.InputSchema)
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no action property: %+v", props)
	}
	enum, _ := action["enum"].([]string)
	want := []string{ActionGet, ActionComplete, ActionBlocked}
	if len(enum) != len(want) {
		t.Fatalf("action enum = %v, want %v", enum, want)
	}
	for i, v := range want {
		if enum[i] != v {
			t.Fatalf("action enum = %v, want %v", enum, want)
		}
	}
	for _, key := range []string{"goal_id", "reason"} {
		if _, ok := props[key].(map[string]any); !ok {
			t.Fatalf("schema has no %s property: %+v", key, props)
		}
	}
	required, _ := spec.InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "action" {
		t.Fatalf("required = %v, want only action", required)
	}
}

func TestToolGetWithoutAGoalIsAReadableResult(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	res := runTool(t, Tool{Manager: mgr}, map[string]any{"action": "get"})
	if res.OK {
		t.Fatalf("ok = true with no goal: %+v", res)
	}
	if !strings.Contains(res.Error, ErrNoGoal.Error()) {
		t.Fatalf("error = %q, want it to name %q", res.Error, ErrNoGoal)
	}
	if res.Next == "" {
		t.Fatalf("no next step for an absent goal: %+v", res)
	}
}

func TestToolGetReportsEveryField(t *testing.T) {
	mgr, err := NewManager(nil, Limits{MaxAttempts: 7, BlockedThreshold: 3})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("make the failing test pass")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	res := runTool(t, tool, map[string]any{"action": "get"})

	if !res.OK || res.GoalID != started.State.ID {
		t.Fatalf("result = %+v, want the started goal", res)
	}
	if res.Objective != "make the failing test pass" || res.Status != Active {
		t.Fatalf("objective/status = %q/%q", res.Objective, res.Status)
	}
	if res.Attempts == nil || res.Attempts.Max != 7 || res.Attempts.Used != 0 {
		t.Fatalf("attempts = %+v, want 0/7", res.Attempts)
	}
	if res.Verification == "" {
		t.Fatalf("result is missing the verification rule: %+v", res)
	}
	if len(res.StopWhen) == 0 || len(res.Constraints) == 0 {
		t.Fatalf("result is missing stopping rules: %+v", res)
	}
	for _, rule := range res.StopWhen {
		if strings.Contains(rule, "3 times in a row") {
			return
		}
	}
	t.Fatalf("stop_when does not state the configured blocked threshold: %+v", res.StopWhen)
}

func TestToolGetSurfacesTheBlockedAudit(t *testing.T) {
	mgr, err := NewManager(nil, Limits{BlockedThreshold: 3})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("ship the migration")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.RequestBlocked(started.State.ID, "needs API credentials"); err != nil {
		t.Fatalf("RequestBlocked: %v", err)
	}
	res := runTool(t, Tool{Manager: mgr}, map[string]any{"action": "get"})
	if res.Blocked == nil {
		t.Fatalf("result has no blocked audit: %+v", res)
	}
	if res.Blocked.Count != 1 || res.Blocked.Threshold != 3 {
		t.Fatalf("blocked = %+v, want 1/3", res.Blocked)
	}
	if res.Blocked.LastReason != "needs API credentials" {
		t.Fatalf("blocked reason = %q", res.Blocked.LastReason)
	}
	if res.Status != Active {
		t.Fatalf("status = %q, want active after one blocker report", res.Status)
	}
}

func TestToolCompleteNeedsReasonAndTheCurrentID(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("fix the bug")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}

	for _, tc := range []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"missing goal_id", map[string]any{"action": "complete", "reason": "done"}, "goal_id"},
		{"missing reason", map[string]any{"action": "complete", "goal_id": started.State.ID}, "reason"},
		{"blank goal_id", map[string]any{"action": "complete", "goal_id": "  ", "reason": "done"}, "goal_id"},
		{"non-string reason", map[string]any{"action": "complete", "goal_id": started.State.ID, "reason": 7}, "reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Run(context.Background(), tc.input); err == nil {
				t.Fatal("expected an input error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
	if snap := mgr.Snapshot(); snap.Status != Active {
		t.Fatalf("rejected input changed the status to %q", snap.Status)
	}
}

func TestToolCompleteSucceeds(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("fix the bug")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	res := runTool(t, tool, map[string]any{
		"action":  "complete",
		"goal_id": started.State.ID,
		"reason":  "go test ./internal/session passes",
	})
	if !res.OK || res.Status != Complete {
		t.Fatalf("result = %+v, want ok with complete", res)
	}
	if snap := mgr.Snapshot(); snap.Status != Complete || snap.LastReason != "go test ./internal/session passes" {
		t.Fatalf("manager state = %+v", snap)
	}

	// A second completion is rejected as terminal state, and the result still
	// explains the current status.
	again := runTool(t, tool, map[string]any{
		"action":  "complete",
		"goal_id": started.State.ID,
		"reason":  "still done",
	})
	if again.OK || again.Status != Complete {
		t.Fatalf("second complete = %+v, want a readable rejection", again)
	}
	if !strings.Contains(again.Error, ErrTransition.Error()) {
		t.Fatalf("error = %q, want it to name %q", again.Error, ErrTransition)
	}
	if again.Next == "" {
		t.Fatalf("terminal rejection has no next step: %+v", again)
	}
}

func TestToolRejectsAStaleGoalID(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	first, err := mgr.Start("first objective")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	second, err := mgr.Start("second objective")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	res := runTool(t, tool, map[string]any{
		"action":  "complete",
		"goal_id": first.State.ID,
		"reason":  "done",
	})
	if res.OK {
		t.Fatalf("stale update was accepted: %+v", res)
	}
	if !strings.Contains(res.Error, ErrStaleGoal.Error()) {
		t.Fatalf("error = %q, want it to name %q", res.Error, ErrStaleGoal)
	}
	if res.GoalID != second.State.ID {
		t.Fatalf("result names %q, want the current goal %q", res.GoalID, second.State.ID)
	}
	if snap := mgr.Snapshot(); snap.Status != Active || snap.ID != second.State.ID {
		t.Fatalf("stale update changed state: %+v", snap)
	}
}

func TestToolBlockedNeedsRepeatedEvidence(t *testing.T) {
	mgr, err := NewManager(nil, Limits{BlockedThreshold: 2})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("deploy the service")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	input := map[string]any{
		"action":  "blocked",
		"goal_id": started.State.ID,
		"reason":  "needs production credentials",
	}

	first := runTool(t, tool, input)
	if first.OK {
		t.Fatalf("first blocker report was applied: %+v", first)
	}
	if first.Blocked == nil || first.Blocked.Count != 1 || first.Blocked.Threshold != 2 {
		t.Fatalf("blocked audit = %+v, want 1/2", first.Blocked)
	}
	if first.Status != Active {
		t.Fatalf("status = %q, want active", first.Status)
	}
	if first.Next == "" {
		t.Fatalf("unapplied blocker has no next step: %+v", first)
	}

	second := runTool(t, tool, input)
	if !second.OK || second.Status != Blocked {
		t.Fatalf("repeated blocker = %+v, want it applied", second)
	}
	if snap := mgr.Snapshot(); snap.Status != Blocked {
		t.Fatalf("manager status = %q, want blocked", snap.Status)
	}

	// A blocked goal cannot be completed by the model either.
	after := runTool(t, tool, map[string]any{
		"action":  "complete",
		"goal_id": started.State.ID,
		"reason":  "done anyway",
	})
	if after.OK || after.Status != Blocked {
		t.Fatalf("complete on a blocked goal = %+v", after)
	}
}

func TestToolHasNoUserControlActions(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Start("stay active"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	for _, action := range []string{"pause", "resume", "clear", "budget_limited", "set"} {
		if _, err := tool.Run(context.Background(), map[string]any{"action": action}); err == nil {
			t.Fatalf("action %q was accepted; the model must not control user-only transitions", action)
		}
	}
	if snap := mgr.Snapshot(); snap.Status != Active {
		t.Fatalf("status = %q, want active", snap.Status)
	}
}

func TestToolRejectsMissingAndUnknownActions(t *testing.T) {
	tool := Tool{}
	for _, input := range []map[string]any{
		{},
		{"action": ""},
		{"action": "  "},
		{"action": 3},
	} {
		if _, err := tool.Run(context.Background(), input); err == nil {
			t.Fatalf("input %v was accepted", input)
		}
	}
}

func TestToolWithoutAManagerIsSafe(t *testing.T) {
	tool := Tool{}
	if _, err := tool.Run(context.Background(), map[string]any{"action": "get"}); err != nil {
		t.Fatalf("get without a manager returned an error: %v", err)
	}
	res := runTool(t, tool, map[string]any{
		"action":  "complete",
		"goal_id": "goal_x",
		"reason":  "done",
	})
	if res.OK || !strings.Contains(res.Error, ErrNoGoal.Error()) {
		t.Fatalf("complete without a manager = %+v", res)
	}
}

// TestToolSatisfiesTheRegistryContract keeps the tool usable from the chat
// registry without any adapter.
func TestToolSatisfiesTheRegistryContract(t *testing.T) {
	reg := tools.NewRegistry(Tool{})
	if _, ok := reg.Get("goal"); !ok {
		t.Fatal("registry did not accept the goal tool")
	}
	specs := reg.Specs()
	if len(specs) != 1 || specs[0].Name != "goal" {
		t.Fatalf("specs = %+v", specs)
	}
	// The tool exposes no ParallelSafe capability, so the registry must treat it
	// as serial: transitions are ordered state changes.
	if reg.ParallelSafe("goal", map[string]any{"action": "get"}) {
		t.Fatal("goal tool reported parallel-safe; state transitions must stay serial")
	}
}

func TestToolReportsSuccessAndStateWithoutRawErrors(t *testing.T) {
	mgr, err := NewManager(nil, Limits{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("x")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	tool := Tool{Manager: mgr}
	out, err := tool.Run(context.Background(), map[string]any{
		"action":  "complete",
		"goal_id": started.State.ID,
		"reason":  "verified by a passing test run",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"ok", "action", "goal_id", "status", "reason", "next"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("success result missing %q: %s", key, out)
		}
	}
	if m["status"] != string(Complete) {
		t.Fatalf("status = %v, want %q", m["status"], Complete)
	}
}

// A rejected transition must still tell the model what the current status
// permits, which is the difference between a recoverable answer and a dead end.
func TestToolFailuresCarryTheStatusAndNextStep(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, mgr *Manager, id string)
		want    Status
		nextHas string
	}{
		{
			name: "paused",
			setup: func(t *testing.T, mgr *Manager, id string) {
				if _, err := mgr.Pause("user interrupt"); err != nil {
					t.Fatalf("Pause: %v", err)
				}
			},
			want:    Paused,
			nextHas: "paused",
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
			want:    BudgetLimited,
			nextHas: "budget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, err := NewManager(nil, Limits{MaxAttempts: 1})
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			started, err := mgr.Start("finish the work")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			tt.setup(t, mgr, started.State.ID)

			res := runTool(t, Tool{Manager: mgr}, map[string]any{
				"action":  "complete",
				"goal_id": started.State.ID,
				"reason":  "done",
			})
			if res.OK {
				t.Fatalf("complete on a %s goal was accepted: %+v", tt.want, res)
			}
			if res.Status != tt.want {
				t.Fatalf("status = %q, want %q", res.Status, tt.want)
			}
			if !strings.Contains(strings.ToLower(res.Next), tt.nextHas) {
				t.Fatalf("next = %q, want it to mention %q", res.Next, tt.nextHas)
			}
		})
	}
}

func TestNextStepCoversEveryStatus(t *testing.T) {
	for _, s := range []Status{Active, Paused, Complete, Blocked, BudgetLimited, UsageLimited, Cleared} {
		if nextStep(s) == "" {
			t.Fatalf("nextStep(%q) is empty", s)
		}
	}
	if nextStep("mystery") != "" {
		t.Fatalf("nextStep accepted an unknown status: %q", nextStep("mystery"))
	}
}
