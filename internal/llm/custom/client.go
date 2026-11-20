// Package custom builds provider clients from a user-declared backend under
// `providers:` in neo.yaml. Every such backend speaks the OpenAI Chat
// Completions shape, so the package is a thin constructor over
// internal/llm/chatcompletions rather than a new adapter.
package custom

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/chatcompletions"
	"github.com/owainlewis/neo/internal/llm/retry"
)

// Spec is the subset of a custom provider's configuration the client needs. It
// is defined here so the llm layer does not import internal/config.
type Spec struct {
	// Name is the provider name used in neo.yaml and in error messages.
	Name string
	// Endpoint is the chat-completions URL. A base URL such as https://host/v1
	// is completed with the chat-completions path, matching how config
	// normalizes providers.<name>.endpoint.
	Endpoint string
	// APIKeyEnv names the environment variable holding the API key. Empty
	// means the endpoint is unauthenticated and no Authorization header is
	// sent.
	APIKeyEnv string
	// DefaultModel is used when the session supplies no model.
	DefaultModel string
}

// New builds a chat-completions client for spec. A configured but unset or
// empty API key environment variable fails immediately, so a missing credential
// surfaces at startup instead of as a 401 mid-conversation.
func New(spec Spec) (*chatcompletions.Client, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf("custom provider: name is required")
	}
	endpoint := strings.TrimSpace(spec.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("custom provider %s: endpoint is required", name)
	}
	endpoint = chatCompletionsEndpoint(endpoint)

	key, err := apiKey(spec.APIKeyEnv, name)
	if err != nil {
		return nil, err
	}
	return &chatcompletions.Client{
		ProviderName:   name,
		APIKey:         key,
		Endpoint:       endpoint,
		DefaultModel:   strings.TrimSpace(spec.DefaultModel),
		HTTP:           retry.NewHTTPClient(),
		MaxRetries:     4,
		BaseDelay:      500 * time.Millisecond,
		CredentialHint: credentialHint(spec.APIKeyEnv, name),
	}, nil
}

// apiKey reads the configured environment variable. An empty variable name is
// not an error: local runtimes commonly need no key at all.
func apiKey(env, name string) (string, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		return "", nil
	}
	key := strings.TrimSpace(os.Getenv(env))
	if key == "" {
		return "", fmt.Errorf(
			"%s is not set. Export the API key for custom provider %q with `export %s=\"your-api-key\"`, or remove api_key_env from providers.%s if the endpoint needs no authentication. Run `neo doctor` to check your setup",
			env, name, env, name,
		)
	}
	return key, nil
}

// credentialHint is attached to 401/403 responses so the advice names the
// variable this backend actually reads.
func credentialHint(env, name string) string {
	env = strings.TrimSpace(env)
	if env == "" {
		return fmt.Sprintf("custom provider %s sends no API key; check providers.%s.api_key_env", name, name)
	}
	return "check " + env
}

// chatCompletionsEndpoint completes a custom provider endpoint. OpenAI-compatible
// clients are normally configured with a base URL such as https://host/v1 and
// append the chat-completions path themselves, so a base URL gets that path
// exactly once. An endpoint that already names the path is left untouched.
func chatCompletionsEndpoint(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}

// ensure the constructor satisfies the provider interface at compile time.
var _ llm.Provider = (*chatcompletions.Client)(nil)
