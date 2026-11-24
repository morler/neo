package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// scriptExample extracts the fenced JavaScript from scriptCapability. The
// prompt section is a Go string literal assembled with concatenation, so the
// example is read back from the constant rather than copied here: a test that
// held its own copy would keep passing while the prompt rotted.
func scriptExample(t *testing.T) string {
	t.Helper()
	start := strings.Index(scriptCapability, "```js\n")
	if start < 0 {
		t.Fatal("scriptCapability has no js example fence")
	}
	body := scriptCapability[start+len("```js\n"):]
	end := strings.Index(body, "\n```")
	if end < 0 {
		t.Fatal("the js example fence is not closed")
	}
	return body[:end]
}

// TestScriptExampleRuns is the guard the prompt needs. An example a model
// copies and cannot run is worse than no example: it costs a turn and teaches
// the wrong calling convention, which is what the first measurement found.
func TestScriptExampleRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("first\n// TODO: one\n// TODO: two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	example := strings.Replace(scriptExample(t), "{pattern: \"TODO\"}", "{pattern: \"TODO\", path: \""+dir+"\"}", 1)
	s := tools.Script{Grep: tools.Grep{Root: dir}}
	out, err := s.Run(context.Background(), map[string]any{"source": example})
	if err != nil {
		t.Fatalf("the prompt example failed: %v\n%s", err, example)
	}
	if !strings.HasPrefix(out, "2 matches: ") {
		t.Fatalf("example output = %q, want it to start with 2 matches: ", out)
	}
	if !strings.Contains(out, "notes.txt") {
		t.Fatalf("example output = %q, want the matching path", out)
	}
}

// TestScriptExampleAvoidsAnEmptyStringTrap checks the sentence next to the
// example. grep answers "no matches" rather than "", so a script written in
// the obvious style reports one phantom match; the prompt has to say so.
func TestScriptExampleAvoidsAnEmptyStringTrap(t *testing.T) {
	if !strings.Contains(scriptCapability, `"no matches"`) {
		t.Fatal("the prompt does not warn about grep's no-match string")
	}
	s := tools.Script{Grep: tools.Grep{Root: t.TempDir()}}
	if err := os.WriteFile(filepath.Join(s.Grep.Root, "plain.txt"), []byte("nothing relevant\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(context.Background(), map[string]any{
		"source": `tools.grep({pattern: "definitely-not-here"})`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "no matches" {
		t.Fatalf("grep = %q, want the string the prompt promises", out)
	}
}

// TestScriptCapabilityDoesNotRestateTheWhitelist keeps one owner for the tool
// list. Both the schema and this section named the tools once, the schema's
// copy went stale, and the model believed the stale one.
func TestScriptCapabilityDoesNotRestateTheWhitelist(t *testing.T) {
	for _, name := range []string{"read_file", "write_file", "edit_file", "glob"} {
		if strings.Contains(scriptCapability, name) {
			t.Fatalf("scriptCapability names %q; the schema owns that list", name)
		}
	}
}
