package config

import (
	"path/filepath"
	"strings"
	"testing"
)

const validCustomProviderYAML = `provider: local
providers:
  local:
    endpoint: http://127.0.0.1:8000/v1/chat/completions
    api_key_env: LOCAL_LLM_API_KEY
    default_model: qwen3-coder
    models:
      - id: qwen3-coder
        name: Qwen3 Coder
        description: Local coder model
`

func loadYAML(t *testing.T, dir, body string) (*Config, error) {
	t.Helper()
	t.Setenv("HOME", dir)
	writeFile(t, filepath.Join(dir, "neo.yaml"), body)
	return Load()
}

func TestLoad_ParsesCustomProvider(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, validCustomProviderYAML)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "local" {
			t.Fatalf("provider = %q, want local", cfg.Provider)
		}
		if cfg.Model != "qwen3-coder" {
			t.Fatalf("model = %q, want the custom default_model", cfg.Model)
		}
		p, ok := cfg.ProviderConfig("local")
		if !ok {
			t.Fatal("ProviderConfig(local) not found")
		}
		if p.Endpoint != "http://127.0.0.1:8000/v1/chat/completions" {
			t.Fatalf("endpoint = %q", p.Endpoint)
		}
		if p.APIKeyEnv != "LOCAL_LLM_API_KEY" {
			t.Fatalf("api_key_env = %q", p.APIKeyEnv)
		}
		if p.DefaultModel != "qwen3-coder" {
			t.Fatalf("default_model = %q", p.DefaultModel)
		}
		if p.API != APIProtocolOpenAICompatible {
			t.Fatalf("api = %q, want default %q", p.API, APIProtocolOpenAICompatible)
		}
		if len(p.Models) != 1 || p.Models[0].Name != "Qwen3 Coder" {
			t.Fatalf("models = %#v", p.Models)
		}
	})
}

func TestLoad_CustomProviderWithoutDefaultModelKeepsExplicitModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `provider: local
model: explicit-model
providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Model != "explicit-model" {
			t.Fatalf("model = %q, want explicit model untouched", cfg.Model)
		}
		p, _ := cfg.ProviderConfig("local")
		if p.APIKeyEnv != "" {
			t.Fatalf("api_key_env = %q, want empty (no auth header)", p.APIKeyEnv)
		}
	})
}

func TestLoad_CustomProviderAbsentFromModelChoicesIsAllowed(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `provider: local
providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		p, _ := cfg.ProviderConfig("local")
		if p.Models != nil {
			t.Fatalf("models = %#v, want nil when omitted", p.Models)
		}
	})
}

func TestLoad_ProviderNamesListsBuiltinsThenCustom(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `provider: zeta
providers:
  zeta:
    endpoint: https://zeta.internal/v1/chat/completions
  alpha:
    endpoint: https://alpha.internal/v1/chat/completions
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		got := strings.Join(cfg.ProviderNames(), ",")
		want := "anthropic,openai,openrouter,google,alpha,zeta"
		if got != want {
			t.Fatalf("provider names = %q, want %q", got, want)
		}
	})
}

func TestLoad_TrimsCustomProviderNameAndFields(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `provider: " spaced "
providers:
  " spaced ":
    endpoint: "  https://llm.internal/v1/chat/completions  "
    default_model: "  my-model  "
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		p, ok := cfg.ProviderConfig("spaced")
		if !ok {
			t.Fatalf("trimmed name not found; providers = %#v", cfg.Providers)
		}
		if p.Endpoint != "https://llm.internal/v1/chat/completions" || p.DefaultModel != "my-model" {
			t.Fatalf("fields not trimmed: %#v", p)
		}
	})
}

func TestLoad_SubagentsProviderAcceptsCustomName(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `provider: anthropic
subagents:
  provider: local
providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
    default_model: worker-model
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Subagents.Provider != "local" || cfg.Subagents.Model != "worker-model" {
			t.Fatalf("subagents backend = %#v", cfg.Subagents)
		}
	})
}

func TestLoad_RejectsCustomProviderThatShadowsBuiltin(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `providers:
  anthropic:
    endpoint: https://llm.internal/v1/chat/completions
`)
		if err == nil {
			t.Fatal("expected shadowing a built-in provider to fail")
		}
		for _, want := range []string{"neo.yaml", "providers.anthropic", "built-in"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_RejectsCustomProviderWithoutEndpoint(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, "providers:\n  local:\n    api_key_env: LOCAL_KEY\n")
		if err == nil {
			t.Fatal("expected missing endpoint to fail")
		}
		for _, want := range []string{"providers.local.endpoint", "required"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_RejectsCustomProviderEndpointWithoutSchemeOrHost(t *testing.T) {
	cases := map[string]string{
		"no scheme":  "127.0.0.1:8000/v1/chat/completions",
		"ftp scheme": "ftp://llm.internal/v1/chat/completions",
		"no host":    "https:///v1/chat/completions",
	}
	for name, endpoint := range cases {
		t.Run(name, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				_, err := loadYAML(t, dir, "providers:\n  local:\n    endpoint: "+endpoint+"\n")
				if err == nil {
					t.Fatalf("expected endpoint %q to fail", endpoint)
				}
				if !strings.Contains(err.Error(), "providers.local.endpoint") {
					t.Fatalf("error %q should name the offending field", err.Error())
				}
			})
		})
	}
}

func TestLoad_RejectsInvalidAPIKeyEnvName(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
    api_key_env: "not a name"
`)
		if err == nil {
			t.Fatal("expected invalid api_key_env to fail")
		}
		for _, want := range []string{"providers.local.api_key_env", "not a name"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_RejectsSharedAPIKeyEnvAcrossProviders(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `providers:
  alpha:
    endpoint: https://alpha.internal/v1/chat/completions
    api_key_env: SHARED_KEY
  beta:
    endpoint: https://beta.internal/v1/chat/completions
    api_key_env: SHARED_KEY
`)
		if err == nil {
			t.Fatal("expected a shared api_key_env to fail")
		}
		for _, want := range []string{"SHARED_KEY", "providers.alpha", "providers.beta"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_RejectsUnknownAPIProtocol(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `providers:
  local:
    api: anthropic_compatible
    endpoint: https://llm.internal/v1/messages
`)
		if err == nil {
			t.Fatal("expected unsupported api protocol to fail")
		}
		for _, want := range []string{"providers.local.api", APIProtocolOpenAICompatible, "anthropic_compatible"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_ModelEntriesRequireIDAndDropDuplicates(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
    models:
      - name: unnamed
`)
		if err == nil {
			t.Fatal("expected model entry without id to fail")
		}
		if !strings.Contains(err.Error(), "providers.local.models[0].id") {
			t.Fatalf("error %q should name the offending entry", err.Error())
		}
	})

	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
    models:
      - id: m1
      - id: m1
        name: duplicate
      - id: m2
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		p, _ := cfg.ProviderConfig("local")
		if len(p.Models) != 2 || p.Models[0].ID != "m1" || p.Models[1].ID != "m2" {
			t.Fatalf("models = %#v, want m1 then m2", p.Models)
		}
	})
}

func TestLoad_UnknownProviderNamesEveryAlternative(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, `provider: mystery
providers:
  local:
    endpoint: https://llm.internal/v1/chat/completions
`)
		if err == nil {
			t.Fatal("expected undeclared provider to fail")
		}
		for _, want := range []string{"neo.yaml", `unknown provider "mystery"`, "provider", "anthropic", "openai", "openrouter", "google", "local", "providers:"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_EmptyProviderNameEntryIsRejected(t *testing.T) {
	withTempDir(t, func(dir string) {
		_, err := loadYAML(t, dir, "providers:\n  \" \":\n    endpoint: https://llm.internal/v1/chat/completions\n")
		if err == nil {
			t.Fatal("expected an empty provider name to fail")
		}
		if !strings.Contains(err.Error(), "non-empty name") {
			t.Fatalf("error %q should explain the empty name", err.Error())
		}
	})
}

func TestCustomAPIKeyEnvNames(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, `providers:
  beta:
    endpoint: https://beta.internal/v1/chat/completions
    api_key_env: BETA_KEY
  alpha:
    endpoint: https://alpha.internal/v1/chat/completions
    api_key_env: ALPHA_KEY
  local:
    endpoint: http://127.0.0.1:8000/v1/chat/completions
`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		got := strings.Join(cfg.CustomAPIKeyEnvNames(), ",")
		if got != "ALPHA_KEY,BETA_KEY" {
			t.Fatalf("names = %q, want ALPHA_KEY,BETA_KEY (sorted, keyless provider skipped)", got)
		}
	})
}

func TestCustomAPIKeyEnvNames_EmptyWithoutCustomProviders(t *testing.T) {
	withTempDir(t, func(dir string) {
		cfg, err := loadYAML(t, dir, "provider: anthropic\n")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if names := cfg.CustomAPIKeyEnvNames(); len(names) != 0 {
			t.Fatalf("names = %#v, want none", names)
		}
	})
}
