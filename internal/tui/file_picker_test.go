package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestFilePicker_ShowsWorkspacePathSubstringMatches(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
		"internal/tools/search.go",
		"README.md",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@mod")
	m.updateFilePicker()

	if !m.files.visible {
		t.Fatal("expected file picker to be visible")
	}
	if len(m.files.matches) != 1 || m.files.matches[0] != "internal/tui/model.go" {
		t.Fatalf("expected model.go match, got %+v", m.files.matches)
	}
}

func TestFilePicker_SkipsHeavyDirectories(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
		".git/config",
		"node_modules/pkg/index.js",
		"dist/app.js",
		"vendor/pkg/file.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@")
	m.updateFilePicker()

	got := strings.Join(m.files.matches, "\n")
	for _, skipped := range []string{".git/config", "node_modules/pkg/index.js", "dist/app.js", "vendor/pkg/file.go"} {
		if strings.Contains(got, skipped) {
			t.Fatalf("expected %s to be skipped, got %q", skipped, got)
		}
	}
	if !strings.Contains(got, "internal/tui/model.go") {
		t.Fatalf("expected regular file match, got %q", got)
	}
}

func TestFilePicker_ArrowKeysCycleSelection(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"a/one.go",
		"b/two.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@go")
	m.updateFilePicker()

	m.Update(keyPress(tea.KeyUp))
	if got := m.files.matches[m.files.selected]; got != "b/two.go" {
		t.Fatalf("expected up to wrap to b/two.go, got %s", got)
	}
	m.Update(keyPress(tea.KeyDown))
	if got := m.files.matches[m.files.selected]; got != "a/one.go" {
		t.Fatalf("expected down to wrap to a/one.go, got %s", got)
	}
}

func TestFilePicker_TabAcceptsHighlightedPath(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"a/one.go",
		"b/two.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("read @go")
	m.updateFilePicker()
	m.Update(keyPress(tea.KeyDown))
	m.Update(keyPress(tea.KeyTab))

	if got := m.input.Value(); got != "read @b/two.go" {
		t.Fatalf("expected selected path in input, got %q", got)
	}
	if m.files.visible {
		t.Fatal("expected picker to hide after accepting a path")
	}
}

func TestFilePicker_EnterAcceptsPathWithoutSubmitting(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@mod")
	m.updateFilePicker()
	m.Update(keyPress(tea.KeyEnter))

	if got := m.input.Value(); got != "@internal/tui/model.go" {
		t.Fatalf("expected selected path in input, got %q", got)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("expected input not to submit, got %d blocks", len(m.blocks))
	}
}

func TestFilePicker_EscapeDismissesUntilTokenChanges(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"internal/tui/model.go",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)
	m.input.SetValue("@mod")
	m.updateFilePicker()

	m.Update(keyPress(tea.KeyEsc))
	if m.files.visible {
		t.Fatal("expected picker to hide after escape")
	}
	m.updateFilePicker()
	if m.files.visible {
		t.Fatal("expected picker to stay hidden for dismissed token")
	}
	m.input.SetValue("@mode")
	m.updateFilePicker()
	if !m.files.visible {
		t.Fatal("expected picker to reappear when token changes")
	}
}

func TestFilePicker_DoesNotOpenForEmailLikeText(t *testing.T) {
	root := makeFilePickerTree(t, []string{
		"example.com",
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	for _, input := range []string{"owain@example.com", "mail owain@example"} {
		m.input.SetValue(input)
		m.updateFilePicker()
		if m.files.visible {
			t.Fatalf("expected picker to stay hidden for %q", input)
		}
	}
}

func TestFilePicker_RenderFitsNarrowWidth(t *testing.T) {
	width := 16
	picker := filePicker{
		visible: true,
		matches: []string{
			"internal/tui/very-long-file-name.go",
			"README.md",
		},
	}
	out := plain(renderFilePicker(width, picker))
	for _, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line exceeds width %d: got %d line %q\n%s", width, got, line, out)
		}
	}
}

// TestFilePicker_MultiHopParentCompletesFromDisk covers the complaint: "@../../"
// used to match nothing because the index only holds paths relative to the
// workspace root. A path-shaped token is now completed from disk, one segment at
// a time, so each "/" descends another level.
func TestFilePicker_MultiHopParentCompletesFromDisk(t *testing.T) {
	root, parent := makeParentedFilePickerTree(t, map[string][]string{
		"docs": {"guide.md"},
	})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@../")
	m.updateFilePicker()
	if !m.files.visible {
		t.Fatal("expected the parent directory listing to open")
	}
	if !containsString(m.files.matches, "../docs/") {
		t.Fatalf("expected the sibling directory of the workspace, got %v in %s", m.files.matches, parent)
	}
	if !containsString(m.files.matches, "../work/") {
		t.Fatalf("expected the workspace beside its sibling, got %v", m.files.matches)
	}

	m.input.SetValue("@../../")
	m.updateFilePicker()
	if !m.files.visible {
		t.Fatal("expected a two-hop listing to open")
	}

	m.input.SetValue("@../docs/")
	m.updateFilePicker()
	if len(m.files.matches) != 1 || m.files.matches[0] != "../docs/guide.md" {
		t.Fatalf("expected ../docs/guide.md, got %v", m.files.matches)
	}
}

func TestFilePicker_TabAcceptsAParentPath(t *testing.T) {
	root, _ := makeParentedFilePickerTree(t, map[string][]string{"docs": nil})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("read @../doc")
	m.updateFilePicker()
	m.Update(keyPress(tea.KeyTab))

	if got := m.input.Value(); got != "read @../docs/" {
		t.Fatalf("expected the parent path in input, got %q", got)
	}
	if m.files.visible {
		t.Fatalf("expected the picker to hide after accepting, matches %v", m.files.matches)
	}
}

// TestFilePicker_TypingDotsListsTheirDirectory pins the ".." case: appending a
// name to dots would name a sibling of the wrong directory, so the dots list the
// directory they name instead of acting as a parent to descend into.
func TestFilePicker_TypingDotsListsTheirDirectory(t *testing.T) {
	root, parent := makeParentedFilePickerTree(t, map[string][]string{"docs": nil})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@..")
	m.updateFilePicker()

	if !containsString(m.files.matches, "../work/") {
		t.Fatalf("expected the workspace to be listed under its own name, got %v in %s", m.files.matches, parent)
	}
}

// TestFilePicker_AbsoluteQueryListsThatDirectory keeps an absolute token usable
// when the file sits outside the workspace.
func TestFilePicker_AbsoluteQueryListsThatDirectory(t *testing.T) {
	root := makeFilePickerTree(t, []string{"README.md"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@" + outside + "/")
	m.updateFilePicker()

	if len(m.files.matches) != 1 || m.files.matches[0] != outside+"/outside.md" {
		t.Fatalf("expected the absolute listing, got %v", m.files.matches)
	}
}

// TestFilePicker_WorkspaceMatchesStaySuffixIndependent guards the indexed
// branch: a workspace-relative query must still match anywhere in the path.
func TestFilePicker_WorkspaceMatchesStaySuffixIndependent(t *testing.T) {
	root := makeFilePickerTree(t, []string{"internal/tui/model.go"})
	m := makeTestModel()
	m.files = newFilePicker(root)

	m.input.SetValue("@tui/mod")
	m.updateFilePicker()

	if len(m.files.matches) != 1 || m.files.matches[0] != "internal/tui/model.go" {
		t.Fatalf("expected the workspace match, got %v", m.files.matches)
	}
}

// makeParentedFilePickerTree builds <base>/work plus <base> children, so a test
// can walk one ("..") or two ("../..") hops away from the workspace root.
func makeParentedFilePickerTree(t *testing.T, parentFiles map[string][]string) (root, parent string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "work")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, files := range parentFiles {
		dir = filepath.Join(parent, dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root, parent
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func makeFilePickerTree(t *testing.T, paths []string) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range paths {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
