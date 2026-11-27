package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// The script tool is built on a set of engine behaviours, not on an API shape
// alone. Each test below pins one of them, so a moejs upgrade that changes a
// semantic fails here with the reason the tool depends on it, instead of
// surfacing later as a confusing tool result.
//
// Read the comment on a failing test before changing it: most of them record a
// limitation the design documents rather than works around, and "fixing" the
// test can silently remove the guarantee.

// newProbeRuntime returns a runtime with the options the script tool uses in its
// default path, so the probes below exercise the shipped configuration.
func newProbeRuntime(t *testing.T) *moejs.Runtime {
	t.Helper()
	return moejs.NewRuntime(moejs.Options{MemoryLimit: 64 << 20})
}

// runProbe compiles and runs source, returning the completion value.
func runProbe(t *testing.T, rt *moejs.Runtime, source string) engine.Value {
	t.Helper()
	script, err := moejs.CompileScript("probe.js", source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	value, err := rt.RunScript(script)
	if err != nil {
		t.Fatalf("run %q: %v", source, err)
	}
	return value
}

// jsonOf renders a value the way the tool does, so a test compares the same
// bytes the model would see.
func jsonOf(t *testing.T, rt *moejs.Runtime, v engine.Value) string {
	t.Helper()
	out, err := rt.AppendJSON(nil, v)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(out)
}

// TestMoejsDependencyIsPinned pins the single semantic every script result
// depends on: a classic script's value is its last expression's value.
func TestMoejsDependencyIsPinned(t *testing.T) {
	rt := newProbeRuntime(t)
	if got := jsonOf(t, rt, runProbe(t, rt, "1 + 41")); got != "42" {
		t.Fatalf("RunScript result = %s, want the last expression's value", got)
	}
}

// TestMoejsCompletionValueIsTheLastExpressionNotTheFirst separates the two
// readings a host could take. script.go relies on the last one: a script that
// ends in a statement must not report an earlier expression.
func TestMoejsCompletionValueIsTheLastExpressionNotTheFirst(t *testing.T) {
	rt := newProbeRuntime(t)
	if got := jsonOf(t, rt, runProbe(t, rt, "1 + 1; 7 * 6")); got != "42" {
		t.Fatalf("completion value = %s, want the last expression (42)", got)
	}
}

// TestMoejsEvalReturnsValueButFunctionBodyDoesNot records why the wrapper uses
// eval. (new Function("1+41"))() is undefined: a function body answers with an
// explicit return, not with its last expression. Switching the wrapper to a
// function would make every script return nothing.
func TestMoejsEvalReturnsValueButFunctionBodyDoesNot(t *testing.T) {
	rt := newProbeRuntime(t)
	if got := jsonOf(t, rt, runProbe(t, rt, `eval("1 + 41")`)); got != "42" {
		t.Fatalf("eval = %s, want 42", got)
	}
	got := jsonOf(t, rt, runProbe(t, rt, `(new Function("1 + 41"))()`))
	if got != "" && got != "null" {
		t.Fatalf("new Function body = %q, want undefined/null", got)
	}
}

// TestMoejsClassicScriptHasNoTopLevelAwait records a limitation the script tool
// documents rather than works around: a classic script is not a module, so
// top-level await is a syntax error. Supporting it would need an event loop.
func TestMoejsClassicScriptHasNoTopLevelAwait(t *testing.T) {
	if _, err := moejs.CompileScript("probe.js", "await 1"); err == nil {
		t.Fatal("top-level await compiled; the script tool's documented limit is now wrong")
	}
}

// TestMoejsClassicScriptHasNoImport keeps the documented no-import promise true.
// A classic script is not a module, so a bare import statement is a syntax error
// rather than a way to reach the host's files.
func TestMoejsClassicScriptHasNoImport(t *testing.T) {
	if _, err := moejs.CompileScript("probe.js", `import "node:fs"`); err == nil {
		t.Fatal("a classic script compiled an import; the tool's no-import promise is now wrong")
	}
}

// TestMoejsPromiseFromClassicScriptIsDetectable pins the check the wrapper makes.
// A classic script cannot await, but it can still hand back a Promise, and JSON
// encoding of one is an empty object: a silent empty result, which is why the
// wrapper throws instead.
func TestMoejsPromiseFromClassicScriptIsDetectable(t *testing.T) {
	rt := newProbeRuntime(t)
	promise := runProbe(t, rt, "Promise.resolve(1)")

	thenable, err := rt.ToGo(runProbe(t, rt, `(function (v) {
		return v !== null && typeof v === "object" && typeof v.then === "function";
	})(Promise.resolve(1))`))
	if err != nil {
		t.Fatalf("probe thenable: %v", err)
	}
	if thenable != true {
		t.Fatalf("thenable check = %v, want true; the wrapper's Promise guard would not fire", thenable)
	}
	if got := jsonOf(t, rt, promise); got != "{}" {
		t.Fatalf("rendered Promise = %q, want {} — the reason the wrapper refuses one", got)
	}
}

// TestMoejsThrowIsAnExceptionAndBadSourceIsASyntaxError pins the two error classes
// describeScriptError tells apart: the model's next move differs between your
// source did not compile and your source threw.
func TestMoejsThrowIsAnExceptionAndBadSourceIsASyntaxError(t *testing.T) {
	if _, err := moejs.CompileScript("probe.js", "function {"); err == nil {
		t.Fatal("bad source compiled")
	} else {
		var syntax *moejs.SyntaxError
		if !errors.As(err, &syntax) {
			t.Fatalf("compile error = %T, want *moejs.SyntaxError", err)
		}
	}

	rt := newProbeRuntime(t)
	script, err := moejs.CompileScript("probe.js", `throw new Error("boom")`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = rt.RunScript(script)
	if err == nil {
		t.Fatal("throw returned no error")
	}
	var exception *moejs.Exception
	if !errors.As(err, &exception) {
		t.Fatalf("run error = %T, want *moejs.Exception", err)
	}
	if exception.Name() != "Error" {
		t.Fatalf("exception name = %q, want Error (the hints read Name)", exception.Name())
	}
}

// TestMoejsSyntaxErrorThrownAtRuntimeIsStillAnException pins why
// describeScriptError checks Exception.Name() as well: source compiled at runtime
// through eval is parsed when it runs, so a SyntaxError arrives as a thrown
// value, not as a compile-time *SyntaxError. The model must not go looking for a
// runtime bug it cannot find.
func TestMoejsSyntaxErrorThrownAtRuntimeIsStillAnException(t *testing.T) {
	rt := newProbeRuntime(t)
	script, err := moejs.CompileScript("probe.js", "eval('function {')")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = rt.RunScript(script)
	var exception *moejs.Exception
	if !errors.As(err, &exception) || exception.Name() != "SyntaxError" {
		t.Fatalf("runtime syntax error = %v (%T), want a *moejs.Exception named SyntaxError", err, err)
	}
}

// TestMoejsHostErrorBecomesACatchableThrow pins the boundary the host tools rely
// on: a NativeFunc that returns an error throws in JavaScript, so a script can
// catch a failed tool call and keep going. The wrapped Go error stays reachable
// through errors.Is, which is what lets a host test assert on the cause.
func TestMoejsHostErrorBecomesACatchableThrow(t *testing.T) {
	rt := newProbeRuntime(t)
	sentinel := errors.New("tool failed")
	if err := rt.SetGlobal("explode", map[string]any{
		"go": moejs.NativeFunc(func(*engine.Realm, engine.Value, []engine.Value) (engine.Value, error) {
			return engine.Undefined(), sentinel
		}),
	}); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}

	caught := runProbe(t, rt, `(function () {
		try { explode.go(); return "no-throw" } catch (e) { return "caught" }
	})()`)
	out, err := rt.ToGo(caught)
	if err != nil {
		t.Fatalf("ToGo: %v", err)
	}
	if out != "caught" {
		t.Fatalf("host error handling = %v, want the script to catch it", out)
	}

	// An uncaught host error still reaches Go as an error, so the tool reports a
	// failure rather than losing it.
	script, err := moejs.CompileScript("probe.js", "explode.go()")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := rt.RunScript(script); err == nil {
		t.Fatal("uncaught host error returned no error")
	} else if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to unwrap to the Go cause", err)
	}
}

// TestMoejsToolsNamespaceShape pins the call shape the tool's description
// promises: an object of named arguments reaching a host function that receives
// Go values. The tool converts args[0] to Go before running anything, so this is
// the exact seam that conversion happens at.
func TestMoejsToolsNamespaceShape(t *testing.T) {
	rt := newProbeRuntime(t)
	got := ""
	if err := rt.SetGlobal("tools", map[string]any{
		"read_file": moejs.NativeFunc(func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
			input, ok := r.ToGo(args[0]).(map[string]any)
			if !ok {
				return engine.Undefined(), errors.New("argument was not an object")
			}
			path, _ := input["path"].(string)
			got = path
			return engine.StringValue(engine.FromGoString("contents of " + path)), nil
		}),
	}); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}

	value := runProbe(t, rt, `tools.read_file({path: "a.txt"})`)
	out, err := rt.ToGo(value)
	if err != nil {
		t.Fatalf("ToGo: %v", err)
	}
	if out != "contents of a.txt" {
		t.Fatalf("result = %v", out)
	}
	if got != "a.txt" {
		t.Fatalf("host saw path = %q, want a.txt", got)
	}
}

// TestMoejsGlobalThisSurvivesAcrossScripts is the code-mode contract: the kept
// runtime is how a session builds up work, so a value assigned to globalThis must
// still be there for the next script.
func TestMoejsGlobalThisSurvivesAcrossScripts(t *testing.T) {
	rt := newProbeRuntime(t)
	runProbe(t, rt, "globalThis.kept = 40")
	out, err := rt.ToGo(runProbe(t, rt, "globalThis.kept + 2"))
	if err != nil {
		t.Fatalf("ToGo: %v", err)
	}
	if out != int64(42) && out != 42 {
		t.Fatalf("globalThis value = %v (%T), want 42", out, out)
	}
}

// TestMoejsLocalDeclarationDoesNotSurvive records a limitation the design
// documents rather than fixes: a declaration made inside eval belongs to the eval
// scope. The tool answers this by telling the model to keep state on globalThis.
func TestMoejsLocalDeclarationDoesNotSurvive(t *testing.T) {
	rt := newProbeRuntime(t)
	runProbe(t, rt, `eval("const local = 1")`)
	out, err := rt.ToGo(runProbe(t, rt, "typeof local"))
	if err != nil {
		t.Fatalf("ToGo: %v", err)
	}
	if out != "undefined" {
		t.Fatalf("eval-local binding = %v, want it gone with the eval scope", out)
	}
}

// TestMoejsRepeatedGlobalDeclarationThrows pins the other half of that contract:
// a kept runtime rejects a second top-level const of the same name before
// anything runs, which is why the tool wraps code-mode source in eval.
func TestMoejsRepeatedGlobalDeclarationThrows(t *testing.T) {
	rt := newProbeRuntime(t)
	runProbe(t, rt, "const repeated = 1")
	script, err := moejs.CompileScript("probe.js", "const repeated = 2")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := rt.RunScript(script); err == nil {
		t.Fatal("a repeated top-level const ran; the tool's eval wrapper is no longer needed")
	}
}

// TestMoejsInterruptStopsARunningScript is the only cancellation model the tool
// has: RunScript is one blocking call, so a deadline can only reach a runaway
// loop through Interrupt. ClearInterrupt must then let the next script run,
// because a kept session reuses the runtime.
func TestMoejsInterruptStopsARunningScript(t *testing.T) {
	rt := newProbeRuntime(t)
	script, err := moejs.CompileScript("probe.js", "while (true) {}")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := rt.RunScript(script); done <- err }()

	time.Sleep(50 * time.Millisecond)
	rt.Interrupt(context.DeadlineExceeded)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted script returned no error")
		}
		var interrupted *moejs.InterruptedError
		if !errors.As(err, &interrupted) {
			t.Fatalf("error = %T, want *moejs.InterruptedError", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("interrupt took %v", elapsed)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("script did not stop after Interrupt")
	}

	// A kept runtime must be usable after an interrupt, or one timeout would end
	// the session's ability to script.
	rt.ClearInterrupt()
	if got := jsonOf(t, rt, runProbe(t, rt, "1 + 1")); got != "2" {
		t.Fatalf("after ClearInterrupt the runtime returned %s, want 2", got)
	}
}

// TestMoejsMemoryLimitIsATypedError pins that the memory limit reports itself as
// such. The model acts on allocate less only if it is told that, and the tool
// separates this error from a deadline.
func TestMoejsMemoryLimitIsATypedError(t *testing.T) {
	rt := moejs.NewRuntime(moejs.Options{MemoryLimit: 2 << 20})
	script, err := moejs.CompileScript("probe.js", `
		var held = [];
		while (true) { held.push(new Array(10000).fill(0)); }
	`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = rt.RunScript(script)
	if err == nil {
		t.Fatal("the script outran a 2 MiB limit without an error")
	}
	var limit *moejs.MemoryLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("error = %v (%T), want *moejs.MemoryLimitError", err, err)
	}
}

// TestMoejsHasNoConsoleOrPrint records that the engine ships no console. The tool
// installs one because every model writing JavaScript reaches for console.log, and
// its absence reads as a broken tool rather than a missing one.
func TestMoejsHasNoConsoleOrPrint(t *testing.T) {
	rt := newProbeRuntime(t)
	for _, name := range []string{"console", "print"} {
		out, err := rt.ToGo(runProbe(t, rt, "typeof "+name))
		if err != nil {
			t.Fatalf("ToGo: %v", err)
		}
		if out != "undefined" {
			t.Fatalf("typeof %s = %v, want undefined: the engine has none, so the tool adds it", name, out)
		}
	}
}

// TestMoejsToGoNormalisesNullish pins the conversion scriptResult relies on:
// undefined and null both arrive as nil, so a script with no useful value reports
// an empty result rather than the string undefined.
func TestMoejsToGoNormalisesNullish(t *testing.T) {
	rt := newProbeRuntime(t)
	for _, source := range []string{"undefined", "null"} {
		value, err := rt.ToGo(runProbe(t, rt, source))
		if err != nil {
			t.Fatalf("ToGo(%s): %v", source, err)
		}
		if value != nil {
			t.Fatalf("%s converted to %v (%T), want nil", source, value, value)
		}
	}
}

// TestMoejsStringResultIsReturnedWhole pins that a string completion value
// arrives without JSON quoting. The wrapper returns strings as-is on purpose:
// quoting would put visible quotes around every file a script echoes back.
func TestMoejsStringResultIsReturnedWhole(t *testing.T) {
	rt := newProbeRuntime(t)
	value, err := rt.ToGo(runProbe(t, rt, `"alpha\nbeta"`))
	if err != nil {
		t.Fatalf("ToGo: %v", err)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("value = %T, want string", value)
	}
	if text != "alpha\nbeta" {
		t.Fatalf("value = %q, want the raw text", text)
	}
	if strings.HasPrefix(text, `"`) {
		t.Fatal("the string was JSON-quoted; the wrapper returns strings as-is")
	}
}
