package session

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
)

func testGoal() *goal.State {
	created := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	return &goal.State{
		ID:        "goal_abc123",
		Objective: "make go test ./... pass again",
		Contract: goal.Contract{
			Outcome:      "make go test ./... pass again",
			Verification: "go test ./... passes",
			Constraints:  []string{"do not redefine success"},
			Boundaries:   []string{"internal/"},
			StopWhen:     []string{"tests pass"},
		},
		Status:   goal.Active,
		Attempts: goal.Attempts{Used: 3, Max: 20},
		Blocked: goal.BlockedAudit{
			Signature:  "needs api credentials",
			Count:      1,
			LastReason: "needs API credentials",
		},
		LastReason: "tests still fail in internal/session",
		CreatedAt:  created,
		UpdatedAt:  created.Add(4 * time.Minute),
	}
}

func TestStoreSaveLoadGoal(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{CWD: "/repo", Model: "test-model"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := testGoal()
	sess.Goal = want
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(context.Background(), sess.Metadata.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Goal
	if got == nil {
		t.Fatal("Load dropped the goal")
	}
	if got.ID != want.ID || got.Objective != want.Objective {
		t.Fatalf("identity drifted:\n got %+v\nwant %+v", got, want)
	}
	if got.Status != want.Status {
		t.Fatalf("status = %q, want %q", got.Status, want.Status)
	}
	if got.Attempts != want.Attempts {
		t.Fatalf("attempts = %+v, want %+v", got.Attempts, want.Attempts)
	}
	if got.Blocked != want.Blocked {
		t.Fatalf("blocked audit = %+v, want %+v", got.Blocked, want.Blocked)
	}
	if got.LastReason != want.LastReason {
		t.Fatalf("last reason = %q, want %q", got.LastReason, want.LastReason)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("timestamps drifted: created %v/%v updated %v/%v",
			got.CreatedAt, want.CreatedAt, got.UpdatedAt, want.UpdatedAt)
	}
	if len(got.Contract.StopWhen) != 1 || got.Contract.StopWhen[0] != "tests pass" {
		t.Fatalf("contract stop rules = %+v", got.Contract.StopWhen)
	}
	if len(got.Contract.Boundaries) != 1 || got.Contract.Boundaries[0] != "internal/" {
		t.Fatalf("contract boundaries = %+v", got.Contract.Boundaries)
	}
	if got.Contract.Verification != want.Contract.Verification {
		t.Fatalf("verification = %q", got.Contract.Verification)
	}
}

func TestStoreSaveWithoutGoalOmitsTheField(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{CWD: "/repo", Model: "test-model"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(store.sessionPath(sess.Metadata.ID))
	if err != nil {
		t.Fatalf("read session json: %v", err)
	}
	if strings.Contains(string(b), `"goal"`) {
		t.Fatalf("session without a goal serialized a goal key:\n%s", b)
	}

	// Clearing the goal must remove the key again rather than leaving a null.
	sess.Goal = testGoal()
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save with goal: %v", err)
	}
	sess.Goal = nil
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save after clear: %v", err)
	}
	b, err = os.ReadFile(store.sessionPath(sess.Metadata.ID))
	if err != nil {
		t.Fatalf("read session json: %v", err)
	}
	if strings.Contains(string(b), `"goal"`) {
		t.Fatalf("cleared session kept a goal key:\n%s", b)
	}
}

func TestStoreLoadOlderSessionWithoutGoal(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{
  "metadata": {
    "id": "sess_pre_goal",
    "cwd": "/repo",
    "model": "test",
    "created_at": "2026-01-01T00:00:00Z",
    "updated_at": "2026-01-01T00:00:00Z"
  },
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "hello"}]}
  ],
  "usage": {"input_tokens": 5, "output_tokens": 7}
}`
	if err := os.WriteFile(store.sessionPath("sess_pre_goal"), []byte(body), 0o600); err != nil {
		t.Fatalf("write old session: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_pre_goal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal != nil {
		t.Fatalf("goal = %+v, want nil for a session saved before goals existed", loaded.Goal)
	}
	if loaded.Usage.InputTokens != 5 || loaded.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("old session content changed: %+v", loaded)
	}
}

func TestStoreLoadTreatsEmptyGoalObjectAsAbsent(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{
  "metadata": {"id": "sess_empty_goal", "cwd": "/repo", "model": "test"},
  "messages": [],
  "goal": {}
}`
	if err := os.WriteFile(store.sessionPath("sess_empty_goal"), []byte(body), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_empty_goal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal != nil {
		t.Fatalf("goal = %+v, want nil for an empty goal object", loaded.Goal)
	}
}

func TestStoreLoadKeepsAnIncompleteGoalForTheManagerToValidate(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An id with no objective is not silently dropped: the goal manager owns
	// validation and reports a usable warning on resume.
	const body = `{
  "metadata": {"id": "sess_half_goal", "cwd": "/repo", "model": "test"},
  "messages": [],
  "goal": {"id": "goal_half", "status": "active"}
}`
	if err := os.WriteFile(store.sessionPath("sess_half_goal"), []byte(body), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_half_goal")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Goal == nil || loaded.Goal.ID != "goal_half" {
		t.Fatalf("goal = %+v, want the incomplete record preserved", loaded.Goal)
	}
}

func TestGoalDoesNotAffectTitleOrSearch(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := &Session{
		Metadata: Metadata{ID: "sess_title", CWD: "/repo", Model: "test"},
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: "text", Text: "fix the flaky session test"}},
		}},
		Goal: testGoal(),
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_title")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Metadata.Title; got != "fix the flaky session test" {
		t.Fatalf("title = %q, want it taken from the first user message", got)
	}

	// The goal objective is "make go test ./... pass again"; a query that only
	// the goal contains must not match.
	results, warnings, err := store.Search(context.Background(), "make go test")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("search warnings = %+v", warnings)
	}
	if len(results) != 0 {
		t.Fatalf("search matched goal text: %+v", results)
	}

	results, _, err = store.Search(context.Background(), "flaky")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Metadata.ID != "sess_title" {
		t.Fatalf("transcript search = %+v, want one match", results)
	}
}

func TestGoalIsNotInTheSessionIndex(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{CWD: "/repo", Model: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Goal = testGoal()
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(store.indexPath())
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if strings.Contains(string(b), "goal") {
		t.Fatalf("index.json carries goal state:\n%s", b)
	}
	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != sess.Metadata.ID {
		t.Fatalf("List = %+v", items)
	}
}

func TestGoalRoundTripsThroughEveryStatus(t *testing.T) {
	statuses := []goal.Status{
		goal.Active, goal.Paused, goal.Complete, goal.Blocked,
		goal.BudgetLimited, goal.UsageLimited,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store := NewStore(t.TempDir())
			sess, err := store.Create(context.Background(), Metadata{CWD: "/repo", Model: "test"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			g := testGoal()
			g.Status = status
			sess.Goal = g
			if err := store.Save(context.Background(), sess); err != nil {
				t.Fatalf("Save: %v", err)
			}
			loaded, err := store.Load(context.Background(), sess.Metadata.ID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if loaded.Goal == nil || loaded.Goal.Status != status {
				t.Fatalf("status = %+v, want %q", loaded.Goal, status)
			}
			// A loaded goal must be usable by the manager without repair.
			if _, err := goal.NewManager(loaded.Goal, goal.Limits{}); err != nil {
				t.Fatalf("NewManager rejected a persisted goal: %v", err)
			}
		})
	}
}

func TestSavedGoalSurvivesASessionJSONRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(context.Background(), Metadata{CWD: "/repo", Model: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Goal = testGoal()
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(store.sessionPath(sess.Metadata.ID))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var decoded struct {
		Goal *goal.State `json:"goal"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Goal == nil || decoded.Goal.ID != "goal_abc123" {
		t.Fatalf("goal key on disk = %+v", decoded.Goal)
	}
}
