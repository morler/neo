package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestClipboardWritesThroughTheRealMessageLoop is an opt-in integration check:
// it needs a live clipboard service, so it runs only when NEO_CLIPBOARD_TEST is
// set. Everything else about the drag path is covered with a fake writer.
//
// Run it with:
//
//	NEO_CLIPBOARD_TEST=1 go test ./internal/tui -run TestClipboardWritesThroughTheRealMessageLoop -v
func TestClipboardWritesThroughTheRealMessageLoop(t *testing.T) {
	if os.Getenv("NEO_CLIPBOARD_TEST") == "" {
		t.Skip("set NEO_CLIPBOARD_TEST=1 to exercise the system clipboard")
	}
	if _, err := exec.LookPath("wl-paste"); err != nil {
		t.Skip("no wl-paste to verify the clipboard")
	}

	m := selectableModel()
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 0, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 10, Y: 1, Button: tea.MouseLeft}))
	_, cmd := m.Update(tea.MouseReleaseMsg(tea.Mouse{X: 10, Y: 1, Button: tea.MouseLeft}))
	if cmd == nil {
		t.Fatal("release produced no copy command")
	}
	msg := cmd()
	clip, ok := msg.(clipboardResultMsg)
	if !ok {
		t.Fatalf("copy result = %T", msg)
	}
	if clip.via != "local" {
		t.Skipf("local clipboard unavailable (%s); the OSC52 path is not observable from a test", clip.via)
	}
	m.Update(clip)

	out, err := exec.Command("wl-paste", "--no-newline").Output()
	if err != nil {
		t.Fatalf("read clipboard: %v", err)
	}
	if got, want := string(out), "alpha beta\ngamma delta"; got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
	if got := plain(m.statusLine()); !strings.Contains(got, "copied") {
		t.Fatalf("status line lost the copy confirmation: %q", got)
	}
}
