package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

// stubTool stands in for a coordinating tool such as agent or goal: it adds a
// name to the surface without touching files or running programs, which is all
// the guard's condition depends on.
type stubTool struct{}

func (stubTool) Name() string                     { return "stub" }
func (stubTool) Spec() llm.ToolSpec               { return llm.ToolSpec{Name: "stub"} }
func (stubTool) ParallelSafe(map[string]any) bool { return false }
func (stubTool) Run(context.Context, map[string]any) (string, error) {
	return "", nil
}

func stubTools() []tools.Tool { return []tools.Tool{stubTool{}} }

// noToolWorkspace writes the configuration #102 is about: the default code mode
// with the script tool switched off. It returns the workspace directory.
func noToolWorkspace(t *testing.T, endpoint string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(dir)

	var b strings.Builder
	b.WriteString("provider: local\nmodel: qwen3-coder\nproviders:\n  local:\n")
	b.WriteString("    endpoint: " + endpoint + "\n")
	b.WriteString("    default_model: qwen3-coder\n")
	b.WriteString("    models:\n      - id: qwen3-coder\n        name: Qwen3 Coder\n")
	b.WriteString("features:\n  codemode: codemode\n  script: false\n")
	if err := os.WriteFile(filepath.Join(dir, "neo.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestHeadlessRunRefusesSessionWithNoTools is #102 end to end: the run must fail
// with an actionable message instead of answering prose while changing nothing.
func TestHeadlessRunRefusesSessionWithNoTools(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) { return textResponse("should not be asked") }}
	server := backend.start(t)
	noToolWorkspace(t, server.URL+"/v1/chat/completions")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	streams, out, errOut := bufferedStdio()
	if code := run([]string{"run", "--timeout", "30s", "hi"}, streams); code == 0 {
		t.Fatalf("run exited 0; stdout = %q", out.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", out.String())
	}
	for _, want := range []string{"no tools", "features.script: true", "codemode: classic"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr = %q, want it to mention %q", errOut.String(), want)
		}
	}
	if len(backend.requests) != 0 {
		t.Fatalf("the refusal must precede the first request, got %d", len(backend.requests))
	}
}

// TestRegistryForNoScriptSurface pins what the guard sees, so it keeps firing
// for the right reason: code mode with script off has no acting tools, and
// classic keeps bash whatever script says.
func TestRegistryForNoScriptSurface(t *testing.T) {
	off := false
	codeMode := &config.Config{}
	codeMode.Features.CodeMode = config.CodeModeCodemode
	codeMode.Features.Script = &off
	if got := registryFor(codeMode, "", "").Names(); len(got) != 0 {
		t.Fatalf("code mode without script registered %v, want nothing", got)
	}
	// The interactive surface supplies coordinating tools, which is why the
	// guard belongs at the headless entry point rather than in registryFor.
	withCoordinators := registryFor(codeMode, "", "", stubTools()...)
	if len(withCoordinators.Names()) == 0 {
		t.Fatal("coordinating tools should keep the interactive session alive")
	}
	classic := &config.Config{}
	classic.Features.CodeMode = config.CodeModeClassic
	classic.Features.Script = &off
	if _, ok := registryFor(classic, "", "").Get("bash"); !ok {
		t.Fatal("classic mode without script must keep bash, as before")
	}
}

// TestSurfaceNameMatchesTheConfigValue keeps the refusal message honest about
// which surface the user is in. It reads the same two constants the config
// uses, so a message cannot name a surface the config does not have.
func TestSurfaceNameMatchesTheConfigValue(t *testing.T) {
	codeMode := &config.Config{}
	codeMode.Features.CodeMode = config.CodeModeCodemode
	if got := surfaceName(codeMode); got != config.CodeModeCodemode {
		t.Fatalf("surfaceName(codemode) = %q", got)
	}
	classic := &config.Config{}
	classic.Features.CodeMode = config.CodeModeClassic
	if got := surfaceName(classic); got != config.CodeModeClassic {
		t.Fatalf("surfaceName(classic) = %q", got)
	}
	// An unset surface is the default one, so the message says so.
	if got := surfaceName(&config.Config{}); got != config.CodeModeCodemode {
		t.Fatalf("surfaceName(default) = %q", got)
	}
}
