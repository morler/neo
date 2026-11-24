package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/llm"
)

// formatTokenCount abbreviates a token count for display. Counts below a
// thousand stay exact; larger ones keep one decimal and a k or M suffix. The
// promotion thresholds are the rounded limits, not the unit boundaries, so a
// count just under a million reads "999.9k" and never "1000.0k".
func formatTokenCount(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1_000:
		return strconv.Itoa(n)
	case n < 999_950:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// cacheHitPercent is the share of the prompt served from the cache. It rounds
// half up in integer arithmetic, so a fully cached prompt reports exactly 100%
// and an empty prompt can never divide by zero.
func cacheHitPercent(u llm.Usage) int {
	prompt := u.PromptTokens()
	if prompt <= 0 {
		return 0
	}
	return (u.CacheReadTokens*100 + prompt/2) / prompt
}

// usageBlock renders the session's token accounting from agent.Usage().
//
// The prompt is reported as one total with its three parts beneath it, because
// llm.Usage partitions it: InputTokens, CacheCreationTokens, and
// CacheReadTokens never overlap, and PromptTokens() is their sum. Output is a
// separate line rather than a prompt part. The cache hit rate divides by
// PromptTokens() for the same reason; any other denominator would count cache
// reads against the wrong total.
type usageBlock struct{ usage llm.Usage }

func (b usageBlock) render(width int, _ *glamour.TermRenderer) string {
	u := b.usage
	prompt := u.PromptTokens()
	if prompt == 0 {
		text := "no responses in this session yet · usage appears after the first reply"
		return accentCard(styMuted.Render(indentContinuation(
			strings.Join(wrapParts(strings.Fields(text), max(width-4, 1), " "), "\n"))), colMuted)
	}

	lines := []string{styAccent.Render("usage")}
	// The accent bar and indentation cost cells, so drop the indent when the
	// pane is narrow. A part that still does not fit keeps its own line rather
	// than being cut: a truncated token count is worse than a wide one.
	indent := "  "
	if width < 30 {
		indent = ""
	}
	groups := [][]string{
		{"prompt " + formatTokenCount(prompt), fmt.Sprintf("%d%% cached", cacheHitPercent(u))},
		{
			"input " + formatTokenCount(u.InputTokens),
			"cache write " + formatTokenCount(u.CacheCreationTokens),
			"cache read " + formatTokenCount(u.CacheReadTokens),
		},
		{"output " + formatTokenCount(u.OutputTokens)},
	}
	for _, parts := range groups {
		for _, line := range wrapParts(parts, max(width-2-ansi.StringWidth(indent), 1), " · ") {
			lines = append(lines, styMuted.Render(indent+line))
		}
	}
	return accentCard(strings.Join(lines, "\n"), colAccent)
}

// wrapParts packs parts onto lines of at most width cells, joined by sep and
// breaking before a part that does not fit. A part wider than width gets its
// own line rather than being cut, because a token count must stay readable.
// Callers pass a positive width.
func wrapParts(parts []string, width int, sep string) []string {
	var lines []string
	cur := ""
	for _, p := range parts {
		switch {
		case cur == "":
			cur = p
		case ansi.StringWidth(cur)+ansi.StringWidth(sep)+ansi.StringWidth(p) <= width:
			cur += sep + p
		default:
			lines = append(lines, cur)
			cur = p
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
