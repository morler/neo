package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func captureAPIRequest(t *testing.T, req llm.Request) map[string]any {
	t.Helper()
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer srv.Close()
	c := &Client{APIKey: "test", Endpoint: srv.URL, HTTP: srv.Client(), MaxRetries: 0}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return raw
}

func TestComplete_EffortUnsetOmitsReasoning(t *testing.T) {
	body := captureAPIRequest(t, llm.Request{Model: "m"})
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("unset effort must omit the reasoning key entirely, got %v", body["reasoning"])
	}
}

func TestComplete_EffortIsPassedThrough(t *testing.T) {
	body := captureAPIRequest(t, llm.Request{Model: "m", Effort: "High"})
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning missing or wrong shape: %v", body["reasoning"])
	}
	if got := reasoning["effort"]; got != "high" {
		t.Fatalf("effort = %v, want high (normalized)", got)
	}
}

func TestComplete_UnknownEffortStillReachesTheAPI(t *testing.T) {
	// The accepted set belongs to the backend; an unfamiliar word must arrive so
	// the API's own error explains it rather than Neo silently dropping it.
	body := captureAPIRequest(t, llm.Request{Model: "m", Effort: "ultra"})
	reasoning, _ := body["reasoning"].(map[string]any)
	if got := reasoning["effort"]; got != "ultra" {
		t.Fatalf("effort = %v, want it passed through verbatim", got)
	}
}
