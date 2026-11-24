package compact

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// summarizeOnce compacts a transcript big enough to trip the trigger and
// returns what survived, so a test can drive the next decision against the
// state that compaction left behind.
func summarizeOnce(t *testing.T, s *Summarizer, msgs []llm.Message) []llm.Message {
	t.Helper()
	result, err := s.Compact(context.Background(), msgs, 0)
	if err != nil {
		t.Fatalf("first compaction: %v", err)
	}
	if len(result.Messages) >= len(msgs) {
		t.Fatalf("first compaction did not run: %d messages in, %d out", len(msgs), len(result.Messages))
	}
	if s.LastCompactedTokens <= 0 {
		t.Fatalf("LastCompactedTokens = %d after a successful compaction, want a positive size", s.LastCompactedTokens)
	}
	return result.Messages
}

// grow appends text turns until the transcript's estimate reaches target
// tokens, so a test can express "10% more work" instead of counting messages.
func grow(t *testing.T, msgs []llm.Message, target int) []llm.Message {
	t.Helper()
	filler := strings.Repeat("x", 400) // 100 estimated tokens
	for EstimateTokens(msgs) < target {
		msgs = append(msgs, userText(filler), assistantText(filler))
	}
	return msgs
}

// belowTriggerSummarizer builds a summarizer whose trigger turns(40) trips. The
// size kept after that compaction is small, so a test can grow the transcript
// by a known fraction of the triggering size and watch the floor decide.
func belowTriggerSummarizer(t *testing.T, prov llm.Provider) *Summarizer {
	t.Helper()
	s := NewSummarizer(prov, "m")
	s.TriggerTokens = 3_000 // turns(40) estimates ~4,000 tokens
	s.KeepRecent = 4
	return s
}

func TestSummarizer_GrowthFloorSkipsAnImmediateSecondCompaction(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first summary"),
		llmtest.Text("second summary"),
	}}
	s := belowTriggerSummarizer(t, prov)

	compacted := summarizeOnce(t, s, turns(40))
	// 10% growth is real work, but not enough to justify another summary. The
	// transcript must be past the trigger (otherwise the size check alone skips
	// it, which says nothing about the floor) but under trigger*growthFloor so
	// the floor is what stops it.
	near := append(compacted, userText("a small amount of new work"))
	// 10% of the size that triggered the last compaction: over the trigger,
	// under the floor.
	reported := int(float64(s.LastCompactedTokens) * 1.10)
	if float64(reported) >= float64(s.LastCompactedTokens)*growthFloor {
		t.Fatalf("test setup: reported size %d is already past the floor %.0f",
			reported, float64(s.LastCompactedTokens)*growthFloor)
	}

	result, err := s.Compact(context.Background(), near, reported)
	if err != nil {
		t.Fatalf("second compaction: %v", err)
	}
	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want 1: 10%% growth must not compact again", len(prov.Calls))
	}
	if len(result.Messages) != len(near) {
		t.Fatalf("messages changed under the growth floor: %d in, %d out", len(near), len(result.Messages))
	}
	if s.LastCompactedTokens <= 0 {
		t.Fatal("a skipped compaction must not clear the recorded size")
	}
}

func TestSummarizer_GrowthFloorAllowsCompactionAfterRealGrowth(t *testing.T) {
	summaryResponse := llmtest.Text("second summary")
	summaryResponse.Usage = llm.Usage{InputTokens: 11, OutputTokens: 12}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first summary"),
		summaryResponse,
	}}
	s := belowTriggerSummarizer(t, prov)

	compacted := summarizeOnce(t, s, turns(40))
	before := s.LastCompactedTokens
	grown := grow(t, compacted, int(float64(before)*1.30))

	result, err := s.Compact(context.Background(), grown, int(float64(before)*1.30))
	if err != nil {
		t.Fatalf("second compaction: %v", err)
	}
	if len(prov.Calls) != 2 {
		t.Fatalf("provider calls = %d, want 2: 30%% growth must compact again", len(prov.Calls))
	}
	if len(result.Messages) >= len(grown) {
		t.Fatalf("second compaction did not shrink the transcript: %d in, %d out",
			len(grown), len(result.Messages))
	}
	if result.Usage != summaryResponse.Usage {
		t.Fatalf("second compaction usage = %+v, want %+v", result.Usage, summaryResponse.Usage)
	}
}

// TestSummarizer_GrowthFloorRecordsTheTriggeringSize pins the scale the floor
// compares against: the size that caused this compaction, in the same units the
// next call will pass in (a reported prompt size, or the estimate before one
// exists). Mixing a whole-request figure with a messages-only estimate would
// make the floor meaningless.
func TestSummarizer_GrowthFloorRecordsTheTriggeringSize(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first summary"),
		llmtest.Text("second summary"),
	}}
	s := belowTriggerSummarizer(t, prov)

	msgs := turns(40)
	summarizeOnce(t, s, msgs)
	if want := EstimateTokens(msgs); s.LastCompactedTokens != want {
		t.Fatalf("LastCompactedTokens = %d, want the triggering estimate %d",
			s.LastCompactedTokens, want)
	}
	if s.LastCompactedTokens < s.TriggerTokens {
		t.Fatalf("a compaction ran at %d, below the trigger %d",
			s.LastCompactedTokens, s.TriggerTokens)
	}

	// A reported size replaces the estimate, so a compaction driven by one
	// records that figure.
	big := turns(80)
	if _, err := s.Compact(context.Background(), big, 9_000); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if s.LastCompactedTokens != 9_000 {
		t.Fatalf("LastCompactedTokens = %d, want the reported 9000", s.LastCompactedTokens)
	}
}

// TestSummarizer_GrowthFloorUsesTheReportedSize checks the provider-reported
// path, which is what a live session hits after its first response.
func TestSummarizer_GrowthFloorUsesTheReportedSize(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first summary"),
		llmtest.Text("second summary"),
	}}
	s := belowTriggerSummarizer(t, prov)

	compacted := summarizeOnce(t, s, turns(40))
	before := s.LastCompactedTokens

	// A reported size under the floor must not compact, even when it is over
	// the trigger.
	result, err := s.Compact(context.Background(), compacted, s.TriggerTokens+1)
	if err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if len(prov.Calls) != 1 || len(result.Messages) != len(compacted) {
		t.Fatalf("a reported size under the floor compacted again: calls=%d", len(prov.Calls))
	}

	// A reported size past the floor must compact, even when the message count
	// alone would have kept the second run below the trigger.
	if _, err := s.Compact(context.Background(), compacted, int(float64(before)*1.30)); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if len(prov.Calls) != 2 {
		t.Fatalf("provider calls = %d, want 2 after real growth", len(prov.Calls))
	}
}

// TestTriggerTokensScaleWithTheWindow is the other half of the issue: a large
// window must buy a proportionally larger trigger instead of the fixed 140k a
// 200k default produced.
func TestTriggerTokensScaleWithTheWindow(t *testing.T) {
	small := TriggerTokensForContextWindow(200_000)
	large := TriggerTokensForContextWindow(1_000_000)
	if large <= small {
		t.Fatalf("1M window trigger = %d, want more than the 200k trigger %d", large, small)
	}
	for _, window := range []int{200_000, 1_000_000} {
		want := int(float64(window) * triggerRatio)
		if got := TriggerTokensForContextWindow(window); got != want {
			t.Errorf("TriggerTokensForContextWindow(%d) = %d, want %d", window, got, want)
		}
	}
	// Unknown and non-positive windows keep the conservative default rather
	// than producing a zero trigger that compacts on every turn.
	for _, window := range []int{0, -1} {
		if got := TriggerTokensForContextWindow(window); got != small {
			t.Errorf("TriggerTokensForContextWindow(%d) = %d, want the default %d", window, got, small)
		}
	}
}

// TestGrowthFloorLeavesRoomForRealWork documents the constant: too small a
// floor re-fires immediately, too large a one lets a session reach the window.
func TestGrowthFloorLeavesRoomForRealWork(t *testing.T) {
	if growthFloor <= 1 {
		t.Fatalf("growthFloor = %v, want a value above 1 so compaction needs new work", growthFloor)
	}
	// A compaction near the trigger leaves roughly `trigger` worth of context;
	// 20% growth must still fit inside the window the trigger was derived from.
	trigger := TriggerTokensForContextWindow(DefaultContextWindowTokens)
	next := int(float64(trigger) * growthFloor)
	if next >= DefaultContextWindowTokens {
		t.Fatalf("the next compaction point %d reaches the %d-token window", next, DefaultContextWindowTokens)
	}
}
