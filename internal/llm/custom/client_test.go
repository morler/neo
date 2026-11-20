package custom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func chatRequest() llm.Request {
	return llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}}
}

func TestNewBuildsClientWithConfiguredEndpointAndKey(t *testing.T) {
	t.Setenv("LOCAL_LLM_API_KEY", "secret-key")

	var gotPath, gotAuth, gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := New(Spec{
		Name:         "local",
		Endpoint:     server.URL + "/v1/chat/completions",
		APIKeyEnv:    "LOCAL_LLM_API_KEY",
		DefaultModel: "qwen3-coder",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()
	if client.Name() != "local" {
		t.Fatalf("name = %q, want local", client.Name())
	}

	resp, err := client.Complete(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth = %q, want bearer key", gotAuth)
	}
	if gotModel != "qwen3-coder" {
		t.Fatalf("model = %q, want the configured default_model", gotModel)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestNewOmitsAuthorizationWhenNoKeyEnvConfigured(t *testing.T) {
	var sawHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client, err := New(Spec{Name: "ollama", Endpoint: server.URL, DefaultModel: "llama"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()
	if _, err := client.Complete(context.Background(), chatRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if sawHeader {
		t.Fatal("unauthenticated custom provider must not send an Authorization header")
	}
}

func TestNewFailsWhenConfiguredKeyEnvIsUnset(t *testing.T) {
	t.Setenv("MISSING_LLM_KEY", "")
	_, err := New(Spec{Name: "local", Endpoint: "https://llm.internal/v1/chat/completions", APIKeyEnv: "MISSING_LLM_KEY"})
	if err == nil {
		t.Fatal("expected a missing api key to fail at construction")
	}
	for _, want := range []string{"MISSING_LLM_KEY", "local", "providers.local"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestNewRequiresNameAndEndpoint(t *testing.T) {
	if _, err := New(Spec{Endpoint: "https://llm.internal/v1/chat/completions"}); err == nil {
		t.Fatal("expected a missing name to fail")
	}
	if _, err := New(Spec{Name: "local"}); err == nil {
		t.Fatal("expected a missing endpoint to fail")
	}
}

func TestAuthFailureNamesCustomKeyVariable(t *testing.T) {
	t.Setenv("LOCAL_LLM_API_KEY", "wrong")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := New(Spec{Name: "local", Endpoint: server.URL, APIKeyEnv: "LOCAL_LLM_API_KEY"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()

	_, err = client.Complete(context.Background(), chatRequest())
	if err == nil {
		t.Fatal("expected a 401 error")
	}
	for _, want := range []string{"local 401", "invalid api key", "check LOCAL_LLM_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestAuthFailureExplainsMissingKeyConfigurationWhenUnauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusForbidden)
	}))
	defer server.Close()

	client, err := New(Spec{Name: "local", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()

	_, err = client.Complete(context.Background(), chatRequest())
	if err == nil {
		t.Fatal("expected a 403 error")
	}
	if !strings.Contains(err.Error(), "providers.local.api_key_env") {
		t.Fatalf("error %q should point at api_key_env", err.Error())
	}
}

func TestServerErrorsStaySingleLine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(Spec{Name: "local", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.HTTP = server.Client()

	_, err = client.Complete(context.Background(), chatRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("error should be a single line, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "local 404: model not found") {
		t.Fatalf("error = %q", err.Error())
	}
}
