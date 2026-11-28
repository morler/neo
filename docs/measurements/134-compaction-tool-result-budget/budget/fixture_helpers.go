package budget

import (
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// The fixture is shared by the tests and the probe command, so what a test
// measures and what a real run summarizes cannot drift apart.

func toolUse(id, name string) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool_use", ID: id, Name: name, Input: map[string]any{"path": "x"}}
}

func toolResult(id, text string) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool_result", ToolUseID: id, Content: text}
}

func text(s string) llm.ContentBlock { return llm.ContentBlock{Type: "text", Text: s} }

// Fixture is the shape the issue asks for: a user constraint, a large log, a
// parallel tool result, an open question, and a recent unanswered input.
//
// It is eight messages so the safe split lands at 5, which summarizes the large
// log at index 4 while keeping the last three messages verbatim.
func Fixture() []llm.Message { return transcript(BigLog()) }

func transcript(log string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{text("run the suite")}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{text("checking both packages")}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{text("also check the auth package; do not change the retry budget")}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			text("I'll run both."),
			toolUse("a", "run"), toolUse("b", "run"),
		}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			toolResult("a", log), toolResult("b", "ok  internal/compact 0.02s"),
		}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{toolUse("c", "read_file")}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{toolResult("c", "package compact")}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{text("why did the auth test fail?")}},
	}
}

// BigLog is the 4,000-line log the fixture carries: one failure at line 2871,
// a retry warning at 2872, a non-zero exit at the end.
func BigLog() string { return bigLog() }

func bigLog() string {
	var b strings.Builder
	for i := 1; i <= 4000; i++ {
		switch i {
		case 2871:
			b.WriteString("ERROR handler failed: dial tcp 10.4.2.9:6379: connect: connection refused\n")
		case 2872:
			b.WriteString("WARN  retrying after handler failure\n")
		default:
			b.WriteString("INFO request completed status=200\n")
		}
	}
	b.WriteString("exit status 1\n")
	return b.String()
}

// FirstText returns the summary message Compact produced, or "".
func FirstText(msgs []llm.Message) string {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && len(b.Text) > 0 {
				return b.Text
			}
		}
	}
	return ""
}

// Size is the text size of a transcript, the unit the summary budget is in.
func Size(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			n += len(b.Text) + len(b.Content) + len(b.Raw)
		}
	}
	return n
}
