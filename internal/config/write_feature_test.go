package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFeatureInsertsAndToggles covers both shapes: adding the key to an
// existing features block, and flipping it afterwards. Every other line has to
// survive, because a switch that reformats the config is a switch nobody trusts.
func TestWriteFeatureInsertsAndToggles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.yaml")
	original := "model: m\nfeatures:\n  agents_file: true\n  skills: false\nother: keep\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{source: path}

	if _, err := cfg.WriteFeature("script", true); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, path)
	if !strings.Contains(body, "script: true") {
		t.Fatalf("after on:\n%s", body)
	}
	for _, keep := range []string{"agents_file: true", "skills: false", "other: keep"} {
		if !strings.Contains(body, keep) {
			t.Fatalf("%q was lost:\n%s", keep, body)
		}
	}

	if _, err := cfg.WriteFeature("script", false); err != nil {
		t.Fatal(err)
	}
	body = readFile(t, path)
	if !strings.Contains(body, "script: false") {
		t.Fatalf("after off:\n%s", body)
	}
	if strings.Count(body, "script:") != 1 {
		t.Fatalf("script key duplicated:\n%s", body)
	}
}

// TestWriteFeatureCreatesTheBlock covers a config that has no features section.
func TestWriteFeatureCreatesTheBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.yaml")
	if err := os.WriteFile(path, []byte("model: m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{source: path}
	if _, err := cfg.WriteFeature("script", true); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, path)
	if !strings.Contains(body, "features:") || !strings.Contains(body, "script: true") {
		t.Fatalf("body:\n%s", body)
	}
	// The result must still parse, or the next start fails.
	if _, err := parseConfig([]byte(body), path); err != nil {
		t.Fatalf("written config does not parse: %v\n%s", err, body)
	}
}

// TestScriptEnabledDefaultsOff is the product decision as a test.
func TestScriptEnabledDefaultsOff(t *testing.T) {
	if (&Config{}).ScriptEnabled() {
		t.Fatal("script is enabled by default")
	}
	on := true
	if !(&Config{Features: Features{Script: &on}}).ScriptEnabled() {
		t.Fatal("script: true did not enable it")
	}
}

// TestSetScriptUpdatesTheSnapshot keeps in-memory state in step with the file,
// so a prompt rebuild in the same session sees the new value.
func TestSetScriptUpdatesTheSnapshot(t *testing.T) {
	cfg := &Config{}
	cfg.SetScript(true)
	if !cfg.ScriptEnabled() {
		t.Fatal("SetScript(true) did not stick")
	}
	cfg.SetScript(false)
	if cfg.ScriptEnabled() {
		t.Fatal("SetScript(false) did not stick")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
