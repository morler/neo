package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/logx"
)

// TestScriptSubCallsAreLoggableCharacterisesTheMergeSignal covers #101: the
// merge rate has to be decidable from NEO_LOG alone, since a headless run has no
// SubCall sink. The log has to name each sub-call and whether it started or
// finished, so "one script that read and wrote" is a fact rather than a reading
// of the final answer.
func TestScriptSubCallsAreLoggableCharacterisesTheMergeSignal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "neo.log")
	t.Setenv("NEO_LOG", logPath)
	t.Setenv("NEO_LOG_VERBOSE", "1")
	if err := logx.InitFromEnv(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logx.Close() })

	s := NewScript(nil)
	s.Read = ReadFile{}
	s.Edit = EditFile{}
	s.Session = &ScriptSession{}
	if _, err := s.Run(context.Background(), map[string]any{"source": "const t = tools.read_file({path: " + jsString(path) + "}); tools.edit_file({path: " + jsString(path) + ", old_string: \"two\", new_string: \"three\"}); t"}); err != nil {
		t.Fatal(err)
	}

	log := readLog(t, logPath)
	for _, want := range []string{`"msg":"script sub-call"`, `"name":"read_file"`, `"name":"edit_file"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
	// The shape, not the contents: paths and file bodies stay out of the log.
	if strings.Contains(log, path) {
		t.Fatalf("log leaked a path:\n%s", log)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	_ = logx.Close()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func jsString(s string) string { return "\"" + strings.ReplaceAll(s, "\\", "\\\\") + "\"" }
