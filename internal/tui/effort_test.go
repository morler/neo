package tui

import (
	"errors"
	"strings"
	"testing"
)

// lastText renders the newest block, which is what the user reads after a
// command. Every assertion is about that sentence, not the layout.
func lastText(m *model) string {
	return strings.Join(strings.Fields(plain(m.blocks[len(m.blocks)-1].render(80, nil))), " ")
}

// withEffort installs a switch that records each value and a reader that
// reports the last successful one, mirroring how the CLI wires /effort.
func withEffort(m *model, current *string) (*[]string, *error) {
	calls := new([]string)
	fail := new(error)
	m.effortSwitcher = func(v string) error {
		*calls = append(*calls, v)
		if *fail != nil {
			return *fail
		}
		*current = v
		return nil
	}
	m.effort = func() string { return *current }
	return calls, fail
}

func TestEffortCommandBareReportsUnset(t *testing.T) {
	m := makeTestModel()
	current := ""
	withEffort(m, &current)
	m.handleSlashCommand("/effort")
	if out := lastText(m); !strings.Contains(out, "effort unset") {
		t.Fatalf("bare /effort should explain the unset state, got %q", out)
	}
}

func TestEffortCommandSetsAndReportsTheValue(t *testing.T) {
	m := makeTestModel()
	current := ""
	calls, _ := withEffort(m, &current)

	m.handleSlashCommand("/effort HIGH")
	if len(*calls) != 1 || (*calls)[0] != "high" {
		t.Fatalf("switcher calls = %v, want one call with high", *calls)
	}
	if current != "high" {
		t.Fatalf("current = %q, want high", current)
	}
	if out := lastText(m); !strings.Contains(out, "effort high") {
		t.Fatalf("status line did not confirm the value: %q", out)
	}
}

func TestEffortCommandPassesUnknownValuesThrough(t *testing.T) {
	// The backend owns the accepted set. A word Neo has not heard of must reach
	// it so its error can explain the problem; blocking it here would reject a
	// setting the server accepts.
	m := makeTestModel()
	current := ""
	calls, _ := withEffort(m, &current)

	m.handleSlashCommand("/effort ultra")
	if len(*calls) != 1 || (*calls)[0] != "ultra" {
		t.Fatalf("switcher calls = %v, want the unknown value passed through", *calls)
	}
}

func TestEffortCommandShowsAFailedWrite(t *testing.T) {
	m := makeTestModel()
	current := ""
	_, fail := withEffort(m, &current)
	*fail = errors.New("disk full")

	m.handleSlashCommand("/effort high")
	if current == "high" {
		t.Fatal("a failed switch must not report the new value as applied")
	}
	if out := lastText(m); !strings.Contains(out, "disk full") {
		t.Fatalf("failure was not reported: %q", out)
	}
}

func TestEffortCommandWithoutSwitcherSaysSo(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/effort high")
	if out := lastText(m); !strings.Contains(out, "unavailable") {
		t.Fatalf("a session without the switch should say so, got %q", out)
	}
}

func TestEffortStatusLineNamesTheEffect(t *testing.T) {
	// "medium" alone does not say whether a request carries anything, which is
	// the question a user asking about effort actually has.
	m := makeTestModel()
	current := ""
	withEffort(m, &current)
	if got := m.effortStatusLine(); !strings.Contains(got, "no reasoning field") {
		t.Fatalf("unset status = %q, want it to mention that nothing is sent", got)
	}
	current = "high"
	if got := m.effortStatusLine(); !strings.Contains(got, "high") || !strings.Contains(got, "every request") {
		t.Fatalf("set status = %q, want the value and its scope", got)
	}
}

// TestEffortStatusLineExplainsNone: "none" is a setting, not the absence of
// one, and the status line has to tell those apart — the unset message says the
// backend's default applies, which is the opposite of what none asked for.
func TestEffortStatusLineExplainsNone(t *testing.T) {
	m := makeTestModel()
	current := ""
	withEffort(m, &current)
	current = "none"
	got := m.effortStatusLine()
	if !strings.Contains(got, "turned off") {
		t.Fatalf("none status = %q, want it to say thinking is off", got)
	}
	if strings.Contains(got, "default applies") {
		t.Fatalf("none status = %q, must not read like unset", got)
	}
}
