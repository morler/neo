package tools_test

import (
	"context"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// TestGrepOnADirectoryWithNoFilesSaysNoMatches covers a measured failure. With
// no path argument ripgrep searches the working directory, and when that holds
// no files it exits 2 with "No files were searched" rather than the 1 that means
// "nothing matched". The model saw ripgrep's explanation as a tool error, and in
// a one-tool session there is no bash call to work around it.
func TestGrepOnADirectoryWithNoFilesSaysNoMatches(t *testing.T) {
	dir := t.TempDir()
	for _, form := range []map[string]any{
		{"pattern": "anything"},
		{"pattern": "anything", "path": "."},
	} {
		out, err := (tools.Grep{Root: dir}).Run(context.Background(), form)
		if err != nil {
			t.Fatalf("%v failed: %v", form, err)
		}
		if out != "no matches" {
			t.Fatalf("%v = %q, want no matches", form, out)
		}
	}
}

// TestGlobOnADirectoryWithNoFilesSaysNoMatches is the same rule for glob, which
// already behaved this way; the test keeps the two from diverging.
func TestGlobOnADirectoryWithNoFilesSaysNoMatches(t *testing.T) {
	out, err := (tools.Glob{Root: t.TempDir()}).Run(context.Background(), map[string]any{"pattern": "*"})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if out != "no matches" {
		t.Fatalf("glob = %q, want no matches", out)
	}
}

// TestGrepStillReportsRealErrors keeps the relaxation narrow: a genuine ripgrep
// failure is still an error, not an empty result.
func TestGrepStillReportsRealErrors(t *testing.T) {
	dir := t.TempDir()
	// An invalid regexp makes ripgrep exit 2 with a message that is not the
	// "no files" explanation.
	if _, err := (tools.Grep{Root: dir}).Run(context.Background(), map[string]any{"pattern": "["}); err == nil {
		t.Fatal("an invalid pattern was reported as no matches")
	}
}
