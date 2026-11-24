package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/tools"
)

// countTool counts executions so a test can tell "ran eleven times" from "ran
// twelve times" without inspecting the transcript.
type countTool struct {
	mu   sync.Mutex
	runs int
}

func (t *countTool) Name() string { return "count_tool" }
func (t *countTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "count_tool", InputSchema: map[string]any{"type": "object"}}
}
func (t *countTool) Run(context.Context, map[string]any) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs++
	return fmt.Sprintf("run %d", t.runs), nil
}
func (t *countTool) runsSoFar() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs
}

// repeatResponse scripts n identical calls in one response, then a final text
// response, mirroring a model that reissues the same call inside a turn.
func repeatResponse(name string, n int) *llmtest.FakeProvider {
	content := make([]llm.ContentBlock, n)
	for i := range content {
		content[i] = llm.ContentBlock{Type: "tool_use", ID: fmt.Sprintf("call_%d", i), Name: name}
	}
	return &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: content, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
}

// remindersUpTo reports how many reminder thresholds a run of n identical calls
// reaches, which is how many notes the transcript should carry.
func remindersUpTo(n int) int {
	count := 0
	for threshold := range repeatNoticeAt {
		if n >= threshold {
			count++
		}
	}
	return count
}

// reminderCount counts the reminders in the transcript a turn produced.
func reminderCount(t *testing.T, msgs []llm.Message) int {
	t.Helper()
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "<system-reminder>") {
				n++
			}
		}
	}
	return n
}

// TestRepeatedToolCallRemindsAtThreeFiveEight checks each threshold earns
// exactly one reminder and the call still runs. A longer run earns the earlier
// reminders too, so the count is the number of thresholds reached.
func TestRepeatedToolCallRemindsAtThreeFiveEight(t *testing.T) {
	for _, count := range []int{3, 5, 8} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			tool := &countTool{}
			prov := repeatResponse(tool.Name(), count)
			ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool)})
			var events []Event
			ag.SetEventHandler(func(e Event) { events = append(events, e) })

			if _, err := ag.Send(context.Background(), "go"); err != nil {
				t.Fatalf("%d repeats: %v", count, err)
			}
			if got := tool.runsSoFar(); got != count {
				t.Fatalf("tool ran %d times, want %d: a reminder must not block the call", got, count)
			}
			want := remindersUpTo(count)
			if got := reminderCount(t, ag.Transcript()); got != want {
				t.Fatalf("reminders = %d, want %d at %d repeats", got, want, count)
			}
			if want == 0 {
				t.Fatalf("%d repeats should earn a reminder", count)
			}
			for _, e := range events {
				if e.Kind == EventError {
					t.Fatalf("unexpected error event at %d repeats: %v", count, e.Err)
				}
			}
		})
	}
}

// TestRepeatedToolCallStopsAtTwelve checks the stop threshold refuses the call
// before it runs, ends the turn with a readable error, and reports an event.
func TestRepeatedToolCallStopsAtTwelve(t *testing.T) {
	// The number is written out, not read from the constant: a test that follows
	// the constant would pass whatever it became, and this threshold is a product
	// decision rather than an implementation detail.
	const wantStopAt = 12
	if repeatStopAt != wantStopAt {
		t.Fatalf("repeatStopAt = %d, want %d", repeatStopAt, wantStopAt)
	}
	tool := &countTool{}
	prov := repeatResponse(tool.Name(), wantStopAt)
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool)})
	var gotErr error
	ag.SetEventHandler(func(e Event) {
		if e.Kind == EventError {
			gotErr = e.Err
		}
	})

	_, err := ag.Send(context.Background(), "go")
	if err == nil {
		t.Fatal("a repeated call at the stop threshold should end the turn with an error")
	}
	if !errors.Is(err, ErrRepeatedToolCall) {
		t.Fatalf("error = %v, want ErrRepeatedToolCall", err)
	}
	if want := wantStopAt - 1; tool.runsSoFar() != want {
		t.Fatalf("tool ran %d times, want %d: the last call must be refused before running", tool.runsSoFar(), want)
	}
	if gotErr == nil {
		t.Fatal("no error event reached the handler")
	}
	for _, want := range []string{tool.Name(), fmt.Sprint(wantStopAt)} {
		if !strings.Contains(gotErr.Error(), want) {
			t.Errorf("error %q should name %q", gotErr, want)
		}
	}
	// The refusal is visible in the transcript as a failed tool result, so the
	// model (and the saved session) can see why the turn ended.
	var refused bool
	for _, m := range ag.Transcript() {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.IsError && strings.Contains(b.Content, "identical arguments") {
				refused = true
			}
		}
	}
	if !refused {
		t.Fatal("the refused call should appear as a failed tool_result in the transcript")
	}
}

// TestRepeatedToolCallKeepsRunningAtEleven pins the boundary: one below the
// stop threshold still runs and still earns the reminder.
func TestRepeatedToolCallKeepsRunningAtEleven(t *testing.T) {
	tool := &countTool{}
	prov := repeatResponse(tool.Name(), 11)
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool)})

	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatalf("11 repeats: %v", err)
	}
	if got := tool.runsSoFar(); got != 11 {
		t.Fatalf("tool ran %d times, want 11", got)
	}
	if got, want := reminderCount(t, ag.Transcript()), 3; got != want {
		t.Fatalf("reminders = %d, want %d at 11 repeats", got, want)
	}
}

// TestRepeatReminderLeavesThePrefixByteIdentical is rule 1 of the token budget:
// a reminder says something new at the end of the conversation, never in the
// system prompt or the tool definitions, so the cached prefix never moves.
func TestRepeatReminderLeavesThePrefixByteIdentical(t *testing.T) {
	tool := &countTool{}
	reg := tools.NewRegistry(tool)
	system := "static system prompt"
	prov := repeatResponse(tool.Name(), 5)
	ag := New(Config{
		Model:        "test",
		Provider:     prov,
		Tools:        reg,
		System:       system,
		SystemBlocks: []llm.SystemBlock{{Text: system, Cache: true}},
	})

	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(prov.Calls) < 2 {
		t.Fatalf("provider calls = %d, want the follow-up request", len(prov.Calls))
	}
	first, last := prov.Calls[0], prov.Calls[len(prov.Calls)-1]
	if first.System != last.System || first.System != system {
		t.Fatal("the system prompt changed after a reminder")
	}
	firstTools, err := json.Marshal(first.Tools)
	if err != nil {
		t.Fatal(err)
	}
	lastTools, err := json.Marshal(last.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstTools) != string(lastTools) {
		t.Fatal("the tool definitions changed after a reminder")
	}
	if len(last.SystemBlocks) != 1 || last.SystemBlocks[0].Text != system || !last.SystemBlocks[0].Cache {
		t.Fatalf("system blocks changed after a reminder: %#v", last.SystemBlocks)
	}
	// The reminder must be in the newest message, not spliced into the past.
	if got, want := reminderCount(t, ag.Transcript()), remindersUpTo(5); got != want {
		t.Fatalf("reminders = %d, want %d", got, want)
	}
	// The reminder rides on the tool-result message it belongs to, which is the
	// newest content the provider saw before it produced the final answer.
	msgs := ag.Transcript()
	reminderIndex := -1
	for i, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "system-reminder") {
				reminderIndex = i
			}
		}
	}
	if reminderIndex < 0 {
		t.Fatal("no reminder in the transcript")
	}
	if reminderIndex != len(msgs)-2 {
		t.Fatalf("reminder is at message %d of %d; it should be the last content sent before the answer",
			reminderIndex, len(msgs)-1)
	}
}

// TestRepeatCountsResetPerUserTurn checks the guard does not leak across turns:
// two identical calls in one turn and two in the next are not a loop.
func TestRepeatCountsResetPerUserTurn(t *testing.T) {
	tool := &countTool{}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "a1", Name: tool.Name()},
			{Type: "tool_use", ID: "a2", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("first done"),
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "b1", Name: tool.Name()},
			{Type: "tool_use", ID: "b2", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("second done"),
	}}
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool)})

	for i := 0; i < 2; i++ {
		if _, err := ag.Send(context.Background(), "go"); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
	}
	if got := reminderCount(t, ag.Transcript()); got != 0 {
		t.Fatalf("reminders = %d, want none: each turn only made two identical calls", got)
	}
	if got := tool.runsSoFar(); got != 4 {
		t.Fatalf("tool ran %d times, want 4", got)
	}
}

// TestDifferentArgumentsAreNotARepeat checks the key includes the arguments, so
// alternating between different inputs never trips the guard.
func TestDifferentArgumentsAreNotARepeat(t *testing.T) {
	tool := &countTool{}
	var content []llm.ContentBlock
	for i := 0; i < 12; i++ {
		content = append(content, llm.ContentBlock{
			Type:  "tool_use",
			ID:    fmt.Sprintf("call_%d", i),
			Name:  tool.Name(),
			Input: map[string]any{"path": fmt.Sprintf("file_%d.txt", i)},
		})
	}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: content, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool)})

	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatalf("distinct arguments should not be treated as repeats: %v", err)
	}
	if got := tool.runsSoFar(); got != 12 {
		t.Fatalf("tool ran %d times, want 12", got)
	}
	if got := reminderCount(t, ag.Transcript()); got != 0 {
		t.Fatalf("reminders = %d, want none", got)
	}
}

// TestRunToolIsNotCountedAsARepeat keeps a user-invoked tool (the TUI's "!cmd"
// and skill paths) outside the guard: it is not a model loop, and counting it
// would make a repeated direct call fail for no reason.
func TestRunToolIsNotCountedAsARepeat(t *testing.T) {
	tool := &countTool{}
	ag := New(Config{
		Model:    "test",
		Provider: &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}},
		Tools:    tools.NewRegistry(tool),
	})
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	// One more than the stop threshold, after a turn already ran.
	for i := 0; i < 13; i++ {
		out, isErr := ag.RunTool(context.Background(), tool.Name(), nil)
		if isErr {
			t.Fatalf("RunTool call %d was refused: %s", i+1, out)
		}
	}
}

// TestRepeatKeyIsStableAndArgumentSensitive covers the key itself: stable for
// the same input, different for different input, and independent of map order.
func TestRepeatKeyIsStableAndArgumentSensitive(t *testing.T) {
	a := map[string]any{"path": "x", "offset": 1}
	b := map[string]any{"offset": 1, "path": "x"}
	if repeatKey("read_file", a) != repeatKey("read_file", b) {
		t.Fatal("the repeat key should not depend on argument order")
	}
	if repeatKey("read_file", a) == repeatKey("read_file", map[string]any{"path": "y"}) {
		t.Fatal("different arguments must produce different keys")
	}
	if repeatKey("read_file", a) == repeatKey("write_file", a) {
		t.Fatal("different tools must produce different keys")
	}
	if got := repeatKey("bash", map[string]any{"f": func() {}}); got == "" {
		t.Fatal("an unmarshalable input should still produce a key")
	}
}
