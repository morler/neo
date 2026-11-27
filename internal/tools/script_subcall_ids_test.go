package tools_test

import (
	"context"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// Sub-call IDs key the UI's rows, so they must be unique across the whole
// script, not just within tools.* or within run(). codeModeExtras used to build
// a second namespace, which restarted the counter at #1: run()'s result then
// settled onto a read_file row, and a failed program marked a successful read
// as failed.
func TestCodeModeSubCallIDsAreUniqueAcrossToolsAndRun(t *testing.T) {
	dir := t.TempDir()
	s := codeModeScript(t, dir)

	var events []tools.SubCall
	ctx := tools.WithSubCallSink(context.Background(), func(call tools.SubCall) {
		events = append(events, call)
	})
	ctx = tools.WithCallMetadata(ctx, tools.CallMetadata{ToolUseID: "call_script"})

	// A program that cannot start is a real sub-call failure (a non-zero exit is
	// data, not an error), so it is the one that must not land on the glob row.
	// The script catches the throw and still succeeds.
	source := "tools.glob({pattern: '*'}); try { run(['/nonexistent/neo-probe']); } catch (e) {} 'done'"
	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatalf("script failed: %v", err)
	}

	ids := map[string]string{}
	failures := map[string]bool{}
	for _, ev := range events {
		if prev, ok := ids[ev.ID]; ok && prev != ev.Name {
			t.Fatalf("sub-call ID %q used by both %q and %q", ev.ID, prev, ev.Name)
		}
		ids[ev.ID] = ev.Name
		if ev.Done && ev.IsError {
			failures[ev.Name] = true
		}
	}
	if len(ids) != 2 {
		t.Fatalf("distinct sub-call IDs = %d (%v), want 2", len(ids), ids)
	}
	if !failures["run"] {
		t.Fatalf("run() exiting non-zero was not reported as a failed sub-call: %v", events)
	}
	if failures["glob"] {
		t.Fatal("a successful glob was reported as failed")
	}
}

// A run() sub-call has to announce its start, because the program it starts can
// take minutes. Without it the UI has nothing to draw while the command runs.
func TestRunSubCallReportsStartAndCompletion(t *testing.T) {
	dir := t.TempDir()
	s := codeModeScript(t, dir)

	var events []tools.SubCall
	ctx := tools.WithSubCallSink(context.Background(), func(call tools.SubCall) {
		events = append(events, call)
	})
	ctx = tools.WithCallMetadata(ctx, tools.CallMetadata{ToolUseID: "call_script"})

	source := "run(['echo', 'hi'])"
	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("run() reported %d events, want a start and a completion: %+v", len(events), events)
	}
	if events[0].Done {
		t.Fatalf("first event is already Done: %+v", events[0])
	}
	if events[0].ID != events[1].ID {
		t.Fatalf("start and completion IDs differ: %q vs %q", events[0].ID, events[1].ID)
	}
	if !events[1].Done {
		t.Fatalf("second event is not Done: %+v", events[1])
	}
}
