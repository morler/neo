package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/approval"
	"github.com/owainlewis/neo/internal/tools"
)

// TestScriptWriteRequiresApproval is the bypass check: a rule naming write_file
// must fire when a script writes, and the file must not change until it does.
func TestScriptWriteRequiresApproval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	matcher := approval.New([]string{"write_file"})

	asked := 0
	gate := &tools.ApprovalGate{
		Requires: matcher.Requires,
		Approve: func(_ context.Context, req tools.ApprovalRequest) (bool, error) {
			asked++
			if req.Parent != "script" {
				t.Errorf("approval parent = %q, want script", req.Parent)
			}
			return false, nil
		},
	}
	s := tools.Script{Write: tools.WriteFile{}}
	ctx := tools.WithApprovalGate(context.Background(), gate)

	source := `tools.write_file({path: "` + path + `", content: "x"})`
	if _, err := s.Run(ctx, map[string]any{"source": source}); err == nil {
		t.Fatal("expected the refusal to surface")
	}
	if asked != 1 {
		t.Fatalf("approvals = %d, want 1", asked)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a denied write created the file")
	}
}

// TestScriptWriteAfterApprovalLands proves the positive path, so the test above
// cannot pass by refusing everything.
func TestScriptWriteAfterApprovalLands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	matcher := approval.New([]string{"write_file"})
	gate := &tools.ApprovalGate{
		Requires: matcher.Requires,
		Approve:  func(context.Context, tools.ApprovalRequest) (bool, error) { return true, nil },
	}
	s := tools.Script{Write: tools.WriteFile{}}
	ctx := tools.WithApprovalGate(context.Background(), gate)

	source := `tools.write_file({path: "` + path + `", content: "written"})`
	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "written" {
		t.Fatalf("file = %q, want written", body)
	}
}

// TestScriptWriteWithoutApproverDoesNotWrite is the headless case: nobody can
// approve, so nothing may be written.
func TestScriptWriteWithoutApproverDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	matcher := approval.New([]string{"write_file"})
	gate := &tools.ApprovalGate{Requires: matcher.Requires} // no approver
	s := tools.Script{Write: tools.WriteFile{}}
	ctx := tools.WithApprovalGate(context.Background(), gate)

	source := `tools.write_file({path: "` + path + `", content: "x"})`
	_, err := s.Run(ctx, map[string]any{"source": source})
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	if !strings.Contains(err.Error(), "no approver is configured") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("a write without an approver created the file")
	}
}

// TestScriptApprovalAndExecutionShareOneArgumentCopy is the TOCTOU case. The
// gate records what it saw, the tool writes what it was given, and a script
// that rewrites its own object between the two must not change either.
func TestScriptApprovalAndExecutionShareOneArgumentCopy(t *testing.T) {
	dir := t.TempDir()
	approvedPath := filepath.Join(dir, "approved.txt")
	otherPath := filepath.Join(dir, "other.txt")

	var seen string
	gate := &tools.ApprovalGate{
		Requires: func(string, map[string]any) bool { return true },
		Approve: func(_ context.Context, req tools.ApprovalRequest) (bool, error) {
			seen, _ = req.Args["path"].(string)
			return true, nil
		},
	}
	s := tools.Script{Write: tools.WriteFile{}}
	ctx := tools.WithApprovalGate(context.Background(), gate)

	// The script hands its object to the tool, then mutates it. The tool already
	// received a Go copy, so the mutation must not reach the file it writes.
	source := `var arg = {path: "` + approvedPath + `", content: "approved"};
tools.write_file(arg);
arg.path = "` + otherPath + `";
arg.content = "other";
"done"`
	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatal(err)
	}
	if seen != approvedPath {
		t.Fatalf("gate saw %q, want %q", seen, approvedPath)
	}
	if _, err := os.Stat(otherPath); !os.IsNotExist(err) {
		t.Fatal("mutating the argument object changed what was written")
	}
	body, err := os.ReadFile(approvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "approved" {
		t.Fatalf("approved file = %q", body)
	}
}

// TestScriptCannotReachDangerousTools keeps the whitelist honest now that writes
// are allowed. A name that is absent cannot be approved into existence.
func TestScriptCannotReachDangerousTools(t *testing.T) {
	s := writeScriptForNames(t)
	for _, name := range []string{"bash", "agent", "goal", "workflow", "script"} {
		out, err := s.Run(context.Background(), map[string]any{"source": `typeof tools.` + name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != "undefined" {
			t.Fatalf("typeof tools.%s = %q, want undefined", name, out)
		}
	}
}

// TestScriptCannotReachNodeFS records the decision that file access stays on
// Neo's tools: a second path would have its own approval semantics.
func TestScriptCannotReachNodeFS(t *testing.T) {
	s := writeScriptForNames(t)
	for _, expr := range []string{
		`typeof require`,
		`typeof process`,
	} {
		out, err := s.Run(context.Background(), map[string]any{"source": expr})
		if err != nil {
			t.Fatal(err)
		}
		if out != "undefined" {
			t.Fatalf("%s = %q, want undefined", expr, out)
		}
	}
	// An import has no host resolver, so it must fail rather than load anything.
	if _, err := s.Run(context.Background(), map[string]any{"source": `import("fs")`}); err == nil {
		t.Fatal("import() should not resolve inside a script")
	}
}

// TestScriptEditFailureReachesTheScript checks the failure rule: a failed write
// is an error the script sees, not a silent partial result.
func TestScriptEditFailureReachesTheScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := tools.Script{Edit: tools.EditFile{}}
	source := `var message = "none";
try { tools.edit_file({path: "` + path + `", old_string: "missing", new_string: "x"}) }
catch (e) { message = "caught" }
message`
	out, err := s.Run(context.Background(), map[string]any{"source": source})
	if err != nil {
		t.Fatal(err)
	}
	if out != "caught" {
		t.Fatalf("result = %q, want the script to see the failure", out)
	}
}

func writeScriptForNames(t *testing.T) tools.Script {
	t.Helper()
	return tools.Script{
		Read:  tools.ReadFile{},
		Grep:  tools.Grep{Root: t.TempDir()},
		Glob:  tools.Glob{Root: t.TempDir()},
		Write: tools.WriteFile{},
		Edit:  tools.EditFile{},
	}
}
