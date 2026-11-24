package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

// decodeHeadlessJSON parses the single JSON object a `neo run --json` prints on
// stdout. Diagnostics belong on stderr, so stdout must hold nothing else.
func decodeHeadlessJSON(t *testing.T, out string) headlessResult {
	t.Helper()
	var got headlessResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return got
}

// TestHeadlessJSONReportsUsageAndRequests drives the real CLI entry point
// through a tool call and a final answer, then checks that the summary adds up
// the usage of both provider responses and counts both requests.
func TestHeadlessJSONReportsUsageAndRequests(t *testing.T) {
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
	customProviderWorkspace(t, server.URL+"/v1/chat/completions", "")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	streams, out, errOut := bufferedStdio()
	if code := run([]string{"run", "--json", "--timeout", "30s", "read the note"}, streams); code != 0 {
		t.Fatalf("run exited %d; stderr = %q", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on success", errOut.String())
	}

	got := decodeHeadlessJSON(t, out.String())
	if !got.OK {
		t.Fatalf("ok = false; error = %q", got.Error)
	}
	if got.Requests != 2 {
		t.Fatalf("requests = %d, want 2 (tool call then final answer)", got.Requests)
	}
	// The stub reports 5 prompt / 3 completion tokens for a tool call response
	// and 11 prompt / 7 completion tokens for a text response, so the totals
	// prove both responses were counted rather than only the last one.
	want := llm.Usage{InputTokens: 16, OutputTokens: 10}
	if got.Usage != want {
		t.Fatalf("usage = %+v, want %+v", got.Usage, want)
	}
	if got.ToolCalls != 1 || got.ToolErrors != 0 {
		t.Fatalf("tool activity = %d calls / %d errors, want 1 / 0", got.ToolCalls, got.ToolErrors)
	}
	if !strings.Contains(got.Final, "file contents") {
		t.Fatalf("final = %q, want the answer", got.Final)
	}
}

// TestHeadlessJSONUsageIncludesCompaction covers the path where the transcript
// is compacted before a request: the summary call is a provider request too, so
// its tokens and its request must both appear in the summary.
func TestHeadlessJSONUsageIncludesCompaction(t *testing.T) {
	target := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(target, []byte("file contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Compaction needs more messages than KeepRecent (default 20), so the stub
	// runs eleven tool exchanges before it stops. Eleven exchanges leave 23
	// messages, past KeepRecent (20) and already at a safe split point, so
	// compaction is eligible; the stub then reports a prompt larger than the
	// trigger so it actually runs. Every tool response reports 5 prompt / 3
	// completion tokens and every text response 11 / 7, plus 1000 prompt tokens
	// on the summary call, which makes the totals distinguishable.
	const toolRounds = 11
	const promptOverhead = 1_000
	const toolPrompt = 5
	backend := &customBackend{respondTo: func(n int, req recordedRequest) (int, string) {
		// Report a prompt above the trigger on every tool response so the next
		// turn is eligible; the agent decides on the previous response's size.
		if n < toolRounds {
			return usageToolCallResponse(fmt.Sprintf("call_%d", n), "read_file",
				fmt.Sprintf(`{"path":%q}`, target), 5, 3)
		}
		if !req.HasTools {
			return usageTextResponse("summary of the earlier work", promptOverhead+5, 3)
		}
		return usageTextResponse("done", 10, 7)
	}}
	server := backend.start(t)
	dir, _ := customProviderWorkspace(t, server.URL+"/v1/chat/completions", "")
	if err := os.WriteFile(filepath.Join(dir, "neo.yaml"), []byte(
		// TriggerTokensForContextWindow rounds down, so 8 yields a trigger of 5:
		// exactly the prompt size this stub reports, which is what makes compaction
		// eligible once the transcript is longer than KeepRecent. A smaller window
		// would round to 0 and silently fall back to the default threshold.
		"provider: local\nmodel: qwen3-coder\ncompaction:\n  context_window_tokens: 8\n"+
			"providers:\n  local:\n    endpoint: "+server.URL+"/v1/chat/completions\n"+
			"    default_model: qwen3-coder\n    models:\n      - id: qwen3-coder\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCAL_LLM_API_KEY", "")

	streams, out, errOut := bufferedStdio()
	if code := run([]string{"run", "--json", "--timeout", "30s", "read the note"}, streams); code != 0 {
		t.Fatalf("run exited %d; stderr = %q", code, errOut.String())
	}

	got := decodeHeadlessJSON(t, out.String())
	// Every tool response plus the final answer, and one summary call that
	// compaction made without the loop asking for it.
	const wantRequests = toolRounds + 2
	if got.Requests != wantRequests {
		t.Fatalf("requests = %d, want %d", got.Requests, wantRequests)
	}
	if got.Requests != len(backend.recorded(t)) {
		t.Fatalf("requests = %d, but the backend saw %d", got.Requests, len(backend.recorded(t)))
	}
	// The prompt total pins which responses were billed: 5 per tool response,
	// plus the compaction summary and the final answer.
	// 11 tool responses at 5 prompt tokens, the summary at promptOverhead+5,
	// and the final answer at 10.
	wantPrompt := toolRounds*toolPrompt + promptOverhead + 5 + 10
	if got.Usage.PromptTokens() != wantPrompt {
		t.Fatalf("prompt tokens = %d, want %d (compaction summary counted); usage = %+v",
			got.Usage.PromptTokens(), wantPrompt, got.Usage)
	}
	// 11 tool responses at 3 output tokens, the summary at 3, the answer at 7.
	if want := toolRounds*3 + 3 + 7; got.Usage.OutputTokens != want {
		t.Fatalf("output tokens = %d, want %d", got.Usage.OutputTokens, want)
	}
}

// TestHeadlessJSONFailureStillReportsUsage keeps the shape of the summary
// stable when the turn fails: a consumer reads usage and requests
// unconditionally, so they must be present even when nothing was spent.
func TestHeadlessJSONFailureStillReportsUsage(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) {
		// 400 is not retryable, so the failure path stays fast.
		return http.StatusBadRequest, `{"error":{"message":"boom"}}`
	}}
	server := backend.start(t)
	customProviderWorkspace(t, server.URL+"/v1/chat/completions", "")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	streams, out, errOut := bufferedStdio()
	if code := run([]string{"run", "--json", "--timeout", "30s", "hello"}, streams); code == 0 {
		t.Fatalf("a failed provider call should exit non-zero; stderr = %q", errOut.String())
	}
	got := decodeHeadlessJSON(t, out.String())
	if got.OK || got.Error == "" {
		t.Fatalf("failed run should report an error: %+v", got)
	}
	// The fields must be present even when nothing was spent, so a consumer can
	// read them unconditionally.
	if !strings.Contains(out.String(), `"usage"`) || !strings.Contains(out.String(), `"requests"`) {
		t.Fatalf("failed run should still include usage and requests: %s", out.String())
	}
}

// TestHeadlessJSONNonJSONOutputUnchanged pins the plain-text contract: adding
// usage to the JSON summary must not add anything to the default output.
func TestHeadlessJSONNonJSONOutputUnchanged(t *testing.T) {
	backend := &customBackend{respond: func(int) (int, string) { return textResponse("plain answer") }}
	server := backend.start(t)
	customProviderWorkspace(t, server.URL+"/v1/chat/completions", "")
	t.Setenv("LOCAL_LLM_API_KEY", "")

	streams, out, _ := bufferedStdio()
	if code := run([]string{"run", "--timeout", "30s", "hi"}, streams); code != 0 {
		t.Fatalf("run exited %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "plain answer" {
		t.Fatalf("stdout = %q, want only the final answer", got)
	}
}
