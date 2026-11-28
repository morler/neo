package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

// stubProvider answers the summary call and records the exact request the
// production path would send, so the wire size is measured rather than
// estimated. No network.
type stubProvider struct{ calls []llm.Request }

func (s *stubProvider) Name() string { return "stub" }

func (s *stubProvider) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.calls = append(s.calls, req)
	return &llm.Response{
		Content:    []llm.ContentBlock{{Type: "text", Text: "summary"}},
		StopReason: "end_turn",
	}, nil
}

// wireBytes is the JSON body the chat-completions adapter would put on the wire,
// which is what the provider bills for.
func wireBytes(req llm.Request) int {
	body, err := json.Marshal(chatcompletions.BuildRequest(req, "m"))
	if err != nil {
		return -1
	}
	return len(body)
}

// summaryRequest runs the real Summarizer with a trigger of 1 so it always
// compacts, and returns the single request it made.
func summaryRequest(msgs []llm.Message) (llm.Request, error) {
	prov := &stubProvider{}
	// SafeSplitPoint(8, 5) == 2, so KeepRecent 3 splits at 5: messages 0..4
	// (including the large log at index 4) are summarized and 5..7 are kept
	// verbatim. Larger keeps push the split earlier and would leave the log in
	// the retained tail, so the preview would never be exercised.
	s := compact.Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 3}
	if _, err := s.Compact(context.Background(), msgs, 0); err != nil {
		return llm.Request{}, err
	}
	if len(prov.calls) != 1 {
		return llm.Request{}, fmt.Errorf("provider calls = %d, want 1", len(prov.calls))
	}
	return prov.calls[0], nil
}

// TestSummaryRequestCarriesTheLargeLogVerbatim establishes the control arm: the
// current code re-sends the whole tool result on the summary request.
func TestSummaryRequestCarriesTheLargeLogVerbatim(t *testing.T) {
	log := bigLog()
	req, err := summaryRequest(transcript(log))
	if err != nil {
		t.Fatal(err)
	}
	if !carries(req, log) {
		t.Fatal("control arm does not carry the full log, so the comparison would be meaningless")
	}
	t.Logf("verbatim summary request: %d bytes carrying a %d byte log", wireBytes(req), len(log))
}

// TestPreviewBudgetReducesTheSummaryRequest is the issue's primary figure: the
// summary request gets smaller while its shape stays valid.
func TestPreviewBudgetReducesTheSummaryRequest(t *testing.T) {
	in := transcript(bigLog())
	whole, err := summaryRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := summaryRequest(Preview(in, Options{Budget: DefaultPreview}))
	if err != nil {
		t.Fatal(err)
	}

	before, after := wireBytes(whole), wireBytes(bounded)
	if after >= before {
		t.Fatalf("bounded summary request is not smaller: %d >= %d", after, before)
	}
	t.Logf("summary request: %d -> %d bytes (-%.0f%%), budget %d bytes per result",
		before, after, 100*float64(before-after)/float64(before), DefaultPreview)

	if len(bounded.Messages) != len(whole.Messages) {
		t.Fatalf("message count changed: %d, want %d", len(bounded.Messages), len(whole.Messages))
	}
	for i := range whole.Messages {
		if bounded.Messages[i].Role != whole.Messages[i].Role {
			t.Fatalf("message %d role changed", i)
		}
		if len(bounded.Messages[i].Content) != len(whole.Messages[i].Content) {
			t.Fatalf("message %d block count changed", i)
		}
	}
	if err := validPairing(bounded.Messages); err != nil {
		t.Fatalf("bounded request broke pairing: %v", err)
	}
}

// TestRecentSegmentIsIdenticalToTheControl keeps the issue's requirement that
// the retained recent messages are untouched.
//
// The summary request only ever carries the summarized head, so the retained
// tail cannot be checked there. It is checked on the Compact result: everything
// after the split must be the original messages, byte for byte.
func TestRecentSegmentIsIdenticalToTheControl(t *testing.T) {
	in := transcript(bigLog())
	split := compact.SafeSplitPoint(in, len(in)-3)
	if split <= 0 {
		t.Fatalf("no safe split point for the fixture (split=%d)", split)
	}

	// The control arm: no preview at all.
	control := compactMessages(in, Options{})
	// The bounded arm: the summary request sees a preview.
	bounded := compactMessages(Preview(in, Options{Budget: DefaultPreview}), Options{})

	if len(control) != len(bounded) {
		t.Fatalf("compacted length changed: %d, want %d", len(bounded), len(control))
	}
	// control[0] is the summary message; control[1:] is the retained tail.
	if len(control) != len(in)-split+1 {
		t.Fatalf("compacted length = %d, want %d (split %d)", len(control), len(in)-split+1, split)
	}
	for i := 1; i < len(control); i++ {
		a, b := control[i], bounded[i]
		if a.Role != b.Role || len(a.Content) != len(b.Content) {
			t.Fatalf("retained message %d changed shape", i)
		}
		for j := range a.Content {
			if a.Content[j].Content != b.Content[j].Content || a.Content[j].Text != b.Content[j].Text {
				t.Fatalf("retained message %d block %d changed", i, j)
			}
		}
	}
	// And the retained tail must equal the original messages after the split.
	for i := 0; i < len(control)-1; i++ {
		orig := in[split+i]
		got := control[i+1]
		if len(orig.Content) != len(got.Content) {
			t.Fatalf("tail message %d block count changed", i)
		}
		for j := range orig.Content {
			if orig.Content[j].Content != got.Content[j].Content {
				t.Fatalf("tail message %d block %d differs from the original", i, j)
			}
		}
	}
}

// compactMessages runs Compact over msgs and returns the resulting messages.
func compactMessages(msgs []llm.Message, _ Options) []llm.Message {
	prov := &stubProvider{}
	s := compact.Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 3}
	res, err := s.Compact(context.Background(), msgs, 0)
	if err != nil {
		return nil
	}
	return res.Messages
}

// carries reports whether any tool_result in req still holds the whole log.
func carries(req llm.Request, log string) bool {
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.Content == log {
				return true
			}
		}
	}
	return false
}

// validPairing re-checks the invariant on a request rather than a transcript.
func validPairing(msgs []llm.Message) error {
	pending := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				if pending[b.ID] {
					return fmt.Errorf("duplicate tool_use id %q", b.ID)
				}
				pending[b.ID] = true
			case "tool_result":
				if !pending[b.ToolUseID] {
					return fmt.Errorf("tool_result %q has no earlier tool_use", b.ToolUseID)
				}
				delete(pending, b.ToolUseID)
			}
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("unanswered tool_use ids: %v", pending)
	}
	return nil
}
