package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBackendRewritesOnlyProviderAndModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		const body = `# project config
provider: anthropic   # primary
model: "claude-opus-5"

providers:
  local:
    endpoint: http://127.0.0.1:8000/v1
    models:
      - id: qwen3-coder

features:
  skills: true
`
		path := filepath.Join(dir, projectConfigName)
		writeFile(t, path, body)
		cfg, err := loadYAML(t, dir, body)
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		written, err := cfg.WriteBackend("local", "qwen3-coder")
		if err != nil {
			t.Fatalf("WriteBackend: %v", err)
		}
		// Load resolves the project file relatively, so the persisted path is
		// relative to the working directory it was found in.
		if written != projectConfigName {
			t.Fatalf("written path = %q, want %q", written, projectConfigName)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(strings.Replace(body,
			"provider: anthropic   # primary", "provider: local   # primary", 1),
			`model: "claude-opus-5"`, "model: qwen3-coder", 1)
		if string(got) != want {
			t.Fatalf("config after switch:\n%s\nwant:\n%s", got, want)
		}

		reloaded, err := loadYAML(t, dir, string(got))
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.Provider != "local" || reloaded.Model != "qwen3-coder" {
			t.Fatalf("reloaded backend = %s/%s", reloaded.Provider, reloaded.Model)
		}
	})
}

func TestWriteBackendAppendsMissingKeys(t *testing.T) {
	withTempDir(t, func(dir string) {
		body := "features:\n  skills: true\n"
		path := filepath.Join(dir, projectConfigName)
		writeFile(t, path, body)
		cfg, err := loadYAML(t, dir, body)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := cfg.WriteBackend("openai", "gpt-6.1-sol"); err != nil {
			t.Fatalf("WriteBackend: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body+"provider: openai\nmodel: gpt-6.1-sol\n" {
			t.Fatalf("config after switch:\n%s", got)
		}
	})
}

func TestWriteBackendFromEmbeddedCreatesUserConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "embedded" {
			t.Fatalf("source = %q, want embedded", cfg.Source())
		}

		path, err := cfg.WriteBackend("google", "gemini-3.5-flash")
		if err != nil {
			t.Fatalf("WriteBackend: %v", err)
		}
		wantPath := filepath.Join(dir, userConfigDir, userConfigName)
		if path != wantPath {
			t.Fatalf("written path = %q, want %q", path, wantPath)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "\nprovider: google\n") {
			t.Fatalf("embedded config keeps the commented examples but must set provider: google:\n%s", got)
		}
		if !strings.Contains(string(got), "\nmodel: gemini-3.5-flash\n") {
			t.Fatalf("model not persisted:\n%s", got)
		}
		// The documented examples must survive the rewrite.
		if !strings.Contains(string(got), "# provider: openrouter") {
			t.Fatalf("provider examples were lost:\n%s", got)
		}

		reloaded, err := Load()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.Source() != wantPath || reloaded.Provider != "google" || reloaded.Model != "gemini-3.5-flash" {
			t.Fatalf("reloaded %s = %s/%s", reloaded.Source(), reloaded.Provider, reloaded.Model)
		}
	})
}

func TestWriteBackendRejectsEmptyValues(t *testing.T) {
	cfg := &Config{source: "embedded"}
	if _, err := cfg.WriteBackend("", "model"); err == nil {
		t.Fatal("expected an empty provider to fail")
	}
	if _, err := cfg.WriteBackend("anthropic", " "); err == nil {
		t.Fatal("expected an empty model to fail")
	}
}

func TestWriteModelLeavesProviderAndRestUntouched(t *testing.T) {
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

		if _, err := cfg.WriteModel("claude-sonnet-5"); err != nil {
			t.Fatalf("WriteModel: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(body, `model: "claude-opus-5"`, "model: claude-sonnet-5", 1)
		if string(got) != want {
			t.Fatalf("config after /model:\n%s\nwant:\n%s", got, want)
		}
		if !strings.Contains(string(got), "provider: anthropic   # primary") {
			t.Fatalf("provider or its comment changed:\n%s", got)
		}
	})
}

func TestWriteModelFromEmbeddedCreatesUserConfigWithDefaults(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		path, err := cfg.WriteModel("claude-haiku-4-5-20251001")
		if err != nil {
			t.Fatalf("WriteModel: %v", err)
		}
		if want := filepath.Join(dir, userConfigDir, userConfigName); path != want {
			t.Fatalf("written path = %q, want %q", path, want)
		}
		reloaded, err := Load()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.Model != "claude-haiku-4-5-20251001" {
			t.Fatalf("reloaded model = %q", reloaded.Model)
		}
		// Only the model moves; the provider stays whatever the defaults say.
		if reloaded.Provider != cfg.Provider {
			t.Fatalf("provider changed to %q, want %q", reloaded.Provider, cfg.Provider)
		}
	})
}

func TestWriteModelRejectsEmptyValue(t *testing.T) {
	cfg := &Config{source: "embedded"}
	if _, err := cfg.WriteModel(" "); err == nil {
		t.Fatal("expected an empty model to fail")
	}
}

func TestYamlScalarQuotesOnlyWhenNeeded(t *testing.T) {
	cases := map[string]string{
		"anthropic":                 "anthropic",
		"anthropic/claude-sonnet-5": "anthropic/claude-sonnet-5",
		"#not-a-comment":            `"#not-a-comment"`,
		"":                          `""`,
	}
	for in, want := range cases {
		if got := yamlScalar(in); got != want {
			t.Fatalf("yamlScalar(%q) = %q, want %q", in, got, want)
		}
	}
}
