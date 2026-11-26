package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/llm/custom"
	"github.com/owainlewis/neo/internal/llm/google"
	"github.com/owainlewis/neo/internal/llm/openai"
	"github.com/owainlewis/neo/internal/llm/openrouter"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tui"
)

func newProvider(cfg *config.Config, name string) (llm.Provider, error) {
	if p, ok := cfg.ProviderConfig(name); ok {
		return custom.New(customSpec(name, p))
	}
	switch name {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return newCodexProvider()
		}
		return openai.New()
	case "openrouter":
		return openrouter.New()
	case "google":
		return google.New()
	case "anthropic", "":
		return anthropic.New()
	default:
		return nil, fmt.Errorf("unknown provider %q; available: %s (declare a custom backend under providers: in neo.yaml)",
			name, strings.Join(cfg.ProviderNames(), ", "))
	}
}

func checkedProvider(ctx context.Context, cfg *config.Config, name string) (llm.Provider, error) {
	if name == "openai" && cfg.OpenAIAuth == config.OpenAIAuthSubscription {
		store, err := auth.DefaultStore()
		if err != nil {
			return nil, err
		}
		if _, err := auth.NewTokenSource(store, auth.ProviderOpenAICodex).Token(ctx); err != nil {
			return nil, fmt.Errorf("OpenAI subscription credentials: %w", err)
		}
	}
	return newProvider(cfg, name)
}

func chatSessionProvider(ctx context.Context, cfg *config.Config, sess *session.Session, name string) (llm.Provider, error) {
	if sess != nil {
		return checkedProvider(ctx, cfg, name)
	}
	return newProvider(cfg, name)
}

// newCodexProvider builds the ChatGPT/Codex subscription client from stored
// device-code credentials, erroring clearly if the user hasn't logged in.
func newCodexProvider() (llm.Provider, error) {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil, err
	}
	if _, ok, err := store.Get(auth.ProviderOpenAICodex); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not logged in to an OpenAI subscription: run `neo login`")
	}
	src := auth.NewTokenSource(store, auth.ProviderOpenAICodex)
	return openai.NewCodex(codexCredentials{ts: src}), nil
}

// codexCredentials adapts auth.TokenSource to openai.CredentialSource.
type codexCredentials struct{ ts *auth.TokenSource }

func (c codexCredentials) Token(ctx context.Context) (accessToken, accountID string, err error) {
	cr, err := c.ts.Token(ctx)
	if err != nil {
		// Only `neo login` fixes a missing or rejected login, so the provider
		// must not spend its retry budget on it. A transient refresh failure
		// (network, token endpoint 5xx) stays retryable.
		if errors.Is(err, auth.ErrLoginRequired) {
			err = retry.Permanent(err)
		}
		return "", "", err
	}
	return cr.AccessToken, cr.AccountID, nil
}

// runLogin performs the OpenAI subscription device-code flow and stores the
// result.
func runLogin(ctx context.Context, streams stdio) int {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(streams.err, "login: %v\n", err)
		return 1
	}

	creds, err := auth.LoginOpenAI(ctx, auth.LoginOptions{
		OnDeviceCode: func(url, code string) {
			fmt.Fprintln(streams.out, "Log in to OpenAI with this device code:")
			fmt.Fprintln(streams.out, "\n  "+url)
			fmt.Fprintln(streams.out, "  Code: "+code+"\n")
			fmt.Fprintln(streams.out, "The code expires after 15 minutes. Never share it.")
			fmt.Fprintln(streams.out, "Waiting for authorization to complete...")
		},
	})
	if err != nil {
		fmt.Fprintf(streams.err, "login failed: %v\n", err)
		return 1
	}
	if err := store.Set(auth.ProviderOpenAICodex, creds); err != nil {
		fmt.Fprintf(streams.err, "save credentials: %v\n", err)
		return 1
	}
	fmt.Fprintln(streams.out, "Login complete. Credentials saved to "+store.Path()+".")
	fmt.Fprintln(streams.out, "Set `provider: openai` and `openai_auth: subscription` in neo.yaml to use them.")
	return 0
}

// runLogout removes stored subscription credentials.
func runLogout(streams stdio) int {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(streams.err, "logout: %v\n", err)
		return 1
	}
	if err := store.Delete(auth.ProviderOpenAICodex); err != nil {
		fmt.Fprintf(streams.err, "logout: %v\n", err)
		return 1
	}
	fmt.Fprintln(streams.out, "Logged out of OpenAI subscription.")
	return 0
}

func sessionBackend(cfg *config.Config, meta session.Metadata, errOut io.Writer) (string, string) {
	if meta.Provider == "" || meta.Model == "" {
		return cfg.Provider, cfg.Model
	}
	savedProvider := sessionProviderID(meta.Provider)
	if savedProvider == "openai" {
		savedAuth := savedOpenAIAuth(meta)
		configuredAuth := configuredOpenAIAuth(cfg)
		if savedAuth != configuredAuth {
			fmt.Fprintf(errOut, "warning: session OpenAI auth mode %s does not match configured %s; continuing with %s model %s\n",
				savedAuth, configuredAuth, cfg.Provider, cfg.Model)
			return cfg.Provider, cfg.Model
		}
	}
	// A custom backend stays restorable while it is still declared; a missing
	// key then fails loudly when the provider is constructed instead of
	// silently moving the conversation to a different model.
	if cfg.IsCustomProvider(savedProvider) {
		return savedProvider, meta.Model
	}
	if savedProvider == cfg.Provider || providerCredentialPresent(cfg, savedProvider) {
		return savedProvider, meta.Model
	}
	fmt.Fprintf(errOut, "warning: session provider %s is not configured; continuing with %s model %s\n",
		meta.Provider, cfg.Provider, cfg.Model)
	return cfg.Provider, cfg.Model
}

// sessionProviderID maps adapter-specific names to stable configuration
// provider IDs suitable for session persistence and reconstruction. The Codex
// subscription adapter historically exposed openai-codex as its diagnostic
// name, but it is configured and resumed through the openai provider.
func sessionProviderID(adapterName string) string {
	if adapterName == "openai-codex" {
		return "openai"
	}
	return adapterName
}

func adapterOpenAIAuth(adapterName string) string {
	switch adapterName {
	case "openai-codex":
		return config.OpenAIAuthSubscription
	case "openai":
		return config.OpenAIAuthAPIKey
	default:
		return ""
	}
}

func savedOpenAIAuth(meta session.Metadata) string {
	if meta.OpenAIAuth != "" {
		return meta.OpenAIAuth
	}
	// Before auth mode was persisted, openai-codex was written only by the
	// subscription adapter and openai only by the API-key adapter.
	return adapterOpenAIAuth(meta.Provider)
}

func configuredOpenAIAuth(cfg *config.Config) string {
	if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
		return config.OpenAIAuthSubscription
	}
	return config.OpenAIAuthAPIKey
}

func modelChoices(cfg *config.Config, activeProvider string) []tui.ModelChoice {
	if activeProvider == "" {
		activeProvider = cfg.Provider
		if activeProvider == "" {
			activeProvider = "anthropic"
		}
	}
	return providerModelChoices(cfg, activeProvider)
}

// customModelChoices maps a custom provider's static model list into the
// picker. It is the fallback while the live catalogue loads, so an omitted list
// simply leaves the picker showing the current model.
func customModelChoices(p config.CustomProvider) []tui.ModelChoice {
	choices := make([]tui.ModelChoice, 0, len(p.Models))
	for _, m := range p.Models {
		choices = append(choices, tui.ModelChoice{ID: m.ID, Name: m.Name, Description: m.Description})
	}
	return choices
}

func providerCredentialPresent(cfg *config.Config, provider string) bool {
	// A custom provider with no api_key_env needs no credential at all, so it
	// is always usable; otherwise it is present only when its variable is set.
	if p, ok := cfg.ProviderConfig(provider); ok {
		return customCredentialPresent(p)
	}
	switch provider {
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			store, err := auth.DefaultStore()
			if err != nil {
				return false
			}
			_, ok, err := store.Get(auth.ProviderOpenAICodex)
			return err == nil && ok
		}
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	case "openrouter":
		return strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != ""
	case "google":
		return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
	default:
		return false
	}
}

// customCredentialPresent reports whether a custom provider's credential is
// configured. Endpoints without api_key_env are unauthenticated by design.
func customCredentialPresent(p config.CustomProvider) bool {
	if p.APIKeyEnv == "" {
		return true
	}
	return strings.TrimSpace(os.Getenv(p.APIKeyEnv)) != ""
}

func providerModelChoices(cfg *config.Config, provider string) []tui.ModelChoice {
	if p, ok := cfg.ProviderConfig(provider); ok {
		return customModelChoices(p)
	}
	switch provider {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return []tui.ModelChoice{
				{ID: openai.DefaultCodexModel, Name: "GPT-6.1 Sol", Description: "Supported ChatGPT/Codex subscription model"},
			}
		}
		return []tui.ModelChoice{
			{ID: openai.DefaultModel, Name: "GPT-6.1 Sol", Description: "Recommended flagship model for coding and agentic tasks"},
			{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", Description: "Balance of intelligence and cost, competitive with GPT-5.5"},
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", Description: "Fastest, most affordable GPT-5.6 model"},
			{ID: "gpt-5.2", Name: "GPT-5.2", Description: "Previous-generation flagship model"},
			{ID: "gpt-5.1", Name: "GPT-5.1", Description: "Coding and agentic model with configurable reasoning"},
			{ID: "gpt-5", Name: "GPT-5", Description: "Previous GPT-5 reasoning model"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Description: "Faster, lower-cost GPT-5 model"},
			{ID: "gpt-5-nano", Name: "GPT-5 nano", Description: "Smallest GPT-5 model"},
			{ID: "gpt-4.1", Name: "GPT-4.1", Description: "Non-reasoning model for general coding tasks"},
			{ID: "gpt-4o", Name: "GPT-4o", Description: "Fast multimodal GPT-4o model"},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", Description: "Smaller GPT-4o model"},
		}
	case "openrouter":
		// The live catalogue is loaded lazily when the model browser opens;
		// startup must not wait on the network. normalizeModelChoices adds the
		// configured model when it differs from the default.
		return []tui.ModelChoice{
			{ID: openrouter.DefaultModel, Name: openrouter.DefaultModel, Description: "Default OpenRouter model"},
		}
	case "google":
		return []tui.ModelChoice{
			{ID: google.DefaultModel, Name: "Gemini 3.5 Flash", Description: "Stable Google Gemini model for coding and agentic tasks"},
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro Preview", Description: "Higher-capability preview model for complex coding tasks"},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash-Lite", Description: "Lower-cost stable Gemini model"},
		}
	default:
		return []tui.ModelChoice{
			{ID: "claude-opus-5", Name: "Claude Opus 5", Description: "Default Anthropic model, flagship reasoning and coding"},
			{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", Description: "Faster, lower-cost frontier Anthropic model"},
			{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", Description: "Smallest, fastest Anthropic model"},
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Description: "Previous-generation Anthropic model"},
		}
	}
}

// loadOpenRouterModelChoices fetches the live OpenRouter model catalogue for
// the model picker. It is called from a TUI command when the picker opens, not
// during startup, and is time-boxed so a slow network cannot hang the picker.
func loadOpenRouterModelChoices(ctx context.Context) ([]tui.ModelChoice, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	models, err := openrouter.Models(fetchCtx, nil)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("openrouter: empty model catalogue")
	}

	choices := make([]tui.ModelChoice, 0, len(models))
	for _, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		choices = append(choices, tui.ModelChoice{ID: m.ID, Name: name, Description: m.Description})
	}
	return choices, nil
}

// loadCustomModelChoices fetches a custom backend's live model catalogue for
// the picker. It reuses the configured static list for display names, so a
// gateway that lists ids only still shows the friendly names of known models.
// The request is time-boxed: a slow or offline gateway leaves the fallback
// visible and reports the error inside the picker.
func loadCustomModelChoices(cfg *config.Config, name string) func(context.Context) ([]tui.ModelChoice, error) {
	p, ok := cfg.ProviderConfig(name)
	if !ok {
		return nil
	}
	spec := customSpec(name, p)
	known := make(map[string]tui.ModelChoice, len(p.Models))
	for _, m := range customModelChoices(p) {
		known[m.ID] = m
	}
	return func(ctx context.Context) ([]tui.ModelChoice, error) {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		models, err := custom.Models(fetchCtx, spec, nil)
		if err != nil {
			return nil, err
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("%s: empty model catalogue", name)
		}

		choices := make([]tui.ModelChoice, 0, len(models))
		for _, m := range models {
			choice := tui.ModelChoice{ID: m.ID, Name: m.Name, Description: m.Description}
			if prev, ok := known[m.ID]; ok {
				if strings.TrimSpace(choice.Name) == "" {
					choice.Name = prev.Name
				}
				if strings.TrimSpace(choice.Description) == "" {
					choice.Description = prev.Description
				}
			}
			choices = append(choices, choice)
		}
		return choices, nil
	}
}

// modelChoicesLoader returns the lazy catalogue loader for providers whose
// model list is not static. Other providers return nil.
func modelChoicesLoader(cfg *config.Config, provider string) func(context.Context) ([]tui.ModelChoice, error) {
	switch {
	case cfg.IsCustomProvider(provider):
		return loadCustomModelChoices(cfg, provider)
	case provider == "openrouter":
		return loadOpenRouterModelChoices
	default:
		return nil
	}
}

// providerChoices lists every usable backend for the /provider picker: the
// built-ins plus any declared custom provider, each with the credential source
// a user would need to make it work.
func providerChoices(cfg *config.Config) []tui.ProviderChoice {
	names := cfg.ProviderNames()
	choices := make([]tui.ProviderChoice, 0, len(names))
	for _, name := range names {
		choices = append(choices, tui.ProviderChoice{
			ID:          name,
			Name:        name,
			Description: providerDescription(cfg, name),
		})
	}
	return choices
}

func providerDescription(cfg *config.Config, name string) string {
	if _, ok := cfg.ProviderConfig(name); ok {
		return "custom OpenAI-compatible backend"
	}
	switch name {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return "ChatGPT/Codex subscription (neo login)"
		}
		return "OpenAI API key (OPENAI_API_KEY)"
	case "openrouter":
		return "OpenRouter (OPENROUTER_API_KEY)"
	case "google":
		return "Google Gemini (GOOGLE_API_KEY)"
	default:
		return "Anthropic API key (ANTHROPIC_API_KEY)"
	}
}

// chatCompactor builds the summarizer for one model, resolving the window from
// the config. Every path that changes the model — startup, /model, /provider,
// and a background discovery refresh — rebuilds it, because a window belongs to
// a model and carrying the previous one over would keep the wrong trigger.
func chatCompactor(prov llm.Provider, model string, cfg *config.Config) compact.Compactor {
	name := ""
	if prov != nil {
		name = prov.Name()
	}
	return compact.NewSummarizerForContextWindow(prov, model, cfg.ContextWindowFor(name, model))
}

// setChatModel moves the running chat — the coordinator and a subagent runner
// that follows it — onto model, keeping the current provider. SetBackend
// validates before it changes anything, so a rejected model leaves the
// running chat as it was.
func setChatModel(prov llm.Provider, cfg *config.Config, ag *agent.Agent, runner *subagent.AgentRunner, runnerFollows bool, model string) error {
	if err := ag.SetBackend(prov, model, chatCompactor(prov, model, cfg)); err != nil {
		return err
	}
	if runner != nil && runnerFollows {
		if err := runner.SetBackend(prov, model); err != nil {
			return err
		}
	}
	return nil
}

// restoreChatModel puts the chat back on the model it had before a failed
// /model switch. An empty model leaves the running chat as it is.
func restoreChatModel(prov llm.Provider, cfg *config.Config, ag *agent.Agent, runner *subagent.AgentRunner, runnerFollows bool, model string) {
	if prov == nil || model == "" {
		return
	}
	_ = ag.SetBackend(prov, model, chatCompactor(prov, model, cfg))
	if runner != nil && runnerFollows {
		_ = runner.SetBackend(prov, model)
	}
}

// switchChatBackend moves the coordinator — and a subagent runner that follows
// it — onto next, then rewrites provider and model in the config file Neo
// loaded. Any failure restores the previous backend, so a failed switch leaves
// the running chat and the config file unchanged.
func switchChatBackend(cfg *config.Config, ag *agent.Agent, runner *subagent.AgentRunner, runnerFollows bool, next llm.Provider, name, model string, prev llm.Provider, prevModel string) error {
	if err := ag.SetBackend(next, model, chatCompactor(next, model, cfg)); err != nil {
		return err
	}
	if runner != nil && runnerFollows {
		if err := runner.SetBackend(next, model); err != nil {
			restoreChatBackend(cfg, ag, runner, runnerFollows, prev, prevModel)
			return err
		}
	}
	if _, err := cfg.WriteBackend(name, model); err != nil {
		restoreChatBackend(cfg, ag, runner, runnerFollows, prev, prevModel)
		return err
	}
	return nil
}

// restoreChatBackend puts the coordinator, and a following subagent runner,
// back on the backend they had before a failed switch.
func restoreChatBackend(cfg *config.Config, ag *agent.Agent, runner *subagent.AgentRunner, runnerFollows bool, prev llm.Provider, prevModel string) {
	if prev == nil || prevModel == "" {
		return
	}
	_ = ag.SetBackend(prev, prevModel, chatCompactor(prev, prevModel, cfg))
	if runner != nil && runnerFollows {
		_ = runner.SetBackend(prev, prevModel)
	}
}

// customSpec is the llm-layer view of a configured custom provider. Both the
// chat client and the catalogue fetch build it, so a new field cannot be
// forgotten on one of the two paths.
func customSpec(name string, p config.CustomProvider) custom.Spec {
	return custom.Spec{
		Name:         name,
		Endpoint:     p.Endpoint,
		APIKeyEnv:    p.APIKeyEnv,
		DefaultModel: p.DefaultModel,
	}
}

// discoveryTimeout bounds the background catalogue fetch that looks for a
// context window. It matches the picker's own budget: this is the same request
// made without a user waiting on it.
const discoveryTimeout = 5 * time.Second

// requestContextWindowDiscovery starts a background fetch of a custom backend's
// catalogue so the window it reports can be used without the user configuring
// one. It returns a channel that is closed when discovery has finished, or nil
// for providers that are not custom: a built-in backend's models are not
// declared in the config and its catalogue is not probed.
//
// Discovery is asynchronous on purpose. The fetch is a network call, and both
// startup and a /model switch have to stay responsive; a slow or offline
// gateway must cost nothing but the window Neo would have guessed anyway.
//
// onDone runs in the fetching goroutine once the catalogue answers, so it may
// touch cfg and the agent but must not block on the UI. It runs even when
// nothing was discovered, because a caller refreshes the compactor either way.
func requestContextWindowDiscovery(cfg *config.Config, name, model string, onDone func()) <-chan struct{} {
	if cfg == nil || !cfg.IsCustomProvider(name) {
		return nil
	}
	p, ok := cfg.ProviderConfig(name)
	if !ok {
		return nil
	}
	spec := customSpec(name, p)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		models, err := custom.Models(ctx, spec, nil)
		if err != nil {
			// A backend that lists no catalogue, or answers with something Neo
			// cannot read, simply has no discoverable window. Say so in the debug
			// log and keep the configured or default window.
			logx.Debug("context window discovery failed", "provider", name, "error", logx.SafeString(err.Error(), 200))
		} else {
			for _, m := range models {
				cfg.SetDiscoveredContextWindow(name, m.ID, m.ContextWindowTokens)
			}
			if window, ok := cfg.DiscoveredContextWindow(name, model); ok {
				logx.Debug("context window discovered", "provider", name, "model", model, "window", window)
			}
		}
		if onDone != nil {
			onDone()
		}
	}()
	return done
}

// waitContextWindowDiscovery blocks until the background discovery finishes, or
// until ctx ends. A caller that dropped the wait would simply start on the
// configured window, which is the pre-#109 behaviour, so the wait never turns a
// slow gateway into a failure.
func waitContextWindowDiscovery(done <-chan struct{}, ctx context.Context) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}
