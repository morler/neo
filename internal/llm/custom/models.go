package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Model is one entry from a custom backend's catalogue. Name and Description
// are optional: a gateway that only lists ids still produces a usable picker.
type Model struct {
	ID          string
	Name        string
	Description string
	// ContextWindowTokens is the window the backend reports for this model, or
	// 0 when it reports none. Discovery is best effort: implementations disagree
	// about the field and its placement, so an absent, mismatched, or unknown
	// shape means "not discovered" rather than an error. See decodeCatalogue.
	ContextWindowTokens int
}

// ModelsEndpoint derives the catalogue URL from the chat-completions endpoint
// so users configure one URL per backend. A base URL such as
// http://127.0.0.1:8000/v1 becomes http://127.0.0.1:8000/v1/models, matching
// the OpenAI-compatible servers Neo targets.
func ModelsEndpoint(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	trimmed = strings.TrimSuffix(trimmed, "/chat/completions")
	return trimmed + "/models"
}

// Models fetches the live model catalogue from GET <endpoint>/models. It speaks
// the OpenAI list shape ({"data":[{"id":...}]}), accepts a bare JSON array as
// well, and sorts the result by id for a stable picker order.
//
// httpClient may be nil, in which case a client with a short timeout is used.
// Callers that must not hang the UI pass a context with a deadline.
func Models(ctx context.Context, spec Spec, httpClient *http.Client) ([]Model, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf("custom provider: name is required")
	}
	endpoint := strings.TrimSpace(spec.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("custom provider %s: endpoint is required", name)
	}
	key, err := apiKey(spec.APIKeyEnv, name)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsEndpoint(endpoint), nil)
	if err != nil {
		return nil, fmt.Errorf("custom provider %s: models: %w", name, err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom provider %s: models: %w", name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("custom provider %s: models: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("custom provider %s: models: unexpected status %s", name, resp.Status)
	}

	models, err := decodeCatalogue(raw)
	if err != nil {
		return nil, fmt.Errorf("custom provider %s: models: %w", name, err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// decodeCatalogue accepts both documented shapes: an OpenAI-style object with
// a data array, and a bare array. Entries are decoded as raw maps rather than
// straight into Model because a listing carries many fields Neo does not model,
// and the context window is one of them: it has to be probed out of shapes the
// server is free to choose (see discoverContextWindow).
func decodeCatalogue(raw []byte) ([]Model, error) {
	var entries []map[string]any
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
	} else {
		var payload struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		entries = payload.Data
	}
	out := make([]Model, 0, len(entries))
	for _, entry := range entries {
		m := Model{
			ID:                  strings.TrimSpace(textField(entry, "id")),
			Name:                strings.TrimSpace(textField(entry, "name")),
			Description:         strings.TrimSpace(textField(entry, "description")),
			ContextWindowTokens: discoverContextWindow(entry),
		}
		if m.ID == "" {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// contextWindowFields are the keys OpenAI-compatible servers use for a model's
// context window. The list is a superset on purpose: the catalogue shape is not
// a contract, so Neo looks where it has seen a window and treats everything
// else as not reported.
//
//   - meta.n_ctx: llama.cpp's llama-server reports an object of runtime facts
//     (verified against a local server, 2026-11-25).
//   - max_model_len: vLLM.
//   - context_length: OpenRouter and most gateways.
var contextWindowFields = []string{"context_length", "max_model_len", "context_window", "max_context_length"}

// contextWindowMetaFields are the same idea inside llama.cpp's meta object.
var contextWindowMetaFields = []string{"n_ctx", "n_ctx_train"}

// discoverContextWindow reads a window out of one catalogue entry, returning 0
// when the entry reports none. It never fails: a window is an optimisation, and
// a shape Neo did not anticipate must not stop the catalogue from loading.
func discoverContextWindow(entry map[string]any) int {
	for _, key := range contextWindowFields {
		if window := positiveInt(entry[key]); window > 0 {
			return window
		}
	}
	if meta, ok := entry["meta"].(map[string]any); ok {
		for _, key := range contextWindowMetaFields {
			if window := positiveInt(meta[key]); window > 0 {
				return window
			}
		}
	}
	return 0
}

// positiveInt converts a JSON number or numeric string to a positive int. Any
// other value, including a float, a boolean, a nested object, or a negative
// number, is reported as "no window".
func positiveInt(v any) int {
	switch n := v.(type) {
	case float64:
		if n >= 1 && n == float64(int64(n)) && n <= math.MaxInt32 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	case json.Number:
		if parsed, err := n.Int64(); err == nil && parsed > 0 && parsed <= math.MaxInt32 {
			return int(parsed)
		}
	case string:
		trimmed := strings.TrimSpace(n)
		if trimmed == "" {
			return 0
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil && parsed > 0 && parsed <= math.MaxInt32 {
			return int(parsed)
		}
	}
	return 0
}

// textField returns a string field, or "" when the entry carries something
// else under that key.
func textField(entry map[string]any, key string) string {
	s, _ := entry[key].(string)
	return s
}
