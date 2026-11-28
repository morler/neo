package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/session"
)

// TestNearestToolNameNamesTheLikelyFix keeps the warning actionable. A message
// that only says "not a tool name" leaves the user to guess which one they meant.
func TestNearestToolNameNamesTheLikelyFix(t *testing.T) {
	names := []string{"bash", "edit_file", "glob", "grep", "read_file", "write_file"}
	tests := []struct {
		rule string
		want string
	}{
		{"write_fiel", "write_file"},
		{"read_flie", "read_file"},
		{"grep", "grep"},
		{"edt_file", "edit_file"},
	}
	for _, tt := range tests {
		if got := nearestToolName(tt.rule, names); got != tt.want {
			t.Fatalf("nearestToolName(%q) = %q, want %q", tt.rule, got, tt.want)
		}
	}
}

func TestNearestToolNameSurvivesAnEmptyRegistry(t *testing.T) {
	if got := nearestToolName("write_fiel", nil); got == "" {
		t.Fatal("nearestToolName returned an empty message")
	}
}

// TestBuildChatRuntimeWarnsAboutAMisspelledToolApproval is the wiring check.
// The rule cannot be an error, because a tool_approvals entry may also be a bash
// prefix, so startup is the only place that can report it -- and if nothing
// reports it, the confirmation the user asked for silently never happens.
func TestBuildChatRuntimeWarnsAboutAMisspelledToolApproval(t *testing.T) {
	cfg := keylessConfig()
	cfg.Features.CodeMode = config.CodeModeClassic
	cfg.ToolApprovals = []string{"write_fiel", "git"}
	cwd := t.TempDir()
	var out, errOut bytes.Buffer

	buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		&session.Session{}, profile.Profile{}, cwd, cwd, stdio{out: &out, err: &errOut})

	got := errOut.String()
	if !strings.Contains(got, "\"write_fiel\" is not a tool name") {
		t.Fatalf("stderr = %q, want the misspelling warning", got)
	}
	if !strings.Contains(got, "write_file") {
		t.Fatalf("stderr = %q, want the suggestion to name write_file", got)
	}
	// "git" is a command prefix by design and must not be warned about; a
	// warning on every ordinary entry is one the user learns to ignore.
	if strings.Contains(got, "\"git\" is not a tool name") {
		t.Fatalf("stderr = %q, want no warning for a command prefix", got)
	}
}

// TestBuildChatRuntimeIsQuietAboutCorrectApprovals pins the other half: a config
// with only real tool names and command prefixes prints nothing new.
func TestBuildChatRuntimeIsQuietAboutCorrectApprovals(t *testing.T) {
	cfg := keylessConfig()
	cfg.Features.CodeMode = config.CodeModeClassic
	cfg.ToolApprovals = []string{"write_file", "rm -rf"}
	cwd := t.TempDir()
	var out, errOut bytes.Buffer

	buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		&session.Session{}, profile.Profile{}, cwd, cwd, stdio{out: &out, err: &errOut})

	if got := errOut.String(); strings.Contains(got, "is not a tool name") {
		t.Fatalf("stderr = %q, want no warning", got)
	}
}
