package tools

import (
	"testing"

	"github.com/MoeForge/moejs"
)

// TestMoejsDependencyIsPinned keeps the engine in go.mod while nothing in the
// production build imports it yet. The script tool lands in a later step; until
// then a plain require would be removed by the next `go mod tidy`, and a
// production import would grow the binary before the budget is raised.
//
// It also pins the engine semantics the script tool depends on, so an upgrade
// that changes them fails here rather than in a confusing place later.
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
