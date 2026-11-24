package tui

import (
	"errors"
	"strings"
	"testing"
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
