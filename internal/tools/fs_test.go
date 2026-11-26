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

func TestReadFile_OversizedNeedsPagination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	big := make([]byte, MaxReadBytes+1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected error for oversized read without pagination")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFile_PaginatedSelectionExceedsCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long-line.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxReadBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  1,
	})
	if err == nil {
		t.Fatal("expected error for oversized paginated selection")
	}
	if !strings.Contains(err.Error(), "selection exceeds") {
		t.Fatalf("unexpected error: %v", err)
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
