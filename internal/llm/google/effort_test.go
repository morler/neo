package google

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func TestBuildRequest_EffortUnsetLeavesGenerationConfigAlone(t *testing.T) {
	got := buildRequest(llm.Request{Model: "m"})
	if got.GenerationConfig != nil {
		t.Fatalf("unset effort and tokens must not create generationConfig: %+v", got.GenerationConfig)
	}
}

func TestBuildRequest_EffortSetsThinkingBudget(t *testing.T) {
	for effort, budget := range EffortBudgets {
		t.Run(effort, func(t *testing.T) {
			got := buildRequest(llm.Request{Model: "m", Effort: effort})
			if got.GenerationConfig == nil || got.GenerationConfig.ThinkingConfig == nil {
				t.Fatalf("effort %q did not set a thinking budget", effort)
			}
			if got.GenerationConfig.ThinkingConfig.ThinkingBudget != budget {
				t.Fatalf("budget = %d, want %d",
					got.GenerationConfig.ThinkingConfig.ThinkingBudget, budget)
			}
		})
	}
}

func TestBuildRequest_UnknownEffortIsAnError(t *testing.T) {
	// Gemini takes a budget, not a word, so an unmappable name can never reach
	// the API and the API can never report it. Surfacing it here is the only
	// way the user learns the setting was not applied.
	got := buildRequest(llm.Request{Model: "m", Effort: "ultra"})
	if got.buildErr == nil {
		t.Fatal("expected a build error for an unmappable effort")
	}
	for _, want := range []string{"ultra", "none, low, medium, high, xhigh, max"} {
		if !strings.Contains(got.buildErr.Error(), want) {
			t.Errorf("error %q does not mention %q", got.buildErr, want)
		}
	}
}

func TestBuildRequest_NumericBudgetIsAccepted(t *testing.T) {
	got := buildRequest(llm.Request{Model: "m", Effort: "0"})
	if got.buildErr != nil {
		t.Fatalf("numeric budget rejected: %v", got.buildErr)
	}
	if got.GenerationConfig == nil || got.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("numeric budget did not produce a thinking config")
	}
	if b := got.GenerationConfig.ThinkingConfig.ThinkingBudget; b != 0 {
		t.Fatalf("budget = %d, want 0", b)
	}
}

func TestBuildRequest_NegativeBudgetIsAnError(t *testing.T) {
	if got := buildRequest(llm.Request{Model: "m", Effort: "-5"}); got.buildErr == nil {
		t.Fatal("a negative budget should be rejected")
	}
}

func TestBuildRequest_EffortKeepsMaxTokens(t *testing.T) {
	got := buildRequest(llm.Request{Model: "m", MaxTokens: 4096, Effort: "low"})
	if got.GenerationConfig == nil {
		t.Fatal("generationConfig missing")
	}
	if got.GenerationConfig.MaxOutputTokens != 4096 {
		t.Fatalf("maxOutputTokens = %d, want 4096", got.GenerationConfig.MaxOutputTokens)
	}
}

// TestEffortNamesMatchesTheBudgetTable keeps the guidance and the mapping from
// drifting apart.
func TestEffortNamesMatchesTheBudgetTable(t *testing.T) {
	names := strings.Split(EffortNames, ", ")
	for _, name := range names {
		if _, ok := EffortBudgets[name]; !ok {
			t.Errorf("EffortNames lists %q but EffortBudgets has no entry", name)
		}
	}
	if len(names) != len(EffortBudgets) {
		t.Errorf("EffortNames lists %d names, EffortBudgets has %d", len(names), len(EffortBudgets))
	}
}

func TestEffortBudgetsRiseWithTheName(t *testing.T) {
	order := []string{"none", "low", "medium", "high", "xhigh", "max"}
	for i := 1; i < len(order); i++ {
		prev, cur := EffortBudgets[order[i-1]], EffortBudgets[order[i]]
		if cur <= prev {
			t.Errorf("%s budget %d is not above %s's %d", order[i], cur, order[i-1], prev)
		}
	}
}

// TestNoneSerializesAZeroBudget is the trap omitempty sets: a 0 with omitempty
// vanishes, and the API then falls back to dynamic thinking, which is the
// opposite of what "none" asked for.
func TestNoneSerializesAZeroBudget(t *testing.T) {
	apiReq := buildRequest(llm.Request{Model: "m", Effort: "none"})
	if apiReq.buildErr != nil {
		t.Fatalf("none rejected: %v", apiReq.buildErr)
	}
	body, err := json.Marshal(apiReq)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"thinkingBudget":0`) {
		t.Fatalf("none did not serialize a zero budget: %s", body)
	}
}
