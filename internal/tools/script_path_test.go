package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/tools"
)

// TestRelativePathsResolveAgainstTheScriptDirectory covers #95: the file tools
// used the process directory while run() used the script's, so "./a.txt" meant
// two different files. In a one-tool session there is no cd to work around it.
func TestRelativePathsResolveAgainstTheScriptDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := codeModeScript(t, dir)

	out, err := s.Run(context.Background(), map[string]any{
		"source": `tools.write_file({path: "./b.txt", content: "two\n"}); tools.read_file({path: "./b.txt"})`,
	})
	if err != nil {
		t.Fatalf("relative write then read failed: %v", err)
	}
	if !strings.Contains(out, "two") {
		t.Fatalf("result = %q, want the content just written", out)
	}
	written, err := os.ReadFile(filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatalf("the write did not land in the script directory: %v", err)
	}
	if string(written) != "two\n" {
		t.Fatalf("b.txt = %q", written)
	}
}

// TestRelativePathsKeepTheReadAndEditInStep is the reason resolution happens
// before approval and before the tool runs: the stale-read guard keys on the
// path, so a read through one spelling and an edit through another would look
// like editing a file nobody read.
func TestRelativePathsKeepTheReadAndEditInStep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := codeModeScript(t, dir)

	out, err := s.Run(context.Background(), map[string]any{
		"source": `tools.read_file({path: "./a.txt"});
tools.edit_file({path: "./a.txt", old_string: "one", new_string: "ONE"});
tools.read_file({path: "./a.txt"})`,
	})
	if err != nil {
		t.Fatalf("read then edit through one relative path failed: %v", err)
	}
	if !strings.Contains(out, "ONE") {
		t.Fatalf("result = %q, want the edited content", out)
	}
}

// TestNoScriptDirectoryKeepsProcessRelativePaths pins the unchanged default: a
// Script with no CWD still resolves against the process directory.
func TestNoScriptDirectoryKeepsProcessRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := append([]tools.Tool{
		tools.Grep{Root: dir},
		tools.Glob{Root: dir},
	}, tools.NewFileTools()...)
	s := tools.NewScript(files)

	// An absolute path still works, and a relative one is left exactly as given.
	out, err := s.Run(context.Background(), map[string]any{
		"source": `tools.read_file({path: "` + path + `"})`,
	})
	if err != nil {
		t.Fatalf("absolute read failed: %v", err)
	}
	if !strings.Contains(out, "one") {
		t.Fatalf("result = %q", out)
	}
}
