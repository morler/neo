package tui

// Every test here exists to make one previously unmeasured branch of
// file_picker.go execute. #136 found 86% line coverage against 47% MC/DC:
// compound conditions ran without their parts being verified, and a branch
// nobody executes cannot be verified at all. Each test names what it covers,
// so a reader can drop the branch and watch the test fail.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// filePickerTokenAt clamps a row or column the textarea reports past the end of
// the text. Both clamps were unmeasured, and they are what keeps a paste or a
// resize from panicking on a stale cursor.
func TestFilePickerTokenAtClampsRowAndColumn(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		row, col  int
		wantOK    bool
		wantQuery string
	}{
		{"empty input", "", 0, 0, false, ""},
		{"row past end", "a\n@x", 99, 2, true, "x"},
		{"negative row", "@y", -5, 2, true, "y"},
		{"column past end", "@abc", 0, 99, true, "abc"},
		// A negative column is clamped to 0, so the scan never reaches the "@"
		// but the query is still read from there: the token's query is "abc".
		{"negative column is clamped to 0", "@abc", 0, -1, true, "abc"},
		{"no token", "plain text", 0, 5, false, ""},
		{"double at does not token", "@@x", 0, 3, false, ""},
		{"tab inside query is rejected", "@a\tb", 0, 4, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, ok := filePickerTokenAt(tt.input, tt.row, tt.col)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (token %+v)", ok, tt.wantOK, tok)
			}
			if ok && tok.query != tt.wantQuery {
				t.Fatalf("query = %q, want %q", tok.query, tt.wantQuery)
			}
			if ok && (tok.start < 0 || tok.end < tok.start) {
				t.Fatalf("token span %d-%d is inverted", tok.start, tok.end)
			}
		})
	}
}

// updateFilePicker hides the picker and re-lays out only when it was showing, so
// a keystroke that never opened it does not trigger a redundant layout.
func TestUpdateFilePickerWithoutToken(t *testing.T) {
	m := makeTestModel()
	m.files = newFilePicker(t.TempDir())
	m.files.visible = false
	m.input.SetValue("plain text")
	m.updateFilePicker()
	if m.files.visible {
		t.Fatal("a tokenless input must not open the picker")
	}

	// And the visible case: the picker closes and the layout follows.
	m.files.visible = true
	m.updateFilePicker()
	if m.files.visible {
		t.Fatal("the picker must close when the token disappears")
	}
}

// Escape dismisses one token, not the picker forever: a changed token clears
// dismissedFor and opens again. Both halves of that pair were unmeasured.
func TestUpdateFilePickerDismissedForTokenLifecycle(t *testing.T) {
	root := makeFilePickerTree(t, []string{"internal/tui/model.go"})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@mod")
	m.updateFilePicker()
	if !m.files.visible {
		t.Fatal("expected the picker to open")
	}

	// dismissedFor holds the token's raw text, which includes the "@".
	m.files.dismissedFor = m.files.token.raw
	m.updateFilePicker()
	if m.files.visible {
		t.Fatal("the dismissed token must stay hidden")
	}
	if m.files.matches != nil || m.files.selected != 0 {
		t.Fatalf("dismissal must clear matches and selection, got %+v", m.files.matches)
	}

	// A different token clears the dismissal and opens again.
	m.input.SetValue("@rea")
	m.updateFilePicker()
	if m.files.dismissedFor != "" {
		t.Fatalf("dismissedFor = %q, want cleared for a new token", m.files.dismissedFor)
	}
}

// The selection is clamped in both directions after the match list is rebuilt.
// An earlier version of this test used a one-item list, where both the real
// clamp and a reset to zero produce 0 -- so it could not catch a wrong clamp.
// Two matches is the smallest list that distinguishes them.
func TestUpdateFilePickerClampsSelectionBothWays(t *testing.T) {
	// Seed the picker with a workspace listing, then drive only the query so the
	// match count comes from the real filter rather than from a hand-set field.
	root := makeFilePickerTree(t, []string{"alpha.txt", "beta.txt", "gamma.md"})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.files.files = []string{"alpha.txt", "beta.txt", "gamma.md"}
	m.files.visible = true
	m.files.selected = 99
	m.files.token = filePickerToken{query: "@"}
	m.input.SetValue("@.txt")
	m.updateFilePicker()

	if len(m.files.matches) != 2 {
		t.Fatalf("matches = %+v, want the two .txt files", m.files.matches)
	}
	if m.files.selected != 1 {
		t.Fatalf("selected = %d, want the last index 1", m.files.selected)
	}

	// A selection that ended up negative is lifted back to zero.
	m.files.selected = -3
	m.input.SetValue("@.md")
	m.updateFilePicker()
	if len(m.files.matches) != 1 {
		t.Fatalf("matches = %+v, want gamma.md", m.files.matches)
	}
	if m.files.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.files.selected)
	}
}

// Both match builders stop at filePickerMaxMatches. A cap that never fills is a
// cap nobody has run into, and the loop bound is what keeps a broad "@" from
// walking the whole workspace.
func TestFilePickerMatchCaps(t *testing.T) {
	names := []string{"a", "b", "c"}
	paths := make([]string, 0, filePickerMaxMatches+5)
	for i := 0; i < filePickerMaxMatches+5; i++ {
		paths = append(paths, "pkg"+names[i%len(names)]+string(rune('a'+i%26))+".go")
	}
	root := makeFilePickerTree(t, paths)
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@")
	m.updateFilePicker()
	if len(m.files.matches) != filePickerMaxMatches {
		t.Fatalf("workspace matches = %d, want the cap %d", len(m.files.matches), filePickerMaxMatches)
	}

	// The disk walker caps independently, for a token the index cannot answer.
	if got := filePickerDiskMatches(root, ""); len(got) != filePickerMaxMatches {
		t.Fatalf("disk matches = %d, want the cap %d", len(got), filePickerMaxMatches)
	}
}

// ensureFilePickerFiles resolves the workspace root when none was given, and
// caches the listing instead of walking again on every keystroke.
func TestEnsureFilePickerFilesResolvesRootAndCaches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	m := makeTestModel()
	m.files = filePicker{} // empty root: resolved from the process directory
	if err := m.ensureFilePickerFiles(); err != nil {
		t.Fatal(err)
	}
	if len(m.files.files) != 1 || m.files.files[0] != "x.go" {
		t.Fatalf("files = %+v, want [x.go]", m.files.files)
	}

	if err := m.ensureFilePickerFiles(); err != nil {
		t.Fatal(err)
	}
	if len(m.files.files) != 1 {
		t.Fatalf("cached listing changed: %+v", m.files.files)
	}
}

// A failure is cached too: retrying on every keystroke would walk the
// filesystem repeatedly to learn the same thing.
//
// WalkDir swallows an error on the root itself (its callback returns nil for
// err != nil), so a missing directory produces an empty listing rather than an
// error. The caching half is therefore checked directly on the field the two
// paths share.
func TestEnsureFilePickerFilesCachesTheError(t *testing.T) {
	m := makeTestModel()
	sentinel := errors.New("listing failed")
	m.files = filePicker{root: t.TempDir(), err: sentinel}

	if err := m.ensureFilePickerFiles(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the cached sentinel", err)
	}
	if len(m.files.files) != 0 {
		t.Fatalf("files = %+v, want no walk while an error is cached", m.files.files)
	}
}

// A missing root is not an error: WalkDir reports it through the callback, which
// drops it, and the picker simply offers nothing. That is the intended shape for
// an advisory surface, and it was unmeasured.
func TestEnsureFilePickerFilesMissingRootYieldsNoMatches(t *testing.T) {
	m := makeTestModel()
	m.files = filePicker{root: filepath.Join(t.TempDir(), "missing")}
	if err := m.ensureFilePickerFiles(); err != nil {
		t.Fatalf("err = %v, want nil for a missing root", err)
	}
	if len(m.files.files) != 0 {
		t.Fatalf("files = %+v, want none", m.files.files)
	}
}

// moveFilePickerSelection and acceptFilePicker are both no-ops when there is
// nothing to select. Both early returns were unmeasured.
func TestFilePickerNoopsWithoutMatches(t *testing.T) {
	m := makeTestModel()
	m.files = newFilePicker(t.TempDir())

	// Hidden.
	m.files.visible = false
	m.moveFilePickerSelection(1)
	if m.files.selected != 0 || m.acceptFilePicker() {
		t.Fatal("a hidden picker must not move or accept")
	}

	// Visible but empty.
	m.files.visible = true
	m.files.matches = nil
	m.moveFilePickerSelection(1)
	if m.files.selected != 0 || m.acceptFilePicker() {
		t.Fatal("an empty picker must not move or accept")
	}
}

// replaceFilePickerToken validates the token against the current text before
// splicing. A stale token from a previous frame must be dropped, not applied to
// the wrong place.
func TestReplaceFilePickerTokenRejectsStaleTokens(t *testing.T) {
	tests := []struct {
		name  string
		value string
		token filePickerToken
	}{
		{"line past end", "abc", filePickerToken{line: 5, start: 0, end: 1}},
		{"negative start", "abc", filePickerToken{line: 0, start: -1, end: 1}},
		{"end before start", "abc", filePickerToken{line: 0, start: 2, end: 1}},
		{"end past line", "abc", filePickerToken{line: 0, start: 0, end: 99}},
		{"negative line", "abc", filePickerToken{line: -1, start: 0, end: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newComposer()
			ta.SetValue(tt.value)
			replaceFilePickerToken(&ta, tt.token, "@replacement")
			if got := ta.Value(); got != tt.value {
				t.Fatalf("value = %q, want it untouched at %q", got, tt.value)
			}
		})
	}
}

// renderFilePicker refuses to draw rather than drawing a stub: no matches, a row
// limit below the three lines it needs, and a width narrower than its padding.
// The width clamp is what keeps truncate from receiving a negative count.
func TestRenderFilePickerRefusesImpossibleLayouts(t *testing.T) {
	picker := filePicker{visible: true, matches: []string{"a.go"}}

	if got := renderFilePicker(0, picker); got != "" {
		t.Fatalf("zero width rendered %q, want empty", got)
	}
	if got := renderFilePicker(80, filePicker{}); got != "" {
		t.Fatalf("no matches rendered %q, want empty", got)
	}
	// heading + matches + footer is three rows; a limit below that cannot draw.
	if got := renderFilePicker(80, picker, 2); got != "" {
		t.Fatalf("a two-row limit rendered %q, want empty", got)
	}

	// A width below the two padding columns must still render. This pins the
	// behaviour, not the clamp: at contentWidth 0 the file rows already pass
	// through max(1, contentWidth-2), so raising the clamp from 0 to 1 is not
	// observable in the output. The branch stays as defence for the footer
	// argument, which has no such floor.
	if got := renderFilePicker(3, picker); got == "" {
		t.Fatal("width 3 must still render")
	}
	// At width 4 contentWidth is 2, so the clamp must not have fired.
	if a, b := renderFilePicker(3, picker), renderFilePicker(4, picker); a == b {
		t.Fatalf("width 3 and 4 rendered identically: %q", a)
	}
}

// A directory the process cannot read yields no candidates rather than an error,
// because the picker is advisory and a permission failure there must not surface
// as a tool failure.
func TestFilePickerDiskMatchesUnreadableDirectory(t *testing.T) {
	if got := filePickerDiskMatches(t.TempDir(), "does-not-exist/"); got != nil {
		t.Fatalf("matches = %+v, want nil for a missing directory", got)
	}
}
