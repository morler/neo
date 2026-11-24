package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
)

// codemodeModel builds a model whose /codemode switch is backed by the given
// callback, with the state kept in the test.
func codemodeModel(t *testing.T, initial bool, switchErr error) (*model, *bool, *int) {
	t.Helper()
	m := makeTestModel()
	on := initial
	calls := new(int)
	m.scriptEnabled = func() bool { return on }
	m.scriptSwitcher = func(want bool) error {
		*calls++
		if switchErr != nil {
			return switchErr
		}
		on = want
		return nil
	}
	return m, &on, calls
}

// TestCodeModeStatusReportsState covers the read-only form.
func TestCodeModeStatusReportsState(t *testing.T) {
	m, _, calls := codemodeModel(t, false, nil)
	m.handleSlashCommand("/codemode status")
	if *calls != 0 {
		t.Fatal("status must not switch anything")
	}
	last := m.blocks[len(m.blocks)-1]
	text := plain(last.render(80, nil))
	if !strings.Contains(text, "off") {
		t.Fatalf("status = %q, want it to report off", text)
	}
}

// TestCodeModeOnTurnsTheToolOn is the switch itself.
func TestCodeModeOnTurnsTheToolOn(t *testing.T) {
	m, on, calls := codemodeModel(t, false, nil)
	m.handleSlashCommand("/codemode on")
	if *calls != 1 || !*on {
		t.Fatalf("calls = %d, on = %v", *calls, *on)
	}
	text := plain(m.blocks[len(m.blocks)-1].render(80, nil))
	if !strings.Contains(text, "on") {
		t.Fatalf("result = %q, want it to report on", text)
	}
}

// TestCodeModeOffTurnsTheToolOff is the other direction.
func TestCodeModeOffTurnsTheToolOff(t *testing.T) {
	m, on, calls := codemodeModel(t, true, nil)
	m.handleSlashCommand("/codemode off")
	if *calls != 1 || *on {
		t.Fatalf("calls = %d, on = %v", *calls, *on)
	}
}

// TestCodeModeIsIdempotent keeps a repeated switch from writing the config
// again for no reason.
func TestCodeModeIsIdempotent(t *testing.T) {
	m, _, calls := codemodeModel(t, true, nil)
	m.handleSlashCommand("/codemode on")
	if *calls != 0 {
		t.Fatalf("calls = %d, want none when the state already matches", calls)
	}
}

// TestCodeModeFailureIsReported checks a failed write does not look like a
// successful switch.
func TestCodeModeFailureIsReported(t *testing.T) {
	m, on, _ := codemodeModel(t, false, errors.New("disk full"))
	m.handleSlashCommand("/codemode on")
	if *on {
		t.Fatal("a failed switch changed the state")
	}
	if !strings.Contains(plain(m.blocks[len(m.blocks)-1].render(80, nil)), "disk full") {
		t.Fatal("the failure was not shown")
	}
}

// TestCodeModeRejectsBadArguments covers usage errors rather than guessing.
func TestCodeModeRejectsBadArguments(t *testing.T) {
	m, _, calls := codemodeModel(t, false, nil)
	for _, arg := range []string{"", "maybe"} {
		m.handleSlashCommand("/codemode " + arg)
		if *calls != 0 {
			t.Fatalf("%q switched something", arg)
		}
	}
}

// TestCodeModeUnavailableWithoutSwitcher keeps the command honest in a session
// that was built without it.
func TestCodeModeUnavailableWithoutSwitcher(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/codemode on")
	text := plain(m.blocks[len(m.blocks)-1].render(80, nil))
	if !strings.Contains(text, "unavailable") {
		t.Fatalf("result = %q, want it to say the command is unavailable", text)
	}
}

// TestCodeModeRefusesMidTurn protects the in-flight request: swapping the tool
// list while a turn is running would leave it describing tools that are gone.
func TestCodeModeRefusesMidTurn(t *testing.T) {
	m, _, calls := codemodeModel(t, false, nil)
	m.busy = true
	m.handleSlashCommand("/codemode on")
	if *calls != 0 {
		t.Fatal("a mid-turn switch was allowed")
	}
}

// codeModeStatusText runs /codemode status and returns what the user reads.
// The block wraps and pads to the terminal width, so whitespace is collapsed
// before comparing: the assertion is about the sentence, not the layout.
func codeModeStatusText(m *model) string {
	m.handleSlashCommand("/codemode status")
	return strings.Join(strings.Fields(plain(m.blocks[len(m.blocks)-1].render(80, nil))), " ")
}

// TestCodeModeStatusCountsScriptUse is the point of the line: a user who paid
// the prompt cost every turn can see whether the tool was ever chosen.
func TestCodeModeStatusCountsScriptUse(t *testing.T) {
	m, _, _ := codemodeModel(t, true, nil)
	for i := 0; i < 3; i++ {
		m.handleEvent(scriptCall("call_" + string(rune('a'+i))))
	}
	if text := codeModeStatusText(m); !strings.Contains(text, "used 3 times this session") {
		t.Fatalf("status = %q, want the use count", text)
	}
}

// TestCodeModeStatusSaysNothingWhenUnused keeps the line quiet: "used 0 times"
// only repeats what the absence of a count already says.
func TestCodeModeStatusSaysNothingWhenUnused(t *testing.T) {
	m, _, _ := codemodeModel(t, true, nil)
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "read_file", ToolUseID: "call_a"})
	if text := codeModeStatusText(m); strings.Contains(text, "used") {
		t.Fatalf("status = %q, want no usage line", text)
	}
}

// TestCodeModeStatusHidesCountWhenOff covers the state where a count has no
// meaning: with the tool unavailable, earlier calls are not a cost being paid.
func TestCodeModeStatusHidesCountWhenOff(t *testing.T) {
	m, _, _ := codemodeModel(t, true, nil)
	m.handleEvent(scriptCall("call_a"))
	m.handleSlashCommand("/codemode off")
	if text := codeModeStatusText(m); strings.Contains(text, "used") {
		t.Fatalf("status = %q, want no usage line while off", text)
	}
}

// TestCodeModeCountIgnoresScriptSubCalls separates "the model chose the tool"
// from "the tool did work": a script's own calls carry a GroupID and must not
// inflate the number the user reads.
func TestCodeModeCountIgnoresScriptSubCalls(t *testing.T) {
	m, _, _ := codemodeModel(t, true, nil)
	m.handleEvent(scriptCall("call_script"))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#1", "call_script", "read_file", nil, "", false))
	m.handleEvent(scriptSub(agent.EventToolCall, "call_script#2", "call_script", "grep", nil, "", false))
	if m.scriptCalls != 1 {
		t.Fatalf("scriptCalls = %d, want 1 (sub-calls are not choices)", m.scriptCalls)
	}
}

// TestCodeModeCountIgnoresOtherTools keeps a parallel batch of read-only calls
// from being read as repeated script use.
func TestCodeModeCountIgnoresOtherTools(t *testing.T) {
	m, _, _ := codemodeModel(t, true, nil)
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "read_file", ToolUseID: "call_a", GroupID: "group-1", GroupSize: 3})
	if m.scriptCalls != 0 {
		t.Fatalf("scriptCalls = %d, want 0", m.scriptCalls)
	}
}
