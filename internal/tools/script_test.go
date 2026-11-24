package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

func newScript(t *testing.T) (tools.Script, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The zero ReadFile tracks no prior reads, which is what a script needs:
	// it must not satisfy the coordinator's stale-edit guard by reading.
	return tools.Script{Read: tools.ReadFile{}}, path
}

func runScript(t *testing.T, s tools.Script, source string) (string, error) {
	t.Helper()
	return s.Run(context.Background(), map[string]any{"source": source})
}

// TestScriptReadsAFile is the shortest useful path: a tool call, a transform,
// and a value back.
func TestScriptReadsAFile(t *testing.T) {
	s, path := newScript(t)
	out, err := runScript(t, s, `tools.read_file({path: "`+path+`"})`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("result = %q, want the file contents", out)
	}
}

// TestScriptLastExpressionIsTheResult pins the semantic the tool description
// promises. Without it, a model would reasonably write `return` and get nothing.
func TestScriptLastExpressionIsTheResult(t *testing.T) {
	s, _ := newScript(t)
	out, err := runScript(t, s, "var a = 1;\nvar b = 2;\na + b")
	if err != nil {
		t.Fatal(err)
	}
	if out != "3" {
		t.Fatalf("result = %q, want 3", out)
	}
}

// TestScriptWhitelistIsClosed is the security-shaped test: the tools a script
// can reach are exactly the ones written in script.go. It asserts absence
// rather than a friendly error, because a name that exists but fails is a name
// someone can still wire up later by accident.
//
// write_file and edit_file are absent from this list on purpose: they are
// allowed now, and gated. The tools that must never appear are the ones Neo
// cannot classify or that would recurse.
func TestScriptWhitelistIsClosed(t *testing.T) {
	s, _ := newScript(t)
	for _, name := range []string{"bash", "agent", "goal", "workflow", "script"} {
		out, err := runScript(t, s, `typeof tools.`+name)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		// Strings come back verbatim, so the value of `typeof` is the bare word.
		if out != "undefined" {
			t.Fatalf("typeof tools.%s = %q, want undefined", name, out)
		}
	}
}

// TestScriptRejectsPromiseResults covers the failure this tool would otherwise
// hide: a Promise stringifies to {} and the model would see an empty success.
func TestScriptRejectsPromiseResults(t *testing.T) {
	s, _ := newScript(t)
	for _, source := range []string{
		`Promise.resolve(1)`,
		`async function f() {}; f()`,
	} {
		_, err := runScript(t, s, source)
		if err == nil {
			t.Fatalf("%s: expected an error", source)
		}
		if !strings.Contains(err.Error(), "Promise") {
			t.Fatalf("%s: error = %v, want it to mention Promise", source, err)
		}
	}
}

// TestScriptErrorClassesStayDistinct gives the model actionable feedback: a
// syntax error means fix the source, a throw means handle the failure.
func TestScriptErrorClassesStayDistinct(t *testing.T) {
	s, _ := newScript(t)
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"syntax", "1 +", "syntax error"},
		{"top-level await", "await 1", "syntax error"},
		{"throw", `throw new Error("boom")`, "script threw"},
		{"tool failure", `tools.read_file({path: "/nonexistent/file"})`, "script threw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runScript(t, s, tc.source)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestScriptRejectsEmptyAndNonStringSource keeps malformed input on the tool
// error path instead of letting the engine report it as a script failure.
func TestScriptRejectsEmptyAndNonStringSource(t *testing.T) {
	s, _ := newScript(t)
	if _, err := s.Run(context.Background(), map[string]any{"source": "  "}); err == nil {
		t.Fatal("empty source: expected an error")
	}
	if _, err := s.Run(context.Background(), map[string]any{"source": 1}); err == nil {
		t.Fatal("non-string source: expected an error")
	}
	if _, err := s.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatal("missing source: expected an error")
	}
}

// TestScriptIsNeverParallelSafe pins the scheduling decision. Two scripts
// together would be a concurrency choice nobody made, so the answer is false
// regardless of input.
func TestScriptIsNeverParallelSafe(t *testing.T) {
	s, _ := newScript(t)
	if s.ParallelSafe(map[string]any{"source": "1"}) {
		t.Fatal("script must not be parallel-safe")
	}
}

// TestScriptHonorsCancellation stops a cancelled turn before it evaluates
// anything, rather than reporting an engine error the model cannot act on.
func TestScriptHonorsCancellation(t *testing.T) {
	s, _ := newScript(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Run(ctx, map[string]any{"source": "1"}); err == nil {
		t.Fatal("expected cancellation")
	}
}

// TestScriptSpecMatchesGlobals pins the description to the whitelist it
// describes. The text said "no write" long after a script could write, and a
// model that reads a narrower capability than it has simply stops choosing the
// tool, so the drift is a behaviour bug rather than a typo.
func TestScriptSpecMatchesGlobals(t *testing.T) {
	s, _ := newScript(t)
	out, err := runScript(t, s, `Object.keys(tools).sort().join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	actual := strings.Split(out, ",")

	description := tools.Script{}.Spec().Description
	var documented []string
	for _, field := range strings.Fields(description) {
		if name, ok := strings.CutPrefix(field, "tools."); ok {
			documented = append(documented, strings.TrimRight(name, ",."))
		}
	}
	sort.Strings(documented)

	if strings.Join(documented, ",") != strings.Join(actual, ",") {
		t.Fatalf("description names %v, globals expose %v", documented, actual)
	}
}

// TestScriptDescriptionStatesTheReturnShapes covers the one detail a model
// cannot infer: grep and glob return text lines, not arrays, so a script that
// calls .filter on the result would fail for a reason the text could prevent.
func TestScriptDescriptionStatesTheReturnShapes(t *testing.T) {
	description := tools.Script{}.Spec().Description
	for _, want := range []string{"path:line:text", "All return strings", "No bash", "no import"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description lacks %q", want)
		}
	}
}
