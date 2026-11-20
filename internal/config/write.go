package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owainlewis/neo/internal/atomicfile"
)

// WriteBackend persists the selected backend by rewriting the top-level
// `provider` and `model` values of the config file Neo actually loaded. Every
// other line — comments, ordering, custom providers, unknown keys — is left
// byte-for-byte untouched, so a switch never reformats or drops configuration.
//
// When the running config is the embedded fallback there is no file to edit;
// the values are written to ~/.neo/config.yaml, created from the embedded
// defaults so the persisted file keeps the documented provider examples.
//
// It returns the path that was written.
func (c *Config) WriteBackend(provider, model string) (string, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return "", fmt.Errorf("provider is required")
	}
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	return c.writeScalars(scalar{"provider", provider}, scalar{"model", model})
}

// WriteModel persists only the top-level `model` value, leaving `provider` and
// every other line as they were. It is the write path for `/model`, which
// switches models within the backend already selected.
//
// It returns the path that was written.
func (c *Config) WriteModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	return c.writeScalars(scalar{"model", model})
}

// scalar is one top-level YAML key/value pair to rewrite.
type scalar struct{ key, value string }

// writeScalars rewrites the given top-level scalars in the config file Neo
// loaded, creating ~/.neo/config.yaml from the embedded defaults when the
// running config is the embedded fallback.
func (c *Config) writeScalars(values ...scalar) (string, error) {
	base := embeddedConfigYAML
	path := ""
	if c != nil && c.source != "" && c.source != "embedded" {
		b, err := os.ReadFile(c.source)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", c.source, err)
		}
		path = c.source
		base = b
	} else {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("cannot save the config: no config file and no home directory")
		}
		path = filepath.Join(home, userConfigDir, userConfigName)
	}

	out := base
	for _, v := range values {
		out = setTopLevelScalar(out, v.key, v.value)
	}
	if err := atomicfile.WritePreserveMode(path, out, 0o644, 0o755); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

func setTopLevelScalar(b []byte, key, value string) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	for i, line := range lines {
		content := strings.TrimRight(line, "\r\n")
		// Nested keys are indented; commented examples start with '#'.
		if content == "" || content[0] == ' ' || content[0] == '\t' || content[0] == '#' {
			continue
		}
		name, sep, comment, ok := splitTopLevelScalar(content)
		if !ok || name != key {
			continue
		}
		lines[i] = key + ":" + sep + yamlScalar(value) + comment + line[len(content):]
		return []byte(strings.Join(lines, ""))
	}

	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += key + ": " + yamlScalar(value) + "\n"
	return []byte(text)
}

// splitTopLevelScalar splits "key: value # note" into its key, the spacing
// after the colon, and the trailing comment. The comment is preserved so
// `provider: anthropic # why` keeps its explanation after a switch.
func splitTopLevelScalar(line string) (key, sep, comment string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", "", false
	}
	key = line[:colon]
	if strings.ContainsAny(key, " \t#\"'") {
		return "", "", "", false
	}
	after := line[colon+1:]
	trimmed := strings.TrimLeft(after, " \t")
	sep = after[:len(after)-len(trimmed)]
	if sep == "" {
		sep = " "
	}
	if i := commentStart(trimmed); i >= 0 {
		// Keep the whitespace that separated the value from its comment.
		valuePart := strings.TrimRight(trimmed[:i], " \t")
		comment = trimmed[len(valuePart):]
	}
	return key, sep, comment, true
}

// commentStart finds a YAML comment that begins a token, ignoring '#' inside
// a quoted or plain value (model ids and endpoints never contain one).
func commentStart(value string) int {
	var quote byte
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t'):
			return i
		}
	}
	return -1
}

// yamlScalar renders value as a single-line YAML scalar, quoting only when the
// text would otherwise be read as something other than a plain string.
func yamlScalar(value string) string {
	if !needsYAMLQuotes(value) {
		return value
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func needsYAMLQuotes(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return true
	}
	if strings.ContainsAny(value, "#,{}[]&*!|>'\"%@`\n\r\t") {
		return true
	}
	switch value[0] {
	case '-', '?', ':':
		return true
	}
	// A ': ' sequence would start a nested mapping.
	return strings.Contains(value, ": ")
}
