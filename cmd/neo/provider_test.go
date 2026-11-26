package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
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

// TestSetChatModelSwapsTheCompactionWindow covers the half of #108 that a config
// test cannot: the summarizer is rebuilt on every /model, so the new model has
// to arrive with its own window rather than inheriting the previous one.
func TestSetChatModelSwapsTheCompactionWindow(t *testing.T) {
	cfg := &config.Config{
		Compaction: config.Compaction{ContextWindowTokens: 200_000},
		Providers: map[string]config.CustomProvider{
			"local40k": {
				Endpoint: "http://127.0.0.1:8080/v1",
				Models: []config.CustomModel{
					{ID: "Bonsai2.5-27B", ContextWindowTokens: 40_192},
				},
			},
		},
	}
	// The provider has to carry the config's name: the window is resolved by
	// (provider, model), so a stand-in named "fake" would silently fall back to
	// the global key and the test would assert nothing.
	prov := &namedProvider{FakeProvider: &llmtest.FakeProvider{}, name: "local40k"}
	ag := agent.New(agent.Config{Model: "Bonsai2.5-27B", Provider: prov})

	// Start on the local model, then switch to a hosted one with no override.
	if err := setChatModel(prov, cfg, ag, nil, false, "Bonsai2.5-27B"); err != nil {
		t.Fatal(err)
	}
	if got, want := summarizerTrigger(t, ag), compact.TriggerTokensForContextWindow(40_192); got != want {
		t.Fatalf("local model trigger = %d, want %d", got, want)
	}
	if err := setChatModel(prov, cfg, ag, nil, false, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}
	if got, want := summarizerTrigger(t, ag), compact.TriggerTokensForContextWindow(200_000); got != want {
		t.Fatalf("hosted model trigger = %d, want %d", got, want)
	}
}

// TestRequestContextWindowDiscoveryFillsTheModelWindow covers the CLI half of
// #109: the catalogue fetch runs in the background, records what the backend
// reported, and the summarizer rebuilt afterwards uses it.
func TestRequestContextWindowDiscoveryFillsTheModelWindow(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"Bonsai2.5-27B","meta":{"n_ctx":40192}}]}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		Compaction: config.Compaction{ContextWindowTokens: 200_000},
		Providers: map[string]config.CustomProvider{
			"local": {
				Endpoint:     server.URL + "/v1/chat/completions",
				DefaultModel: "Bonsai2.5-27B",
			},
		},
	}
	prov := &namedProvider{FakeProvider: &llmtest.FakeProvider{}, name: "local"}
	ag := agent.New(agent.Config{Model: "Bonsai2.5-27B", Provider: prov})

	// The callback mirrors what buildChatRuntime installs: refresh the
	// compactor for the model the agent is still on.
	done := requestContextWindowDiscovery(cfg, "local", "Bonsai2.5-27B", func() {
		_, current := ag.Backend()
		_ = ag.SetBackend(prov, current, chatCompactor(prov, current, cfg))
	})
	waitContextWindowDiscovery(done, t.Context())

	if sawPath != "/v1/models" {
		t.Fatalf("fetched %q, want the catalogue at /v1/models", sawPath)
	}
	if got, ok := cfg.DiscoveredContextWindow("local", "Bonsai2.5-27B"); !ok || got != 40_192 {
		t.Fatalf("discovered = %d/%v, want 40192", got, ok)
	}
	// 40192 * 0.7 = 28134, which is the trigger the acceptance criteria name.
	if got, want := summarizerTrigger(t, ag), compact.TriggerTokensForContextWindow(40_192); got != want {
		t.Fatalf("trigger = %d, want %d for the discovered window", got, want)
	}
}

// TestRequestContextWindowDiscoveryIsSkippedForBuiltins keeps the probe off
// providers whose catalogues Neo has no reason to read.
func TestRequestContextWindowDiscoveryIsSkippedForBuiltins(t *testing.T) {
	if done := requestContextWindowDiscovery(&config.Config{}, "anthropic", "claude-opus-5", nil); done != nil {
		t.Fatal("a built-in provider must not start a discovery fetch")
	}
	if done := requestContextWindowDiscovery(nil, "local", "m", nil); done != nil {
		t.Fatal("a nil config must not start a discovery fetch")
	}
}

// TestRequestContextWindowDiscoverySurvivesABadCatalogue pins the silent
// fallback: an unreachable or unhelpful backend leaves the configured window in
// place and the session still starts.
func TestRequestContextWindowDiscoverySurvivesABadCatalogue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no catalogue here", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := &config.Config{
		Compaction: config.Compaction{ContextWindowTokens: 200_000},
		Providers: map[string]config.CustomProvider{
			"local": {Endpoint: server.URL + "/v1", DefaultModel: "m"},
		},
	}
	waited := false
	done := requestContextWindowDiscovery(cfg, "local", "m", func() { waited = true })
	waitContextWindowDiscovery(done, t.Context())

	if !waited {
		t.Fatal("the completion callback must run even when nothing was discovered")
	}
	if _, ok := cfg.DiscoveredContextWindow("local", "m"); ok {
		t.Fatal("a failed fetch must not record a window")
	}
	if got := cfg.ContextWindowFor("local", "m"); got != 200_000 {
		t.Fatalf("window = %d, want the configured 200000 to survive", got)
	}
}

// namedProvider lets a test give the fake provider the name a config uses, since
// window resolution looks the model up under that name.
type namedProvider struct {
	*llmtest.FakeProvider
	name string
}

func (p *namedProvider) Name() string { return p.name }

// summarizerTrigger reads the trigger the agent compactor will use. A missing
// summarizer fails the test rather than returning zero, so an agent built with
// NoCompaction cannot pass by accident.
func summarizerTrigger(t *testing.T, ag *agent.Agent) int {
	t.Helper()
	compactor := ag.Compactor()
	s, ok := compactor.(*compact.Summarizer)
	if !ok {
		t.Fatalf("compactor is %T, want *compact.Summarizer", compactor)
	}
	return s.TriggerTokens
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

// TestProviderNameMatchesItsConfigKey pins the assumption window resolution
// rests on: ContextWindowFor is keyed by provider name, so a provider whose
// Name() disagreed with its config key would silently lose its model override.
// Built-in and custom backends are checked because they set the name in two
// different places.
func TestProviderNameMatchesItsConfigKey(t *testing.T) {
	// Building a built-in backend needs a credential to exist; the value is
	// never used because no request is made.
	for _, env := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(env, "test-key")
	}
	cfg := customProviderConfig()
	for _, env := range cfg.CustomAPIKeyEnvNames() {
		t.Setenv(env, "test-key")
	}
	for _, name := range cfg.ProviderNames() {
		prov, err := newProvider(cfg, name)
		if err != nil {
			t.Fatalf("newProvider(%q): %v", name, err)
		}
		if got := prov.Name(); got != name {
			t.Fatalf("provider %q reports Name() = %q; model-level windows would be looked up under the wrong key", name, got)
		}
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

func TestModelChoicesLoader_CustomProviderFetchesCatalogue(t *testing.T) {
	cfg := customProviderConfig()
	p := cfg.Providers["local"]
	p.Models = []config.CustomModel{{ID: "qwen3-coder", Name: "Qwen3 Coder", Description: "Local coder model"}}
	cfg.Providers["local"] = p

	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-coder"}]}`))
	}))
	defer srv.Close()
	cfg.Providers["local"] = config.CustomProvider{
		Endpoint:  srv.URL + "/v1",
		APIKeyEnv: p.APIKeyEnv,
		Models:    p.Models,
	}
	t.Setenv("LOCAL_LLM_API_KEY", "secret")

	loader := modelChoicesLoader(cfg, "local")
	if loader == nil {
		t.Fatal("custom providers load their catalogue when /model opens")
	}
	if static := modelChoicesLoader(cfg, "anthropic"); static != nil {
		t.Fatal("built-in providers with static lists need no loader")
	}

	choices, err := loader(t.Context())
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q, want the configured key", gotAuth)
	}
	if len(choices) != 1 || choices[0].ID != "qwen3-coder" {
		t.Fatalf("choices = %#v, want the live catalogue", choices)
	}
	// The live id keeps the configured display name from the static list.
	if choices[0].Name != "Qwen3 Coder" {
		t.Fatalf("name = %q, want the configured name", choices[0].Name)
	}
}

func TestModelChoicesLoader_CustomProviderErrorsOnFailedFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := customProviderConfig()
	cfg.Providers["local"] = config.CustomProvider{Endpoint: srv.URL + "/v1"}
	loader := modelChoicesLoader(cfg, "local")
	if loader == nil {
		t.Fatal("expected a loader for a custom provider")
	}
	if _, err := loader(t.Context()); err == nil {
		t.Fatal("expected a failed catalogue fetch to surface an error")
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
