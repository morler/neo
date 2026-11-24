package tools_test

import (
	"testing"

	"github.com/MoeForge/moejs"
)

// The engine is not in the production binary yet: the script tool lands in a
// later step, and this package is the only thing that imports the engine. The
// require in go.mod therefore stays because a test needs it, not because the
// shipped binary does.
//
// This test pins the engine semantics the script tool depends on, so an upgrade
// that changes them fails here rather than surfacing as a confusing tool result
// later.
func TestMoejsDependencyIsPinned(t *testing.T) {
	script, err := moejs.CompileScript("probe.js", "1 + 41")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rt := moejs.NewRuntime(moejs.Options{})
	value, err := rt.RunScript(script)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out, err := rt.AppendJSON(nil, value)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := string(out); got != "42" {
		t.Fatalf("RunScript result = %s, want the last expression's value", got)
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
