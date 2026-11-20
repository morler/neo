package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/tools"
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

func TestProviderChoices_ListsBuiltinsAndCustomBackends(t *testing.T) {
	cfg := customProviderConfig()
	choices := providerChoices(cfg)

	seen := map[string]string{}
	for _, c := range choices {
		seen[c.ID] = c.Description
	}
	for _, want := range []string{"anthropic", "openai", "openrouter", "google", "local"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("provider choices missing %s: %#v", want, choices)
		}
	}
	if !strings.Contains(seen["openai"], "OPENAI_API_KEY") {
		t.Fatalf("openai description = %q, want the API key hint", seen["openai"])
	}
	if !strings.Contains(seen["local"], "custom") {
		t.Fatalf("local description = %q, want it marked custom", seen["local"])
	}

	cfg.OpenAIAuth = config.OpenAIAuthSubscription
	for _, c := range providerChoices(cfg) {
		if c.ID == "openai" && !strings.Contains(c.Description, "subscription") {
			t.Fatalf("subscription description = %q", c.Description)
		}
	}
}

// switchTestAgent builds a coordinator on the fake provider with a fixable
// backend so switchChatBackend can be exercised without network access.
func switchTestAgent(t *testing.T) *agent.Agent {
	t.Helper()
	return agent.New(agent.Config{
		Model:    "claude-opus-5",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(tools.ReadFile{}),
	})
}

func TestSetChatModel_SwitchesModelAndKeepsProvider(t *testing.T) {
	cfg := customProviderConfig()
	ag := switchTestAgent(t)
	prov := &llmtest.FakeProvider{}

	if err := setChatModel(prov, cfg, ag, nil, false, "qwen3-coder-small"); err != nil {
		t.Fatalf("setChatModel: %v", err)
	}
	if gotProvider, gotModel := ag.Backend(); gotProvider != "fake" || gotModel != "qwen3-coder-small" {
		t.Fatalf("agent backend = %s/%s", gotProvider, gotModel)
	}
}

func TestSetChatModel_LeavesBackendWhenModelIsRejected(t *testing.T) {
	// SetBackend rejects an empty model before changing anything, so the
	// running backend must stay on its previous model.
	cfg := customProviderConfig()
	ag := switchTestAgent(t)
	if err := setChatModel(&llmtest.FakeProvider{}, cfg, ag, nil, false, " "); err == nil {
		t.Fatal("expected an empty model to fail")
	}
	if _, model := ag.Backend(); model != "claude-opus-5" {
		t.Fatalf("agent model = %q, want the previous model", model)
	}
}

func TestRestoreChatModel_NoPreviousModelIsANoop(t *testing.T) {
	ag := switchTestAgent(t)
	restoreChatModel(&llmtest.FakeProvider{}, customProviderConfig(), ag, nil, false, "")
	if _, model := ag.Backend(); model != "claude-opus-5" {
		t.Fatalf("agent model = %q", model)
	}
}

func TestSwitchChatBackend_PersistsProviderAndModel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Source() != "embedded" {
		t.Fatalf("source = %q, want embedded", cfg.Source())
	}
	ag := switchTestAgent(t)
	prev := &llmtest.FakeProvider{}

	next := &llmtest.FakeProvider{}
	if err := switchChatBackend(cfg, ag, nil, false, next, "openai", "gpt-6.1-sol", prev, "claude-opus-5"); err != nil {
		t.Fatalf("switchChatBackend: %v", err)
	}
	if provider, model := ag.Backend(); provider != "fake" || model != "gpt-6.1-sol" {
		t.Fatalf("agent backend = %s/%s", provider, model)
	}
	written := filepath.Join(dir, "home", ".neo", "config.yaml")
	b, err := os.ReadFile(written)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(b), "\nprovider: openai\n") || !strings.Contains(string(b), "\nmodel: gpt-6.1-sol\n") {
		t.Fatalf("persisted config does not record the switch:\n%s", b)
	}
}

func TestSwitchChatBackend_FailedWriteRestoresBackend(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Point HOME at a regular file so creating ~/.neo/config.yaml must fail.
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocked)

	ag := switchTestAgent(t)
	prev, prevModel := &llmtest.FakeProvider{}, "claude-opus-5"
	if err := switchChatBackend(cfg, ag, nil, false, &llmtest.FakeProvider{}, "openai", "gpt-6.1-sol", prev, prevModel); err == nil {
		t.Fatal("expected the config write to fail")
	}
	if _, model := ag.Backend(); model != prevModel {
		t.Fatalf("agent model = %q after a failed switch, want %q", model, prevModel)
	}
}

func TestSwitchChatBackend_RejectsMissingModel(t *testing.T) {
	cfg := customProviderConfig()
	ag := switchTestAgent(t)
	if err := switchChatBackend(cfg, ag, nil, false, &llmtest.FakeProvider{}, "local", "", &llmtest.FakeProvider{}, "claude-opus-5"); err == nil {
		t.Fatal("expected an empty model to fail")
	}
	if _, model := ag.Backend(); model != "claude-opus-5" {
		t.Fatalf("agent model = %q, want the previous model", model)
	}
}
