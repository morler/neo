package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// withTempDir runs fn from a temp working directory so project-relative
// config lookups can't pick up the repo's actual neo.yaml.
func withTempDir(t *testing.T, fn func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn(dir)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPathsSkipUserConfigWhenHomeIsEmpty(t *testing.T) {
	got := configPaths("")
	if len(got) != 1 || got[0] != projectConfigName {
		t.Fatalf("config paths = %#v, want only %q", got, projectConfigName)
	}
}

func TestLoad_FallsBackToEmbeddedWhenNoLocalConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		// Force the HOME lookup to a place with no config.
		t.Setenv("HOME", dir)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "embedded" {
			t.Fatalf("expected embedded source, got %q", cfg.Source())
		}
		if cfg.Model != defaultModel {
			t.Fatalf("embedded model: got %q want %q", cfg.Model, defaultModel)
		}
	})
}

func TestLoad_RejectsRemovedPhasesKey(t *testing.T) {
	withTempDir(t, func(dir string) {
		writeFile(t, filepath.Join(dir, "neo.yaml"), "phases:\n  security:\n    prompt: inspect\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected removed phases key to fail")
		}
		for _, want := range []string{"neo.yaml", "phases has been removed", ".neo/skills"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_PrefersProjectConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: project-model\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "neo.yaml" {
			t.Fatalf("expected source 'neo.yaml', got %q", cfg.Source())
		}
		if cfg.Model != "project-model" {
			t.Fatalf("expected project model, got %q", cfg.Model)
		}
	})
}

func TestLoad_RejectsInvalidYAML(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: [unclosed\n")
		if _, err := Load(); err == nil {
			t.Fatal("expected error on malformed yaml")
		}
	})
}

// TestLoad_RejectsAnUnknownCodeMode covers #104. The CLI flag already refuses an
// unknown surface, but the config file is where the value usually comes from, and
// ClassicCodeMode only tested for "classic", so every other value ran the default
// surface silently -- including "solo", a mode ad63f36 had removed. That silence
// is what let a measurement compare a surface against itself (#103).
func TestLoad_RejectsAnUnknownCodeMode(t *testing.T) {
	for _, mode := range []string{"solo", "classicc", "codemodee", "solo "} {
		t.Run(mode, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				t.Setenv("HOME", dir)
				writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  codemode: "+strings.TrimSpace(mode)+"x\n")
				_, err := Load()
				if err == nil {
					t.Fatalf("codemode %q was accepted", mode)
				}
				for _, want := range []string{"codemode must be", "classic"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error = %v, want it to mention %q", err, want)
					}
				}
			})
		})
	}
}

// TestLoad_AcceptsEveryValidCodeMode pins the states that must keep working,
// including the empty value, which means "unset" rather than a surface name.
func TestLoad_AcceptsEveryValidCodeMode(t *testing.T) {
	cases := map[string]bool{
		"":          false, // unset: default surface
		"codemode":  false,
		"classic":   true,
		" Classic ": true,
		"CODEmode":  false,
	}
	for mode, wantClassic := range cases {
		name := mode
		if name == "" {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				t.Setenv("HOME", dir)
				body := "model: m\n"
				if mode != "" {
					body += "features:\n  codemode: \"" + mode + "\"\n"
				}
				writeFile(t, filepath.Join(dir, "neo.yaml"), body)
				cfg, err := Load()
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if got := cfg.ClassicCodeMode(); got != wantClassic {
					t.Fatalf("ClassicCodeMode() = %v, want %v", got, wantClassic)
				}
			})
		})
	}
}
func TestLoad_DefaultsProviderToAnthropic(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "anthropic" {
			t.Fatalf("expected default provider anthropic, got %q", cfg.Provider)
		}
	})
}

func TestLoad_OpenAIProviderGetsOpenAIDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "openai" {
			t.Fatalf("provider: got %q want openai", cfg.Provider)
		}
		if cfg.Model != defaultOpenAIModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultOpenAIModel)
		}
	})
}

func TestLoad_OpenRouterProviderGetsOpenRouterDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openrouter\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "openrouter" {
			t.Fatalf("provider: got %q want openrouter", cfg.Provider)
		}
		if cfg.Model != defaultOpenRouterModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultOpenRouterModel)
		}
	})
}

func TestLoad_GoogleProviderGetsGoogleDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: google\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "google" {
			t.Fatalf("provider: got %q want google", cfg.Provider)
		}
		if cfg.Model != defaultGoogleModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultGoogleModel)
		}
	})
}

func TestLoad_OpenAIDefaultsToAPIKeyAuth(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.OpenAIAuth != OpenAIAuthAPIKey {
			t.Fatalf("openai_auth: got %q want api_key", cfg.OpenAIAuth)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("api_key auth must not report SubscriptionAuth")
		}
	})
}

func TestLoad_OpenAIAcceptsExplicitAPIKeyAuth(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nopenai_auth: api_key\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.OpenAIAuth != OpenAIAuthAPIKey {
			t.Fatalf("openai_auth: got %q want %q", cfg.OpenAIAuth, OpenAIAuthAPIKey)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("api_key auth must not report SubscriptionAuth")
		}
	})
}

func TestLoad_OpenAISubscriptionGetsCodexModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nopenai_auth: subscription\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SubscriptionAuth() {
			t.Fatal("expected SubscriptionAuth to be true")
		}
		if cfg.Model != defaultCodexModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultCodexModel)
		}
	})
}

func TestLoad_RejectsInvalidOpenAIAuthMode(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "openai_auth: nope\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected invalid openai_auth to fail")
		}
		for _, want := range []string{"neo.yaml", "openai_auth", OpenAIAuthAPIKey, OpenAIAuthSubscription, "nope"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not contain %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_OpenAIAuthDoesNotChangeAnthropicStartupDefault(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: anthropic\nopenai_auth: subscription\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "anthropic" {
			t.Fatalf("provider: got %q want anthropic", cfg.Provider)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("anthropic startup must not use OpenAI subscription auth")
		}
		if cfg.Model != defaultModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultModel)
		}
	})
}

func TestLoad_ExplicitModelOverridesProviderDefault(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nmodel: gpt-custom\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Model != "gpt-custom" {
			t.Fatalf("model: got %q want gpt-custom", cfg.Model)
		}
	})
}

func TestLoad_SubagentsBackendDefaultsToCoordinator(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nmodel: gpt-main\nsubagents:\n  model: gpt-worker\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SubagentsConfigured() {
			t.Fatal("subagents backend should be configured")
		}
		if cfg.Subagents.Provider != "openai" || cfg.Subagents.Model != "gpt-worker" {
			t.Fatalf("subagents backend = %#v", cfg.Subagents)
		}
	})
}

func TestLoad_SubagentsProviderGetsProviderDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "subagents:\n  provider: google\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Subagents.Provider != "google" || cfg.Subagents.Model != defaultGoogleModel {
			t.Fatalf("subagents backend = %#v", cfg.Subagents)
		}
	})
}

func TestLoad_SubagentsBackendAbsentFollowsCoordinator(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: main-model\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.SubagentsConfigured() {
			t.Fatalf("subagents backend unexpectedly configured: %#v", cfg.Subagents)
		}
	})
}

func TestLoad_RejectsUnknownSubagentsProvider(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "subagents:\n  provider: mystery\n  model: worker\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected unknown subagents provider to fail")
		}
		for _, want := range []string{"neo.yaml", "subagents.provider", "mystery"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not contain %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_CompactionContextWindowOverride(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ncompaction:\n  context_window_tokens: 1000000\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Compaction.ContextWindowTokens != 1_000_000 {
			t.Fatalf("context_window_tokens = %d, want 1000000", cfg.Compaction.ContextWindowTokens)
		}
	})
}

func TestFeatures_AgentsFileDefaultsOnWhenAbsent(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.AgentsFileEnabled() {
			t.Fatal("expected agents_file to default on when omitted")
		}
	})
}

func TestFeatures_AgentsFileExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  agents_file: false\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.AgentsFileEnabled() {
			t.Fatal("expected agents_file disabled when set to false")
		}
	})
}

func TestFeatures_SkillsDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SkillsEnabled() {
			t.Fatal("expected skills to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  skills: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.SkillsEnabled() {
			t.Fatal("expected skills disabled when set to false")
		}
	})
}

func TestOutput_VerboseDefaultsOffWhenAbsent(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose to default off when omitted")
		}
	})
}

func TestOutput_VerboseExplicitTrueEnables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\noutput:\n  verbose: true\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose: true to enable verbose output")
		}
	})
}

func TestOutput_VerboseExplicitFalseStaysOff(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\noutput:\n  verbose: false\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose: false to stay off")
		}
	})
}

func TestFeatures_LegacyMemoryKeyIsIgnored(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  memory: true\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Model != "m" {
			t.Fatalf("model = %q, want m", cfg.Model)
		}
	})
}

func TestFeatures_PromptCachingDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.PromptCachingEnabled() {
			t.Fatal("expected prompt_caching to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  prompt_caching: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.PromptCachingEnabled() {
			t.Fatal("expected prompt_caching disabled when set to false")
		}
	})
}

func TestToolApprovals_DefaultsEmpty(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.ToolApprovals) != 0 {
			t.Fatalf("tool_approvals = %#v, want empty", cfg.ToolApprovals)
		}
	})
}

func TestToolApprovals_TrimsAndDeduplicates(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ntool_approvals:\n  - ' git '\n  - write_file\n  - git\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.ToolApprovals) != 2 || cfg.ToolApprovals[0] != "git" || cfg.ToolApprovals[1] != "write_file" {
			t.Fatalf("tool_approvals = %#v", cfg.ToolApprovals)
		}
	})
}

func TestToolApprovals_RejectsEmptyEntry(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ntool_approvals:\n  - '   '\n")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("error = %v, want empty-entry error", err)
		}
	})
}

func TestPermissions_RejectsRemovedConfigWithMigration(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\npermissions:\n  mode: readonly\n")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "use tool_approvals") {
			t.Fatalf("error = %v, want migration error", err)
		}
	})
}

func TestLoad_OpenAIDefaultModels(t *testing.T) {
	for _, auth := range []string{OpenAIAuthAPIKey, OpenAIAuthSubscription} {
		t.Run(auth, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				t.Setenv("HOME", dir)
				writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nopenai_auth: "+auth+"\nsubagents:\n  provider: openai\n")
				cfg, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Model != "gpt-6.1-sol" || cfg.Subagents.Model != "gpt-6.1-sol" {
					t.Fatalf("default models = %q/%q, want gpt-6.1-sol for both", cfg.Model, cfg.Subagents.Model)
				}
			})
		})
	}
}

func TestGoalMaxAttemptsDefaultsWhenOmitted(t *testing.T) {
	cfg, err := parseConfig([]byte("provider: anthropic\n"), "test")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.GoalMaxAttempts(); got != DefaultGoalMaxAttempts {
		t.Fatalf("GoalMaxAttempts = %d, want the default %d", got, DefaultGoalMaxAttempts)
	}
	if DefaultGoalMaxAttempts != 20 {
		t.Fatalf("default budget changed to %d; the docs and the design spec say 20", DefaultGoalMaxAttempts)
	}
}

func TestGoalMaxAttemptsHonorsAnExplicitValue(t *testing.T) {
	cfg, err := parseConfig([]byte("goals:\n  max_attempts: 5\n"), "test")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.GoalMaxAttempts(); got != 5 {
		t.Fatalf("GoalMaxAttempts = %d, want 5", got)
	}
}

func TestGoalMaxAttemptsRejectsNegativeValues(t *testing.T) {
	_, err := parseConfig([]byte("goals:\n  max_attempts: -1\n"), "neo.yaml")
	if err == nil {
		t.Fatal("expected a config error for a negative attempt budget")
	}
	for _, want := range []string{"goals.max_attempts", "neo.yaml", "negative"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestGoalMaxAttemptsRejectsNonIntegerValues(t *testing.T) {
	if _, err := parseConfig([]byte("goals:\n  max_attempts: lots\n"), "test"); err == nil {
		t.Fatal("expected a config error for a non-integer attempt budget")
	}
}

func TestGoalConfigDoesNotChangeOtherFields(t *testing.T) {
	body := `provider: openai
model: gpt-test
goals:
  max_attempts: 3
features:
  skills: false
`
	cfg, err := parseConfig([]byte(body), "test")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-test" {
		t.Fatalf("provider/model = %q/%q", cfg.Provider, cfg.Model)
	}
	if cfg.SkillsEnabled() {
		t.Fatal("skills flag was ignored")
	}
	if got := cfg.GoalMaxAttempts(); got != 3 {
		t.Fatalf("GoalMaxAttempts = %d, want 3", got)
	}
}

// TestContextWindowFor covers #108. Resolution is per (provider, model) because
// one config can serve models with different windows, which is what made the
// single global key wrong: a 40K local model and a 200K hosted one cannot share
// a compaction trigger.
func TestContextWindowFor(t *testing.T) {
	cfg := &Config{
		Compaction: Compaction{ContextWindowTokens: 200_000},
		Providers: map[string]CustomProvider{
			"local40k": {
				Endpoint:     "http://127.0.0.1:8080/v1",
				DefaultModel: "Bonsai2.5-27B",
				Models: []CustomModel{
					{ID: "Bonsai2.5-27B", ContextWindowTokens: 40_192},
					{ID: "big-model"},
				},
			},
		},
	}
	cases := []struct {
		name            string
		provider, model string
		want            int
	}{
		{"model override wins", "local40k", "Bonsai2.5-27B", 40_192},
		{"declared model without override", "local40k", "big-model", 200_000},
		{"model not in the catalogue", "local40k", "pulled-from-catalogue", 200_000},
		{"built-in provider", "anthropic", "claude-opus-5", 200_000},
		{"unknown provider", "nope", "whatever", 200_000},
		{"model id is trimmed", "local40k", "  Bonsai2.5-27B  ", 40_192},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.ContextWindowFor(tc.provider, tc.model); got != tc.want {
				t.Fatalf("ContextWindowFor(%q, %q) = %d, want %d", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

// TestContextWindowForFallsThroughToZero checks the "nothing configured" answer.
// Zero is the signal to use the compact package default; returning a made-up
// number here would silently override it.
func TestContextWindowForFallsThroughToZero(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.ContextWindowFor("anthropic", "m"); got != 0 {
		t.Fatalf("nil config = %d, want 0", got)
	}
	if got := (&Config{}).ContextWindowFor("local", "m"); got != 0 {
		t.Fatalf("empty config = %d, want 0", got)
	}
	onlyModel := &Config{Providers: map[string]CustomProvider{
		"local": {Endpoint: "http://h/v1", Models: []CustomModel{{ID: "m"}}},
	}}
	if got := onlyModel.ContextWindowFor("local", "m"); got != 0 {
		t.Fatalf("no override, no global = %d, want 0", got)
	}
}

// TestContextWindowPrecedence covers #109: a window the backend reports sits
// below an explicit setting and above the global key. It has to sit above the
// global key because the embedded defaults set that one to 200000, so a
// discovered window placed below it could never take effect.
func TestContextWindowPrecedence(t *testing.T) {
	newCfg := func() *Config {
		return &Config{
			Compaction: Compaction{ContextWindowTokens: 200_000},
			Providers: map[string]CustomProvider{
				"local": {
					Endpoint: "http://127.0.0.1:8080/v1",
					Models: []CustomModel{
						{ID: "explicit", ContextWindowTokens: 65_536},
						{ID: "discovered-only"},
					},
				},
			},
		}
	}

	// Nothing discovered yet: the global key answers.
	cfg := newCfg()
	if got := cfg.ContextWindowFor("local", "discovered-only"); got != 200_000 {
		t.Fatalf("before discovery = %d, want the global 200000", got)
	}

	cfg.SetDiscoveredContextWindow("local", "discovered-only", 40_192)
	if got := cfg.ContextWindowFor("local", "discovered-only"); got != 40_192 {
		t.Fatalf("discovered = %d, want 40192 to beat the global key", got)
	}
	if got, source := cfg.ContextWindow("local", "discovered-only"); got != 40_192 || source != ContextWindowFromCatalogue {
		t.Fatalf("ContextWindow = %d/%s, want 40192/discovered", got, source)
	}

	// An explicit setting still wins over a discovered value.
	cfg.SetDiscoveredContextWindow("local", "explicit", 4_096)
	if got := cfg.ContextWindowFor("local", "explicit"); got != 65_536 {
		t.Fatalf("explicit = %d, want the configured 65536 to beat the discovered 4096", got)
	}
	if got, source := cfg.ContextWindow("local", "explicit"); got != 65_536 || source != ContextWindowFromModel {
		t.Fatalf("ContextWindow = %d/%s, want 65536/config", got, source)
	}

	// A model nothing applies to falls back to the global key, then to zero.
	if got := cfg.ContextWindowFor("local", "unknown-model"); got != 200_000 {
		t.Fatalf("unknown model = %d, want the global key", got)
	}
	empty := &Config{}
	empty.SetDiscoveredContextWindow("local", "m", 8_192)
	if got, source := empty.ContextWindow("local", "m"); got != 8_192 || source != ContextWindowFromCatalogue {
		t.Fatalf("no global key = %d/%s, want the discovered window", got, source)
	}
	if got, source := (&Config{}).ContextWindow("anthropic", "claude-opus-5"); got != 0 || source != ContextWindowFromDefault {
		t.Fatalf("nothing configured = %d/%s, want 0/default", got, source)
	}
}

// TestDiscoveredContextWindowRejectsUselessValues keeps the map from holding
// entries that would answer "known" for a window nobody can compact against.
func TestDiscoveredContextWindowRejectsUselessValues(t *testing.T) {
	cfg := &Config{}
	cfg.SetDiscoveredContextWindow("local", "m", 0)
	cfg.SetDiscoveredContextWindow("local", "m", -5)
	cfg.SetDiscoveredContextWindow("", "m", 1024)
	cfg.SetDiscoveredContextWindow("local", "", 1024)
	if _, ok := cfg.DiscoveredContextWindow("local", "m"); ok {
		t.Fatal("a non-positive window must not be stored")
	}
	var nilCfg *Config
	nilCfg.SetDiscoveredContextWindow("local", "m", 1024)
	if nilCfg != nil {
		t.Fatal("nil config should be a no-op")
	}
}

// TestDiscoveredContextWindowIsNotPersisted guards the decision that discovery
// stays a runtime observation: it must never be written back as if the user had
// configured it.
func TestDiscoveredContextWindowIsNotPersisted(t *testing.T) {
	cfg := &Config{}
	cfg.SetDiscoveredContextWindow("local", "m", 40_192)
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "40192") {
		t.Fatalf("discovered window leaked into YAML: %s", encoded)
	}
}

func TestEmbeddedDefaultsParseWithAGoalBudget(t *testing.T) {
	cfg, err := parseConfig(embeddedConfigYAML, "embedded:neo.yaml")
	if err != nil {
		t.Fatalf("embedded defaults: %v", err)
	}
	if got := cfg.GoalMaxAttempts(); got != DefaultGoalMaxAttempts {
		t.Fatalf("embedded GoalMaxAttempts = %d, want %d", got, DefaultGoalMaxAttempts)
	}
	// The documented section stays commented out: the default must not require
	// users to edit their config.
	if strings.Contains(string(embeddedConfigYAML), "\ngoals:") {
		t.Fatal("embedded defaults set an active goals section; keep it commented")
	}
}

func TestGoalMaxAttemptsOnANilConfig(t *testing.T) {
	var cfg *Config
	if got := cfg.GoalMaxAttempts(); got != DefaultGoalMaxAttempts {
		t.Fatalf("nil config GoalMaxAttempts = %d, want %d", got, DefaultGoalMaxAttempts)
	}
}
