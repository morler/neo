package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

// captureRequest runs one completion against a local server and returns the
// decoded body the client actually sent.
func captureRequest(t *testing.T, req llm.Request) apiRequest {
	t.Helper()
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseResponse([]llm.ContentBlock{{Type: "text", Text: "ok"}}, "end_turn", nil)))
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return got
}

func TestComplete_EffortUnsetSendsNoThinking(t *testing.T) {
	got := captureRequest(t, llm.Request{Model: "m"})
	if got.Thinking != nil {
		t.Fatalf("unset effort must send no thinking block, got %+v", got.Thinking)
	}
}

func TestComplete_EffortSetsBudgetAndLeavesRoomToAnswer(t *testing.T) {
	for effort, budget := range EffortBudgets {
		t.Run(effort, func(t *testing.T) {
			got := captureRequest(t, llm.Request{Model: "m", Effort: effort})
			if got.Thinking == nil {
				t.Fatalf("effort %q did not send a thinking block", effort)
			}
			if got.Thinking.Type != "enabled" || got.Thinking.BudgetToks != budget {
				t.Fatalf("thinking = %+v, want enabled/%d", got.Thinking, budget)
			}
			// The API rejects budget_tokens >= max_tokens, and an answer needs
			// room after the budget; max_tokens is the field both constraints hit.
			if got.MaxTokens <= budget {
				t.Fatalf("max_tokens %d leaves no room above budget %d", got.MaxTokens, budget)
			}
		})
	}
}

func TestComplete_UnknownEffortIsRejectedWithGuidance(t *testing.T) {
	// The Messages API takes a budget, not a word. A word Neo cannot map is
	// therefore never sendable, so the API can never complain about it — this
	// error is the only place the user can learn the setting did nothing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an unexpressible effort must not reach the network")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m", Effort: "ultra"})
	if err == nil {
		t.Fatal("expected an error for an effort anthropic cannot express")
	}
	for _, want := range []string{"ultra", "none, low, medium, high, xhigh, max", "token budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestComplete_NumericBudgetIsAccepted(t *testing.T) {
	// A budget is what the API actually takes, so the user can always say the
	// number directly when a named effort is not what they want.
	got := captureRequest(t, llm.Request{Model: "m", Effort: "4096"})
	if got.Thinking == nil || got.Thinking.BudgetToks != 4096 {
		t.Fatalf("numeric effort was not used as the budget: %+v", got.Thinking)
	}
}

func TestComplete_TooSmallNumericBudgetIsRejected(t *testing.T) {
	if _, err := thinkingFor("100", new(int)); err == nil {
		t.Fatal("a budget below the API minimum should be rejected")
	}
}

func TestComplete_EffortIsCaseInsensitive(t *testing.T) {
	got := captureRequest(t, llm.Request{Model: "m", Effort: " HIGH "})
	if got.Thinking == nil || got.Thinking.BudgetToks != EffortBudgets["high"] {
		t.Fatalf("effort normalization failed: %+v", got.Thinking)
	}
}

// TestEffortNamesMatchesTheBudgetTable keeps the error/help vocabulary and the
// mapping from drifting apart: a name that exists but is not in EffortNames
// would be unreachable from the guidance a user is shown.
func TestEffortNamesMatchesTheBudgetTable(t *testing.T) {
	names := strings.Split(EffortNames, ", ")
	for _, name := range names {
		if name == EffortNone {
			continue
		}
		if _, ok := EffortBudgets[name]; !ok {
			t.Errorf("EffortNames lists %q but EffortBudgets has no entry", name)
		}
	}
	for name := range EffortBudgets {
		found := false
		for _, listed := range names {
			if listed == name {
				found = true
			}
		}
		if !found {
			t.Errorf("EffortBudgets has %q but EffortNames does not list it", name)
		}
	}
}

// TestEffortBudgetsRiseWithTheName is the property a user relies on when they
// pick a bigger word: every step up must cost at least as much thinking.
func TestEffortBudgetsRiseWithTheName(t *testing.T) {
	order := []string{"low", "medium", "high", "xhigh", "max"}
	for i := 1; i < len(order); i++ {
		prev, cur := EffortBudgets[order[i-1]], EffortBudgets[order[i]]
		if cur <= prev {
			t.Errorf("%s budget %d is not above %s's %d", order[i], cur, order[i-1], prev)
		}
	}
}

// TestComplete_NoneSendsNoThinking documents the one place "none" and "unset"
// are indistinguishable: the Messages API turns thinking off by omitting the
// field, so both requests are identical. What matters is that "none" is not an
// error and does not inflate max_tokens.
func TestComplete_NoneSendsNoThinking(t *testing.T) {
	got := captureRequest(t, llm.Request{Model: "m", Effort: "none"})
	if got.Thinking != nil {
		t.Fatalf("none must send no thinking block, got %+v", got.Thinking)
	}
	if got.MaxTokens != defaultMaxTokens {
		t.Fatalf("none changed max_tokens: %d", got.MaxTokens)
	}
}
