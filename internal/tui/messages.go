package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/logx"
)

// updateResult is what one message handler reports back to Update.
//
// A handler that ends the turn of events early (a key the browser consumed, or
// a quit) sets early and stops there: Update must not append the spinner tick
// or batch the commands. Everything else returns the commands to batch and lets
// Update run the shared tail.
type updateResult struct {
	cmds  []tea.Cmd
	early bool
	cmd   tea.Cmd
}

// immediately reports a result that skips the shared tail.
func immediately(cmd tea.Cmd) updateResult {
	return updateResult{early: true, cmd: cmd}
}

// handleMsg dispatches one message to its handler. Keeping the switch a pure
// dispatch table is the point: the message families below stay readable on
// their own, and Update only owns the busy/spinner bookkeeping.
func (m *model) handleMsg(msg tea.Msg) updateResult {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		return m.handleBackgroundColor(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseMotionMsg:
		m.clearApprovalSelection()
		return updateResult{}
	case modelChoicesMsg:
		return m.handleModelChoices(msg)
	case agentEventMsg:
		return m.handleAgentEvent(msg)
	case approvalRequestMsg:
		return m.handleApprovalRequest(msg)
	case sendResultMsg:
		return m.handleSendResult(msg)
	case persistenceRetryResultMsg:
		return m.handlePersistenceRetryResult(msg)
	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	case workflowEventMsg:
		m.handleWorkflowEvent(msg.ev)
		return updateResult{}
	case branchMsg:
		return m.handleBranch(msg)
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return updateResult{cmds: []tea.Cmd{cmd}}
	}
}

// handleBackgroundColor only restyles: the terminal answered the query Init
// sent, so the renderer and the layout follow the real background.
func (m *model) handleBackgroundColor(msg tea.BackgroundColorMsg) updateResult {
	style := "dark"
	if !msg.IsDark() {
		style = "light"
	}
	m.setMarkdownStyle(style)
	m.layout()
	return updateResult{}
}

func (m *model) handleWindowSize(msg tea.WindowSizeMsg) updateResult {
	m.width = msg.Width
	m.height = msg.Height
	m.layout()
	m.refreshViewport()
	return updateResult{}
}

// handleKeyMsg gives the open overlay first refusal: a visible browser or a
// pending approval owns the keyboard and reports back whether it consumed the
// key. Escapes are otherwise handled by handleKey.
func (m *model) handleKeyMsg(msg tea.KeyMsg) updateResult {
	if m.providers.visible {
		_, cmd := m.handleProviderBrowserKey(msg)
		return earlyWith(cmd)
	}
	if m.models.visible {
		_, cmd := m.handleModelBrowserKey(msg)
		return earlyWith(cmd)
	}
	if m.approval != nil {
		return immediately(m.handleApprovalKey(msg))
	}
	return updateResult{cmds: []tea.Cmd{m.handleKey(msg)}}
}

// earlyWith reports that an overlay consumed the key: the browser handlers
// mutate m in place, so only the command they returned matters.
func earlyWith(cmd tea.Cmd) updateResult {
	return updateResult{early: true, cmd: cmd}
}

// handlePaste drops pasted text where it would corrupt state: an approval
// selection or a browser is open, or a quit is pending.
func (m *model) handlePaste(msg tea.PasteMsg) updateResult {
	if m.approval != nil {
		m.clearApprovalSelection()
		return updateResult{}
	}
	if m.models.visible || m.providers.visible || m.quitPending {
		return updateResult{}
	}
	return updateResult{cmds: []tea.Cmd{m.updateInput(msg)}}
}

func (m *model) handleMouseWheel(msg tea.MouseWheelMsg) updateResult {
	m.clearApprovalSelection()
	// Some terminals and multiplexers report vertical trackpad gestures
	// with Shift set. The viewport interprets those as horizontal wheel
	// events, but Neo intentionally disables horizontal transcript scrolling,
	// turning the gesture into a no-op. Vertical wheel buttons should always
	// move through transcript history regardless of that modifier.
	if msg.Button == tea.MouseWheelUp || msg.Button == tea.MouseWheelDown {
		msg.Mod &^= tea.ModShift
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return updateResult{cmds: []tea.Cmd{cmd}}
}

// handleModelChoices stores a fetched catalog. A failure keeps the last good
// list and surfaces the error only in the open browser, and loading flags are
// cleared either way so a retry can start.
func (m *model) handleModelChoices(msg modelChoicesMsg) updateResult {
	m.modelChoicesLoading = false
	m.models.loading = false
	if msg.err != nil {
		m.modelChoicesLoaded = false
		if m.models.visible {
			m.models.err = msg.err
		}
		m.layout()
		return updateResult{}
	}
	m.modelChoicesLoaded = true
	m.modelChoices = normalizeModelChoices(m.modelTag, msg.choices)
	if m.models.visible {
		m.models.err = nil
		m.ensureModelSelection()
	}
	m.layout()
	return updateResult{}
}

func (m *model) handleAgentEvent(msg agentEventMsg) updateResult {
	m.handleEvent(msg.ev)
	// Tie the dot's color to whether a tool is currently running.
	if len(m.inflight) > 0 {
		m.setDotColor(colDotTool)
	} else if m.busy {
		m.setDotColor(colDotThinking)
	}
	m.layout()
	return updateResult{}
}

func (m *model) handleApprovalRequest(msg approvalRequestMsg) updateResult {
	logx.Debug("tui approval requested", "tool", msg.req.ToolName)
	m.approval = &approvalState{req: msg.req, reply: msg.reply}
	m.appendBlock(approvalBlock{req: msg.req})
	m.layout()
	return updateResult{}
}

// handleSendResult closes out a turn: it reports the outcome, honours a pending
// quit, and decides whether an idle goal or a queued follow-up may start next.
func (m *model) handleSendResult(msg sendResultMsg) updateResult {
	elapsed := time.Since(m.busySince)
	m.busy = false
	m.clearInflight()
	m.layout()
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	m.reportTurnOutcome(msg.err, elapsed)
	if msg.saveAttempted {
		m.recordPersistenceResult(msg.saveErr)
		if msg.saveErr != nil {
			m.appendBlock(errorBlock{err: fmt.Errorf("save session: %w", msg.saveErr)})
		}
	}
	cmds := []tea.Cmd{refreshBranch()}
	m.recoverDroppedInput(msg.err, msg.saveErr)
	if res, done := m.finishPendingQuit(); done {
		if res.cmd != nil {
			return res
		}
		return updateResult{cmds: cmds}
	}
	if m.queued != nil { // a successful turn starts its single follow-up
		queued := m.queued
		m.queued = nil
		m.appendBlock(noticeBlock{text: "starting queued follow-up"})
		cmds = append(cmds, m.submitUserTurn(queued.displayText, queued.agentText, queued.images))
		return updateResult{cmds: cmds}
	}
	// The turn ended and nothing else claimed it, so this is the one place
	// an idle goal can start its next continuation. A user follow-up, a
	// pending quit, a canceled turn, and a turn that could not be saved all
	// skip it: input wins, and a goal should not advance on state the
	// session could not record.
	if msg.err == nil && msg.saveErr == nil {
		cmds = append(cmds, m.maybeContinueGoal())
	}
	return updateResult{cmds: cmds}
}

// reportTurnOutcome turns the turn's error, if any, into transcript output. A
// max-turns stop is silent here because the agent already reported it, and a
// cancellation also pauses the goal so a continuation cannot follow it.
func (m *model) reportTurnOutcome(err error, elapsed time.Duration) {
	switch {
	case errors.Is(err, context.Canceled):
		m.appendBlock(noticeBlock{text: "turn canceled"})
		// Esc and Ctrl-C already paused the goal when they canceled the
		// turn. A cancellation that arrived another way still must not
		// fall through to a continuation, so it pauses the goal here.
		m.interruptTurn("turn canceled")
	case errors.Is(err, agent.ErrMaxTurns):
		// EventMaxTurnsReached already appended a maxTurnsBlock
		// describing this turn; don't also emit a result summary.
	case err != nil:
		m.appendBlock(errorBlock{err: err})
	default:
		if summary, ok := m.resultSummary(err, elapsed); ok {
			m.appendBlock(summary)
		}
	}
}

// recoverDroppedInput returns text a failed turn never consumed to the
// composer: unapplied steering, then a queued follow-up, in that order.
func (m *model) recoverDroppedInput(turnErr, saveErr error) {
	var recovered []string
	if turnErr != nil && len(m.pendingSteering) > 0 {
		recovered = append(recovered, m.pendingSteering...)
		m.appendBlock(noticeBlock{text: "unapplied steering returned to the composer"})
	}
	if (turnErr != nil || saveErr != nil) && m.queued != nil {
		recovered = append(recovered, m.queued.displayText)
		m.queued = nil
		m.appendBlock(noticeBlock{text: "queued follow-up returned to the composer"})
	}
	if len(recovered) > 0 {
		m.restoreInput(recovered...)
	}
	m.pendingSteering = nil
}

// finishPendingQuit quits if a quit was waiting on this turn. When the session
// could not be saved the quit is canceled instead, and done reports false so
// the caller keeps its normal path.
func (m *model) finishPendingQuit() (updateResult, bool) {
	if !m.quitPending {
		return updateResult{}, false
	}
	if m.persistenceErr != nil {
		m.quitPending = false
		m.appendBlock(noticeBlock{text: "quit canceled because the session was not saved; press ctrl+c to retry"})
		return updateResult{}, true
	}
	m.quitting = true
	return immediately(tea.Quit), true
}

// handlePersistenceRetryResult finishes a quit that was held back for a session
// save: success quits, failure cancels the quit and says how to retry.
func (m *model) handlePersistenceRetryResult(msg persistenceRetryResultMsg) updateResult {
	m.recordPersistenceResult(msg.err)
	if msg.err != nil {
		m.quitPending = false
		m.appendBlock(errorBlock{err: fmt.Errorf("save session: %w", msg.err)})
		m.appendBlock(noticeBlock{text: "quit canceled because the session was not saved; press ctrl+c to retry"})
		return updateResult{}
	}
	m.quitting = true
	return immediately(tea.Quit)
}

// handleSpinnerTick keeps the tick chain alive only while a turn runs; a tick
// that arrives after the turn ended stops rescheduling itself.
func (m *model) handleSpinnerTick(msg spinner.TickMsg) updateResult {
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	if m.busy {
		return updateResult{cmds: []tea.Cmd{cmd}}
	}
	m.spinning = false
	return updateResult{}
}

func (m *model) handleBranch(msg branchMsg) updateResult {
	if strings.TrimSpace(msg.branch) != "" {
		m.branch = msg.branch
	}
	return updateResult{}
}
