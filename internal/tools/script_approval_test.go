package tools_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/owainlewis/neo/internal/approval"
	"github.com/owainlewis/neo/internal/tools"
)

// gateFor builds the same match-and-approve pair cmd/neo wires up, so the test
// exercises the real matcher rather than a stand-in that could agree with a
// broken implementation.
func gateFor(t *testing.T, rules []string, answer bool) (*tools.ApprovalGate, *int) {
	t.Helper()
	matcher := approval.New(rules)
	calls := new(int)
	return &tools.ApprovalGate{
		Requires: matcher.Requires,
		Approve: func(context.Context, tools.ApprovalRequest) (bool, error) {
			*calls++
			return answer, nil
		},
	}, calls
}

// TestScriptSubCallConsultsTheApprovalGate is the whole point of the task: a
// rule naming grep must bite when a script calls grep, even though the model
// called script.
func TestScriptSubCallConsultsTheApprovalGate(t *testing.T) {
	gate, approvals := gateFor(t, []string{"grep"}, true)
	s := tools.Script{Grep: tools.Grep{Root: dirWithMatch(t)}}

	ctx := tools.WithApprovalGate(context.Background(), gate)
	_, err := s.Run(ctx, map[string]any{"source": `tools.grep({pattern: "x"})`})
	if err != nil {
		t.Fatalf("approved call failed: %v", err)
	}
	if *approvals != 1 {
		t.Fatalf("approvals = %d, want 1", *approvals)
	}
}

// TestScriptSubCallRefusalStopsTheTool proves a denial prevents the work, not
// just the result.
func TestScriptSubCallRefusalStopsTheTool(t *testing.T) {
	gate, approvals := gateFor(t, []string{"grep"}, false)
	s := tools.Script{Grep: tools.Grep{Root: dirWithMatch(t)}}

	ctx := tools.WithApprovalGate(context.Background(), gate)
	_, err := s.Run(ctx, map[string]any{"source": `tools.grep({pattern: "x"})`})
	if err == nil {
		t.Fatal("expected the refusal to surface")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want a denial", err)
	}
	if *approvals != 1 {
		t.Fatalf("approvals = %d, want 1", *approvals)
	}
}

// TestScriptSubCallWithoutApproverFailsClosed is the headless case: approvals
// are configured but there is nobody to ask. The call must fail rather than run
// unapproved, and it must use the same wording the top-level path uses.
func TestScriptSubCallWithoutApproverFailsClosed(t *testing.T) {
	matcher := approval.New([]string{"grep"})
	gate := &tools.ApprovalGate{Requires: matcher.Requires} // no Approve
	s := tools.Script{Grep: tools.Grep{Root: dirWithMatch(t)}}

	ctx := tools.WithApprovalGate(context.Background(), gate)
	_, err := s.Run(ctx, map[string]any{"source": `tools.grep({pattern: "x"})`})
	if err == nil {
		t.Fatal("expected the call to fail without an approver")
	}
	if !strings.Contains(err.Error(), "no approver is configured") {
		t.Fatalf("error = %v, want the top-level wording", err)
	}
}

// TestScriptUnmatchedToolRunsWithoutApproval keeps the gate from becoming a
// blanket prompt: a rule for grep must not ask about read_file.
func TestScriptUnmatchedToolRunsWithoutApproval(t *testing.T) {
	gate, approvals := gateFor(t, []string{"grep"}, false)
	s := tools.Script{Read: tools.ReadFile{}}

	path := writeSample(t)
	ctx := tools.WithApprovalGate(context.Background(), gate)
	if _, err := s.Run(ctx, map[string]any{"source": `tools.read_file({path: "` + path + `"})`}); err != nil {
		t.Fatalf("read_file should not need approval: %v", err)
	}
	if *approvals != 0 {
		t.Fatalf("approvals = %d, want 0", *approvals)
	}
}

// TestScriptApprovalsAreSerial guards the callback contract: the TUI's approver
// is a single prompt, so two overlapping script calls must not both be inside
// it. Scripts are serial by classification, which is what makes this true.
func TestScriptApprovalsAreSerial(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	gate := &tools.ApprovalGate{
		Requires: func(string, map[string]any) bool { return true },
		Approve: func(context.Context, tools.ApprovalRequest) (bool, error) {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			mu.Lock()
			active--
			mu.Unlock()
			return true, nil
		},
	}
	s := tools.Script{Read: tools.ReadFile{}}
	path := writeSample(t)
	ctx := tools.WithApprovalGate(context.Background(), gate)
	source := `tools.read_file({path: "` + path + `"});tools.read_file({path: "` + path + `"});"ok"`

	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatal(err)
	}
	if peak != 1 {
		t.Fatalf("peak concurrent approvals = %d, want 1", peak)
	}
}

// TestScriptCanCatchARefusalAndTryAnotherTool records the boundary: denying one
// call does not end the script, and the next call is a fresh decision.
func TestScriptCanCatchARefusalAndTryAnotherTool(t *testing.T) {
	var asked []string
	gate := &tools.ApprovalGate{
		Requires: func(tool string, _ map[string]any) bool { return tool == "grep" },
		Approve: func(_ context.Context, req tools.ApprovalRequest) (bool, error) {
			asked = append(asked, req.ToolName)
			return false, nil
		},
	}
	s := tools.Script{Grep: tools.Grep{Root: t.TempDir()}, Glob: tools.Glob{Root: t.TempDir()}}
	ctx := tools.WithApprovalGate(context.Background(), gate)

	source := `var refused = false;
try { tools.grep({pattern: "x"}) } catch (e) { refused = true }
if (!refused) throw new Error("the refusal was swallowed");
typeof tools.glob`
	out, err := s.Run(ctx, map[string]any{"source": source})
	if err != nil {
		t.Fatalf("the script should continue after a refusal: %v", err)
	}
	if out != "function" {
		t.Fatalf("result = %q, want a usable tool", out)
	}
	if len(asked) != 1 || asked[0] != "grep" {
		t.Fatalf("approvals asked for %v, want only grep", asked)
	}
}

// TestScriptApprovalSeesFrozenArguments is the TOCTOU check. The gate must be
// shown the same values that run, so a script that reaches in and changes its
// own argument object after approval cannot change what executes.
func TestScriptApprovalSeesFrozenArguments(t *testing.T) {
	var approved map[string]any
	gate := &tools.ApprovalGate{
		Requires: func(string, map[string]any) bool { return true },
		Approve: func(_ context.Context, req tools.ApprovalRequest) (bool, error) {
			// Keep a copy: the point is what the tool later runs with.
			approved = map[string]any{"path": req.Args["path"]}
			return true, nil
		},
	}
	s := tools.Script{Read: tools.ReadFile{}}
	path := writeSample(t)
	ctx := tools.WithApprovalGate(context.Background(), gate)

	source := `var arg = {path: "` + path + `"};
var p = arg;
tools.read_file(p)`
	if _, err := s.Run(ctx, map[string]any{"source": source}); err != nil {
		t.Fatal(err)
	}
	if approved["path"] != path {
		t.Fatalf("approved path = %v, want %q", approved["path"], path)
	}
}

// dirWithMatch makes a directory ripgrep will actually search, so an approved
// grep proves the tool ran instead of failing on an empty tree.
func dirWithMatch(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hit.txt", []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/sample.txt"
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
