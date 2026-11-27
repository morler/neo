package tui

import (
	"strings"

	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/workflow"
)

// handleWorkflowEvent applies one workflow tool call to the live checklist.
// The checklist is an ordinary transcript block, appended when created and
// mutated in place afterwards; it never occupies fixed screen space.
func (m *model) handleWorkflowEvent(ev workflow.Event) {
	if ev.Generation != m.conversationGeneration {
		logx.Debug("stale workflow event ignored", "event_generation", ev.Generation, "conversation_generation", m.conversationGeneration)
		return
	}
	if ev.Action == "clear" {
		m.workflow = nil
		m.refreshViewport()
		return
	}
	m.turn.workflow = true
	if ev.Action == "create" {
		wb := &workflowBlock{title: ev.State.Title, items: ev.State.Items}
		m.workflow = wb
		m.appendBlock(wb)
		return
	}
	if m.workflow == nil {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID != ev.ID {
			continue
		}
		switch ev.Action {
		case "start":
			m.workflow.active = ev.ID
			m.workflow.items[i].Status = workflow.Running
		case "complete":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Done
		case "fail":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Failed
		case "skip":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Skipped
		}
		if ev.Detail != "" {
			m.workflow.items[i].Detail = ev.Detail
		}
		m.refreshViewport()
		return
	}
}

// noteWorkflowActivity attaches the latest tool activity to the running item
// so the checklist shows what the agent is doing for that step.
func (m *model) noteWorkflowActivity(detail string) {
	if m.workflow == nil || m.workflow.active == "" || strings.TrimSpace(detail) == "" {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID == m.workflow.active {
			m.workflow.items[i].Detail = detail
			m.refreshViewport()
			return
		}
	}
}

// handleSubagentEvent attaches one child-agent event to the live workflow item.
//
// A delegated child is one step of the plan, not a new transcript row. Its tool
// calls update that step's detail line the way the coordinator's own calls do,
// because a block per child event would grow with the child's work and bury the
// plan it belongs to. The parent "agent" receipt still reports the outcome, so
// nothing here is the only record of what the child did.
func (m *model) handleSubagentEvent(ev subagent.Event) {
	if ev.Generation != m.conversationGeneration {
		logx.Debug("stale subagent event ignored", "event_generation", ev.Generation, "conversation_generation", m.conversationGeneration)
		return
	}
	if m.workflow == nil || m.workflow.active == "" {
		return
	}
	// Attach only while the parent agent call is still running. A child event
	// that arrives after the coordinator moved on would otherwise relabel a
	// later step with work that belongs to a finished one.
	if !m.agentCallInFlight(ev.CallID) {
		return
	}
	if detail := subagentDetail(ev.Ev); detail != "" {
		m.noteWorkflowActivity(detail)
	}
}

// agentCallInFlight reports whether the agent call that started this child is
// still running. An empty call ID matches any running agent call, which keeps
// events from a supervisor that predates parent attribution visible.
func (m *model) agentCallInFlight(callID string) bool {
	for id, call := range m.inflight {
		if call.name != "agent" {
			continue
		}
		if callID == "" || id == callID {
			return true
		}
	}
	return false
}

// subagentDetail renders one child event as a short activity line. Text and
// usage events say nothing about what the child is doing, so they are skipped.
//
// The lifecycle kinds are mapped so a finished child does not leave the step
// showing the last thing it happened to run, which would read as still-running
// work. The parent's own receipt remains the record of the outcome.
func subagentDetail(ev subagent.AgentEvent) string {
	switch ev.Kind {
	case "tool", "error":
		body := oneLine(strings.TrimSpace(ev.Body))
		if body == "" {
			return ""
		}
		return "Subagent · " + truncate(body, 60)
	case "done":
		return "Subagent finished"
	case "fail":
		return "Subagent failed"
	}
	return ""
}
