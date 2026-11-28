// Command summaryprobe runs the production summarizer against a live provider
// with and without the tool-result preview, so the two summaries can be compared
// on what they kept. #134's second acceptance criterion is about exactly that:
// user constraints, key errors, open work, and verification conclusions.
//
// It is a measurement tool. Nothing here is imported by Neo.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/owainlewis/neo/docs/measurements/134-compaction-tool-result-budget/budget"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

func main() {
	endpoint := flag.String("endpoint", "", "chat-completions URL")
	keyEnv := flag.String("key-env", "", "environment variable holding the API key")
	model := flag.String("model", "", "model id")
	budgetBytes := flag.Int("budget", 0, "per-result preview budget; 0 keeps everything")
	flag.Parse()

	inner := &chatcompletions.Client{
		ProviderName: "probe",
		APIKey:       os.Getenv(*keyEnv),
		Endpoint:     *endpoint,
		DefaultModel: *model,
		MaxRetries:   2,
		BaseDelay:    time.Second,
	}
	rec := &recorder{inner: inner}
	in := budget.Fixture()
	msgs := in
	if *budgetBytes > 0 {
		msgs = budget.Preview(in, budget.Options{Budget: *budgetBytes})
	}
	s := compact.Summarizer{Provider: rec, Model: *model, TriggerTokens: 1, KeepRecent: 3}
	res, err := s.Compact(context.Background(), msgs, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compact: %v\n", err)
		os.Exit(1)
	}
	out := map[string]any{
		"budget":        *budgetBytes,
		"request_bytes": rec.bytes,
		"input_tokens":  res.Usage.InputTokens,
		"cache_read":    res.Usage.CacheReadTokens,
		"output_tokens": res.Usage.OutputTokens,
		"summary":       budget.FirstText(res.Messages),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// recorder measures the JSON body the adapter would send, which is what the
// provider bills for.
type recorder struct {
	inner *chatcompletions.Client
	bytes int
}

func (r *recorder) Name() string { return "probe" }

func (r *recorder) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if body, err := json.Marshal(chatcompletions.BuildRequest(req, req.Model)); err == nil {
		r.bytes = len(body)
	}
	return r.inner.Complete(ctx, req)
}
