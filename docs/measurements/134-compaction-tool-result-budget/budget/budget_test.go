package budget

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/owainlewis/neo/internal/llm"
)

func TestPreviewShrinksOnlyToolResults(t *testing.T) {
	in := Fixture()
	out := Preview(in, Options{Budget: 500})

	if len(out) != len(in) {
		t.Fatalf("message count changed: %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Role != in[i].Role {
			t.Fatalf("message %d role changed", i)
		}
		if len(out[i].Content) != len(in[i].Content) {
			t.Fatalf("message %d block count changed", i)
		}
		for j := range in[i].Content {
			if out[i].Content[j].Type != in[i].Content[j].Type {
				t.Fatalf("message %d block %d type changed", i, j)
			}
		}
	}
	if Size(out) >= Size(in) {
		t.Fatalf("preview did not shrink: %d >= %d", Size(out), Size(in))
	}
}

// TestPairingAndOrderSurvive is the issue's first acceptance criterion: the
// tool_use/tool_result pairing and the message order must stay valid.
func TestPairingAndOrderSurvive(t *testing.T) {
	in := Fixture()
	out := Preview(in, Options{Budget: 500})

	pending := map[string]bool{}
	for i, m := range out {
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				if pending[b.ID] {
					t.Fatalf("duplicate tool_use id %q at %d", b.ID, i)
				}
				pending[b.ID] = true
			case "tool_result":
				if !pending[b.ToolUseID] {
					t.Fatalf("tool_result %q at %d has no earlier tool_use", b.ToolUseID, i)
				}
				delete(pending, b.ToolUseID)
			}
		}
		if i == 0 && containsType(m, "tool_result") {
			t.Fatal("transcript leads with a tool_result")
		}
	}
	if len(pending) != 0 {
		t.Fatalf("unanswered tool_use ids: %v", pending)
	}
}

// TestSourceIsNotMutated keeps the requirement that the transcript itself is
// untouched: Preview works on a copy.
func TestSourceIsNotMutated(t *testing.T) {
	in := Fixture()
	before := Size(in)
	_ = Preview(in, Options{Budget: 500})
	if got := Size(in); got != before {
		t.Fatalf("source transcript changed: %d, want %d", got, before)
	}
}

func TestSmallResultsAreUntouched(t *testing.T) {
	// The budget must exceed the whole transcript for this to be a no-op; the
	// generated log is ~152 KB, so 1 MB is comfortable and 100 KB was not.
	in := Fixture()
	out := Preview(in, Options{Budget: 1_000_000})
	for i := range in {
		for j := range in[i].Content {
			if out[i].Content[j].Content != in[i].Content[j].Content {
				t.Fatalf("message %d block %d changed under a large budget", i, j)
			}
		}
	}
}

func TestZeroBudgetIsTheControlArm(t *testing.T) {
	in := Fixture()
	out := Preview(in, Options{})
	if Size(out) != Size(in) {
		t.Fatal("zero budget changed the transcript")
	}
}

// TestMarkerNamesTheDroppedBytes keeps the summarizer honest about what it did
// not see.
func TestMarkerNamesTheDroppedBytes(t *testing.T) {
	log := BigLog()
	out := Preview(Fixture(), Options{Budget: 400})
	got := out[4].Content[0].Content
	if !strings.Contains(got, "tool result truncated for the summary request") {
		t.Fatalf("no marker in preview: %q", got[:80])
	}
	if !strings.Contains(got, "bytes omitted") {
		t.Fatalf("marker does not count the dropped bytes: %q", got[:80])
	}
	if !utf8.ValidString(got) {
		t.Fatal("preview is not valid UTF-8")
	}
	if !strings.HasPrefix(got, log[:50]) {
		t.Fatal("preview lost the head of the log")
	}
	if !strings.HasSuffix(got, "exit status 1\n") {
		t.Fatalf("preview lost the tail: %q", got[len(got)-40:])
	}
}

func TestHeadAndTailSplitIsHonoured(t *testing.T) {
	log := strings.Repeat("H", 100) + strings.Repeat("T", 100)
	out := Preview(transcript(log), Options{Budget: 60, Head: 20, Tail: 10})
	got := out[4].Content[0].Content
	if !strings.HasPrefix(got, strings.Repeat("H", 20)) {
		t.Fatalf("head = %q", got[:25])
	}
	if !strings.HasSuffix(got, strings.Repeat("T", 10)) {
		t.Fatalf("tail = %q", got[len(got)-15:])
	}
}

func containsType(m llm.Message, typ string) bool {
	for _, b := range m.Content {
		if b.Type == typ {
			return true
		}
	}
	return false
}
