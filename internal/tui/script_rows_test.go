package tui

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
)

// scriptCall is the parent receipt a script reports when it starts.
func scriptCall(id string) agent.Event {
	return agent.Event{Kind: agent.EventToolCall, ToolUseID: id, Name: "script", Args: map[string]any{"source": "..."}}
}

// scriptSub is one call a script made. GroupSize stays zero: the number of
// sub-calls is unknown while the script runs, which is how the TUI tells this
// apart from a parallel group.
func scriptSub(kind agent.EventKind, id, parent, name string, args map[string]any, text string, failed bool) agent.Event {
	return agent.Event{
		Kind: kind, ToolUseID: id, GroupID: parent, Name: name,
		Args: args, Text: text, IsError: failed,
	}
}

// TestScriptSubCallsRenderUnderTheirParent covers the visible contract: a row
// per call, in the order the calls happened.
func TestScriptSubCallsRenderUnderTheirParent(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(scriptCall("call_script"))

	for i, path := range []string{"a.go", "b.go", "c.go"} {
		id := "call_script#" + string(rune('1'+i))
		m.handleEvent(scriptSub(agent.EventToolCall, id, "call_script", "read_file", map[string]any{"path": path}, "", false))
		m.handleEvent(scriptSub(agent.EventToolResult, id, "call_script", "read_file", map[string]any{"path": path}, "body", false))
	}

	var rows []subCallBlock
	for _, b := range m.blocks {
		if row, ok := b.(subCallBlock); ok {
			rows = append(rows, row)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("rendered %d sub-call rows, want 3", len(rows))
	}
	want := []string{"a.go", "b.go", "c.go"}
	for i, row := range rows {
		if got := plain(row.render(80, nil)); !strings.Contains(got, want[i]) {
			t.Fatalf("row %d = %q, want it to mention %q", i, got, want[i])
		}
		if !row.done {
			t.Fatalf("row %d did not settle", i)
		}
	}
}

// TestScriptSubCallSettlesWithoutAppending is the no-layout-shift rule: a
// finished call updates its row instead of adding another block.
func TestScriptSubCallSettlesWithoutAppending(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(scriptCall("call_script"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#1", "call_script", "read_file", map[string]any{"path": "a.go"}, "", false))

	before := len(m.blocks)
	m.handleEvent(scriptSub(agent.EventToolResult, "call_script#1", "call_script", "read_file", map[string]any{"path": "a.go"}, "body", false))
	if after := len(m.blocks); after != before {
		t.Fatalf("block count went from %d to %d; a settle must not append", before, after)
	}
}

// TestScriptSubCallFailureStaysVisible checks the failure glyph, the one thing
// a reader has to notice without expanding anything.
func TestScriptSubCallFailureStaysVisible(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(scriptCall("call_script"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#1", "call_script", "read_file", map[string]any{"path": "a.go"}, "", false))
	m.handleEvent(scriptSub(agent.EventToolResult, "call_script#1", "call_script", "read_file", map[string]any{"path": "a.go"}, "no such file", true))

	row, ok := m.blocks[len(m.blocks)-1].(subCallBlock)
	if !ok {
		t.Fatal("last block is not a sub-call row")
	}
	if !row.isError {
		t.Fatal("row did not record the failure")
	}
	if m.turn.errors == 0 {
		t.Fatal("a failed sub-call should count as an error for the turn")
	}
}

// TestScriptSubCallsDoNotCrossParents keeps two scripts' rows apart even though
// both use the same shape of ID.
func TestScriptSubCallsDoNotCrossParents(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(scriptCall("call_a"))
	m.handleEvent(scriptCall("call_b"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_a#1", "call_a", "read_file", map[string]any{"path": "a.go"}, "", false))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_b#1", "call_b", "read_file", map[string]any{"path": "b.go"}, "", false))

	// Settling b must not touch a's row.
	m.handleEvent(scriptSub(agent.EventToolResult, "call_b#1", "call_b", "read_file", map[string]any{"path": "b.go"}, "body", true))

	idxA, ok := m.subCalls["call_a#1"]
	if !ok {
		t.Fatal("a's row was not tracked")
	}
	rowA := m.blocks[idxA].(subCallBlock)
	if rowA.done {
		t.Fatal("settling b settled a")
	}
}

// TestScriptSubCallWithoutParentStillShows falls back to a flat receipt when
// the parent is not on screen, so the work is never silently dropped.
func TestScriptSubCallWithoutParentStillShows(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	before := len(m.blocks)
	m.handleEvent(scriptSub(agent.EventToolCall, "orphan#1", "missing", "read_file", map[string]any{"path": "a.go"}, "", false))
	if len(m.blocks) == before {
		t.Fatal("an orphan sub-call produced no block")
	}
}

// TestScriptRowsDoNotMoveEarlierLines is the layout rule stated in terms the
// renderer actually guarantees: appending a row must not change the rendered
// text of any block that came before it. The viewport scrolls because it has a
// fixed height, which moves pixels, not block order.
func TestScriptRowsDoNotMoveEarlierLines(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.appendBlock(textBlock{text: "first line"})
	m.handleEvent(scriptCall("call_script"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#1", "call_script", "read_file", map[string]any{"path": "a.go"}, "", false))

	before := append([]string(nil), m.rendered...)
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#2", "call_script", "read_file", map[string]any{"path": "b.go"}, "", false))

	for i := range before {
		if i >= len(m.rendered) {
			t.Fatalf("block %d lost its rendering", i)
		}
		if m.rendered[i] != before[i] {
			t.Fatalf("block %d re-rendered:\n before %q\n after  %q", i, before[i], m.rendered[i])
		}
	}
}

// TestScriptRowTruncatesOnNarrowTerminals checks the 40-column case: a row is
// cut, never wrapped into extra lines that would push the composer down.
func TestScriptRowTruncatesOnNarrowTerminals(t *testing.T) {
	row := subCallBlock{name: "read_file", args: map[string]any{"path": strings.Repeat("deep/", 40) + "file.go"}, done: true}
	out := plain(row.render(40, nil))
	if strings.Contains(out, "\n") {
		t.Fatalf("row wrapped: %q", out)
	}
	if len(out) > 40 {
		t.Fatalf("row is %d cells, want at most 40", len(out))
	}
}

// TestScriptApprovalShowsItsRequester covers the one thing a user needs to judge
// a prompt correctly: whether the model asked, or a script did.
func TestScriptApprovalShowsItsRequester(t *testing.T) {
	card := approvalBlock{req: agent.ApprovalRequest{
		ToolName: "write_file",
		Parent:   "script",
		Args:     map[string]any{"path": "notes.txt", "content": "x"},
	}}
	out := plain(card.render(80, nil))
	if !strings.Contains(out, "approval required by script") {
		t.Fatalf("card = %q, want the requester named", out)
	}
	// The card names the operation rather than the tool identifier, which is
	// what the same card shows for a top-level call.
	if !strings.Contains(out, "write notes.txt") {
		t.Fatalf("card = %q, want the operation named", out)
	}
}

// TestDirectApprovalHasNoRequester keeps the ordinary prompt unchanged: naming
// a parent that does not exist would be worse than saying nothing.
func TestDirectApprovalHasNoRequester(t *testing.T) {
	card := approvalBlock{req: agent.ApprovalRequest{ToolName: "bash", Args: map[string]any{"command": "ls"}}}
	out := plain(card.render(80, nil))
	if strings.Contains(out, " by ") {
		t.Fatalf("card = %q, want no requester for a top-level call", out)
	}
}

// run() is how a code-mode script does every test, build and git command, so it
// has to render a row like the file tools do. It used to report only on
// completion, which left the TUI nothing to append.
func TestRunSubCallRendersAndSettlesInPlace(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(scriptCall("call_script"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#1", "call_script", "run",
		map[string]any{"argv": []any{"go", "test", "./..."}}, "", false))

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want one row for the running command", len(m.blocks))
	}
	row, ok := m.blocks[0].(subCallBlock)
	if !ok {
		t.Fatalf("block 0 = %T, want a sub-call row", m.blocks[0])
	}
	if got := plain(row.render(100, nil)); !strings.Contains(got, "go test") {
		t.Fatalf("row = %q, want it to name the command", got)
	}
	before := len(m.blocks)
	m.handleEvent(scriptSub(agent.EventToolResult, "call_script#1", "call_script", "run",
		map[string]any{"argv": []any{"go", "test", "./..."}}, "exit 0", false))

	if len(m.blocks) != before {
		t.Fatalf("blocks went from %d to %d; a settle must not append", before, len(m.blocks))
	}
	settled, ok := m.blocks[0].(subCallBlock)
	if !ok || !settled.done {
		t.Fatalf("row did not settle: %+v", m.blocks[0])
	}
	if settled.isError {
		t.Fatal("a command that succeeded was marked as failed")
	}
}
