package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(SearchBinary); err != nil {
		t.Skipf("%s is not installed", SearchBinary)
	}
}

// searchWorkspace builds a small git repo with an ignored directory, so tests
// can assert that discovery honours .gitignore.
func searchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "build/\n")
	write("main.go", "package main\n\nfunc needle() {}\n")
	write("pkg/helper.go", "package pkg\n\n// needle lives here too\n")
	write("build/generated.go", "package build\n\nfunc needle() {}\n")
	return root
}

func TestGrep_FindsMatchesWithLineNumbers(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{"main.go:3:", "pkg/helper.go:3:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGrep_HonoursGitignore(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if strings.Contains(out, "build/generated.go") {
		t.Fatalf("ignored directory was searched:\n%s", out)
	}
}

func TestGrep_NoMatchesIsNotAnError(t *testing.T) {
	requireRipgrep(t)

	out, err := Grep{Root: searchWorkspace(t)}.Run(context.Background(), map[string]any{"pattern": "haystack"})
	if err != nil {
		t.Fatalf("a pattern that matches nothing is a result, not a failure: %v", err)
	}
	if out != "no matches" {
		t.Fatalf("out = %q, want %q", out, "no matches")
	}
}

func TestGrep_InvalidPatternIsAnError(t *testing.T) {
	requireRipgrep(t)

	if _, err := (Grep{Root: searchWorkspace(t)}).Run(context.Background(), map[string]any{"pattern": "("}); err == nil {
		t.Fatal("expected an error for an unparseable pattern")
	}
}

func TestGrep_ContextLines(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{
		"pattern":       "needle",
		"path":          "main.go",
		"context_lines": 2,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "package main") {
		t.Fatalf("context lines were not returned:\n%s", out)
	}
}

func TestGrep_TruncatesAtMaxMatches(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	var body strings.Builder
	for i := range 50 {
		fmt.Fprintf(&body, "needle %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle", "max_matches": 5})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if got := strings.Count(out, "many.txt:"); got != 5 {
		t.Fatalf("returned %d matches, want 5:\n%s", got, out)
	}
	// No total: reporting one would mean reading every match, which is exactly
	// what the early stop avoids.
	if !strings.Contains(out, "showing the first 5 matching lines") {
		t.Fatalf("truncation was not reported:\n%s", out)
	}
}

func TestGlob_MatchesRecursivelyAndHonoursGitignore(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, want := range []string{"main.go", "pkg/helper.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "build/generated.go") {
		t.Fatalf("ignored directory was listed:\n%s", out)
	}
}

// TestGlob_AbsolutePathsAreUsable pins the shape the default code-mode surface
// uses: every line must be a real, absolute file path that the next tool call
// can open. A doubly-joined path ("a.txta.txt") made tools.read_file fail with
// a misleading "no such file" error.
func TestGlob_AbsolutePathsAreUsable(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Glob{Root: root, Absolute: true}.Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("glob returned no lines")
	}
	for _, line := range lines {
		if !filepath.IsAbs(line) {
			t.Fatalf("line %q is not absolute", line)
		}
		if _, err := os.Stat(line); err != nil {
			t.Fatalf("line %q does not name a file: %v", line, err)
		}
		if strings.HasSuffix(line, "main.gomain.go") || strings.HasSuffix(line, "helper.gohelper.go") {
			t.Fatalf("path was joined twice: %q", line)
		}
	}
}

// TestGrep_AbsoluteKeepsLabels checks that the glob fix left grep's
// "path:line:text" shape intact, including match text that contains colons.
func TestGrep_AbsoluteKeepsLabels(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	path := filepath.Join(root, "colon.go")
	if err := os.WriteFile(path, []byte("package p\n// note: needle: here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root, Absolute: true}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	want := path + ":2:// note: needle: here"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

// TestSearch_RelativeOutputUnchanged guards the Absolute: false surfaces: the
// classic tool face and the subagent inspect face must keep ripgrep's own
// relative output byte for byte.
func TestSearch_RelativeOutputUnchanged(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	globOut, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, line := range strings.Split(globOut, "\n") {
		if filepath.IsAbs(line) {
			t.Fatalf("relative glob returned an absolute path: %q", line)
		}
	}
	grepOut, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(grepOut, "main.go:3:") {
		t.Fatalf("relative grep shape changed:\n%s", grepOut)
	}
}

// TestGlob_NeverListsGitInternals pins the ordering of ripgrep's --glob flags.
// A later --glob overrides an earlier one, so passing "!.git/" before the user's
// pattern let a broad pattern replace it: glob("*") returned .git/HEAD and the
// object files. The patterns below are the ones measured to leak, plus the
// narrow ones that never did.
func TestGlob_NeverListsGitInternals(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go")
	write("pkg/helper.go")
	// A real checkout has these: a match here means the model can read the
	// object store, including the contents of committed files.
	write(".git/HEAD")
	write(".git/config")
	write(".git/objects/ab/cdef")
	write(".git/hooks/sample.go")

	for _, pattern := range []string{
		"*", "**", "**/*", "*.*", "**/**", "{.git,*}",
		"*.go", "**/*.go", ".git/*",
	} {
		out, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": pattern})
		if err != nil {
			t.Fatalf("pattern %q: %v", pattern, err)
		}
		for _, line := range strings.Split(out, "\n") {
			if line == "" || strings.HasPrefix(line, "[") {
				continue
			}
			if line == ".git" || strings.HasPrefix(line, ".git/") {
				t.Fatalf("pattern %q listed git internals: %q\nfull output:\n%s", pattern, line, out)
			}
		}
	}
}

// TestGlob_BroadPatternsStillListProjectFiles keeps the fix from over-reaching:
// excluding .git must not narrow what a broad pattern returns.
func TestGlob_BroadPatternsStillListProjectFiles(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": "*"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, want := range []string{"main.go", ".gitignore", filepath.Join(".github", "workflows", "ci.yml")} {
		if !strings.Contains(out, want) {
			t.Fatalf("pattern %q dropped %q, which is an ordinary project file:\n%s", "*", want, out)
		}
	}
	// build/ does appear here, and that is ripgrep's own rule rather than a
	// regression: a --glob overrides .gitignore, and it did so before this fix
	// too. The narrow pattern that .gitignore still governs is asserted below.
	if !strings.Contains(out, "build/") {
		t.Logf("note: ripgrep no longer lets --glob override .gitignore; revisit the comment above")
	}

	narrow, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if strings.Contains(narrow, "build/") {
		t.Fatalf("**/*.go listed the ignored build/ directory:\n%s", narrow)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	requireRipgrep(t)

	out, err := Glob{Root: searchWorkspace(t)}.Run(context.Background(), map[string]any{"pattern": "**/*.rs"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if out != "no matches" {
		t.Fatalf("out = %q, want %q", out, "no matches")
	}
}

func TestSearch_CancelledContext(t *testing.T) {
	requireRipgrep(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (Grep{Root: searchWorkspace(t)}).Run(ctx, map[string]any{"pattern": "needle"}); err == nil {
		t.Fatal("expected a cancelled search to fail")
	}
}

func TestSearch_MissingPattern(t *testing.T) {
	for _, tool := range []Tool{Grep{}, Glob{}} {
		if _, err := tool.Run(context.Background(), map[string]any{}); err == nil {
			t.Fatalf("%s: expected an error for a missing pattern", tool.Name())
		}
	}
}

func TestSearchToolsAreParallelSafe(t *testing.T) {
	registry := NewRegistry(Grep{}, Glob{}, Bash{})
	for _, name := range []string{"grep", "glob"} {
		if !registry.ParallelSafe(name, nil) {
			t.Fatalf("%s must stay parallel-safe; it is why these are not folded into bash", name)
		}
	}
	if registry.ParallelSafe("bash", nil) {
		t.Fatal("bash must remain serial")
	}
}

func TestSearch_IncludesHiddenFilesButNotGitInternals(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("needle: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real checkout has a .git directory full of files nobody wants matched.
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "objects", "blob"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, filepath.Join(".github", "workflows", "ci.yml")) {
		t.Fatalf("dotfiles are ordinary project files and must be searched:\n%s", out)
	}
	if strings.Contains(out, ".git/objects") {
		t.Fatalf("git internals must stay out:\n%s", out)
	}
}

// TestGrep_LongResultLineIsAnError covers a silent false negative. ripgrep
// prints a match whatever its length, but bufio.Scanner stops permanently at
// MaxOutputBytes, so before the fix everything after an over-long line was
// dropped and the survivors were reported as a complete result: "no matches"
// when the long line was the only match. A minified bundle or a lock file
// produces exactly this shape.
func TestGrep_LongResultLineIsAnError(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	long := "needle" + strings.Repeat("x", MaxOutputBytes)
	body := "needle before\n" + long + "\nneedle after\n"
	if err := os.WriteFile(filepath.Join(root, "bundle.min.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err == nil {
		t.Fatalf("a line over the scanner limit must be an error, not a partial result:\n%.200s", out)
	}
	for _, want := range []string{"grep:", "limit", "narrow the pattern"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// TestReadLines_ReportsATooLongLine pins the reader on its own, so the branch
// is covered on a machine without ripgrep too.
func TestReadLines_ReportsATooLongLine(t *testing.T) {
	input := "short line before\n" + strings.Repeat("x", MaxOutputBytes+1) + "\nshort line after\n"
	_, _, err := readLines(strings.NewReader(input), defaultSearchMax)
	if err == nil {
		t.Fatal("a line longer than the buffer must be an error")
	}
	if !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("error does not name the limit: %v", err)
	}

	// A long line past the requested limit is not an error: those lines were
	// going to be discarded, and the caller already has what it asked for.
	input = "first\n" + strings.Repeat("x", MaxOutputBytes+1) + "\n"
	lines, total, err := readLines(strings.NewReader(input), 1)
	if err != nil {
		t.Fatalf("a long line beyond the limit must not fail the read: %v", err)
	}
	if total != 1 || len(lines) != 1 {
		t.Fatalf("readLines = %d lines of %d seen, want 1 of 1", len(lines), total)
	}

	// Ordinary input still reads cleanly and reports its lines.
	lines, total, err = readLines(strings.NewReader("a\nb\nc\n"), 2)
	if err != nil {
		t.Fatalf("ordinary input reported an error: %v", err)
	}
	if total != 3 || len(lines) != 2 {
		t.Fatalf("readLines = %d lines of %d seen, want 2 of 3", len(lines), total)
	}
}

// A huge result set must not be buffered in full before the limit applies.
func TestGrep_DoesNotBufferBeyondTheLimit(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	var body strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&body, "needle %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "huge.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle", "max_matches": 3})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if got := strings.Count(out, "huge.txt:"); got != 3 {
		t.Fatalf("returned %d lines, want 3", got)
	}
	if len(out) > 1000 {
		t.Fatalf("output is %d bytes; the limit should apply before buffering", len(out))
	}
}
