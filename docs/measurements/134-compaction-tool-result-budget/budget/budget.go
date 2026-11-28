// Package budget is the experiment for #134: a bounded tool-result preview for
// the *summary request only*.
//
// Compact hands the provider the old segment verbatim, so a 260 KB log is
// re-sent once more on the very request that exists to make the transcript
// smaller. This package measures what happens if the summary request receives a
// head+tail preview instead, while the transcript itself is never touched.
//
// It is deliberately not wired into internal/compact. #134 asks whether the
// budget is worth its complexity, and that answer has to come from a
// measurement rather than from the change being convenient to make.
package budget

import (
	"github.com/owainlewis/neo/internal/llm"
)

// DefaultPreview is the per-result byte budget. The head keeps the beginning of
// a log (the command, the first error), the tail keeps the end (the exit status,
// the summary line), and the marker in between says how much was dropped so the
// summarizer can say "a large log was read" instead of inventing its contents.
const DefaultPreview = 2_000

// Options controls which parts of a tool result survive.
type Options struct {
	// Budget is the maximum bytes of text kept per tool result. Zero keeps
	// everything, which is the current behaviour and the control arm.
	Budget int
	// Head and Tail split the budget. Head is kept from the start, Tail from the
	// end. Head defaults to two thirds of Budget.
	Head, Tail int
}

// Preview rewrites the tool results inside old, returning a new slice. Every
// message, role, block type, and tool_use ID is preserved: only the text of a
// tool_result block larger than the budget shrinks. The caller's slice is never
// modified, because the transcript must stay intact.
func Preview(old []llm.Message, opts Options) []llm.Message {
	if opts.Budget <= 0 {
		return old
	}
	head, tail := opts.Head, opts.Tail
	if head <= 0 && tail <= 0 {
		head = opts.Budget * 2 / 3
		tail = opts.Budget - head
	}
	out := make([]llm.Message, len(old))
	for i, m := range old {
		blocks := make([]llm.ContentBlock, len(m.Content))
		copy(blocks, m.Content)
		for j, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			blocks[j].Content = shrink(b.Content, head, tail)
		}
		out[i] = llm.Message{Role: m.Role, Content: blocks, DisplayText: m.DisplayText}
	}
	return out
}

// shrink keeps the first head and last tail bytes of s, with a marker naming the
// dropped count. A string within the budget is returned unchanged, byte for
// byte, so a small result costs nothing new.
func shrink(s string, head, tail int) string {
	limit := head + tail
	if limit <= 0 || len(s) <= limit {
		return s
	}
	// head and tail count bytes from each end. Clamp so the two windows cannot
	// overlap when the caller asks for more than the string holds.
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	start, end := head, len(s)-tail
	if start > len(s) {
		start = len(s)
	}
	if end < start {
		end = start
	}
	// Cut on rune boundaries so the preview is valid UTF-8.
	start = runeFloor(s, start)
	end = runeCeil(s, end)
	if end < start {
		end = start
	}
	return s[:start] + marker(end-start) + s[end:]
}

func marker(n int) string {
	return "\n... [tool result truncated for the summary request: " + itoa(n) + " bytes omitted] ...\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func runeFloor(s string, i int) int {
	for i > 0 && i < len(s) && !utf8Start(s[i]) {
		i--
	}
	return i
}

func runeCeil(s string, i int) int {
	for i > 0 && i < len(s) && !utf8Start(s[i]) {
		i++
	}
	return i
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
