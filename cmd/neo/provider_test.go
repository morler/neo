package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/session"
)

func customProviderConfig() *config.Config {
	return &config.Config{
		Provider: "local",
		Model:    "qwen3-coder",
		Providers: map[string]config.CustomProvider{
			"local": {
				API:          config.APIProtocolOpenAICompatible,
				Endpoint:     "https://llm.internal/v1/chat/completions",
				APIKeyEnv:    "LOCAL_LLM_API_KEY",
				DefaultModel: "qwen3-coder",
			},
		},
	}
}

func TestNewProviderBuildsCustomProvider(t *testing.T) {
	t.Setenv("LOCAL_LLM_API_KEY", "secret")
	prov, err := newProvider(customProviderConfig(), "local")
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if prov.Name() != "local" {
		t.Fatalf("name = %q, want local", prov.Name())
	}
}

func TestNewProviderReportsMissingCustomAPIKey(t *testing.T) {
	t.Setenv("LOCAL_LLM_API_KEY", "")
	_, err := newProvider(customProviderConfig(), "local")
	if err == nil {
		t.Fatal("expected a missing custom API key to fail")
	}
	for _, want := range []string{"LOCAL_LLM_API_KEY", "local"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestNewProviderUnknownNameListsCustomProviders(t *testing.T) {
	_, err := newProvider(customProviderConfig(), "mystery")
	if err == nil {
		t.Fatal("expected an unknown provider to fail")
	}
	for _, want := range []string{`unknown provider "mystery"`, "anthropic", "local"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestNewProviderDoesNotFallBackToAnthropicForCustomName(t *testing.T) {
	// A configured custom provider must construct itself, never the Anthropic
	// adapter, even when no custom key is needed.
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	cfg := customProviderConfig()
	cfg.Providers["local"] = config.CustomProvider{Endpoint: "https://llm.internal/v1/chat/completions"}
	prov, err := newProvider(cfg, "local")
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if prov.Name() != "local" {
		t.Fatalf("name = %q, want local (must not silently use anthropic)", prov.Name())
	}
}

func TestModelChoices_UsesCustomProviderStaticList(t *testing.T) {
	cfg := customProviderConfig()
	p := cfg.Providers["local"]
	p.Models = []config.CustomModel{
		{ID: "qwen3-coder", Name: "Qwen3 Coder", Description: "Local coder model"},
		{ID: "llama-4", Name: "Llama 4"},
	}
	cfg.Providers["local"] = p

	choices := modelChoices(cfg, "local")
	if len(choices) != 2 {
		t.Fatalf("choices = %#v, want 2 entries", choices)
	}
	if choices[0].ID != "qwen3-coder" || choices[0].Name != "Qwen3 Coder" || choices[0].Description != "Local coder model" {
		t.Fatalf("first choice = %#v", choices[0])
	}
	if choices[1].ID != "llama-4" {
		t.Fatalf("second choice = %#v", choices[1])
	}
}

func TestModelChoices_CustomProviderWithoutModelsIsEmpty(t *testing.T) {
	cfg := customProviderConfig()
	if choices := modelChoices(cfg, "local"); len(choices) != 0 {
		t.Fatalf("choices = %#v, want none when no models are declared", choices)
	}
}

func TestModelChoicesLoader_CustomProviderDoesNotLoadCatalogue(t *testing.T) {
	if loader := modelChoicesLoader("local"); loader != nil {
		t.Fatal("custom providers must not fetch a remote catalogue")
	}
}

func TestProviderCredentialPresent_CustomProvider(t *testing.T) {
	cfg := customProviderConfig()

	t.Setenv("LOCAL_LLM_API_KEY", "secret")
	if !providerCredentialPresent(cfg, "local") {
		t.Fatal("credential should be present when api_key_env is set")
	}

	t.Setenv("LOCAL_LLM_API_KEY", "")
	if providerCredentialPresent(cfg, "local") {
		t.Fatal("credential should be absent when api_key_env is unset")
	}

	p := cfg.Providers["local"]
	p.APIKeyEnv = ""
	cfg.Providers["local"] = p
	if !providerCredentialPresent(cfg, "local") {
		t.Fatal("an endpoint without api_key_env needs no credential")
	}
}

func TestSessionBackend_RestoresDeclaredCustomProvider(t *testing.T) {
	cfg := customProviderConfig()
	meta := session.Metadata{Provider: "local", Model: "qwen3-coder"}

	var warnings bytes.Buffer
	provider, model := sessionBackend(cfg, meta, &warnings)
	if provider != "local" || model != "qwen3-coder" {
		t.Fatalf("session backend = %s/%s, want local/qwen3-coder", provider, model)
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected warning: %q", warnings.String())
	}
}

func TestSessionBackend_FallsBackWhenCustomProviderWasRemoved(t *testing.T) {
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-5"}
	meta := session.Metadata{Provider: "local", Model: "qwen3-coder"}

	var warnings bytes.Buffer
	provider, model := sessionBackend(cfg, meta, &warnings)
	if provider != "anthropic" || model != "claude-opus-5" {
		t.Fatalf("session backend = %s/%s, want anthropic/claude-opus-5", provider, model)
	}
	if !strings.Contains(warnings.String(), "not configured") {
		t.Fatalf("warning = %q, want an explanation", warnings.String())
	}
}
