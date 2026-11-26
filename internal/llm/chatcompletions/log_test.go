package chatcompletions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
)

// TestComplete_LogsTheResolvedEffort pins the only observable evidence that a
// pass-through backend received the setting at all: an unknown value is
// forwarded by design and a server that ignores it answers normally, so without
// this line the two cases are indistinguishable.
func TestComplete_LogsTheResolvedEffort(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "neo.log")
	t.Setenv("NEO_LOG", logPath)
	t.Setenv("NEO_LOG_VERBOSE", "")
	if err := logx.InitFromEnv(); err != nil {
		t.Fatalf("init logging: %v", err)
	}
	t.Cleanup(func() { _ = logx.Close() })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	c := &Client{ProviderName: "gateway", Endpoint: srv.URL, HTTP: srv.Client(), MaxRetries: 0}
	if _, err := c.Complete(context.Background(), llm.Request{Model: "m", Effort: "max"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(raw)
	if !strings.Contains(log, `"reasoning_effort":"max"`) {
		t.Fatalf("log does not record the effort the server received:\n%s", log)
	}
	// The default log must not carry the body itself: it contains the whole
	// transcript, and this file is written on every request.
	if strings.Contains(log, `"payload":"{`) {
		t.Fatalf("payload should be hashed without NEO_LOG_VERBOSE:\n%s", log)
	}
}
