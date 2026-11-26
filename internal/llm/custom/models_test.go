package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelsEndpointDerivesCatalogueURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8000/v1":                    "http://127.0.0.1:8000/v1/models",
		"http://127.0.0.1:8000/v1/":                   "http://127.0.0.1:8000/v1/models",
		"http://127.0.0.1:8000/v1/chat/completions":   "http://127.0.0.1:8000/v1/models",
		"https://llm.internal.example.com/openai/v1/": "https://llm.internal.example.com/openai/v1/models",
	}
	for endpoint, want := range cases {
		if got := ModelsEndpoint(endpoint); got != want {
			t.Fatalf("ModelsEndpoint(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestModelsFetchesAndSortsCatalogue(t *testing.T) {
	t.Setenv("LOCAL_LLM_API_KEY", "secret-key")
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"z/model","name":"Z"},
			{"id":"","name":"skip me"},
			{"id":"a/model","name":"A","description":"first"}
		]}`))
	}))
	defer server.Close()

	models, err := Models(context.Background(), Spec{
		Name:      "local",
		Endpoint:  server.URL + "/v1",
		APIKeyEnv: "LOCAL_LLM_API_KEY",
	}, server.Client())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("authorization = %q, want the configured key", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (blank id dropped): %#v", len(models), models)
	}
	if models[0].ID != "a/model" || models[1].ID != "z/model" {
		t.Fatalf("models not sorted by id: %#v", models)
	}
	if models[0].Name != "A" || models[0].Description != "first" {
		t.Fatalf("model fields not mapped: %#v", models[0])
	}
}

func TestModelsOmitsAuthorizationWithoutKeyEnv(t *testing.T) {
	var sawHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["Authorization"]
		_, _ = w.Write([]byte(`[{"id":"llama3"}]`))
	}))
	defer server.Close()

	models, err := Models(context.Background(), Spec{Name: "ollama", Endpoint: server.URL + "/v1"}, server.Client())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if sawHeader {
		t.Fatal("an endpoint without api_key_env must not receive an Authorization header")
	}
	if len(models) != 1 || models[0].ID != "llama3" {
		t.Fatalf("models = %#v, want the bare-array catalogue", models)
	}
}

func TestModelsErrorsOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := Models(context.Background(), Spec{Name: "local", Endpoint: server.URL + "/v1"}, server.Client())
	if err == nil {
		t.Fatal("expected a non-200 catalogue response to fail")
	}
	for _, want := range []string{"local", "models", "500"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestModelsFailsWhenConfiguredKeyEnvIsUnset(t *testing.T) {
	t.Setenv("MISSING_LLM_KEY", "")
	_, err := Models(context.Background(), Spec{
		Name:      "local",
		Endpoint:  "https://llm.internal/v1",
		APIKeyEnv: "MISSING_LLM_KEY",
	}, nil)
	if err == nil {
		t.Fatal("expected a missing api key to fail")
	}
	if !strings.Contains(err.Error(), "MISSING_LLM_KEY") {
		t.Fatalf("error %q should name the missing variable", err.Error())
	}
}

// TestModelsDiscoversTheReportedContextWindow covers #109: a backend that
// reports its window in the catalogue must have it read, because requiring the
// user to type a number the server already publishes is a needless step.
// llama.cpp's shape is the one verified against a real server.
func TestModelsDiscoversTheReportedContextWindow(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "llama.cpp meta.n_ctx",
			body: `{"data":[{"id":"Bonsai2.5-27B","meta":{"n_ctx":40192,"n_ctx_train":262144,"n_params":27320697856}}]}`,
			want: 40192,
		},
		{
			name: "vllm max_model_len",
			body: `{"data":[{"id":"qwen3-coder","max_model_len":32768}]}`,
			want: 32768,
		},
		{
			name: "openrouter context_length",
			body: `{"data":[{"id":"anthropic/claude-sonnet-5","context_length":200000}]}`,
			want: 200000,
		},
		{
			name: "numeric string",
			body: `{"data":[{"id":"m","context_length":"131072"}]}`,
			want: 131072,
		},
		{
			name: "bare array",
			body: `[{"id":"m","max_model_len":8192}]`,
			want: 8192,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			models, err := Models(context.Background(), Spec{Name: "local", Endpoint: server.URL + "/v1"}, server.Client())
			if err != nil {
				t.Fatalf("Models: %v", err)
			}
			if len(models) != 1 {
				t.Fatalf("got %d models, want 1: %#v", len(models), models)
			}
			if models[0].ContextWindowTokens != tc.want {
				t.Fatalf("window = %d, want %d", models[0].ContextWindowTokens, tc.want)
			}
		})
	}
}

// TestModelsTreatsAnUnreadableWindowAsNotDiscovered covers the tolerance half of
// #109: the catalogue shape is not a contract, so a missing field, a value of
// the wrong type, or a shape Neo did not anticipate must leave the window
// unknown rather than fail the fetch that also supplies the model ids.
func TestModelsTreatsAnUnreadableWindowAsNotDiscovered(t *testing.T) {
	cases := map[string]string{
		"field absent":       `{"data":[{"id":"m"}]}`,
		"wrong type":         `{"data":[{"id":"m","context_length":{"nested":1}}]}`,
		"not numeric":        `{"data":[{"id":"m","context_length":"lots"}]}`,
		"negative":           `{"data":[{"id":"m","max_model_len":-1}]}`,
		"zero":               `{"data":[{"id":"m","max_model_len":0}]}`,
		"boolean":            `{"data":[{"id":"m","context_length":true}]}`,
		"meta not an object": `{"data":[{"id":"m","meta":40192}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			models, err := Models(context.Background(), Spec{Name: "local", Endpoint: server.URL + "/v1"}, server.Client())
			if err != nil {
				t.Fatalf("Models: %v", err)
			}
			if len(models) != 1 || models[0].ID != "m" {
				t.Fatalf("models = %#v, want the id to survive an unreadable window", models)
			}
			if models[0].ContextWindowTokens != 0 {
				t.Fatalf("window = %d, want 0 (not discovered)", models[0].ContextWindowTokens)
			}
		})
	}
}

// TestModelsKeepsNonJSONResponsesAnError records the boundary: an unreadable
// window is tolerated, but a body that is not JSON at all still fails, because
// the model list itself is unusable then.
func TestModelsKeepsNonJSONResponsesAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html>not a catalogue"))
	}))
	defer server.Close()

	if _, err := Models(context.Background(), Spec{Name: "local", Endpoint: server.URL + "/v1"}, server.Client()); err == nil {
		t.Fatal("expected a non-JSON catalogue to fail")
	}
}
