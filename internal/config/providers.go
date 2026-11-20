package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// APIProtocolOpenAICompatible is the only custom-provider wire protocol Neo
// speaks today: the OpenAI Chat Completions request/response shape served by
// internal/llm/chatcompletions. It is the default when `api` is omitted.
const APIProtocolOpenAICompatible = "openai_compatible"

// builtinProviderNames lists the backends compiled into Neo, in the order they
// appear in help text and error messages.
var builtinProviderNames = []string{"anthropic", "openai", "openrouter", "google"}

// CustomProvider declares an OpenAI-compatible backend under providers.<name>
// in neo.yaml. A top-level `provider: <name>` (or subagents.provider) selects
// it exactly like a built-in provider.
type CustomProvider struct {
	// API is the wire protocol. Empty means APIProtocolOpenAICompatible; any
	// other value is rejected because no other adapter exists yet.
	API string `yaml:"api"`
	// Endpoint is the full chat-completions URL, including scheme and host.
	Endpoint string `yaml:"endpoint"`
	// APIKeyEnv names the environment variable holding the API key. Empty
	// means Neo sends no Authorization header.
	APIKeyEnv string `yaml:"api_key_env"`
	// DefaultModel is used when neither `model` nor a saved session supplies
	// one.
	DefaultModel string `yaml:"default_model"`
	// Models is an optional static list for the /model picker. Neo does not
	// fetch a catalogue from a custom backend.
	Models []CustomModel `yaml:"models"`
}

// CustomModel is one selectable model of a custom provider.
type CustomModel struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ProviderConfig returns the custom provider declared as name. Built-in
// providers are not returned; use HasProvider to ask whether a name is usable.
func (c *Config) ProviderConfig(name string) (CustomProvider, bool) {
	if c == nil {
		return CustomProvider{}, false
	}
	p, ok := c.Providers[strings.TrimSpace(name)]
	return p, ok
}

// HasProvider reports whether name is a built-in or configured custom provider.
func (c *Config) HasProvider(name string) bool {
	name = strings.TrimSpace(name)
	if isBuiltinProvider(name) {
		return true
	}
	_, ok := c.ProviderConfig(name)
	return ok
}

// ProviderNames lists every usable provider: built-ins first, then custom ones
// sorted by name. Error messages use it so a typo names the alternatives.
func (c *Config) ProviderNames() []string {
	names := append([]string(nil), builtinProviderNames...)
	if c == nil {
		return names
	}
	custom := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		custom = append(custom, name)
	}
	sort.Strings(custom)
	return append(names, custom...)
}

// IsCustomProvider reports whether name is declared under providers:.
func (c *Config) IsCustomProvider(name string) bool {
	_, ok := c.ProviderConfig(name)
	return ok
}

// CustomAPIKeyEnvNames lists the environment variables custom providers read,
// sorted and deduplicated, so log redaction covers credentials Neo does not
// know about statically.
func (c *Config) CustomAPIKeyEnvNames() []string {
	if c == nil || len(c.Providers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for _, p := range c.Providers {
		if p.APIKeyEnv == "" {
			continue
		}
		seen[p.APIKeyEnv] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isBuiltinProvider(name string) bool {
	for _, builtin := range builtinProviderNames {
		if name == builtin {
			return true
		}
	}
	return false
}

// normalizeProviders trims names and fields, applies protocol defaults, and
// rejects configurations that would silently shadow or conflict with another
// backend. It runs before any provider name is resolved.
func (c *Config) normalizeProviders(source string) error {
	c.Provider = strings.TrimSpace(c.Provider)
	if c.Provider == "" {
		c.Provider = defaultProvider
	}
	c.Subagents.Provider = strings.TrimSpace(c.Subagents.Provider)
	if len(c.Providers) == 0 {
		c.Providers = nil
		return nil
	}
	normalized := make(map[string]CustomProvider, len(c.Providers))
	keyEnvOwner := make(map[string]string, len(c.Providers))
	for rawName, p := range c.Providers {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return fmt.Errorf("%s: providers entries need a non-empty name (got %q)", source, rawName)
		}
		if isBuiltinProvider(name) {
			return fmt.Errorf("%s: providers.%s would override a built-in provider; use another name", source, name)
		}
		if _, duplicate := normalized[name]; duplicate {
			return fmt.Errorf("%s: providers.%s is declared more than once", source, name)
		}
		if err := p.normalize(source, name); err != nil {
			return err
		}
		if p.APIKeyEnv != "" {
			if owner, taken := keyEnvOwner[p.APIKeyEnv]; taken {
				return fmt.Errorf("%s: providers.%s and providers.%s both read api_key_env %s; give each backend its own variable",
					source, owner, name, p.APIKeyEnv)
			}
			keyEnvOwner[p.APIKeyEnv] = name
		}
		normalized[name] = p
	}
	c.Providers = normalized
	return nil
}

func (p *CustomProvider) normalize(source, name string) error {
	p.API = strings.TrimSpace(p.API)
	switch p.API {
	case "":
		p.API = APIProtocolOpenAICompatible
	case APIProtocolOpenAICompatible:
	default:
		return fmt.Errorf("%s: providers.%s.api must be %q (got %q)", source, name, APIProtocolOpenAICompatible, p.API)
	}

	p.Endpoint = strings.TrimSpace(p.Endpoint)
	if p.Endpoint == "" {
		return fmt.Errorf("%s: providers.%s.endpoint is required", source, name)
	}
	u, err := url.Parse(p.Endpoint)
	if err != nil {
		return fmt.Errorf("%s: providers.%s.endpoint %q is not a valid URL: %w", source, name, p.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s: providers.%s.endpoint %q must start with http:// or https://", source, name, p.Endpoint)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: providers.%s.endpoint %q is missing a host", source, name, p.Endpoint)
	}

	p.APIKeyEnv = strings.TrimSpace(p.APIKeyEnv)
	if p.APIKeyEnv != "" && !isEnvName(p.APIKeyEnv) {
		return fmt.Errorf("%s: providers.%s.api_key_env %q is not a valid environment variable name", source, name, p.APIKeyEnv)
	}

	p.DefaultModel = strings.TrimSpace(p.DefaultModel)

	seen := make(map[string]struct{}, len(p.Models))
	models := make([]CustomModel, 0, len(p.Models))
	for i, m := range p.Models {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			return fmt.Errorf("%s: providers.%s.models[%d].id is required", source, name, i)
		}
		if _, duplicate := seen[m.ID]; duplicate {
			continue
		}
		seen[m.ID] = struct{}{}
		m.Name = strings.TrimSpace(m.Name)
		m.Description = strings.TrimSpace(m.Description)
		models = append(models, m)
	}
	if len(models) == 0 {
		models = nil
	}
	p.Models = models
	return nil
}

// isEnvName reports whether s looks like a POSIX environment variable name:
// a letter or underscore followed by letters, digits, or underscores.
func isEnvName(s string) bool {
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case (r >= '0' && r <= '9') && i > 0:
		default:
			return false
		}
	}
	return s != ""
}

// unknownProviderError explains a name that is neither built in nor declared,
// naming every alternative so the fix does not require reading the docs.
func (c *Config) unknownProviderError(source, key, name string) error {
	return fmt.Errorf("%s: unknown provider %q for %s; available: %s (declare a custom backend under providers: in neo.yaml)",
		source, name, key, strings.Join(c.ProviderNames(), ", "))
}

// defaultModelForProvider returns the implicit model for provider, consulting
// a custom backend's default_model before falling back to built-in defaults.
func (c *Config) defaultModelForProvider(provider string) string {
	if p, ok := c.ProviderConfig(provider); ok {
		return p.DefaultModel
	}
	return defaultModelFor(provider, c.OpenAIAuth)
}
