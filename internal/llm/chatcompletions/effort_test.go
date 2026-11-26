package chatcompletions

import (
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func TestBuildRequest_EffortUnsetOmitsReasoningEffort(t *testing.T) {
	got := BuildRequest(llm.Request{Model: "m"}, "m")
	if got.ReasoningEffort != "" {
		t.Fatalf("unset effort must leave reasoning_effort empty, got %q", got.ReasoningEffort)
	}
}

func TestBuildRequest_EffortIsPassedThrough(t *testing.T) {
	got := BuildRequest(llm.Request{Model: "m", Effort: " Medium "}, "m")
	if got.ReasoningEffort != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", got.ReasoningEffort)
	}
}

func TestBuildRequest_UnknownEffortStillReachesTheServer(t *testing.T) {
	// Gateways disagree on the accepted set; the server's error is the honest
	// answer, so the value must not be dropped here.
	got := BuildRequest(llm.Request{Model: "m", Effort: "minimal"}, "m")
	if got.ReasoningEffort != "minimal" {
		t.Fatalf("reasoning_effort = %q, want minimal passed through", got.ReasoningEffort)
	}
}
