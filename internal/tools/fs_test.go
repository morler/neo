package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReadFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hi\nthere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "     1\thi\n     2\tthere\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_RawOmitsGutter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.txt")
	body := "func Max(a, b int) int {\n\tif a < b {\n\t\treturn b\n\t}\n\treturn a\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path, "raw": true})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The point of raw: the result is the file's own text, so edit_file can
	// match it directly. If a gutter survives here the whole feature is void.
	if out != body {
		t.Fatalf("raw read = %q, want the file's own text %q", out, body)
	}
	if strings.Contains(out, "     1\t") {
		t.Fatal("raw read still carries a line-number gutter")
	}
}

// TestReadFile_RawMatchesGutterStripped pins the two modes to one source of
// truth: raw output must equal the default output with its gutter removed, so a
// future change to the gutter cannot make raw and default drift apart.
func TestReadFile_RawMatchesGutterStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paired.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gutter, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read default: %v", err)
	}
	raw, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path, "raw": true})
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	var stripped []string
	for _, line := range strings.Split(gutter, "\n") {
		// The file ends with a newline, so the split leaves one empty final
		// element; it is not a numbered line and carries no tab.
		if line == "" {
			stripped = append(stripped, "")
			continue
		}
		_, after, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("gutter line has no tab: %q", line)
		}
		stripped = append(stripped, after)
	}
	// Joining preserves the trailing empty element, which is the file's own
	// trailing newline: raw keeps it and the gutter form does too.
	if want := strings.Join(stripped, "\n"); raw != want {
		t.Fatalf("raw = %q, want gutter-stripped default %q", raw, want)
	}
}

func TestReadFile_RawHonorsOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "window.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 2.0,
		"limit":  2.0,
		"raw":    true,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "b\nc"; out != want {
		t.Fatalf("raw windowed read = %q, want %q", out, want)
	}
}

// TestReadFile_RawIgnoresNonBooleanFlag keeps the flag honest: a string "true"
// from a sloppy caller must not half-enable raw and silently change the shape of
// what edit_file will have to match.
func TestReadFile_RawIgnoresNonBooleanFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flag.txt")
	if err := os.WriteFile(path, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path, "raw": "true"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "     1\thi\n"; out != want {
		t.Fatalf("a string flag changed the default output: %q", out)
	}
}

// TestReadFile_RawPreservesCRLFAndMissingNewline covers the shapes a real file
// has that the fixture above does not: CRLF line endings and no trailing
// newline. raw must return the file's bytes as-is or the edit_file match fails.
func TestReadFile_RawPreservesCRLFAndMissingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	body := "one\r\ntwo\r\nthree"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path, "raw": true})
	if err != nil {
		t.Fatal(err)
	}
	if out != body {
		t.Fatalf("raw CRLF read = %q, want %q", out, body)
	}
	if strings.Contains(out, "     1\t") {
		t.Fatal("raw read still has a gutter")
	}

	// The edit must succeed with exactly what raw returned, through the same
	// stale-edit guard a coordinator uses.
	e := EditFile{State: newFileState()}
	r := ReadFile{State: e.State}
	if _, err := r.Run(context.Background(), map[string]any{"path": path, "raw": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(context.Background(), map[string]any{
		"path": path, "old_string": "two\r\n", "new_string": "TWO\r\n",
	}); err != nil {
		t.Fatalf("edit with raw-derived old_string failed: %v", err)
	}
	got, _ := os.ReadFile(path)
	if want := "one\r\nTWO\r\nthree"; string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// TestReadFile_RawAbsentKeepsTheGutter is the regression guard for the whole
// feature: an absent flag must keep today's byte-exact output.
func TestReadFile_RawAbsentKeepsTheGutter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if want := "     1\ta\n     2\tb\n"; out != want {
		t.Fatalf("default read changed: %q", out)
	}
}

func TestReadFile_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 2.0, // JSON numbers arrive as float64
		"limit":  2.0,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "     2\tb\n     3\tc"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_LargePaginatedRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large-lines.txt")

	var content strings.Builder
	for i := 1; content.Len() <= MaxReadBytes+1024; i++ {
		fmt.Fprintf(&content, "line-%05d\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 250,
		"limit":  3,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "   250\tline-00250\n   251\tline-00251\n   252\tline-00252"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_OffsetPastEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("a\nb"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 10,
		"limit":  2,
	})
	if err == nil {
		t.Fatal("expected error for offset past EOF")
	}
	if !strings.Contains(err.Error(), "offset 10 is past end of file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFile_EmptyFileOffsetOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  1,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q, want empty string", out)
	}
}

// utf8Lines writes n lines of valid multibyte text, enough to overflow the
// byte cap, and returns the file path plus the expected gutter-prefixed lines.
func utf8Lines(t *testing.T, n int) (string, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "utf8.txt")
	var body strings.Builder
	lines := make([]string, n)
	for i := 1; i <= n; i++ {
		text := fmt.Sprintf("line-%05d-%s", i, strings.Repeat("世", 40))
		body.WriteString(text + "\n")
		lines[i-1] = lineNumberPrefix(i) + text
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, lines
}

// readPage calls read_file and splits the inline continuation marker from the
// delivered page, if any.
func readPage(t *testing.T, input map[string]any) (page string, offset, limit int) {
	t.Helper()
	out, err := ReadFile{}.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(out) > MaxReadBytes {
		t.Fatalf("result = %d bytes, cap = %d", len(out), MaxReadBytes)
	}
	return splitMarker(t, out)
}

// pageLineNumbers extracts gutter-prefixed content lines from a delivered page.
func pageLineNumbers(t *testing.T, page string) []string {
	t.Helper()
	if page == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(page, "\n") {
		if line == "" {
			continue // page ends with a newline
		}
		if !strings.Contains(line, "\t") {
			t.Fatalf("page line has no gutter: %q", line)
		}
		lines = append(lines, line)
	}
	return lines
}

// collectReadPages follows the marker, one page at a time, until the window
// starts past the last line. A page that stops at the caller's limit carries no
// marker: limit is the caller's own request, not truncation.
func collectReadPages(t *testing.T, path string, offset, limit, totalLines int) []string {
	t.Helper()
	var got []string
	for offset <= totalLines {
		call := map[string]any{"path": path, "offset": offset}
		if limit > 0 {
			call["limit"] = limit
		}
		out, err := ReadFile{}.Run(context.Background(), call)
		if err != nil {
			if strings.Contains(err.Error(), "past end of file") {
				return got
			}
			t.Fatalf("read at offset %d: %v", offset, err)
		}
		if len(out) > MaxReadBytes {
			t.Fatalf("result = %d bytes, cap = %d", len(out), MaxReadBytes)
		}
		page, nextOffset, nextLimit := splitMarker(t, out)
		got = append(got, pageLineNumbers(t, page)...)
		if nextOffset == 0 {
			nextOffset = offset + countLines(page)
		}
		if nextOffset <= offset {
			t.Fatalf("paging from %d did not advance", offset)
		}
		offset, limit = nextOffset, nextLimit
	}
	return got
}

// splitMarker separates an inline continuation marker from the delivered page.
func splitMarker(t *testing.T, out string) (page string, offset, limit int) {
	t.Helper()
	match := readAdvice.FindStringSubmatch(out)
	if match == nil {
		return out, 0, 0
	}
	offset, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("bad marker offset %q: %v", match[1], err)
	}
	limit, err = strconv.Atoi(match[2])
	if err != nil {
		t.Fatalf("bad marker limit %q: %v", match[2], err)
	}
	return strings.TrimSuffix(out, firstPageMarker(offset, limit)), offset, limit
}

func countLines(page string) int {
	if page == "" {
		return 0
	}
	return strings.Count(page, "\n") + 1
}

func TestReadFile_OversizedNeedsPagination(t *testing.T) {
	path, lines := utf8Lines(t, 5_000)

	page, offset, _ := readPage(t, map[string]any{"path": path})
	if offset <= 1 {
		t.Fatalf("first page offset = %d, want the line after what fit", offset)
	}
	got := append(pageLineNumbers(t, page), collectReadPages(t, path, offset, 0, len(lines))...)
	if len(got) != len(lines) {
		t.Fatalf("paged read produced %d lines, want %d", len(got), len(lines))
	}
	for i := range lines {
		if got[i] != lines[i] {
			t.Fatalf("line %d = %q, want %q", i+1, got[i], lines[i])
		}
	}
}

func TestReadFile_OverLimitWithExplicitLimitKeepsIt(t *testing.T) {
	path, _ := utf8Lines(t, 5_000)

	_, _, limit := readPage(t, map[string]any{"path": path, "limit": 3_000})
	if limit != 3_000 {
		t.Fatalf("marker limit = %d, want the caller's 3000", limit)
	}
}

func TestReadFile_OverLimitWithOffsetKeepsLineNumbers(t *testing.T) {
	path, lines := utf8Lines(t, 5_000)

	page, offset, _ := readPage(t, map[string]any{"path": path, "offset": 2_000})
	if offset <= 2_000 {
		t.Fatalf("continuation offset = %d, want after 2000", offset)
	}
	got := append(pageLineNumbers(t, page), collectReadPages(t, path, offset, 0, len(lines))...)
	if len(got) != len(lines)-1_999 {
		t.Fatalf("paged read produced %d lines, want %d", len(got), len(lines)-1_999)
	}
	for i, want := range lines[1_999:] {
		if got[i] != want {
			t.Fatalf("line %d = %q, want %q", i+2_000, got[i], want)
		}
	}
}

func TestReadFile_PaginatedSelectionExceedsCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long-line.txt")
	// A valid multibyte line that is too large for the cap must fail with a
	// distinguishable error, never a half line or split UTF-8 as content.
	body := strings.Repeat("世", MaxReadBytes/3+1)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  1,
	})
	if err == nil {
		t.Fatalf("expected error for oversized paginated selection, got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "selection exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "retry with offset=1, limit=1") {
		t.Fatalf("error does not give a usable next call: %v", err)
	}
	if out != "" {
		t.Fatalf("a failed page returned content: %q", out)
	}
}

// TestReadFile_FirstPageSurvivesAnOversizedLaterLine pins the page contract
// when one line cannot fit at all: the complete lines before it are still
// delivered, and the marker resumes on the oversized line rather than past it.
func TestReadFile_FirstPageSurvivesAnOversizedLaterLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.txt")
	body := "short first line\nshort second line\n" + strings.Repeat("世", MaxReadBytes/3+1) + "\ntail\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	page, offset, _ := readPage(t, map[string]any{"path": path})
	want := "     1\tshort first line\n     2\tshort second line"
	if page != want {
		t.Fatalf("first page = %q, want %q", page, want)
	}
	if offset != 3 {
		t.Fatalf("continuation offset = %d, want 3 (the line that did not fit)", offset)
	}
	if !utf8.ValidString(page) {
		t.Fatal("first page is not valid UTF-8")
	}

	// The marker stands on its own line after the last delivered line, so a
	// reader sees where the page ended rather than a blank or glued-on row.
	full, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(full, want+"\n[read_file: first page only;") {
		t.Fatalf("page/marker boundary changed: %q", full[max(0, len(full)-160):])
	}
}

func TestReadFile_RawOverLimitStillErrors(t *testing.T) {
	path, _ := utf8Lines(t, 5_000)

	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path, "raw": true})
	if err == nil {
		t.Fatal("raw read over cap should still fail")
	}
	if out != "" {
		t.Fatalf("raw failure returned partial content: %q", out)
	}
	if !strings.Contains(err.Error(), "selection exceeds") || !strings.Contains(err.Error(), "retry with offset=") {
		t.Fatalf("raw error contract changed: %v", err)
	}
}

func TestWriteFile_CreatesAndIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.txt")
	_, err := WriteFile{}.Run(context.Background(), map[string]any{
		"path":    path,
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q", string(b))
	}
	if got := fileMode(t, path); got != 0o644 {
		t.Fatalf("mode = %v, want %v", got, os.FileMode(0o644))
	}
	// No leftover temp files in the target directory.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".neo-write-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestWriteFile_PreservesExistingMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "executable", mode: 0o755},
		{name: "private", mode: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.txt")
			writeFileWithMode(t, path, []byte("old"), tt.mode)

			if _, err := (WriteFile{}).Run(context.Background(), map[string]any{
				"path":    path,
				"content": "new",
			}); err != nil {
				t.Fatalf("write: %v", err)
			}

			if got := fileMode(t, path); got != tt.mode {
				t.Fatalf("mode = %v, want %v", got, tt.mode)
			}
		})
	}
}

func TestEditFile_UniqueMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("alpha bravo charlie"), 0o644)

	if _, err := (EditFile{}).Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "bravo",
		"new_string": "BRAVO",
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "alpha BRAVO charlie" {
		t.Fatalf("got %q", string(b))
	}
}

func TestEditFile_PreservesExistingMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "executable", mode: 0o755},
		{name: "private", mode: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.txt")
			writeFileWithMode(t, path, []byte("alpha bravo charlie"), tt.mode)

			if _, err := (EditFile{}).Run(context.Background(), map[string]any{
				"path":       path,
				"old_string": "bravo",
				"new_string": "BRAVO",
			}); err != nil {
				t.Fatalf("edit: %v", err)
			}

			if got := fileMode(t, path); got != tt.mode {
				t.Fatalf("mode = %v, want %v", got, tt.mode)
			}
		})
	}
}

func TestEditFile_AmbiguousMatchFailsAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "foo foo foo"
	os.WriteFile(path, []byte(original), 0o644)

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "bar",
	})
	if err == nil {
		t.Fatal("expected error on ambiguous match")
	}
	b, _ := os.ReadFile(path)
	if string(b) != original {
		t.Fatalf("file mutated on failed edit: %q", string(b))
	}
}

func TestEditFile_MissingMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("nothing here"), 0o644)

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "missing",
		"new_string": "x",
	})
	if err == nil {
		t.Fatal("expected error when old_string is absent")
	}
	if !strings.Contains(err.Error(), "read the file and retry with exact text") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestEditFile_EmptyMatchFailsAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "leave me alone"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "",
		"new_string": "surprise",
	})
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected actionable empty match error, got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != original {
		t.Fatalf("file mutated on failed edit: %q", b)
	}
}

func writeFileWithMode(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestReadFile_NumbersMatchGrep(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("alpha\nbravo\nneedle\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	read, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	found, err := Grep{Root: dir}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}

	// grep reports src.txt:3:needle; read_file must number the same line 3.
	fields := strings.SplitN(strings.TrimSpace(found), ":", 3)
	if len(fields) != 3 {
		t.Fatalf("unexpected grep output: %q", found)
	}
	line, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("grep line number %q: %v", fields[1], err)
	}
	if want := lineNumberPrefix(line) + "needle"; !strings.Contains(read, want) {
		t.Fatalf("read_file line %d is not numbered to match grep:\n%s", line, read)
	}
}

func TestEditFile_RejectsExternalChangeSinceRead(t *testing.T) {
	files := NewFileTools()
	read, write, edit := files[0].(ReadFile), files[1].(WriteFile), files[2].(EditFile)
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := read.Run(ctx, map[string]any{"path": path}); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Something outside the agent rewrites the file. The unique-match check
	// would still pass, so only the stamp catches this.
	if err := os.WriteFile(path, []byte("original\nadded by the user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, path)

	_, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "original", "new_string": "edited"})
	if err == nil {
		t.Fatal("expected the stale edit to be rejected")
	}
	if !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("error does not tell the agent what to do: %v", err)
	}

	// Re-reading clears it.
	if _, err := read.Run(ctx, map[string]any{"path": path}); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "original", "new_string": "edited"}); err != nil {
		t.Fatalf("edit after re-read: %v", err)
	}

	// The agent's own edits and writes are not external changes.
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "edited", "new_string": "again"}); err != nil {
		t.Fatalf("consecutive edit: %v", err)
	}
	if _, err := write.Run(ctx, map[string]any{"path": path, "content": "replaced\n"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "replaced", "new_string": "final"}); err != nil {
		t.Fatalf("edit after write: %v", err)
	}
}

func TestEditFile_UnreadFileIsNotStale(t *testing.T) {
	files := NewFileTools()
	edit := files[2].(EditFile)

	path := filepath.Join(t.TempDir(), "never-read.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Run(context.Background(), map[string]any{"path": path, "old_string": "hello", "new_string": "bye"}); err != nil {
		t.Fatalf("editing a file the agent never read must work: %v", err)
	}
}

func TestFileState_IsPerAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinator, subagent := NewFileTools(), NewFileTools()
	if _, err := subagent[0].(ReadFile).Run(context.Background(), map[string]any{"path": path}); err != nil {
		t.Fatalf("subagent read: %v", err)
	}
	if coordinator[2].(EditFile).State.changedSinceRead(path) {
		t.Fatal("a subagent's read must not register as the coordinator having read the file")
	}
}

// touch advances the modification time past the filesystem's timestamp
// granularity so the change is observable regardless of how fast the test runs.
func touch(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestReadFile_CancelledContextBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (ReadFile{}).Run(ctx, map[string]any{"path": path}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// A missing path must still report cancellation, not the filesystem error.
	if _, err := (ReadFile{}).Run(ctx, map[string]any{"path": filepath.Join(t.TempDir(), "absent")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
