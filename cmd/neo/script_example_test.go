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

// TestCodeModeWindowExampleRuns is the guard for the code-mode shape that
// turns a "found N times" refusal into one edit. #100 recorded the failure and
// measured the budget: the example fits in code mode, so it lives next to the
// read-decide-write section rather than in the error message.
//
// The example is extracted from codeModeCapability itself, and it keeps the
// read_file line numbers, which is the part a model gets wrong: the raw text
// after the tab is what edit_file matches, so the window must be given back
// without the prefix.
func TestCodeModeWindowExampleRuns(t *testing.T) {
	source, ok := windowExample(t)
	if !ok {
		t.Fatal("codeModeCapability no longer shows how to build a unique old_string")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	body := "alpha\nbeta\n// TODO: one\n// TODO: two\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// The ambiguous call is the one the prompt is answering: two matches.
	s := tools.NewScript(nil)
	s.Edit = tools.EditFile{}
	s.Read = tools.ReadFile{}
	s.Session = &tools.ScriptSession{}
	if _, err := s.Run(context.Background(), map[string]any{"source": "tools.edit_file({path: " + quoted(path) + ", old_string: \"// TODO:\", new_string: \"// DONE:\"})"}); err == nil {
		t.Fatal("this test needs an old_string that matches more than once")
	}

	src := strings.ReplaceAll(source, "marker", "two")
	// The snippet reads with raw: true so the returned line already matches
	// edit_file's notion of the file's text; only the path is a placeholder.
	src = strings.ReplaceAll(src, "{path, raw: true}", "{path: "+quoted(path)+", raw: true}")
	if strings.Contains(src, "{path,") {
		t.Fatalf("the prompt snippet's placeholder changed shape:\n%s", src)
	}
	if _, err := s.Run(context.Background(), map[string]any{"source": src + "; tools.edit_file({path: " + quoted(path) + ", old_string: raw, new_string: \"// DONE: two\"}); raw"}); err != nil {
		t.Fatalf("the prompt example failed: %v\n%s", err, src)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != "alpha\nbeta\n// TODO: one\n// DONE: two\n" {
		t.Fatalf("file after the windowed edit = %q", updated)
	}
}

// windowExample pulls the one-line window snippet out of the prompt. It looks
// for the read_file-to-raw idiom rather than a fence, because the snippet sits
// in the prose next to the example, where a model reads it in order.
func windowExample(t *testing.T) (string, bool) {
	t.Helper()
	const open = "`const raw = "
	start := strings.Index(codeModeCapability, open)
	if start < 0 {
		return "", false
	}
	rest := codeModeCapability[start+len(open):]
	end := strings.Index(rest, "`")
	if end < 0 {
		t.Fatal("the window snippet is not closed")
	}
	return "const raw = " + rest[:end], true
}

// quoted renders a path as a JavaScript string literal.
func quoted(s string) string {
	return "\"" + strings.ReplaceAll(s, "\\", "\\\\") + "\""
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

// TestScriptCapabilityNamesDependencyNotVolume pins the judgement itself, not
// just the wording around it. #83 measured nine runs on a 2,001-line file: the
// model never called read_file, it narrowed the window with `sed`. A script over
// many files is N serial host calls, so "many files" is the shape where the tool
// loses; the reason to script is that the next call needs this result.
func TestScriptCapabilityNamesDependencyNotVolume(t *testing.T) {
	if strings.Contains(scriptCapability, "across many files") {
		t.Fatal("scriptCapability still offers file count as the reason to script")
	}
	if strings.Contains(scriptCapability, "not over one read") {
		t.Fatal("scriptCapability still discourages a script over a single read")
	}
	if !strings.Contains(scriptCapability, "depends on what this one returns") {
		t.Fatal("scriptCapability does not state the dependency judgement")
	}
}
