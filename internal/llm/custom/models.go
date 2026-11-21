package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Model is one entry from a custom backend's catalogue. Name and Description
// are optional: a gateway that only lists ids still produces a usable picker.
type Model struct {
	ID          string
	Name        string
	Description string
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
// a data array, and a bare array.
func decodeCatalogue(raw []byte) ([]Model, error) {
	var models []Model
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &models); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
	} else {
		var payload struct {
			Data []Model `json:"data"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		models = payload.Data
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
