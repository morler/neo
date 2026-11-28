package tui

import (
	"time"

	"github.com/owainlewis/neo/internal/goal"
)

// resetConversation starts a clean conversation without disturbing the
// backend, workspace, or presentation configuration carried by the model.
// /clear is idle-only, but clearing defensive in-flight handles here keeps all
// conversation-scoped cleanup owned by this one operation.
//
// The session goal is cleared here too, and that is deliberate: a goal that
// survived /clear would keep asking the model to continue against a transcript
// that no longer contains the evidence it needs. Clearing is also what stops a
// pending automatic continuation, because the scheduler reads the goal when it
// decides to enqueue one, so a manager with no goal cannot start a turn.
//
// The cleared goal is returned so the caller can report it. Keeping a goal
// without continuing is /goal pause, not /clear.
func (m *model) resetConversation() *goal.State {
	m.conversationGeneration++
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	if m.approval != nil {
		// A pending approval should not be reachable through the idle-only
		// /clear command. Deny it defensively so its waiter cannot be stranded.
		if m.approval.reply != nil {
			select {
			case m.approval.reply <- false:
			default:
			}
		}
		m.approval = nil
	}

	cleared := m.clearGoal()

	// ag.Clear() drops the transcript and the token accounting together, so
	// /usage reports a fresh session after /clear instead of the old totals.
	m.ag.Clear()
	m.blocks = nil
	m.rendered = nil
	m.clearSelection()
	m.copyNotice = ""
	m.busy = false
	m.busySince = time.Time{}
	m.inflight = nil
	m.workflow = nil
	m.turn = turnStats{}
	m.pendingSteering = nil
	m.queued = nil
	m.models = modelBrowser{}
	m.providers = providerBrowser{}

	// The composer and its pickers are transient activity. Keep the file
	// index, which is rooted in the long-lived working directory, but discard
	// any active token or error from the old conversation.
	fileRoot := m.files.root
	fileIndex := m.files.files
	m.resetInput()
	m.files = filePicker{root: fileRoot, files: fileIndex}
	m.setDotColor(colDotThinking)
	m.refreshViewport()
	return cleared
}

// clearGoal drops the session goal, if any. A session without a goal, or one
// whose manager was never wired, is not an error: /clear still resets the
// conversation.
func (m *model) clearGoal() *goal.State {
	if m.goal == nil {
		return nil
	}
	cleared, err := m.goal.Clear()
	if err != nil {
		return nil
	}
	return cleared
}
