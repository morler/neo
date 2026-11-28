package approval

import "testing"

func TestMatcherRequiresToolOrBashPrefix(t *testing.T) {
	matcher := New([]string{"write_file", "git", "rm -rf"})

	tests := []struct {
		name string
		tool string
		args map[string]any
		want bool
	}{
		{name: "exact tool", tool: "write_file", want: true},
		{name: "other tool", tool: "read_file", want: false},
		{name: "bash prefix", tool: "bash", args: map[string]any{"command": "git status"}, want: true},
		{name: "leading whitespace", tool: "bash", args: map[string]any{"command": "\t rm -rf build"}, want: true},
		{name: "exact command", tool: "bash", args: map[string]any{"command": "git"}, want: true},
		{name: "token boundary", tool: "bash", args: map[string]any{"command": "github status"}, want: false},
		{name: "case sensitive", tool: "bash", args: map[string]any{"command": "Git status"}, want: false},
		{name: "not at command start", tool: "bash", args: map[string]any{"command": "cd repo && git status"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matcher.Requires(tt.tool, tt.args); got != tt.want {
				t.Fatalf("Requires(%q, %#v) = %v, want %v", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestMatcherCopiesRules(t *testing.T) {
	rules := []string{"git"}
	matcher := New(rules)
	rules[0] = "rm"

	if !matcher.Requires("bash", map[string]any{"command": "git status"}) {
		t.Fatal("matcher rules changed with caller slice")
	}
}

// TestRequiresDoesNotMatchAMisspelledToolName is why #135 exists. A tool name
// with a typo is not an error, so it silently becomes a command prefix and the
// tool the user meant to gate runs unconfirmed. This test states the behaviour
// the warning covers; it does not claim the behaviour is acceptable.
func TestRequiresDoesNotMatchAMisspelledToolName(t *testing.T) {
	m := New([]string{"write_fiel"})

	if m.Requires("write_file", nil) {
		t.Fatal("a misspelled rule matched the real tool")
	}
	if !m.Requires("bash", map[string]any{"command": "write_fiel x"}) {
		t.Fatal("the misspelled rule did not act as a command prefix")
	}
	if m.Requires("bash", map[string]any{"command": "write_file x"}) {
		t.Fatal("a misspelled rule gated the real command")
	}
}

// TestUnknownToolsFindsOnlyToolShapedRules keeps the warning narrow. A rule that
// is a program ("git") or a command line ("rm -rf") is a prefix on purpose;
// warning about those would teach the user to ignore the warning.
func TestUnknownToolsFindsOnlyToolShapedRules(t *testing.T) {
	tools := []string{"bash", "read_file", "write_file", "grep"}
	tests := []struct {
		rule string
		want bool
	}{
		{"write_file", false}, // a real tool
		{"git", false},        // a bare program
		{"rm -rf", false},     // a command line
		{"git status", false}, // a command line
		{"WRITE_FILE", false},
		{"_", false},
		{"write_fiel", true}, // looks like a tool, names none
		{"read_flie", true},
		{"write_file2", false},
		{"read-file", false},
	}
	for _, tt := range tests {
		got := UnknownTools([]string{tt.rule}, tools)
		if (len(got) > 0) != tt.want {
			t.Fatalf("UnknownTools(%q) = %v, want flagged=%v", tt.rule, got, tt.want)
		}
	}
}

func TestUnknownToolsReturnsEveryOffenderInOrder(t *testing.T) {
	got := UnknownTools([]string{"write_fiel", "git", "read_flie"}, []string{"write_file", "read_file"})
	want := []string{"write_fiel", "read_flie"}
	if len(got) != len(want) {
		t.Fatalf("UnknownTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("UnknownTools = %v, want %v", got, want)
		}
	}
}

func TestUnknownToolsIsEmptyWithoutRules(t *testing.T) {
	if got := UnknownTools(nil, []string{"bash"}); len(got) != 0 {
		t.Fatalf("UnknownTools(nil) = %v, want none", got)
	}
}
