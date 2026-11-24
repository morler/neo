package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/tools"
)

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{12_345, "12.3k"},
		{1_234_567, "1.2M"},
		// The promotion threshold is the rounded limit, so a count just under
		// a million never renders as "1000.0k".
		{999_949, "999.9k"},
		{999_950, "1.0M"},
		{1_000_000, "1.0M"},
		// A negative count cannot come from a provider, but it must not render
		// as "-1" tokens either.
		{-5, "0"},
	}
	for _, tt := range tests {
		if got := formatTokenCount(tt.in); got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCacheHitPercent(t *testing.T) {
	tests := []struct {
		name  string
		usage llm.Usage
		want  int
	}{
		{"no prompt", llm.Usage{}, 0},
		{"fully cached", llm.Usage{CacheReadTokens: 500}, 100},
		{"uncached prompt", llm.Usage{InputTokens: 400, CacheCreationTokens: 100}, 0},
		{"rounded down", llm.Usage{InputTokens: 2, CacheReadTokens: 1}, 33},
		{"rounded up", llm.Usage{InputTokens: 1, CacheReadTokens: 2}, 67},
		{"half rounds up", llm.Usage{InputTokens: 1, CacheReadTokens: 1}, 50},
	}
	for _, tt := range tests {
		if got := cacheHitPercent(tt.usage); got != tt.want {
			t.Errorf("%s: cacheHitPercent(%+v) = %d, want %d", tt.name, tt.usage, got, tt.want)
		}
	}
}

func TestUsageBlock_RendersEveryPart(t *testing.T) {
	u := llm.Usage{InputTokens: 2_140, CacheCreationTokens: 5_000, CacheReadTokens: 30_000, OutputTokens: 921}
	out := plain(usageBlock{usage: u}.render(80, nil))

	for _, want := range []string{"usage", "prompt", "37.1k", "81% cached", "input", "2.1k", "cache write", "5.0k", "cache read", "30.0k", "output", "921"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output missing %q:\n%s", want, out)
		}
	}
	if got := strings.TrimSpace(out); strings.Contains(got, "NaN") || strings.Contains(got, "inf") {
		t.Fatalf("usage output contains a non-finite value:\n%s", out)
	}
}

func TestUsageBlock_EmptySessionExplainsItself(t *testing.T) {
	out := plain(usageBlock{}.render(80, nil))
	if !strings.Contains(out, "no responses") {
		t.Fatalf("empty usage output = %q, want an explanation", out)
	}
	for _, unwanted := range []string{"NaN", "0% cached", "prompt"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("empty usage output should not contain %q:\n%s", unwanted, out)
		}
	}
}

// TestUsageBlock_NarrowWidthKeepsEveryValue checks the block degrades by
// stacking parts instead of truncating a token count.
func TestUsageBlock_NarrowWidthKeepsEveryValue(t *testing.T) {
	u := llm.Usage{InputTokens: 2_140, CacheCreationTokens: 5_000, CacheReadTokens: 30_000, OutputTokens: 921}
	out := plain(usageBlock{usage: u}.render(20, nil))

	for _, want := range []string{"37.1k", "81% cached", "2.1k", "5.0k", "30.0k", "921"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrow usage output missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %q has trailing space", line)
		}
	}
}

// TestUsageBlock_WideWidthFits asserts the common case stays inside the pane,
// so the block never forces the transcript to scroll sideways.
func TestUsageBlock_WideWidthFits(t *testing.T) {
	u := llm.Usage{InputTokens: 2_140, CacheCreationTokens: 5_000, CacheReadTokens: 30_000, OutputTokens: 921}
	out := plain(usageBlock{usage: u}.render(40, nil))

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := ansi.StringWidth(line); got > 40 {
			t.Errorf("line %q is %d cells wide, want <= 40", line, got)
		}
	}
}

func TestSlashCommand_UsageReadsAgentUsage(t *testing.T) {
	m := makeTestModel()
	want := llm.Usage{InputTokens: 10, CacheCreationTokens: 20, CacheReadTokens: 70, OutputTokens: 5}
	m.ag = agent.New(agent.Config{
		Model:    "test",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(tools.ReadFile{}),
		Usage:    want,
	})

	m.handleSlashCommand("/usage")

	if len(m.blocks) != 1 {
		t.Fatalf("/usage appended %d blocks, want 1", len(m.blocks))
	}
	ub, ok := m.blocks[0].(usageBlock)
	if !ok {
		t.Fatalf("/usage appended %T, want usageBlock", m.blocks[0])
	}
	if ub.usage != want {
		t.Fatalf("usage block = %+v, want %+v", ub.usage, want)
	}
}

func TestSlashCommand_UsageIsIdleOnly(t *testing.T) {
	m := makeTestModel()
	m.busy = true

	m.handleSlashCommand("/usage")

	if len(m.blocks) != 1 {
		t.Fatalf("/usage appended %d blocks, want the refusal only", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("busy /usage appended %T, want errorBlock", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "unavailable while a turn is running") {
		t.Fatalf("busy /usage error = %v", eb.err)
	}
}

// TestUsageAfterClear pins /usage to the same Usage() that resetConversation
// zeroes, so a cleared session cannot keep reporting the old totals.
func TestUsageAfterClear(t *testing.T) {
	m := makeTestModel()
	m.ag = agent.New(agent.Config{
		Model:    "test",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(tools.ReadFile{}),
		Usage:    llm.Usage{InputTokens: 100, CacheReadTokens: 900, OutputTokens: 50},
	})

	m.handleSlashCommand("/clear")
	m.handleSlashCommand("/usage")

	if len(m.blocks) != 1 {
		t.Fatalf("after /clear /usage appended %d blocks, want 1", len(m.blocks))
	}
	ub, ok := m.blocks[0].(usageBlock)
	if !ok {
		t.Fatalf("after /clear /usage appended %T, want usageBlock", m.blocks[0])
	}
	if ub.usage != (llm.Usage{}) {
		t.Fatalf("usage after /clear = %+v, want zero", ub.usage)
	}
}
