// Package config loads neo's single configuration file
// (neo.yaml / ~/.neo/config.yaml / embedded default).
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resolution paths and embedded fallback.
const (
	projectConfigName = "neo.yaml"
	userConfigDir     = ".neo"
	userConfigName    = "config.yaml"

	defaultModel           = "claude-opus-5"
	defaultOpenAIModel     = "gpt-6.1-sol"
	defaultCodexModel      = "gpt-6.1-sol"
	defaultOpenRouterModel = "anthropic/claude-sonnet-5"
	defaultGoogleModel     = "gemini-3.5-flash"
	defaultProvider        = "anthropic"

	// OpenAI auth modes (the openai_auth config key).
	OpenAIAuthAPIKey       = "api_key"
	OpenAIAuthSubscription = "subscription"
)

//go:embed defaults/neo.yaml
var embeddedConfigYAML []byte

// Config is the parsed neo.yaml.
type Config struct {
	// Provider selects the LLM backend: "anthropic" (default), "openai", "openrouter", or "google".
	Provider string `yaml:"provider"`
	// OpenAIAuth selects how the "openai" provider authenticates: "api_key"
	// (default, uses OPENAI_API_KEY) or "subscription" (ChatGPT/Codex
	// device-code credentials via `neo login`). This also applies when OpenAI
	// is selected through /model while another provider is the startup default.
	OpenAIAuth string  `yaml:"openai_auth"`
	Model      string  `yaml:"model"`
	Subagents  Backend `yaml:"subagents"`
	// Providers declares optional OpenAI-compatible backends selected by name
	// through `provider` or `subagents.provider`. Built-in names are reserved.
	Providers  map[string]CustomProvider `yaml:"providers"`
	Features   Features                  `yaml:"features"`
	Compaction Compaction                `yaml:"compaction"`
	Goals      Goals                     `yaml:"goals"`
	// ToolApprovals lists optional interactive tool names and Bash prefixes.
	ToolApprovals []string `yaml:"tool_approvals"`
	Output        Output   `yaml:"output"`

	// source records where this config was loaded from (a file path or
	// "embedded"); surfaced in diagnostics via Source().
	source string

	// discovered holds context windows read from a custom backend's live
	// catalogue, keyed by provider then model. It is deliberately not a yaml
	// field: discovery is a runtime observation, never something the user wrote,
	// and it must lose to anything the user did write (see ContextWindowFor).
	discovered map[string]map[string]int
}

// Backend optionally pins chat-spawned subagents to a provider and model.
// A zero value means subagents follow the coordinator's active backend.
type Backend struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// SubagentsConfigured reports whether subagents use a backend independent of
// the coordinator. parseConfig fills either missing half when one is supplied.
func (c *Config) SubagentsConfigured() bool {
	return c.Subagents.Provider != "" || c.Subagents.Model != ""
}

// Output configures how Neo renders tool activity during a chat session.
type Output struct {
	// Verbose restores the full tool call/result rendering (complete file
	// contents, command output, etc). Defaults to false: Neo shows live activity
	// and concise completed receipts while still surfacing errors in full.
	Verbose *bool `yaml:"verbose"`
}

// VerboseEnabled reports whether full tool output rendering is on (default: false).
func (c *Config) VerboseEnabled() bool { return featureEnabled(c.Output.Verbose, false) }

// DefaultGoalMaxAttempts bounds how many automatic continuation turns one goal
// may start when goals.max_attempts is not set. The turn started by
// /goal <objective> is not one of them.
const DefaultGoalMaxAttempts = 20

// Goals configures the /goal attempt budget. Attempts count automatic
// continuation turns only; Phase 1 has no token or wall-clock accounting.
type Goals struct {
	// MaxAttempts is the number of continuation turns a goal may start before
	// it becomes budget_limited. Omitted or zero means the default.
	MaxAttempts int `yaml:"max_attempts"`
}

// GoalMaxAttempts returns the effective continuation budget.
func (c *Config) GoalMaxAttempts() int {
	if c == nil || c.Goals.MaxAttempts <= 0 {
		return DefaultGoalMaxAttempts
	}
	return c.Goals.MaxAttempts
}

// Compaction configures when long transcripts are summarized.
type Compaction struct {
	// ContextWindowTokens is the global fallback for the active model's context
	// window. A model can override it through
	// providers.<name>.models[].context_window_tokens, which is what a config
	// serving models with different windows needs; see ContextWindowFor. When
	// neither is set, callers fall back to the compact package's default.
	ContextWindowTokens int `yaml:"context_window_tokens"`
}

// ContextWindowFor resolves the context window for one model, or 0 when nothing
// is configured and the caller should use the compact package's default.
//
// The model's own setting wins over the global one, because a window belongs to
// a model: a 40K local model and a 200K hosted model in the same config cannot
// share a trigger. Resolution is by (provider, model) and has to stay in one
// place -- the coordinator, the subagent runner and each worker all need it, and
// three copies would drift the way the script console did in #92.
//
// Precedence is: a model's explicit context_window_tokens, then a window the
// backend reported in its live catalogue, then compaction.context_window_tokens
// (200,000 in the embedded defaults), then the compact package's default.
// Discovery sits below the explicit keys because it is a guess about a server
// Neo does not control, and above the global key because the default file sets
// that one, so placing discovery below it would make it unreachable.
//
// Only custom providers can name a model's window; a built-in provider's models
// are not declared in the config and their catalogues are not probed, so they
// fall back to the global key.
func (c *Config) ContextWindowFor(provider, model string) int {
	window, _ := c.ContextWindow(provider, model)
	return window
}

// SetDiscoveredContextWindow records a window a custom backend reported for one
// model. Recording is best effort by design: discovery runs in the background
// and a failure must not change how a session starts, so callers ignore the
// error rather than reporting it.
func (c *Config) SetDiscoveredContextWindow(provider, model string, tokens int) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if c == nil || provider == "" || model == "" || tokens <= 0 {
		return
	}
	if c.discovered == nil {
		c.discovered = make(map[string]map[string]int)
	}
	if c.discovered[provider] == nil {
		c.discovered[provider] = make(map[string]int)
	}
	c.discovered[provider][model] = tokens
}

// DiscoveredContextWindow reports a window the backend listed for this model.
// The second result separates "nothing is known" from "known", so a caller can
// tell a discovered window apart from a fallback.
func (c *Config) DiscoveredContextWindow(provider, model string) (int, bool) {
	if c == nil || c.discovered == nil {
		return 0, false
	}
	models, ok := c.discovered[strings.TrimSpace(provider)]
	if !ok {
		return 0, false
	}
	window, ok := models[strings.TrimSpace(model)]
	return window, ok && window > 0
}

// ContextWindowSource names where a model's window came from, so the UI can
// explain why compaction fires where it does.
type ContextWindowSource string

const (
	// ContextWindowFromModel is a providers.<name>.models[].context_window_tokens
	// entry.
	ContextWindowFromModel ContextWindowSource = "config"
	// ContextWindowFromCatalogue is a window the backend reported itself.
	ContextWindowFromCatalogue ContextWindowSource = "discovered"
	// ContextWindowFromDefault is compaction.context_window_tokens or, when even
	// that is unset, the compact package's built-in default.
	ContextWindowFromDefault ContextWindowSource = "default"
)

// ContextWindow returns the window for one model and where it came from.
// ContextWindowFor answers the same question without the provenance; this adds
// it so a session can say "discovered" rather than leaving a user to guess
// whether a number was configured.
func (c *Config) ContextWindow(provider, model string) (int, ContextWindowSource) {
	if c == nil {
		return 0, ContextWindowFromDefault
	}
	model = strings.TrimSpace(model)
	if p, ok := c.ProviderConfig(provider); ok {
		for _, m := range p.Models {
			if m.ID == model && m.ContextWindowTokens > 0 {
				return m.ContextWindowTokens, ContextWindowFromModel
			}
		}
	}
	if window, ok := c.DiscoveredContextWindow(provider, model); ok {
		return window, ContextWindowFromCatalogue
	}
	if c.Compaction.ContextWindowTokens > 0 {
		return c.Compaction.ContextWindowTokens, ContextWindowFromDefault
	}
	return 0, ContextWindowFromDefault
}

// Features toggles optional, layered capabilities. The core agent loop is never
// gated by a feature — only capabilities built on top of it. Each flag is a
// tri-state *bool: nil ("absent from config") falls back to a built-in default,
// so a minimal neo.yaml still gets the full experience; set a flag to false to
// turn the capability off explicitly.
type Features struct {
	AgentsFile    *bool `yaml:"agents_file"`    // load AGENTS.md into the chat system prompt
	Skills        *bool `yaml:"skills"`         // discover and expand $name and /name skills
	PromptCaching *bool `yaml:"prompt_caching"` // cache the static system prompt prefix
	// Script registers the JavaScript `script` tool. It is on by default: the
	// tool is how a session writes code instead of issuing one call per step, and
	// the capability is what the prompt and the tool list are built around. Set it
	// to false to go back to a surface with no code execution.
	Script *bool `yaml:"script"`
	// CodeMode is the session's surface shape, read once at startup so the tool
	// list and base prompt never change mid-conversation. "codemode" is the
	// default: every file and command action is written as code inside the script
	// tool, which also carries run(), a console and one interpreter for the
	// session. "classic" keeps the separate bash, read_file, write_file, edit_file,
	// grep and glob tools. Either way the coordinating tools (agent, goal,
	// workflow) stay.
	CodeMode string `yaml:"codemode"`
}

// The two CodeMode values. CodeModeCodemode is the default and also what an
// unrecognized value falls back to.
const (
	CodeModeCodemode = "codemode"
	CodeModeClassic  = "classic"
)

// validateCodeMode rejects a surface this build does not have. The CLI flag is
// already checked, but a config file is where the value usually comes from, and
// ClassicCodeMode's equality test silently treated a typo -- or a mode removed
// from the code, such as the old "solo" -- as the default. A session would then
// run on a surface the user did not ask for with nothing to notice. An empty
// value means "unset", which is the default and stays valid.
func validateCodeMode(mode, source string) error {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return nil
	}
	if strings.EqualFold(trimmed, CodeModeCodemode) || strings.EqualFold(trimmed, CodeModeClassic) {
		return nil
	}
	return fmt.Errorf("%s: codemode must be %q or %q, got %q; fix the value in that file", source, CodeModeCodemode, CodeModeClassic, mode)
}

// ClassicCodeMode reports whether the session keeps the separate per-action
// tools instead of writing code.
func (c *Config) ClassicCodeMode() bool {
	return strings.EqualFold(strings.TrimSpace(c.Features.CodeMode), CodeModeClassic)
}

// AgentsFileEnabled reports whether AGENTS.md loading is on (default: true).
func (c *Config) AgentsFileEnabled() bool { return featureEnabled(c.Features.AgentsFile, true) }

// SkillsEnabled reports whether skill loading is on (default: true).
func (c *Config) SkillsEnabled() bool { return featureEnabled(c.Features.Skills, true) }

// ScriptEnabled reports whether the JavaScript `script` tool is registered
// (default: true).
func (c *Config) ScriptEnabled() bool { return featureEnabled(c.Features.Script, true) }

// SetScript records the script-tool preference in memory, so a switch made in
// the TUI survives the prompt rebuild that follows it.
func (c *Config) SetScript(on bool) { c.Features.Script = &on }

// PromptCachingEnabled reports whether the static system prompt is marked for
// provider-side prompt caching (default: true).
func (c *Config) PromptCachingEnabled() bool {
	return featureEnabled(c.Features.PromptCaching, true)
}

// featureEnabled resolves a tri-state flag: nil means "unset — use the
// default"; a non-nil pointer means the user set it explicitly.
func featureEnabled(flag *bool, def bool) bool {
	if flag == nil {
		return def
	}
	return *flag
}

// Load reads the first available config file:
//
//	./neo.yaml → ~/.neo/config.yaml → embedded default
//
// First hit wins — no merging.
func Load() (*Config, error) {
	home, _ := os.UserHomeDir()

	for _, path := range configPaths(home) {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		cfg, err := parseConfig(b, path)
		if err != nil {
			return nil, err
		}
		cfg.source = path
		return cfg, nil
	}

	cfg, err := parseConfig(embeddedConfigYAML, "embedded:neo.yaml")
	if err != nil {
		return nil, err
	}
	cfg.source = "embedded"
	return cfg, nil
}

func configPaths(home string) []string {
	paths := []string{projectConfigName}
	if home != "" {
		paths = append(paths, filepath.Join(home, userConfigDir, userConfigName))
	}
	return paths
}

func parseConfig(b []byte, source string) (*Config, error) {
	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(b, &keys); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if _, ok := keys["permissions"]; ok {
		return nil, fmt.Errorf("%s: permissions has been removed; use tool_approvals for optional interactive confirmations", source)
	}
	if _, ok := keys["phases"]; ok {
		return nil, fmt.Errorf("%s: phases has been removed; put each prompt in .neo/skills/<name>/SKILL.md (a skill named design, plan, build, or review replaces the built-in one)", source)
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if c.Provider == "" {
		c.Provider = defaultProvider
	}
	if c.Provider == "openai" && c.OpenAIAuth == "" {
		c.OpenAIAuth = OpenAIAuthAPIKey
	}
	if err := validateOpenAIAuth(c.OpenAIAuth, source); err != nil {
		return nil, err
	}
	if err := validateCodeMode(c.Features.CodeMode, source); err != nil {
		return nil, err
	}
	if err := c.normalizeProviders(source); err != nil {
		return nil, err
	}
	if !c.HasProvider(c.Provider) {
		return nil, c.unknownProviderError(source, "provider", c.Provider)
	}
	if c.Model == "" {
		c.Model = c.defaultModelForProvider(c.Provider)
	}
	if c.SubagentsConfigured() {
		if c.Subagents.Provider == "" {
			c.Subagents.Provider = c.Provider
		}
		if !c.HasProvider(c.Subagents.Provider) {
			return nil, c.unknownProviderError(source, "subagents.provider", c.Subagents.Provider)
		}
		if c.Subagents.Model == "" {
			c.Subagents.Model = c.defaultModelForProvider(c.Subagents.Provider)
		}
	}
	if c.Goals.MaxAttempts < 0 {
		return nil, fmt.Errorf("%s: goals.max_attempts must not be negative (got %d); omit it for the default of %d",
			source, c.Goals.MaxAttempts, DefaultGoalMaxAttempts)
	}
	seenApprovals := make(map[string]struct{}, len(c.ToolApprovals))
	approvals := make([]string, 0, len(c.ToolApprovals))
	for i, entry := range c.ToolApprovals {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%s: tool_approvals[%d] must not be empty", source, i)
		}
		if _, duplicate := seenApprovals[entry]; duplicate {
			continue
		}
		seenApprovals[entry] = struct{}{}
		approvals = append(approvals, entry)
	}
	c.ToolApprovals = approvals
	return &c, nil
}

func validateOpenAIAuth(mode, source string) error {
	switch mode {
	case "", OpenAIAuthAPIKey, OpenAIAuthSubscription:
		return nil
	default:
		return fmt.Errorf("%s: openai_auth must be one of %q, %q (got %q)", source, OpenAIAuthAPIKey, OpenAIAuthSubscription, mode)
	}
}

// SubscriptionAuth reports whether the openai provider should authenticate via
// a ChatGPT/Codex subscription rather than an API key.
func (c *Config) SubscriptionAuth() bool {
	return c.Provider == "openai" && c.OpenAIAuth == OpenAIAuthSubscription
}

// defaultModelFor returns the default model when the config omits an explicit
// one, accounting for the openai subscription backend's distinct model ids.
func defaultModelFor(provider, openAIAuth string) string {
	switch provider {
	case "openai":
		if openAIAuth == OpenAIAuthSubscription {
			return defaultCodexModel
		}
		return defaultOpenAIModel
	case "openrouter":
		return defaultOpenRouterModel
	case "google":
		return defaultGoogleModel
	default:
		return defaultModel
	}
}

// Source describes where this Config was loaded from (a file path or
// "embedded"). Useful in error messages and diagnostics.
func (c *Config) Source() string { return c.source }
