package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
)

// customBackend is a fake OpenAI-compatible endpoint. It records every request
// so a test can distinguish a request-shape problem from an auth or parsing
// problem, instead of only observing a generic failure.
type customBackend struct {
	mu       sync.Mutex
	requests []recordedRequest
	// respond produces the response body for the nth request (0-based).
	respond func(n int) (status int, body string)
}

type recordedRequest struct {
	Path          string
	Authorization string
	Model         string
	Messages      []struct {
		Role       string            `json:"role"`
		Content    any               `json:"content"`
		ToolCallID string            `json:"tool_call_id"`
		ToolCalls  []json.RawMessage `json:"tool_calls"`
	} `json:"messages"`
	HasTools bool `json:"-"`
	Raw      map[string]any
}

func (b *customBackend) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var recorded recordedRequest
		recorded.Path = r.URL.Path
		recorded.Authorization = r.Header.Get("Authorization")
		// A catalogue fetch is a GET with no body; chat calls always send one.
		if err := json.NewDecoder(r.Body).Decode(&recorded.Raw); err != nil && r.Method != http.MethodGet {
			t.Errorf("decode request body: %v", err)
		}
		if model, ok := recorded.Raw["model"].(string); ok {
			recorded.Model = model
		}
		if _, ok := recorded.Raw["tools"]; ok {
			recorded.HasTools = true
		}
		if raw, ok := recorded.Raw["messages"].([]any); ok {
			for _, m := range raw {
				if msg, ok := m.(map[string]any); ok {
					entry := struct {
						Role       string            `json:"role"`
						Content    any               `json:"content"`
						ToolCallID string            `json:"tool_call_id"`
						ToolCalls  []json.RawMessage `json:"tool_calls"`
					}{}
					entry.Role, _ = msg["role"].(string)
					entry.Content = msg["content"]
					entry.ToolCallID, _ = msg["tool_call_id"].(string)
					recorded.Messages = append(recorded.Messages, entry)
				}
			}
		}

		b.mu.Lock()
		n := len(b.requests)
		b.requests = append(b.requests, recorded)
		b.mu.Unlock()

		status, body := b.respond(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func (b *customBackend) recorded(t *testing.T) []recordedRequest {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]recordedRequest(nil), b.requests...)
}

func textResponse(text string) (int, string) {
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	if err != nil {
		panic(err)
	}
	return http.StatusOK, string(body)
}

func toolCallResponse(id, name, arguments string) (int, string) {
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"id":       id,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": arguments},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
	})
	if err != nil {
		panic(err)
	}
	return http.StatusOK, string(body)
}

// customProviderWorkspace writes a neo.yaml pointing at endpoint and returns
// the working directory plus the loaded config, mirroring what a chat or
// headless run sees.
func customProviderWorkspace(t *testing.T, endpoint, apiKeyEnv string) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(dir)

	var b strings.Builder
	b.WriteString("provider: local\nmodel: qwen3-coder\nproviders:\n  local:\n")
	b.WriteString("    endpoint: " + endpoint + "\n")
	if apiKeyEnv != "" {
		b.WriteString("    api_key_env: " + apiKeyEnv + "\n")
	}
	b.WriteString("    default_model: qwen3-coder\n")
	b.WriteString("    models:\n      - id: qwen3-coder\n        name: Qwen3 Coder\n")
	if err := os.WriteFile(filepath.Join(dir, "neo.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return dir, cfg
}

func TestCustomProviderEndToEnd_TextTurn(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) { return textResponse("hello from local") }}
	server := backend.start(t)
	dir, cfg := customProviderWorkspace(t, server.URL+"/v1/chat/completions", "LOCAL_LLM_API_KEY")
	t.Setenv("LOCAL_LLM_API_KEY", "secret-key")

	prov, err := checkedProvider(t.Context(), cfg, cfg.Provider)
	if err != nil {
		t.Fatalf("checkedProvider: %v", err)
	}
	if prov.Name() != "local" {
		t.Fatalf("provider name = %q, want local", prov.Name())
	}

	resp, err := prov.Complete(t.Context(), llm.Request{
		Model:    cfg.Model,
		System:   "system instructions",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:    []llm.ToolSpec{{Name: "read_file", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Response parsing: text made it back through the neutral types.
	if len(resp.Content) != 1 || resp.Content[0].Text != "hello from local" {
		t.Fatalf("response content = %#v, want the assistant text", resp.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", resp.StopReason)
	}
	// Usage is recorded, not dropped by the chat-completions translation.
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %#v, want 11 prompt / 7 completion tokens", resp.Usage)
	}

	// Request shape: path, bearer auth, model, and the system message.
	requests := backend.recorded(t)
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	got := requests[0]
	if got.Path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", got.Path)
	}
	if got.Authorization != "Bearer secret-key" {
		t.Fatalf("authorization = %q, want the configured key", got.Authorization)
	}
	if got.Model != "qwen3-coder" {
		t.Fatalf("model = %q, want qwen3-coder", got.Model)
	}
	if len(got.Messages) == 0 || got.Messages[0].Role != "system" {
		t.Fatalf("messages = %#v, want a leading system message", got.Messages)
	}
	if !got.HasTools {
		t.Fatal("expected the tool definitions to be sent to a custom backend")
	}
	_ = dir
}

func TestCustomProviderEndToEnd_ToolCallRoundTrip(t *testing.T) {
	target := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(target, []byte("file contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	backend := &customBackend{respond: func(n int) (int, string) {
		if n == 0 {
			return toolCallResponse("call_1", "read_file", `{"path":"`+target+`"}`)
		}
		return textResponse("read it: file contents")
	}}
	server := backend.start(t)
	_, cfg := customProviderWorkspace(t, server.URL+"/v1/chat/completions", "")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	if !cfg.HasProvider("local") {
		t.Fatal("custom provider should load without an api_key_env")
	}
	if _, err := checkedProvider(t.Context(), cfg, cfg.Provider); err != nil {
		t.Fatalf("checkedProvider: %v", err)
	}

	// Run through the real CLI entry point so the composition in runHeadless
	// (registry, system prompt, compactor, agent loop) is exercised end to end.
	streams, out, errOut := bufferedStdio()
	if code := run([]string{"run", "--timeout", "30s", "read the note"}, streams); code != 0 {
		t.Fatalf("run exited %d; stderr = %q", code, errOut.String())
	}
	output := out.String()
	if !strings.Contains(output, "file contents") {
		t.Fatalf("final output = %q, want the tool result folded into the answer", output)
	}

	// Two provider calls: the tool request, then the continuation.
	requests := backend.recorded(t)
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2 (tool call then final answer)", len(requests))
	}
	if requests[0].Authorization != "" {
		t.Fatalf("authorization = %q, want none when api_key_env is unset", requests[0].Authorization)
	}
	if !requests[0].HasTools {
		t.Fatal("first request should advertise tools")
	}
	// The second request must carry the tool result back to the model.
	var sawToolResult bool
	for _, msg := range requests[1].Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("second request is missing the tool result: %#v", requests[1].Messages)
	}
}

func TestCustomProviderEndToEnd_ModelPickerFetchesCatalogue(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) {
		return http.StatusOK, `{"data":[{"id":"qwen3-coder"},{"id":"llama-4"}]}`
	}}
	server := backend.start(t)
	_, cfg := customProviderWorkspace(t, server.URL+"/v1", "LOCAL_LLM_API_KEY")
	t.Setenv("LOCAL_LLM_API_KEY", "secret-key")

	// The loader is what /model invokes when its picker opens.
	loader := modelChoicesLoader(cfg, cfg.Provider)
	if loader == nil {
		t.Fatal("a custom provider must load its model catalogue for /model")
	}
	choices, err := loader(t.Context())
	if err != nil {
		t.Fatalf("loader: %v", err)
	}

	requests := backend.recorded(t)
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want one catalogue fetch", len(requests))
	}
	got := requests[0]
	if got.Path != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models derived from the chat endpoint", got.Path)
	}
	if got.Authorization != "Bearer secret-key" {
		t.Fatalf("authorization = %q, want the configured key", got.Authorization)
	}
	if len(choices) != 2 || choices[0].ID != "llama-4" || choices[1].ID != "qwen3-coder" {
		t.Fatalf("choices = %#v, want the sorted live catalogue", choices)
	}
	// The configured static example keeps its display name for a listed id.
	if choices[1].Name != "Qwen3 Coder" {
		t.Fatalf("name = %q, want the configured name", choices[1].Name)
	}
}

func TestCustomProviderEndToEnd_AuthFailureNamesProviderAndVariable(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) {
		return http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`
	}}
	server := backend.start(t)
	_, cfg := customProviderWorkspace(t, server.URL+"/v1/chat/completions", "LOCAL_LLM_API_KEY")
	t.Setenv("LOCAL_LLM_API_KEY", "wrong-key")

	prov, err := checkedProvider(t.Context(), cfg, cfg.Provider)
	if err != nil {
		t.Fatalf("checkedProvider: %v", err)
	}
	_, err = prov.Complete(t.Context(), llm.Request{
		Model:    cfg.Model,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("expected a 401 to surface as an error")
	}
	for _, want := range []string{"local 401", "invalid api key", "check LOCAL_LLM_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "wrong-key") {
		t.Fatalf("error leaked the API key: %q", err.Error())
	}
}

func TestCustomProviderEndToEnd_MissingKeyFailsBeforeFirstCall(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) { return textResponse("unused") }}
	server := backend.start(t)
	_, cfg := customProviderWorkspace(t, server.URL+"/v1/chat/completions", "LOCAL_LLM_API_KEY")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	_, err := checkedProvider(t.Context(), cfg, cfg.Provider)
	if err == nil {
		t.Fatal("expected a missing key to fail at construction")
	}
	if !strings.Contains(err.Error(), "LOCAL_LLM_API_KEY") {
		t.Fatalf("error %q should name the missing variable", err.Error())
	}
	if got := backend.recorded(t); len(got) != 0 {
		t.Fatalf("no request should reach the backend, got %d", len(got))
	}
}
