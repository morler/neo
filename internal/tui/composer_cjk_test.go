package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestComposerRendersEveryCharacterAfterInLineEdit pins the defect reported as
// "a character disappears when I edit Chinese text": bubbles v2.1.0's textarea
// indexed the wrapped line with an offset derived from a different wrap, so a
// row containing wide runes could be cut short, and an in-line edit could shift
// the cursor past the end of the rune slice and panic outright.
//
// The bug is not Chinese-specific — a plain ASCII line panics the same way — but
// wide runes fill a row with fewer characters, so they reach it far sooner,
// which is why it reads as a Chinese problem. The guard is character-level on
// purpose: a count of rendered columns would pass while a rune was dropped.
func TestComposerRendersEveryCharacterAfterInLineEdit(t *testing.T) {
	const cjk = "中文测试内容一二三四五"
	cases := []struct {
		name  string
		width int
		text  string
		lefts int
	}{
		{name: "cjk wide line", width: 78, text: cjk, lefts: 4},
		{name: "cjk narrow line", width: 12, text: cjk, lefts: 4},
		{name: "cjk one step left", width: 12, text: cjk, lefts: 1},
		{name: "cjk unwrapped paragraph", width: 20, text: "这是一段没有任何空格的很长的中文内容用来测试横向截断行为", lefts: 6},
		{name: "ascii", width: 40, text: "abcdefghijklmnop", lefts: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A panic here is the defect, so report it as a failure rather than
			// letting it take the process down and hide the other cases.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("composer panicked on an in-line edit: %v", r)
				}
			}()

			ta := newComposer()
			ta.SetWidth(tc.width)
			ta.SetValue(tc.text)
			ta.Focus()
			ta.CursorEnd()
			for i := 0; i < tc.lefts; i++ {
				ta.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			}
			ta.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

			value := ta.Value()
			if value == tc.text {
				t.Fatalf("backspace changed nothing; the case does not exercise deletion")
			}
			rendered := plain(ta.View())
			for _, r := range value {
				if r == ' ' {
					continue
				}
				if !strings.ContainsRune(rendered, r) {
					t.Fatalf("character %q is in the value but missing from the render: %q", r, rendered)
				}
			}
		})
	}
}

// TestComposerKeepsWideRunesWholeOnDelete checks the piece a column count would
// miss: deleting next to a wide rune must not leave half a character behind or
// drop its neighbour. Each surviving rune has to appear exactly once per line,
// in order.
func TestComposerKeepsWideRunesWholeOnDelete(t *testing.T) {
	ta := newComposer()
	ta.SetWidth(14)
	ta.SetValue("中文")
	ta.Focus()
	ta.CursorEnd()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("composer panicked: %v", r)
		}
	}()
	ta.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

	if got := ta.Value(); got != "中" {
		t.Fatalf("value after one backspace = %q, want %q", got, "中")
	}
	rendered := plain(ta.View())
	if !strings.ContainsRune(rendered, '中') {
		t.Fatalf("surviving rune missing from render: %q", rendered)
	}
	if strings.ContainsRune(rendered, '文') {
		t.Fatalf("deleted rune still rendered: %q", rendered)
	}
}

// TestBlocksStayWithinTheirWidth guards the width contract each block owes the
// viewport. Without soft wrap an over-wide row is silently clipped, so a block
// that miscalculates its own width loses content and shows nothing wrong in a
// screenshot: the line simply looks short. thinkingBlock did exactly that by
// appending a two-space indent to lines that wrap had already padded.
func TestBlocksStayWithinTheirWidth(t *testing.T) {
	const run = "这是一段没有任何空格的很长的中文内容用来测试横向截断行为"
	blocks := map[string]block{
		"text":     textBlock{text: run},
		"result":   toolResultBlock{name: "bash", text: run},
		"thinking": thinkingBlock{text: run},
		"user":     userBlock{text: run},
	}
	for _, width := range []int{20, 40, 80} {
		for name, b := range blocks {
			for _, line := range strings.Split(b.render(width, nil), "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("width %d: %s renders %d columns: %q", width, name, got, line)
				}
			}
		}
	}
}
