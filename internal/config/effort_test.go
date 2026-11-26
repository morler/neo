package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasActiveKey reports whether key appears as an actual setting rather than in
// a commented example. The embedded defaults explain this key inside a comment,
// so a plain substring check would report the key as present after it was
// cleared.
func hasActiveKey(body, key string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, key+":") {
			return true
		}
	}
	return false
}

func TestEffortNormalizesCaseAndSpace(t *testing.T) {
	for in, want := range map[string]string{
		"":         "",
		"  HIGH  ": "high",
		"Medium":   "medium",
		"low":      "low",
		"Ultra":    "ultra",
	} {
		cfg := &Config{ReasoningEffort: in}
		if got := cfg.Effort(); got != want {
			t.Errorf("Effort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffortOnNilConfigIsUnset(t *testing.T) {
	var cfg *Config
	if got := cfg.Effort(); got != "" {
		t.Fatalf("nil config Effort() = %q, want empty", got)
	}
}

func TestEffortAcceptsValuesNeoDoesNotKnow(t *testing.T) {
	// The accepted vocabulary belongs to the backend. Refusing a word here would
	// break a config the API would have accepted, so an unknown value is kept
	// verbatim for the API to judge.
	cfg := parseEffortConfig(t, "reasoning_effort: xhigh\n")
	if got := cfg.Effort(); got != "xhigh" {
		t.Fatalf("Effort() = %q, want xhigh", got)
	}
}

func TestEffortUnsetByDefault(t *testing.T) {
	if got := parseEffortConfig(t, "").Effort(); got != "" {
		t.Fatalf("Effort() = %q, want empty when the key is absent", got)
	}
}

// parseEffortConfig loads a config whose only content is the given extra YAML,
// over the minimum the loader requires.
func parseEffortConfig(t *testing.T, extra string) *Config {
	t.Helper()
	body := "provider: anthropic\nmodel: claude-opus-5\n" + extra
	withTempDir(t, func(dir string) {
		writeFile(t, filepath.Join(dir, projectConfigName), body)
	})
	cfg, err := parseConfig([]byte(body), "test.yaml")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

func TestWriteEffortRewritesOnlyThatLine(t *testing.T) {
	withTempDir(t, func(dir string) {
		const body = `# project config
provider: anthropic   # primary
model: "claude-opus-5"

features:
  skills: true
`
		path := filepath.Join(dir, projectConfigName)
		writeFile(t, path, body)
		cfg, err := loadYAML(t, dir, body)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := cfg.WriteEffort("High"); err != nil {
			t.Fatalf("WriteEffort: %v", err)
		}
		got := readFile(t, path)
		if !hasActiveKey(got, "reasoning_effort") {
			t.Fatalf("effort not recorded:\n%s", got)
		}
		// Everything else must survive byte-for-byte: a switch must not
		// reformat comments or drop keys it does not know about.
		for _, want := range []string{"# project config", "provider: anthropic   # primary", `model: "claude-opus-5"`, "  skills: true"} {
			if !strings.Contains(got, want) {
				t.Errorf("lost %q after WriteEffort:\n%s", want, got)
			}
		}
	})
}

func TestWriteEffortReplacesAnExistingLine(t *testing.T) {
	withTempDir(t, func(dir string) {
		body := "provider: anthropic\nmodel: m\nreasoning_effort: low\n"
		path := filepath.Join(dir, projectConfigName)
		writeFile(t, path, body)
		cfg, err := loadYAML(t, dir, body)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := cfg.WriteEffort("high"); err != nil {
			t.Fatalf("WriteEffort: %v", err)
		}
		got := readFile(t, path)
		if strings.Contains(got, "reasoning_effort: low") {
			t.Fatalf("old value survived:\n%s", got)
		}
		if strings.Count(got, "\nreasoning_effort:") != 1 {
			t.Fatalf("expected exactly one reasoning_effort line:\n%s", got)
		}
	})
}

func TestWriteEffortEmptyRemovesTheLine(t *testing.T) {
	withTempDir(t, func(dir string) {
		body := "provider: anthropic\nmodel: m\nreasoning_effort: high\n"
		path := filepath.Join(dir, projectConfigName)
		writeFile(t, path, body)
		cfg, err := loadYAML(t, dir, body)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := cfg.WriteEffort("  "); err != nil {
			t.Fatalf("WriteEffort: %v", err)
		}
		got := readFile(t, path)
		if hasActiveKey(got, "reasoning_effort") {
			t.Fatalf("clearing the effort should remove the key, got:\n%s", got)
		}
		if !strings.Contains(got, "model: m") {
			t.Fatalf("clearing the effort removed other keys:\n%s", got)
		}
	})
}

func TestWriteEffortRemovesTheLineFromTheEmbeddedDefaults(t *testing.T) {
	// A session started from the embedded config writes to ~/.neo/config.yaml.
	// Clearing there must not be a no-op that leaves a stale value behind.
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		cfg, err := parseConfig(embeddedConfigYAML, "embedded:neo.yaml")
		if err != nil {
			t.Fatalf("parse embedded: %v", err)
		}
		cfg.source = "embedded"
		if _, err := cfg.WriteEffort("high"); err != nil {
			t.Fatalf("WriteEffort: %v", err)
		}
		written := filepath.Join(dir, userConfigDir, userConfigName)
		if !strings.Contains(readFile(t, written), "reasoning_effort: high") {
			t.Fatalf("effort not written to the user config")
		}
		if _, err := cfg.WriteEffort(""); err != nil {
			t.Fatalf("WriteEffort clear: %v", err)
		}
		if hasActiveKey(readFile(t, written), "reasoning_effort") {
			t.Fatalf("clear left the key behind")
		}
	})
}

func TestWriteEffortMatchesTheEmbeddedExample(t *testing.T) {
	// The commented example in the defaults names the values users will copy;
	// keep the two from drifting apart.
	if !strings.Contains(string(embeddedConfigYAML), "# reasoning_effort: medium") {
		t.Fatal("embedded defaults no longer show a reasoning_effort example")
	}
	_ = os.Stdout
}
