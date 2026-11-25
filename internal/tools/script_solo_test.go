package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// soloScript builds the single-tool surface: one script tool whose globals
// include run(). It mirrors what cmd/neo's registryFor assembles, so these tests
// cover the mode rather than a stand-in for it.
func soloScript(t *testing.T, dir string) tools.Script {
	t.Helper()
	files := append([]tools.Tool{
		tools.Grep{Root: dir, Absolute: true},
		tools.Glob{Root: dir, Absolute: true},
	}, tools.NewFileTools()...)
	s := tools.NewScript(files)
	s.Solo = true
	s.CWD = dir
	s.Session = &tools.ScriptSession{}
	return s
}

// TestSoloMergesReadDecideWrite is the criterion #91 pre-registered: one call
// reads, branches on what it read, and writes the result. It is the shape that
// three measurements of the default surface never produced.
func TestSoloMergesReadDecideWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha\n// TODO: one\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := soloScript(t, dir)
	out, err := s.Run(context.Background(), map[string]any{"source": `
const hits = tools.grep({pattern: "TODO"}).split("\n").filter(Boolean);
if (hits.length !== 1) throw new Error("expected exactly one match, got " + hits.length);
const path = hits[0].split(":")[0];
const body = tools.read_file({path});
tools.edit_file({path, old_string: "TODO", new_string: "DONE"});
"merged: " + hits.length + " hit in " + body.split("\n").length + " lines"`})
	if err != nil {
		t.Fatalf("merged script failed: %v", err)
	}
	if !strings.HasPrefix(out, "merged: 1 hit") {
		t.Fatalf("result = %q, want the merged summary", out)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "DONE") {
		t.Fatalf("the write did not happen inside the script: %q", updated)
	}
}

// TestSoloRunReturnsExitCodeAsData covers run(): a failing check has to come
// back as a value a script can branch on, not as a thrown error that ends the
// call. Without this the mode cannot run tests at all.
func TestSoloRunReturnsExitCodeAsData(t *testing.T) {
	dir := t.TempDir()
	s := soloScript(t, dir)
	out, err := s.Run(context.Background(), map[string]any{"source": `
const ok = run(["sh", "-c", "echo fine; exit 0"]);
const bad = run(["sh", "-c", "echo broke >&2; exit 3"]);
"ok=" + ok.code + " okOut=" + ok.stdout.trim() + " bad=" + bad.code + " badErr=" + bad.stderr.trim()`})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	for _, want := range []string{"ok=0", "okOut=fine", "bad=3", "badErr=broke"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result = %q, want it to contain %q", out, want)
		}
	}
}

// TestSoloKeepsGlobalStateAcrossCalls is the other half of the mode: state a
// call puts on globalThis is still there for the next one. A local const cannot
// be, because a kept runtime rejects a repeated declaration and every second
// script writes `const cwd = ...`; the mode keeps redeclaration working and
// persists through globalThis instead, which the prompt states.
func TestSoloKeepsGlobalStateAcrossCalls(t *testing.T) {
	s := soloScript(t, t.TempDir())
	if _, err := s.Run(context.Background(), map[string]any{"source": `globalThis.base = 40; globalThis.add = (n) => globalThis.base + n;`}); err != nil {
		t.Fatalf("declaring failed: %v", err)
	}
	out, err := s.Run(context.Background(), map[string]any{"source": `add(2)`})
	if err != nil {
		t.Fatalf("second call lost the state: %v", err)
	}
	if out != "42" {
		t.Fatalf("result = %q, want 42 from the earlier assignment", out)
	}
}

// TestSoloAllowsRepeatedLocalDeclarations is why state goes through globalThis.
// In a kept runtime a second `const cwd = ...` would otherwise be a SyntaxError
// before anything ran, which is exactly what one measured run hit eight times.
func TestSoloAllowsRepeatedLocalDeclarations(t *testing.T) {
	s := soloScript(t, t.TempDir())
	for i := 0; i < 3; i++ {
		out, err := s.Run(context.Background(), map[string]any{"source": `const cwd = "same"; let n = 1; cwd + n`})
		if err != nil {
			t.Fatalf("call %d failed on redeclaration: %v", i+1, err)
		}
		if out != "same1" {
			t.Fatalf("call %d = %q, want same1", i+1, out)
		}
	}
}

// TestSoloWithoutSessionStartsFresh pins the default: a Script with no Session
// keeps the documented behaviour of one call per runtime.
func TestSoloWithoutSessionStartsFresh(t *testing.T) {
	s := soloScript(t, t.TempDir())
	s.Session = nil
	if _, err := s.Run(context.Background(), map[string]any{"source": `const keep = 1;`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), map[string]any{"source": `keep`}); err == nil {
		t.Fatal("a stateless script saw an earlier call's variable")
	}
}

// TestSoloSpecListsRunAndPersistence pins the description to the mode. The
// default description says nothing about run(); the model reads only this text,
// so a missing sentence is a capability it will not use.
func TestSoloSpecListsRunAndPersistence(t *testing.T) {
	solo := tools.Script{Solo: true}.Spec().Description
	for _, want := range []string{"run([", "globalThis"} {
		if !strings.Contains(solo, want) {
			t.Fatalf("solo description lacks %q", want)
		}
	}
	plain := tools.Script{}.Spec().Description
	if strings.Contains(plain, "run([") {
		t.Fatal("the default description offers run(), which it does not have")
	}
}

// TestSoloRunAcceptsEveryNumericTimeout covers a measured failure: a script that
// wrote run([...], 10) was rejected three times in one run because only a
// float64 was accepted. An integer and a string are the same request as 10.5.
func TestSoloRunAcceptsEveryNumericTimeout(t *testing.T) {
	s := soloScript(t, t.TempDir())
	for _, literal := range []string{"10", "10.5", `"10"`} {
		out, err := s.Run(context.Background(), map[string]any{
			"source": `run(["echo", "hi"], ` + literal + `).code`,
		})
		if err != nil {
			t.Fatalf("timeout %s was rejected: %v", literal, err)
		}
		if out != "0" {
			t.Fatalf("timeout %s: code = %q, want 0", literal, out)
		}
	}
}

// TestSoloToolArgumentShapeIsTaught is the other measured failure. Calling
// tools.glob("*") instead of tools.glob({pattern: "*"}) reported "missing
// required input: pattern", which sends the model to the arguments when the call
// itself was the wrong shape.
func TestSoloToolArgumentShapeIsTaught(t *testing.T) {
	s := soloScript(t, t.TempDir())
	_, err := s.Run(context.Background(), map[string]any{"source": `tools.glob("*")`})
	if err == nil {
		t.Fatal("a bare string argument was accepted")
	}
	for _, want := range []string{"glob", "object", "glob({...})"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not explain the shape with %q", err, want)
		}
	}
}

// TestSoloConsoleLogReachesTheResult covers a measured failure: a script that
// logged its findings and returned nothing lost them entirely, and the model saw
// "console is not defined" instead. The engine has no console, so the mode
// supplies one and joins its output to the result.
func TestSoloConsoleLogReachesTheResult(t *testing.T) {
	s := soloScript(t, t.TempDir())
	out, err := s.Run(context.Background(), map[string]any{
		"source": `console.log("first", 1); print("second"); "value"`,
	})
	if err != nil {
		t.Fatalf("console.log failed: %v", err)
	}
	for _, want := range []string{"first 1", "second", "value"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result %q lacks %q", out, want)
		}
	}
}

// TestSoloConsoleOutputSurvivesAFailure keeps the debugging information a
// failing script printed: that is usually why it was printed.
func TestSoloConsoleOutputSurvivesAFailure(t *testing.T) {
	s := soloScript(t, t.TempDir())
	_, err := s.Run(context.Background(), map[string]any{
		"source": `console.log("about to fail"); throw new Error("boom")`,
	})
	if err == nil {
		t.Fatal("expected the throw to surface")
	}
	if !strings.Contains(err.Error(), "about to fail") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q lost the printed line or the cause", err)
	}
}
