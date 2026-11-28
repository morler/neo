package approval

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Matcher reports whether an interactive tool call needs user confirmation.
// Rules are literal user preferences, not a security policy.
type Matcher struct {
	rules []string
}

func New(rules []string) Matcher {
	return Matcher{rules: append([]string(nil), rules...)}
}

func (m Matcher) Requires(tool string, args map[string]any) bool {
	command, _ := args["command"].(string)
	command = strings.TrimLeftFunc(command, unicode.IsSpace)

	for _, rule := range m.rules {
		if rule == tool || tool == "bash" && hasCommandPrefix(command, rule) {
			return true
		}
	}
	return false
}

func hasCommandPrefix(command, prefix string) bool {
	if command == prefix {
		return true
	}
	if !strings.HasPrefix(command, prefix) {
		return false
	}
	next, _ := utf8.DecodeRuneInString(command[len(prefix):])
	return unicode.IsSpace(next)
}

// UnknownTools reports rules that look like a tool name but do not name a
// registered tool. Such a rule is not an error: anything that is not a tool name
// is treated as a bash command prefix, so the entry still works if it meant a
// command.
//
// A misspelled tool name is the case worth warning about. It cannot match the
// tool it was meant for, and it silently becomes a command prefix that matches
// nothing, so the confirmation the user asked for never happens. The shape check
// is deliberately narrow -- lower-case words joined by underscores -- because a
// rule that is a bare program ("git") or a command with arguments ("rm -rf") is
// a prefix by design, and warning about those would train the user to ignore the
// message.
func UnknownTools(rules, toolNames []string) []string {
	if len(rules) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		known[name] = struct{}{}
	}
	var out []string
	for _, rule := range rules {
		if _, ok := known[rule]; ok {
			continue
		}
		if looksLikeToolName(rule) {
			out = append(out, rule)
		}
	}
	return out
}

// looksLikeToolName is the "word_word" shape of every tool this package gates.
// A single word is excluded because "git" and "make" are commands, and anything
// with whitespace or punctuation is excluded because it is a command line.
func looksLikeToolName(s string) bool {
	if !strings.Contains(s, "_") {
		return false
	}
	letters := 0
	for _, r := range s {
		if r == '_' {
			continue
		}
		if r < 'a' || r > 'z' {
			return false
		}
		letters++
	}
	return letters > 0
}
