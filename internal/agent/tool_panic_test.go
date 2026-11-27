package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/tools"
)

// panicTool is the defect these tests contain: a tool implementation that
// panics instead of returning an error. The value is the marker the assertions
// look for, so a changed wording in the result still shows which panic landed.
type panicTool struct{}

func (panicTool) Name() string { return "panicking" }
func (panicTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "panicking", InputSchema: map[string]any{"type": "object"}}
}
func (panicTool) ParallelSafe(map[string]any) bool { return true }
func (panicTool) Run(context.Context, map[string]any) (string, error) {
	panic("probe panic value")
}

func enableDebugLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neo.log")
	t.Setenv("NEO_LOG", path)
	t.Setenv("NEO_LOG_VERBOSE", "")
	if err := logx.InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	t.Cleanup(func() { _ = logx.Close() })
	return path
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	if err := logx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(b)
}

func lastToolResults(t *testing.T, prov *llmtest.FakeProvider) []llm.ContentBlock {
	t.Helper()
	if len(prov.Calls) == 0 {
		t.Fatal("provider was never called")
	}
	last := prov.Calls[len(prov.Calls)-1]
	if len(last.Messages) == 0 {
		t.Fatal("last request carries no messages")
	}
	var results []llm.ContentBlock
	for _, block := range last.Messages[len(last.Messages)-1].Content {
		if block.Type == "tool_result" {
			results = append(results, block)
		}
	}
	return results
}

// A panic in a serial tool call must end that call, not the turn: the next
// request still happens, and the result tells the model this was Neo's fault.
func TestAgent_ToolPanicBecomesAnErrorResult(t *testing.T) {
	logPath := enableDebugLog(t)
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "panicking", nil),
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, panicTool{})
	out, err := ag.Send(context.Background(), "run it")
	if err != nil {
		t.Fatalf("Send returned %v; one panicking call must not end the turn", err)
	}
	if out != "done" {
		t.Fatalf("final text = %q, want the turn to continue to the next response", out)
	}
	results := lastToolResults(t, prov)
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("tool results = %#v, want one error result", results)
	}
	text := results[0].Content
	for _, want := range []string{"probe panic value", "internal Neo error"} {
		if !strings.Contains(text, want) {
			t.Fatalf("result %q lacks %q", text, want)
		}
	}
	// The stack belongs in the log. In the transcript it would spend context
	// and show local paths the model cannot use.
	if strings.Contains(text, "goroutine ") || strings.Contains(text, "runToolSafely") {
		t.Fatalf("result carries a stack trace:\n%s", text)
	}
	log := readLogFile(t, logPath)
	for _, want := range []string{"\"msg\":\"tool panic\"", "\"name\":\"panicking\"", "\"stack\":\""} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
}

// The parallel path runs each call in its own goroutine, so a panic there has
// nothing above it to recover. Only a separate process can show the parent
// survives one.
func TestAgent_ParallelToolPanicKeepsTheProcessAlive(t *testing.T) {
	if os.Getenv("NEO_PANIC_CHILD") != "" {
		t.Skip("child mode is driven by the parent test")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAgent_ParallelToolPanicChild$", "-test.v")
	cmd.Env = append(os.Environ(), "NEO_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child process failed (%v), so the panicking goroutine ended it:\n%s", err, out)
	}
	if !strings.Contains(string(out), "child ok") {
		t.Fatalf("child did not reach its own assertions:\n%s", out)
	}
}

func TestAgent_ParallelToolPanicChild(t *testing.T) {
	if os.Getenv("NEO_PANIC_CHILD") == "" {
		t.Skip("run by TestAgent_ParallelToolPanicKeepsTheProcessAlive")
	}
	logPath := enableDebugLog(t)
	healthy := fastParallelTool{}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: panicTool{}.Name()},
			{Type: "tool_use", ID: "call_b", Name: healthy.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	groups, calls := 0, 0
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(panicTool{}, healthy), OnEvent: func(e Event) {
		switch e.Kind {
		case EventParallelStart:
			groups++
		case EventToolCall:
			calls++
		}
	}})
	out, err := ag.Send(context.Background(), "run both")
	if err != nil {
		t.Fatalf("Send returned %v; the panic must stay inside one call", err)
	}
	if groups != 1 || calls != 2 {
		t.Fatalf("groups = %d, calls = %d, want one parallel group of two", groups, calls)
	}
	results := lastToolResults(t, prov)
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two", results)
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "internal Neo error") {
		t.Fatalf("panicking result = %#v, want an internal-error result", results[0])
	}
	if !strings.Contains(results[0].Content, "probe panic value") {
		t.Fatalf("panicking result %q lacks the panic value", results[0].Content)
	}
	if results[1].IsError || results[1].Content != "ok" {
		t.Fatalf("sibling result = %#v, want the healthy call to complete", results[1])
	}
	if out != "done" {
		t.Fatalf("final text = %q, want the turn to continue", out)
	}
	if log := readLogFile(t, logPath); !strings.Contains(log, "\"msg\":\"tool panic\"") {
		t.Fatalf("log lacks the panic record:\n%s", log)
	}
	t.Log("child ok")
}
