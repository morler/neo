package goal

import (
	"fmt"
	"strings"
	"testing"
)

func TestContinuationPromptCarriesTheObjectiveAndStoppingRules(t *testing.T) {
	mgr, err := NewManager(nil, Limits{MaxAttempts: 20, BlockedThreshold: 2})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	started, err := mgr.Start("make go test ./... pass again")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := mgr.ConsumeAttempt(); err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	state := *mgr.Snapshot()

	prompt := ContinuationPrompt(state, mgr.Limits())

	for _, want := range []string{
		continuationHeader,
		"1/20",
		"<goal>\nmake go test ./... pass again\n</goal>",
		"task context",
		"Verify before completing",
		"recurred 2 times",
		"Do not redefine success as a smaller task",
		"out of reach",
		"complete",
		"blocked",
		"1 of 20",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, started.State.Contract.Verification) {
		t.Fatalf("prompt does not restate the verification rule:\n%s", prompt)
	}
}

func TestContinuationPromptUsesTheConfiguredThresholdAndBudget(t *testing.T) {
	state := State{
		Objective: "ship it",
		Contract:  defaultContract("ship it", Limits{MaxAttempts: 3, BlockedThreshold: 4}),
		Attempts:  Attempts{Used: 2, Max: 3},
	}
	prompt := ContinuationPrompt(state, Limits{MaxAttempts: 3, BlockedThreshold: 4})
	if !strings.Contains(prompt, "recurred 4 times") {
		t.Fatalf("prompt does not use the configured blocked threshold:\n%s", prompt)
	}
	if !strings.Contains(prompt, "2/3") || !strings.Contains(prompt, "2 of 3") {
		t.Fatalf("prompt does not use the configured attempt budget:\n%s", prompt)
	}
}

func TestContinuationPromptNormalizesZeroLimits(t *testing.T) {
	state := State{Objective: "ship it", Contract: defaultContract("ship it", Limits{})}
	prompt := ContinuationPrompt(state, Limits{})
	if !strings.Contains(prompt, fmt.Sprintf("recurred %d times", DefaultBlockedThreshold)) {
		t.Fatalf("prompt does not fall back to the default threshold:\n%s", prompt)
	}
	if !strings.Contains(prompt, fmt.Sprintf("/%d", DefaultMaxAttempts)) {
		t.Fatalf("prompt does not fall back to the default budget:\n%s", prompt)
	}
}

func TestContinuationDisplayIsShortAndMarked(t *testing.T) {
	state := State{
		Objective: "make go test ./... pass again",
		Attempts:  Attempts{Used: 3, Max: 20},
	}
	display := ContinuationDisplay(state)
	if !strings.HasPrefix(display, continuationHeader) {
		t.Fatalf("display = %q, want the continuation marker", display)
	}
	if !strings.Contains(display, "3/20") {
		t.Fatalf("display = %q, want the attempt counter", display)
	}
	if !strings.Contains(display, "make go test ./... pass again") {
		t.Fatalf("display = %q, want a short objective summary", display)
	}
	if strings.Count(display, "\n") > 0 {
		t.Fatalf("display is multi-line: %q", display)
	}
	if len(display) > 120 {
		t.Fatalf("display is too long for a transcript line: %d", len(display))
	}
}

func TestContinuationDisplayWithoutABudget(t *testing.T) {
	got := ContinuationDisplay(State{Objective: "ship it"})
	if !strings.HasPrefix(got, continuationHeader) || strings.Contains(got, "/") {
		t.Fatalf("display = %q", got)
	}
}

func TestIsContinuationRecognizesTheMarker(t *testing.T) {
	state := State{Objective: "ship it", Attempts: Attempts{Max: 5}}
	display := ContinuationDisplay(state)
	prompt := ContinuationPrompt(state, Limits{MaxAttempts: 5})
	if !IsContinuation(display, prompt) {
		t.Fatal("IsContinuation did not recognize a continuation turn")
	}
	if IsContinuation("fix the bug", "fix the bug") {
		t.Fatal("IsContinuation matched an ordinary user turn")
	}
	if !IsContinuation("", prompt) {
		t.Fatal("IsContinuation should recognize the prompt text alone")
	}
}

func TestContinuationPromptKeepsTheObjectiveVerbatim(t *testing.T) {
	objective := "refactor internal/session\n  keep the public API identical"
	state := State{Objective: objective, Attempts: Attempts{Max: 5}}
	prompt := ContinuationPrompt(state, Limits{MaxAttempts: 5})
	if !strings.Contains(prompt, objective) {
		t.Fatalf("prompt rewrote the objective:\n%s", prompt)
	}
}
