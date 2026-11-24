package tools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/tools"
)

// TestScriptStopsAtItsDeadline covers the failure the tool cannot otherwise
// survive: a script the engine will never finish on its own.
func TestScriptStopsAtItsDeadline(t *testing.T) {
	s, _ := newScript(t)
	s.Timeout = 100 * time.Millisecond

	start := time.Now()
	_, err := runScript(t, s, "while (true) {}")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the script to stop")
	}
	if !strings.Contains(err.Error(), "script stopped") {
		t.Fatalf("error = %v, want it to name a stop", err)
	}
	// Generous upper bound: this asserts the interrupt worked, not that the
	// machine is fast.
	if elapsed > 5*time.Second {
		t.Fatalf("script ran %v; the deadline did not interrupt it", elapsed)
	}
}

// TestScriptStopsAtItsMemoryLimit checks the message that tells the model to
// allocate less rather than finish sooner.
func TestScriptStopsAtItsMemoryLimit(t *testing.T) {
	s, _ := newScript(t)
	s.MemoryLimit = 4 << 20

	_, err := runScript(t, s, "var a = []; while (true) { a.push(new Array(4096).fill(1)) }")
	if err == nil {
		t.Fatal("expected the script to stop")
	}
	if !strings.Contains(err.Error(), "memory limit") {
		t.Fatalf("error = %v, want it to name the memory limit", err)
	}
	if strings.Contains(err.Error(), "script stopped") {
		t.Fatalf("error = %v, want it distinct from a deadline stop", err)
	}
}

// TestScriptTruncatesLargeOutput keeps one script from spending the whole
// transcript budget, and reuses the agent's marker so a reader cannot tell
// which layer cut the output.
func TestScriptTruncatesLargeOutput(t *testing.T) {
	s, _ := newScript(t)
	source := `"x".repeat(2 * 1024 * 1024)`

	out, err := runScript(t, s, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > tools.MaxOutputBytes {
		t.Fatalf("output is %d bytes, want at most %d", len(out), tools.MaxOutputBytes)
	}
	for _, want := range []string{"[tool output truncated:", "original 2097152 bytes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output does not contain %q", want)
		}
	}
}

// TestScriptHonorsCallerCancellation proves cancellation does not wait out the
// script's own deadline. The timeout here is far longer than the test, so a
// passing test means ctx itself stopped the engine.
func TestScriptHonorsCallerCancellation(t *testing.T) {
	s, _ := newScript(t)
	s.Timeout = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, map[string]any{"source": "while (true) {}"})
		done <- err
	}()

	// Give the script a moment to start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not stop the script")
	}
}

// TestScriptStaysUsableAfterAnInterrupt records that a stopped runtime leaves
// nothing behind: the next call runs normally. This is why the tool creates a
// runtime per call instead of pooling one.
func TestScriptStaysUsableAfterAnInterrupt(t *testing.T) {
	s, _ := newScript(t)
	s.Timeout = 50 * time.Millisecond
	if _, err := runScript(t, s, "while (true) {}"); err == nil {
		t.Fatal("expected the first script to stop")
	}

	s.Timeout = time.Minute
	out, err := runScript(t, s, "1 + 41")
	if err != nil {
		t.Fatalf("second script failed: %v", err)
	}
	if out != "42" {
		t.Fatalf("second script returned %q, want 42", out)
	}
}

// TestScriptDefaultsAreNonZero guards the one mistake that would remove every
// limit at once: leaving the defaults unset.
func TestScriptDefaultsAreNonZero(t *testing.T) {
	if tools.DefaultScriptTimeout <= 0 {
		t.Fatalf("DefaultScriptTimeout = %v", tools.DefaultScriptTimeout)
	}
	if tools.DefaultScriptMemoryLimit <= 0 {
		t.Fatalf("DefaultScriptMemoryLimit = %d", tools.DefaultScriptMemoryLimit)
	}
}
