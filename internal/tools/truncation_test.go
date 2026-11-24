package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// readAdvice pulls the offset/limit pair out of a read_file over-limit error.
var readAdvice = regexp.MustCompile(`offset=(\d+), limit=(\d+)`)

// writeLongLines writes n lines, each long enough that a full read overflows the
// byte cap, and returns the path.
func writeLongLines(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "long.txt")
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line-%05d-%s\n", i, strings.Repeat("y", 40))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadFile_OverLimitSuggestsTheNextCall is the end-to-end check for the
// advice: parse the suggested offset/limit and use them to read the content that
// did not fit. A string assertion alone would pass on advice that does not work.
func TestReadFile_OverLimitSuggestsTheNextCall(t *testing.T) {
	path := writeLongLines(t, 5_000)

	_, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err == nil {
		t.Fatal("a full read of a file this size should exceed the cap")
	}
	match := readAdvice.FindStringSubmatch(err.Error())
	if match == nil {
		t.Fatalf("error does not suggest a concrete next call: %v", err)
	}
	offset, convErr := strconv.Atoi(match[1])
	if convErr != nil {
		t.Fatalf("offset %q is not a number: %v", match[1], convErr)
	}
	limit, convErr := strconv.Atoi(match[2])
	if convErr != nil {
		t.Fatalf("limit %q is not a number: %v", match[2], convErr)
	}
	if offset <= 1 {
		t.Fatalf("suggested offset = %d, want the line after what already fit", offset)
	}
	if limit <= 0 {
		t.Fatalf("suggested limit = %d, want a positive line count", limit)
	}

	out, retryErr := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": float64(offset),
		"limit":  float64(limit),
	})
	if retryErr != nil {
		t.Fatalf("the suggested call failed: %v", retryErr)
	}
	if out == "" {
		t.Fatal("the suggested call returned nothing")
	}
	// The suggestion must resume where the failed read stopped, not restart or
	// skip past content.
	wantPrefix := fmt.Sprintf(" %5d\tline-%05d-", offset, offset)
	if !strings.HasPrefix(out, wantPrefix) {
		t.Fatalf("retry starts with %q, want %q", strings.SplitN(out, "\n", 2)[0], wantPrefix)
	}
}

// TestReadFile_OverLimitWithExplicitLimitKeepsIt checks the advice reuses a
// caller-supplied limit: the lines after the cut are still unread, so shrinking
// it would silently drop content.
func TestReadFile_OverLimitWithExplicitLimitKeepsIt(t *testing.T) {
	path := writeLongLines(t, 5_000)

	_, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":  path,
		"limit": 3_000,
	})
	if err == nil {
		t.Fatal("3,000 long lines should exceed the cap")
	}
	match := readAdvice.FindStringSubmatch(err.Error())
	if match == nil {
		t.Fatalf("error does not suggest a concrete next call: %v", err)
	}
	if got := match[2]; got != "3000" {
		t.Fatalf("suggested limit = %s, want the caller's 3000", got)
	}
}

// TestReadFile_WithinCapCarriesNoAdvice keeps the normal path quiet: a read
// that fits should not grow a suggestion.
func TestReadFile_WithinCapCarriesNoAdvice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(out, "retry with offset=") {
		t.Fatalf("a read that fits should not suggest a retry: %q", out)
	}
}

// TestBashTruncationSuggestsNarrowing checks the bash marker names a concrete
// command-side narrowing, not only the byte counts.
func TestBashTruncationSuggestsNarrowing(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{
		"command": "printf HEAD; yes x | head -c 400000; printf TAIL",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > MaxBashOutputBytes {
		t.Fatalf("output length = %d, want at most %d", len(out), MaxBashOutputBytes)
	}
	// The existing marker format survives unchanged up to the byte counts; the
	// advice is appended inside the same marker.
	head := "[bash output truncated: omitted "
	if !strings.Contains(out, head) {
		t.Fatalf("marker changed shape: %q", out)
	}
	if !strings.Contains(out, "tail -n") {
		t.Fatalf("marker does not suggest narrowing the command: %q", out)
	}
	for _, want := range []string{"HEAD", "TAIL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bounded output lost the %s sentinel", want)
		}
	}
}
