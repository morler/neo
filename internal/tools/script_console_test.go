package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// defaultScript is the default surface: script on, no solo mode, no session.
// These tests cover the mode #91 left behind, where console was added only to
// the single-tool namespace.
func defaultScript(t *testing.T) tools.Script {
	t.Helper()
	dir := t.TempDir()
	files := append([]tools.Tool{
		tools.Grep{Root: dir},
		tools.Glob{Root: dir},
	}, tools.NewFileTools()...)
	return tools.NewScript(files)
}

// TestDefaultModeConsoleLogReachesTheResult is the defect itself: a default
// session's script that logged and returned a value lost both, because the
// engine has no console and nothing supplied one.
func TestDefaultModeConsoleLogReachesTheResult(t *testing.T) {
	s := defaultScript(t)
	out, err := s.Run(context.Background(), map[string]any{
		"source": `console.log("printed", 1); "value"`,
	})
	if err != nil {
		t.Fatalf("console.log failed in the default mode: %v", err)
	}
	for _, want := range []string{"printed 1", "value"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result %q lacks %q", out, want)
		}
	}
}

// TestDefaultModePrintReachesTheResult covers the other verb models reach for.
func TestDefaultModePrintReachesTheResult(t *testing.T) {
	s := defaultScript(t)
	out, err := s.Run(context.Background(), map[string]any{
		"source": `print("printed"); 2`,
	})
	if err != nil {
		t.Fatalf("print failed in the default mode: %v", err)
	}
	if !strings.Contains(out, "printed") || !strings.Contains(out, "2") {
		t.Fatalf("result = %q, want both the printed line and the value", out)
	}
}

// TestDefaultModeHasNoRun keeps the split intact: run() belongs to the one-tool
// mode, where bash is absent. The default surface has bash for programs.
func TestDefaultModeHasNoRun(t *testing.T) {
	s := defaultScript(t)
	out, err := s.Run(context.Background(), map[string]any{"source": `typeof run`})
	if err != nil {
		t.Fatal(err)
	}
	if out != "undefined" {
		t.Fatalf("typeof run = %q, want undefined in the default mode", out)
	}
}

// TestDefaultModeKeepsOutputBeforeAFailure is the same rule the solo mode has:
// what a failing script printed is usually why it printed.
func TestDefaultModeKeepsOutputBeforeAFailure(t *testing.T) {
	s := defaultScript(t)
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

// TestDefaultModeConsoleIsPerCall pins that the console adds no state: the
// default mode builds a runtime per call, so output from an earlier call must not
// appear in a later one.
func TestDefaultModeConsoleIsPerCall(t *testing.T) {
	s := defaultScript(t)
	if _, err := s.Run(context.Background(), map[string]any{"source": `console.log("first")`}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(context.Background(), map[string]any{"source": `"second"`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "first") {
		t.Fatalf("result %q carried output from the previous call", out)
	}
}
