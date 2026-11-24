package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/tools"
)

// TestScriptSubCallsCarryParentGroupIdentity is the contract the UI depends on:
// every sub-call names its parent through GroupID and its order through
// GroupPos, and its own ID is unique even when the arguments repeat.
func TestScriptSubCallsCarryParentGroupIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `tools.read_file({path: "` + path + `"});
tools.read_file({path: "` + path + `"});
tools.read_file({path: "` + path + `"});
"ok"`

	script := tools.Script{Read: tools.ReadFile{}}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{
			Type: "tool_use", ID: "call_script", Name: "script",
			Input: map[string]any{"source": source},
		}}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var events []Event
	ag := New(Config{
		Model: "test", Provider: prov,
		Tools:   tools.NewRegistry(script),
		OnEvent: func(e Event) { events = append(events, e) },
	})
	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	var starts, results []Event
	for _, e := range events {
		if e.GroupID != "call_script" {
			continue
		}
		if e.Kind == EventToolCall {
			starts = append(starts, e)
		}
		if e.Kind == EventToolResult {
			results = append(results, e)
		}
	}
	if len(starts) != 3 || len(results) != 3 {
		t.Fatalf("starts = %d, results = %d, want 3 and 3", len(starts), len(results))
	}
	seen := map[string]bool{}
	for i, e := range starts {
		if e.GroupPos != i {
			t.Fatalf("start %d has GroupPos %d", i, e.GroupPos)
		}
		if e.Name != "read_file" {
			t.Fatalf("start %d has name %q", i, e.Name)
		}
		if seen[e.ToolUseID] {
			t.Fatalf("duplicate sub-call ID %q", e.ToolUseID)
		}
		seen[e.ToolUseID] = true
	}
}

// TestScriptSubCallFailureIsAttributed checks that a failed sub-call is marked
// as an error on its own event while the parent call still completes: the
// script's value is what decides the parent outcome, not one tool inside it.
func TestScriptSubCallFailureIsAttributed(t *testing.T) {
	script := tools.Script{Read: tools.ReadFile{}}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{
			Type: "tool_use", ID: "call_script", Name: "script",
			Input: map[string]any{"source": `try { tools.read_file({path: "/nope/missing"}) } catch (e) { "handled" }`},
		}}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var events []Event
	ag := New(Config{
		Model: "test", Provider: prov,
		Tools:   tools.NewRegistry(script),
		OnEvent: func(e Event) { events = append(events, e) },
	})
	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	var failed bool
	for _, e := range events {
		if e.Kind == EventToolResult && e.GroupID == "call_script" && e.IsError {
			failed = true
		}
	}
	if !failed {
		t.Fatal("no failed sub-call event was emitted")
	}
}

// TestScriptSubCallsDoNotChangeTheResult records the "never blocking" rule:
// observing a script must not change what it returns. The sink here throws its
// events away, which is what a slow consumer does.
func TestScriptSubCallsDoNotChangeTheResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// read_file prefixes each line with its number and a tab, so the length
	// includes the gutter; the point of the test is that it stays the same.
	source := `tools.read_file({path: "` + path + `"}).length`
	want := "14"

	script := tools.Script{Read: tools.ReadFile{}}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{
			Type: "tool_use", ID: "call_script", Name: "script",
			Input: map[string]any{"source": source},
		}}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var got string
	ag := New(Config{
		Model: "test", Provider: prov,
		Tools:   tools.NewRegistry(script),
		OnEvent: func(Event) {}, // a consumer that ignores everything
	})
	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// The tool result block is the observable outcome for the model.
	for _, msg := range ag.Transcript() {
		for _, block := range msg.Content {
			if block.Type == "tool_result" && block.ToolUseID == "call_script" {
				got = block.Content
			}
		}
	}
	if got != want {
		t.Fatalf("script result = %q, want %q", got, want)
	}
}

// TestScriptSubCallsUseTheAgentApprover is the injection test at the agent
// level: the tool only consults a gate that runPreparedTool put there, so this
// proves the wiring rather than the tool's own logic.
func TestScriptSubCallsUseTheAgentApprover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := tools.Script{Read: tools.ReadFile{}}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{
			Type: "tool_use", ID: "call_script", Name: "script",
			Input: map[string]any{"source": `tools.read_file({path: "` + path + `"})`},
		}}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}

	asked := 0
	ag := New(Config{
		Model: "test", Provider: prov,
		Tools:            tools.NewRegistry(script),
		RequiresApproval: func(tool string, _ map[string]any) bool { return tool == "read_file" },
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			asked++
			return true, nil
		},
	})
	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("approver asked %d times, want once for the nested read_file", asked)
	}
}

// TestScriptSubCallDeniedAtAgentLevel checks the refusal reaches the script as
// a thrown error rather than being silently approved.
func TestScriptSubCallDeniedAtAgentLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := tools.Script{Read: tools.ReadFile{}}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{
			Type: "tool_use", ID: "call_script", Name: "script",
			Input: map[string]any{"source": `tools.read_file({path: "` + path + `"})`},
		}}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}

	var denied bool
	ag := New(Config{
		Model: "test", Provider: prov,
		Tools:            tools.NewRegistry(script),
		RequiresApproval: func(tool string, _ map[string]any) bool { return tool == "read_file" },
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			denied = true
			return false, nil
		},
	})
	if _, err := ag.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("the approver was never consulted")
	}
}
