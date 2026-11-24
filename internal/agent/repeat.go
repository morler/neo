package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrRepeatedToolCall ends a turn whose model kept issuing the same call. It is
// a distinct error so a caller can tell a loop guard from a provider failure.
var ErrRepeatedToolCall = errors.New("repeated tool call")

// Repeated-call protection. A model that gets stuck reissuing the same tool call
// burns a full provider request and a full tool execution per turn until
// MaxTurns, which is a 500-turn fuse rather than a budget. This is the inner,
// targeted guard: it names the call, so the model gets a chance to change
// approach, and it stops a call that repeats far past the point where a change
// of approach is plausible.
//
// MaxTurns stays as the outer safety valve. They do not replace each other: a
// loop that alternates between three different calls trips neither this guard
// nor anything else until MaxTurns, and this guard still helps with a model
// whose turn budget is large.
const (
	// repeatStopAt is the count at which the call is not executed and the turn
	// ends. It sits well past the last reminder because some models ignore them.
	repeatStopAt = 12
)

// repeatNoticeAt lists the counts that earn a reminder in the transcript. A
// reminder is appended at the end of the conversation, never injected into the
// system prompt, the tool list, or earlier messages, so the cached prefix stays
// byte-identical (docs/design/token-budget.md rule 1).
var repeatNoticeAt = map[int]bool{3: true, 5: true, 8: true}

// repeatKey identifies a tool call by name and canonicalized input. The input is
// hashed rather than kept so a looping model cannot grow memory with each
// attempt; two different arguments therefore count separately, which is what
// makes "same call" meaningful.
func repeatKey(name string, input map[string]any) string {
	b, err := json.Marshal(input)
	if err != nil {
		// An unmarshalable input cannot be compared reliably, so give it a key
		// of its own rather than folding unrelated calls together.
		return name + "\x00unmarshalable"
	}
	sum := sha256.Sum256(b)
	return name + "\x00" + string(sum[:])
}

// repeatGuard counts identical calls within one user turn.
//
// State-changing tools (workflow, goal) are counted like any other: they still
// cost a provider request and a turn, and a loop that keeps re-reporting the
// same checklist is exactly the waste this guard is for. Exempting them would
// need a per-tool policy in the agent loop, which MISSION.md keeps out.
type repeatGuard struct {
	counts map[string]int
}

func newRepeatGuard() *repeatGuard {
	return &repeatGuard{counts: map[string]int{}}
}

// observe records one attempt and reports what should happen to it: the count
// (1 for the first), whether a reminder belongs in the transcript, and whether
// the call must be refused instead of run.
func (g *repeatGuard) observe(name string, input map[string]any) (count int, remind, stop bool) {
	if g == nil {
		return 1, false, false
	}
	key := repeatKey(name, input)
	g.counts[key]++
	count = g.counts[key]
	return count, repeatNoticeAt[count], count >= repeatStopAt
}

// repeatReminder is the short note appended for a repeated call. It names the
// count and the call so the model can tell which of several in-flight calls it
// is about, and suggests a concrete change rather than only reporting a number.
func repeatReminder(name string, count int) string {
	return fmt.Sprintf(
		"<system-reminder>This is repeated call #%d of %s with identical arguments. "+
			"Repeating it will not produce a different result: change the approach, "+
			"narrow the call, or report what is blocking progress.</system-reminder>",
		count, name)
}

// repeatStopReason explains a refused call. It is a readable error rather than
// an empty result so the user can tell "the loop was stopped" from "I pressed
// esc", and it reaches the transcript as a failed tool result.
func repeatStopReason(name string, count int) string {
	return fmt.Sprintf(
		"stopped: %s was called %d times in this turn with identical arguments, "+
			"so Neo refused to run it again", name, count)
}
