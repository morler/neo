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
	// ContextWindowTokens is an optional manual override for the active model's
	// context window. When omitted, chat startup uses the compact package's
	// conservative default.
	ContextWindowTokens int `yaml:"context_window_tokens"`
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
	// Script registers the JavaScript `script` tool. It is off by default: the
	// tool costs 1,143 bytes of fixed prompt on every request and a measurement
	// found no case where the model chose it over bash on its own. Turning it on
	// is a decision the session pays for deliberately, not a default.
	Script *bool `yaml:"script"`
	// CodeMode selects how the session's tool surface is composed. "solo" keeps
	// only the script tool, so every action is written as code, and adds run()
	// so programs are still reachable. Empty or "default" keeps the ordinary
	// surface. It is read once at startup: a session's tool list and base prompt
	// must not change mid-conversation, or the cached prefix breaks.
	CodeMode string `yaml:"codemode"`
}

// CodeModeSolo is the only mode value that changes the tool surface.
const CodeModeSolo = "solo"

// SoloCodeMode reports whether the session replaces its tool surface with the
// script tool alone.
func (c *Config) SoloCodeMode() bool {
	return strings.EqualFold(strings.TrimSpace(c.Features.CodeMode), CodeModeSolo)
}

// AgentsFileEnabled reports whether AGENTS.md loading is on (default: true).
func (c *Config) AgentsFileEnabled() bool { return featureEnabled(c.Features.AgentsFile, true) }

// SkillsEnabled reports whether skill loading is on (default: true).
func (c *Config) SkillsEnabled() bool { return featureEnabled(c.Features.Skills, true) }

// ScriptEnabled reports whether the JavaScript `script` tool is registered
// (default: false).
func (c *Config) ScriptEnabled() bool { return featureEnabled(c.Features.Script, false) }

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
