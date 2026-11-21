// Package goal owns the durable control-plane state behind /goal: at most one
// goal per session, its status transitions, attempt budget, and blocked audit.
//
// The TUI, the goal tool, and the continuation scheduler change state only
// through Manager, which serializes transitions so an idle continuation cannot
// race a user command. The core agent loop never learns that goals exist: it
// sees ordinary user turns.
package goal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of a session goal. Only Active lets the
// scheduler continue; every other status stops automatic continuation until
// the user acts.
type Status string

const (
	// Active means the goal may still receive automatic continuation turns.
	Active Status = "active"
	// Paused stops automatic continuation while keeping the goal.
	Paused Status = "paused"
	// Complete means the model reported verified completion.
	Complete Status = "complete"
	// Blocked means the same blocker recurred past the audit threshold.
	Blocked Status = "blocked"
	// BudgetLimited means the attempt budget was exhausted.
	BudgetLimited Status = "budget_limited"
	// UsageLimited is reserved for token or wall-clock accounting, which
	// Phase 1 does not implement. No transition produces it yet.
	UsageLimited Status = "usage_limited"
	// Cleared is reported after the goal is removed, so the transcript can
	// explain the transition. A manager never holds a goal in this status.
	Cleared Status = "cleared"
)

const (
	// DefaultMaxAttempts mirrors the config default for goals.max_attempts.
	// Config and CLI callers should pass the configured value through Limits
	// rather than relying on this constant.
	DefaultMaxAttempts = 20
	// DefaultBlockedThreshold is how many consecutive goal turns must report
	// the same blocker before the goal may move to blocked.
	DefaultBlockedThreshold = 2
)

var (
	// ErrNoGoal reports that the session has no goal.
	ErrNoGoal = errors.New("no active goal")
	// ErrInvalid reports an unusable request, such as an empty objective.
	ErrInvalid = errors.New("invalid goal request")
	// ErrStaleGoal reports a goal ID that does not match the current goal,
	// which happens when the model updates a replaced goal.
	ErrStaleGoal = errors.New("stale goal id")
	// ErrReason reports a missing reason on complete or blocked.
	ErrReason = errors.New("a reason is required")
	// ErrTransition reports a status change that the ownership rules reject.
	ErrTransition = errors.New("transition not allowed")
)

// Contract describes what finishing the goal means. Objective text is always
// user-provided data; the contract only carries defaults so a continuation
// prompt can restate the stopping rules.
type Contract struct {
	Outcome      string   `json:"outcome,omitempty"`
	Verification string   `json:"verification,omitempty"`
	Constraints  []string `json:"constraints,omitempty"`
	Boundaries   []string `json:"boundaries,omitempty"`
	StopWhen     []string `json:"stop_when,omitempty"`
}

// Attempts counts automatic continuation turns. The turn started by
// /goal <objective> is not an attempt.
//
// Phase 1 budgets attempts only. Token and wall-clock accounting would need
// usage the core loop does not attribute to a goal, so UsageLimited exists as a
// reserved status with no transition that produces it.
type Attempts struct {
	Used int `json:"used"`
	Max  int `json:"max"`
}

// BlockedAudit tracks how often the same blocker was reported so a single
// transient failure cannot end a goal.
type BlockedAudit struct {
	Signature  string `json:"signature,omitempty"`
	Count      int    `json:"count,omitempty"`
	LastReason string `json:"last_reason,omitempty"`
}

// State is one session goal. The JSON shape is the session's optional top-level
// "goal" field, so the same type is used for persistence and tool output.
type State struct {
	ID         string       `json:"id"`
	Objective  string       `json:"objective"`
	Contract   Contract     `json:"contract,omitempty"`
	Status     Status       `json:"status"`
	Attempts   Attempts     `json:"attempts"`
	Blocked    BlockedAudit `json:"blocked,omitempty"`
	LastReason string       `json:"last_reason,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// Continues reports whether automatic continuation may run. Only an active
// goal does; every other status waits for the user.
func (s State) Continues() bool { return s.Status == Active }

// Limits bounds one goal. A zero field selects the package default.
type Limits struct {
	MaxAttempts      int
	BlockedThreshold int
}

// StartResult reports the new goal and, when one existed, the goal it
// replaced, so the caller can explain the replacement in the transcript.
type StartResult struct {
	State    State
	Replaced *State
}

// AttemptResult reports whether another automatic continuation turn is
// allowed and the goal state after the decision.
type AttemptResult struct {
	Allowed bool
	State   State
}

// BlockedResult reports the blocked audit outcome. Applied is false when the
// same blocker has not yet recurred enough times.
type BlockedResult struct {
	Applied   bool
	Count     int
	Threshold int
	State     State
}

// Manager owns the session's single goal and serializes every transition.
type Manager struct {
	mu     sync.Mutex
	state  *State
	limits Limits
}

// NewManager builds a manager with no goal, or restores one from persisted
// session state. A restored state is copied and validated: a corrupt record
// fails here so the caller can warn and continue without a goal instead of
// silently starting over.
func NewManager(initial *State, limits Limits) (*Manager, error) {
	m := &Manager{limits: normalizeLimits(limits)}
	if initial == nil {
		return m, nil
	}
	restored, err := m.normalizeRestored(*initial)
	if err != nil {
		return nil, err
	}
	m.state = restored
	return m, nil
}

// Limits reports the bounds this manager enforces. Limits never change after
// construction, so this is safe to call from any goroutine without locking.
func (m *Manager) Limits() Limits { return m.limits }

// Restore reinstates a snapshot this manager produced, replacing the current
// goal or clearing it when snapshot is nil. It exists for callers that must undo
// a transition they could not persist: the state the caller had before is the
// only one that still matches disk.
//
// Restore validates like NewManager and is not a model-facing operation: the
// goal tool exposes no way to call it.
func (m *Manager) Restore(snapshot *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshot == nil {
		m.state = nil
		return nil
	}
	restored, err := m.normalizeRestored(*snapshot)
	if err != nil {
		return err
	}
	m.state = restored
	return nil
}

// Snapshot returns a copy of the current goal, or nil when there is none.
func (m *Manager) Snapshot() *State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil
	}
	return m.state.clone()
}

// Start creates a goal with a fresh ID and a full attempt budget, replacing any
// existing goal. The replacement is returned so the caller can report which
// goal stopped applying.
func (m *Manager) Start(objective string) (StartResult, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return StartResult{}, fmt.Errorf("%w: the objective is empty", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return StartResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var replaced *State
	if m.state != nil {
		previous := m.state.clone()
		replaced = previous
	}
	now := time.Now().UTC()
	next := &State{
		ID:        id,
		Objective: objective,
		Contract:  defaultContract(objective, m.limits),
		Status:    Active,
		Attempts:  Attempts{Max: m.limits.MaxAttempts},
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.state = next
	return StartResult{State: *next.clone(), Replaced: replaced}, nil
}

// Pause stops automatic continuation and keeps the goal. A blank reason keeps
// whatever evidence was last recorded, so an interrupt does not erase why the
// goal was still unfinished.
func (m *Manager) Pause(reason string) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.require()
	if err != nil {
		return nil, err
	}
	if s.Status != Active {
		return nil, transitionError("pause", s.Status)
	}
	s.Status = Paused
	if reason = strings.TrimSpace(reason); reason != "" {
		s.LastReason = reason
	}
	s.touch(time.Now())
	return s.clone(), nil
}

// Resume restarts automatic continuation with a fresh attempt budget and a
// cleared blocked audit. Complete and cleared goals cannot be resumed; start a
// new goal instead.
func (m *Manager) Resume() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.require()
	if err != nil {
		return nil, err
	}
	switch s.Status {
	case Paused, Blocked, BudgetLimited:
	default:
		return nil, transitionError("resume", s.Status)
	}
	s.Status = Active
	s.Attempts.Used = 0
	s.Blocked = BlockedAudit{}
	s.LastReason = ""
	s.touch(time.Now())
	return s.clone(), nil
}

// Clear removes the goal and returns its final snapshot, reported with status
// cleared so the transcript can explain why continuation stopped.
func (m *Manager) Clear() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.require()
	if err != nil {
		return nil, err
	}
	final := s.clone()
	final.Status = Cleared
	final.touch(time.Now())
	m.state = nil
	return final, nil
}

// ConsumeAttempt spends one automatic continuation turn. When the budget is
// already exhausted it moves the goal to budget_limited, reports Allowed=false,
// and leaves the last recorded reason intact for the handoff message.
func (m *Manager) ConsumeAttempt() (AttemptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.require()
	if err != nil {
		return AttemptResult{}, err
	}
	if s.Status != Active {
		return AttemptResult{}, transitionError("continue", s.Status)
	}
	if s.Attempts.Used >= s.Attempts.Max {
		s.Status = BudgetLimited
		s.touch(time.Now())
		return AttemptResult{Allowed: false, State: *s.clone()}, nil
	}
	s.Attempts.Used++
	s.touch(time.Now())
	return AttemptResult{Allowed: true, State: *s.clone()}, nil
}

// RequestComplete marks the goal complete. It is the only completion path for
// the model, so it requires the current goal ID and a reason naming the
// evidence that the objective holds.
func (m *Manager) RequestComplete(goalID, reason string) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.requireModelUpdate(goalID)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: complete needs a reason with evidence", ErrReason)
	}
	s.Status = Complete
	s.LastReason = reason
	s.touch(time.Now())
	return s.clone(), nil
}

// RequestBlocked records a blocker. The same normalized reason must repeat
// BlockedThreshold times in a row before the goal becomes blocked; a different
// reason restarts the audit. Applied=false means "keep working".
func (m *Manager) RequestBlocked(goalID, reason string) (BlockedResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.requireModelUpdate(goalID)
	if err != nil {
		return BlockedResult{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return BlockedResult{}, fmt.Errorf("%w: blocked needs the blocker described", ErrReason)
	}
	signature := blockerSignature(reason)
	if signature == s.Blocked.Signature {
		s.Blocked.Count++
	} else {
		s.Blocked = BlockedAudit{Signature: signature, Count: 1}
	}
	s.Blocked.LastReason = reason
	s.touch(time.Now())
	result := BlockedResult{Count: s.Blocked.Count, Threshold: m.limits.BlockedThreshold}
	if s.Blocked.Count >= m.limits.BlockedThreshold {
		s.Status = Blocked
		s.LastReason = reason
		result.Applied = true
	}
	result.State = *s.clone()
	return result, nil
}

// require returns the current goal under the manager lock.
func (m *Manager) require() (*State, error) {
	if m.state == nil {
		return nil, ErrNoGoal
	}
	return m.state, nil
}

// requireModelUpdate enforces the ownership boundary for model-initiated
// transitions: the goal must exist, the ID must match, and the goal must still
// be active. Pause, resume, budget, and clear belong to the user and system.
func (m *Manager) requireModelUpdate(goalID string) (*State, error) {
	s, err := m.require()
	if err != nil {
		return nil, err
	}
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, fmt.Errorf("%w: goal_id is required; call action=get first", ErrInvalid)
	}
	if goalID != s.ID {
		return nil, fmt.Errorf("%w: %q is not the current goal (%s)", ErrStaleGoal, goalID, s.ID)
	}
	if s.Status != Active {
		return nil, transitionError("update", s.Status)
	}
	return s, nil
}

// normalizeRestored copies and validates a state loaded from a session file.
func (m *Manager) normalizeRestored(in State) (*State, error) {
	// Copy first so the caller's persisted state cannot be mutated through the
	// manager's restored goal.
	s := *in.clone()
	s.ID = strings.TrimSpace(s.ID)
	if s.ID == "" {
		return nil, fmt.Errorf("%w: the saved goal has no id", ErrInvalid)
	}
	s.Objective = strings.TrimSpace(s.Objective)
	if s.Objective == "" {
		return nil, fmt.Errorf("%w: goal %s has no objective", ErrInvalid, s.ID)
	}
	if !validStatus(s.Status) || s.Status == Cleared {
		return nil, fmt.Errorf("%w: goal %s has status %q", ErrInvalid, s.ID, s.Status)
	}
	if s.Attempts.Used < 0 {
		return nil, fmt.Errorf("%w: goal %s has a negative attempt count", ErrInvalid, s.ID)
	}
	if s.Attempts.Max <= 0 {
		s.Attempts.Max = m.limits.MaxAttempts
	}
	if s.Attempts.Used > s.Attempts.Max {
		s.Attempts.Used = s.Attempts.Max
	}
	if s.Blocked.Count < 0 {
		s.Blocked.Count = 0
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	s.Contract = s.Contract.withDefaults(s.Objective, m.limits)
	return &s, nil
}

func (s *State) clone() *State {
	out := *s
	out.Contract.Constraints = append([]string(nil), s.Contract.Constraints...)
	out.Contract.Boundaries = append([]string(nil), s.Contract.Boundaries...)
	out.Contract.StopWhen = append([]string(nil), s.Contract.StopWhen...)
	return &out
}

func (s *State) touch(now time.Time) { s.UpdatedAt = now.UTC() }

// withDefaults fills only the contract fields a caller left blank, so a
// restored goal keeps any contract it already had.
func (c Contract) withDefaults(objective string, limits Limits) Contract {
	defaults := defaultContract(objective, limits)
	if strings.TrimSpace(c.Outcome) == "" {
		c.Outcome = defaults.Outcome
	}
	if strings.TrimSpace(c.Verification) == "" {
		c.Verification = defaults.Verification
	}
	if len(c.Constraints) == 0 {
		c.Constraints = defaults.Constraints
	}
	if len(c.StopWhen) == 0 {
		c.StopWhen = defaults.StopWhen
	}
	return c
}

// defaultContract states the stopping rules a continuation turn must respect.
func defaultContract(objective string, limits Limits) Contract {
	return Contract{
		Outcome:      objective,
		Verification: "Cite the command, test, or file inspection that shows the outcome holds.",
		Constraints: []string{
			"Do not redefine success as a smaller task.",
			"The model may only mark the goal complete or blocked.",
		},
		StopWhen: []string{
			"The outcome is verified with evidence from this transcript.",
			fmt.Sprintf("The same blocker is reported %d times in a row.", limits.BlockedThreshold),
			fmt.Sprintf("The attempt budget (%d continuations) is exhausted.", limits.MaxAttempts),
		},
	}
}

func normalizeLimits(l Limits) Limits {
	if l.MaxAttempts <= 0 {
		l.MaxAttempts = DefaultMaxAttempts
	}
	if l.BlockedThreshold <= 0 {
		l.BlockedThreshold = DefaultBlockedThreshold
	}
	return l
}

func validStatus(s Status) bool {
	switch s {
	case Active, Paused, Complete, Blocked, BudgetLimited, UsageLimited, Cleared:
		return true
	}
	return false
}

// blockerSignature reduces a blocker description to the comparison key for the
// blocked audit, so casing and spacing do not look like a new blocker.
func blockerSignature(reason string) string {
	return strings.ToLower(strings.Join(strings.Fields(reason), " "))
}

func transitionError(action string, status Status) error {
	return fmt.Errorf("%w: cannot %s a goal in state %s", ErrTransition, action, status)
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate goal id: %w", err)
	}
	return "goal_" + hex.EncodeToString(b[:]), nil
}

// Summarize collapses a goal string to one short line for transcript notes and
// tool results, so a long objective does not dominate a status line.
func Summarize(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	line := strings.Join(fields, " ")
	const max = 72
	runes := []rune(line)
	if len(runes) <= max {
		return line
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
