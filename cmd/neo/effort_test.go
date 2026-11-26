package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

// resetEffortOverride isolates the package-level flag between tests; it is
// written once at startup and would otherwise leak into the next case.
func resetEffortOverride(t *testing.T) {
	t.Helper()
	old := effortOverride
	t.Cleanup(func() { effortOverride = old })
	effortOverride = ""
}

func TestTakeEffortFlagAcceptsBothForms(t *testing.T) {
	resetEffortOverride(t)
	for _, args := range [][]string{
		{"--effort=HIGH", "run", "task"},
		{"--effort", "High", "run", "task"},
	} {
		effortOverride = ""
		rest := takeEffortFlag(args)
		if effortOverride != "high" {
			t.Fatalf("%v: effortOverride = %q, want high", args, effortOverride)
		}
		if strings.Join(rest, " ") != "run task" {
			t.Fatalf("%v: leftover args = %v, want the rest untouched", args, rest)
		}
	}
}

func TestTakeEffortFlagLeavesOtherArgsAlone(t *testing.T) {
	resetEffortOverride(t)
	rest := takeEffortFlag([]string{"run", "--json", "task"})
	if effortOverride != "" {
		t.Fatalf("effortOverride = %q, want empty", effortOverride)
	}
	if strings.Join(rest, " ") != "run --json task" {
		t.Fatalf("args = %v, want them unchanged", rest)
	}
}

func TestTakeEffortFlagPassesUnknownValuesThrough(t *testing.T) {
	// The flag does not police the vocabulary: the backend does, and its error
	// is what a user sees.
	resetEffortOverride(t)
	takeEffortFlag([]string{"--effort=ultra"})
	if effortOverride != "ultra" {
		t.Fatalf("effortOverride = %q, want ultra", effortOverride)
	}
}

// TestEffortFlagDoesNotRewriteTheConfig is the contract that separates --effort
// from /effort: a one-off invocation must not silently change the saved
// default.
func TestEffortFlagDoesNotRewriteTheConfig(t *testing.T) {
	resetEffortOverride(t)
	dir := t.TempDir()
	body := "provider: anthropic\nmodel: claude-opus-5\n"
	path := filepath.Join(dir, "neo.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rest := takeEffortFlag([]string{"--effort=high"})
	if strings.Join(rest, " ") != "" {
		t.Fatalf("args not consumed: %v", rest)
	}
	if effortOverride != "high" {
		t.Fatalf("effortOverride = %q, want high", effortOverride)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("--effort rewrote the config:\n%s", after)
	}
}

// TestLoadConfigAppliesTheEffortOverride checks the flag actually reaches the
// running config rather than only the package variable.
func TestLoadConfigAppliesTheEffortOverride(t *testing.T) {
	resetEffortOverride(t)
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "neo.yaml"),
		[]byte("provider: anthropic\nmodel: claude-opus-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	effortOverride = "high"
	var errOut strings.Builder
	cfg, ok := loadConfig(&errOut)
	if !ok {
		t.Fatalf("loadConfig failed: %s", errOut.String())
	}
	if cfg.Effort() != "high" {
		t.Fatalf("Effort() = %q, want high", cfg.Effort())
	}
	// The override is per-process, so the file must still report unset.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Effort() != "" {
		t.Fatalf("effort was persisted by the flag: %q", reloaded.Effort())
	}
}

func TestHeadlessResultReportsEffort(t *testing.T) {
	res := headlessResult{Provider: "anthropic", Model: "m", Effort: "low"}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"effort":"low"`) {
		t.Fatalf("headless summary lost the effort: %s", b)
	}
	empty, err := json.Marshal(headlessResult{Provider: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "effort") {
		t.Fatalf("unset effort should be omitted, got %s", empty)
	}
}
