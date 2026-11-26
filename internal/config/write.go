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

// WriteEffort persists the top-level reasoning_effort value, leaving every
// other line as it was. Setting it to the empty string removes the line
// instead of writing an empty scalar, because an empty value and an absent key
// both mean "unset" and the absent form is the documented default.
//
// It returns the path that was written.
func (c *Config) WriteEffort(effort string) (string, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return c.removeTopLevelScalar("reasoning_effort")
	}
	return c.writeScalars(scalar{"reasoning_effort", effort})
}

// removeTopLevelScalar deletes a top-level key so the file falls back to the
// documented default rather than recording an empty value.
func (c *Config) removeTopLevelScalar(key string) (string, error) {
	path, base, err := c.writeTarget()
	if err != nil {
		return "", err
	}
	if err := atomicfile.WritePreserveMode(path, dropTopLevelScalar(base, key), 0o644, 0o755); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	c.rememberSource(path)
	return path, nil
}

// writeTarget resolves the file a write should edit and the bytes to base it
// on. A running config loaded from a file is edited in place; the embedded
// fallback is seeded from the embedded defaults in ~/.neo/config.yaml so the
// documented examples survive.
//
// Resolution happens per write rather than once, because the first write from
// the embedded fallback creates that file and every later write must edit it —
// rebuilding from the embedded bytes again would silently drop the value the
// previous switch just saved.
func (c *Config) writeTarget() (string, []byte, error) {
	if c != nil && c.source != "" && c.source != "embedded" {
		b, err := os.ReadFile(c.source)
		if err != nil {
			return "", nil, fmt.Errorf("read %s: %w", c.source, err)
		}
		return c.source, b, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", nil, fmt.Errorf("cannot save the config: no config file and no home directory")
	}
	return filepath.Join(home, userConfigDir, userConfigName), embeddedConfigYAML, nil
}

// rememberSource points the running config at the file it was just written to,
// so the next write edits that file instead of re-seeding from the fallback.
func (c *Config) rememberSource(path string) {
	if c != nil {
		c.source = path
	}
}

// dropTopLevelScalar removes the single top-level "key: ..." line, if present.
func dropTopLevelScalar(b []byte, key string) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		content := strings.TrimRight(line, "\r\n")
		if content == "" || content[0] == ' ' || content[0] == '\t' || content[0] == '#' {
			out = append(out, line)
			continue
		}
		if name, _, _, ok := splitTopLevelScalar(content); ok && name == key {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, ""))
}

// scalar is one top-level YAML key/value pair to rewrite.
type scalar struct{ key, value string }

// writeScalars rewrites the given top-level scalars in the config file Neo
// loaded, creating ~/.neo/config.yaml from the embedded defaults when the
// running config is the embedded fallback.
func (c *Config) writeScalars(values ...scalar) (string, error) {
	path, base, err := c.writeTarget()
	if err != nil {
		return "", err
	}
	out := base
	for _, v := range values {
		out = setTopLevelScalar(out, v.key, v.value)
	}
	if err := atomicfile.WritePreserveMode(path, out, 0o644, 0o755); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	c.rememberSource(path)
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

// WriteFeature persists one boolean under the `features:` mapping, leaving every
// other line as it was. Unlike the top-level scalars this has to find an indented
// block, so it edits the child line inside that block rather than appending a
// second `features:` key.
//
// It returns the path that was written.
func (c *Config) WriteFeature(name string, on bool) (string, error) {
	value := "false"
	if on {
		value = "true"
	}
	path, base, err := c.writeTarget()
	if err != nil {
		return "", err
	}

	out := setFeatureScalar(base, name, value)
	if err := atomicfile.WritePreserveMode(path, out, 0o644, 0o755); err != nil {
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	c.rememberSource(path)
	return path, nil
}

// setFeatureScalar rewrites `name:` inside the top-level `features:` block,
// inserting both the block and the key when they are missing.
func setFeatureScalar(b []byte, name, value string) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	inFeatures := false
	insertAt := -1
	for i, line := range lines {
		content := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(content)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := content[0] == ' ' || content[0] == '\t'
		if !indented {
			inFeatures = trimmed == "features:"
			if inFeatures {
				insertAt = i + 1
			}
			continue
		}
		if !inFeatures {
			continue
		}
		key, sep, comment, ok := splitTopLevelScalar(trimmed)
		if !ok || key != name {
			insertAt = i + 1
			continue
		}
		indent := content[:len(content)-len(strings.TrimLeft(content, " \t"))]
		lines[i] = indent + name + ":" + sep + yamlScalar(value) + comment + line[len(content):]
		return []byte(strings.Join(lines, ""))
	}

	indent := "  "
	newLine := indent + name + ": " + yamlScalar(value) + "\n"
	if insertAt < 0 {
		text := strings.Join(lines, "")
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "features:\n" + newLine
		return []byte(text)
	}
	lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
	return []byte(strings.Join(lines, ""))
}
