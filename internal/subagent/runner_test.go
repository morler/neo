package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

func TestRegistryHasCodingToolsWithoutNestedAgent(t *testing.T) {
	r := &AgentRunner{Root: t.TempDir()}
	names := r.registryWithOptions(t.TempDir(), RunOptions{Tools: dynamicAgentTools}).Names()
	for _, want := range dynamicAgentTools {
		if !slices.Contains(names, want) {
			t.Fatalf("registry %v missing %s", names, want)
		}
	}
	if slices.Contains(names, "agent") {
		t.Fatalf("subagents must not receive nested delegation: %v", names)
	}
}

func TestInspectRegistryHasOnlyReadTools(t *testing.T) {
	r := &AgentRunner{Root: t.TempDir()}
	names := r.registryWithOptions(t.TempDir(), RunOptions{Tools: inspectAgentTools}).Names()
	if !slices.Equal(names, []string{"glob", "grep", "read_file"}) {
		t.Fatalf("inspect registry=%v", names)
	}
}

func TestRunAgentReportsUsageAndUsesFixedPrompt(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content:    []llm.ContentBlock{{Type: "text", Text: "VERDICT: PASS"}},
		StopReason: "end_turn",
		Usage:      llm.Usage{InputTokens: 1_000_000, OutputTokens: 100_000},
	}}}
	r := &AgentRunner{Provider: prov, DefaultModel: "model", Root: t.TempDir()}
	events := make(chan AgentEvent, 16)
	out, err := r.RunAgentWithOptions(context.Background(), t.TempDir(), "PR #1", events, RunOptions{Tools: dynamicAgentTools})
	if err != nil {
		t.Fatal(err)
	}
	if out != "VERDICT: PASS" || prov.Calls[0].System != dynamicAgentSystemPrompt || prov.Calls[0].Model != "model" {
		t.Fatalf("out=%q request=%+v", out, prov.Calls[0])
	}
	close(events)
	var kinds []string
	var usage string
	for ev := range events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == "usage" {
			usage = ev.Body
		}
	}
	if !slices.Contains(kinds, "text") || usage != "tokens in=1000000 out=100000 cached=0" {
		t.Fatalf("kinds=%v usage=%q", kinds, usage)
	}
}

func TestAgentRunnerSetBackendAppliesToFutureWorkers(t *testing.T) {
	oldProvider := &llmtest.FakeProvider{}
	newProvider := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("new backend")}}
	r := &AgentRunner{Provider: oldProvider, DefaultModel: "old-model", Root: t.TempDir()}
	if err := r.SetBackend(newProvider, "new-model"); err != nil {
		t.Fatal(err)
	}
	out, err := r.RunAgentWithOptions(context.Background(), t.TempDir(), "task", make(chan AgentEvent, 16), RunOptions{Tools: dynamicAgentTools})
	if err != nil {
		t.Fatal(err)
	}
	if out != "new backend" || len(oldProvider.Calls) != 0 || newProvider.Calls[0].Model != "new-model" {
		t.Fatalf("out=%q old=%d new=%+v", out, len(oldProvider.Calls), newProvider.Calls)
	}
}

func TestAgentRunnerCompactorUsesConfiguredContextWindow(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	r := &AgentRunner{
		Provider:            prov,
		DefaultModel:        "small-model",
		ContextWindowTokens: 100,
	}

	compactor, ok := r.compactor(prov, "small-model").(*compact.Summarizer)
	if !ok {
		t.Fatalf("compactor = %T, want compact.Summarizer", r.compactor(prov, "small-model"))
	}
	if compactor.TriggerTokens != 70 {
		t.Fatalf("trigger tokens = %d, want 70", compactor.TriggerTokens)
	}
}

type toolLoopProvider struct {
	mainCalls      int
	summaryCalls   int
	summaryRequest llm.Request
}

func (*toolLoopProvider) Name() string { return "tool-loop" }

func (p *toolLoopProvider) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	if len(req.Tools) == 0 {
		p.summaryCalls++
		p.summaryRequest = req
		resp := llmtest.Text("compacted child history")
		return &resp, nil
	}
	p.mainCalls++
	if p.mainCalls <= 11 {
		payload := strings.Repeat("x", 400)
		resp := llmtest.ToolUse(
			fmt.Sprintf("tool-%d", p.mainCalls),
			"bash",
			map[string]any{"command": "printf '" + payload + "'"},
		)
		return &resp, nil
	}
	resp := llmtest.Text("child complete")
	return &resp, nil
}

func TestRunAgentCompactsToolHistoryWithConfiguredWindowAfterBackendSwitch(t *testing.T) {
	oldProvider := &llmtest.FakeProvider{}
	newProvider := &toolLoopProvider{}
	r := &AgentRunner{
		Provider:            oldProvider,
		DefaultModel:        "old-model",
		ContextWindowTokens: 100,
		Root:                t.TempDir(),
	}
	if err := r.SetBackend(newProvider, "small-model"); err != nil {
		t.Fatal(err)
	}

	out, err := r.RunAgentWithOptions(
		context.Background(),
		t.TempDir(),
		"exercise a tool-heavy child",
		make(chan AgentEvent, 64),
		RunOptions{Tools: dynamicAgentTools},
	)
	if err != nil {
		t.Fatal(err)
	}
	if out != "child complete" {
		t.Fatalf("output = %q, want child complete", out)
	}
	if len(oldProvider.Calls) != 0 {
		t.Fatalf("old provider calls = %d, want 0", len(oldProvider.Calls))
	}
	if newProvider.summaryCalls != 1 || newProvider.mainCalls != 12 {
		t.Fatalf("provider calls: summary=%d main=%d", newProvider.summaryCalls, newProvider.mainCalls)
	}
	req := newProvider.summaryRequest
	if req.Model != "small-model" {
		t.Fatalf("summary model = %q, want small-model", req.Model)
	}
	if len(req.Messages) != 3 || req.Messages[2].Content[0].Type != "tool_result" {
		t.Fatalf("summary request does not contain one completed tool exchange: %+v", req.Messages)
	}
}

func TestTranslateStatusLines(t *testing.T) {
	ev, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "bash", Args: map[string]any{"command": "just test"}})
	if !ok || ev.Body != "$ just test" {
		t.Fatalf("bash status=%+v", ev)
	}
	if _, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "agent"}); ok {
		t.Fatal("agent call should not produce a status event")
	}
}

// TestScriptToolFollowsTheRunnerSetting covers both directions of the switch.
// A child's tool list is built per run, so a setting that only reached the
// coordinator would leave children silently unable to use the tool.
func TestScriptToolFollowsTheRunnerSetting(t *testing.T) {
	dir := t.TempDir()
	off := NewSupervisor(&AgentRunner{Root: dir}, DefaultBudget())
	if names := off.childRegistry(dir).Names(); slices.Contains(names, "script") {
		t.Fatalf("script registered while disabled: %v", names)
	}
	on := NewSupervisor(&AgentRunner{Root: dir, Script: true}, DefaultBudget())
	names := on.childRegistry(dir).Names()
	if !slices.Contains(names, "script") {
		t.Fatalf("script missing while enabled: %v", names)
	}
}

// TestInspectModeNeverGetsScript pins the deliberate exclusion. The inspect
// whitelist is read-only by construction, and a script reaches write_file
// through the same globals, so enabling the tool must not widen that mode.
func TestInspectModeNeverGetsScript(t *testing.T) {
	dir := t.TempDir()
	r := &AgentRunner{Root: dir, Script: true}
	sup := NewSupervisor(r, DefaultBudget())
	names := r.registryWithOptions(dir, RunOptions{Tools: sup.toolsFor(AgentModeInspect)}).Names()
	if slices.Contains(names, "script") {
		t.Fatalf("inspect registry has script: %v", names)
	}
	if !slices.Equal(names, []string{"glob", "grep", "read_file"}) {
		t.Fatalf("inspect registry=%v", names)
	}
}

// TestScriptSharesReadStateWithTheChildsFileTools is the reason the file tools
// are built once and handed to the script tool rather than built twice: a read
// inside a child's script must count as the child having seen the file, or the
// stale-edit guard protects nothing across the script boundary.
func TestScriptSharesReadStateWithTheChildsFileTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &AgentRunner{Root: dir, Script: true}
	sup := NewSupervisor(r, DefaultBudget())
	reg := sup.childRegistry(dir)

	scripted, ok := reg.Get("script")
	if !ok {
		t.Fatal("script not registered")
	}
	// Read through the script, then change the file behind the agent's back.
	if _, err := scripted.Run(context.Background(), map[string]any{
		"source": `tools.read_file({path: "` + path + `"})`,
	}); err != nil {
		t.Fatalf("scripted read: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	editor, ok := reg.Get("edit_file")
	if !ok {
		t.Fatal("edit_file not registered")
	}
	_, err := editor.Run(context.Background(), map[string]any{
		"path": path, "old_string": "alpha", "new_string": "ALPHA",
	})
	if err == nil || !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("edit error = %v, want the stale-read refusal", err)
	}
}

// TestBashHintFollowsTheChildsScriptTool keeps the child's bash description in
// step with the child's own tool list. A child that can script but reads the
// plain bash description is the same blind decision point #87 fixed for the
// coordinator, one level down.
func TestBashHintFollowsTheChildsScriptTool(t *testing.T) {
	dir := t.TempDir()
	plain := NewSupervisor(&AgentRunner{Root: dir}, DefaultBudget()).childRegistry(dir)
	scripted := NewSupervisor(&AgentRunner{Root: dir, Script: true}, DefaultBudget()).childRegistry(dir)

	off, ok := plain.Get("bash")
	if !ok {
		t.Fatal("child registry has no bash")
	}
	on, ok := scripted.Get("bash")
	if !ok {
		t.Fatal("scripted child registry has no bash")
	}
	if off.Spec().Description == on.Spec().Description {
		t.Fatal("the hint did not reach a child that can script")
	}
	if !slices.Contains(scripted.Names(), "script") {
		t.Fatalf("hinted child has no script tool: %v", scripted.Names())
	}
}
