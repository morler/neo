package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

// codexEndpoint is the ChatGPT/Codex subscription backend. It speaks the same
// Responses shape as the public API but at a different host, requires OAuth
// bearer auth plus a chatgpt-account-id header, and streams results over SSE.
const codexEndpoint = "https://chatgpt.com/backend-api/codex/responses"

// DefaultCodexModel is the model used for subscription requests when the config
// omits one. Override via
// the `model:` config key if needed.
const DefaultCodexModel = "gpt-6.1-sol"

// CredentialSource yields a valid subscription access token and its associated
// ChatGPT account id, refreshing as needed. It is satisfied by
// auth.TokenSource (via a thin adapter in the command layer).
type CredentialSource interface {
	Token(ctx context.Context) (accessToken, accountID string, err error)
}

// CodexClient talks to the ChatGPT/Codex subscription backend using OAuth
// credentials from a CredentialSource.
type CodexClient struct {
	Source     CredentialSource
	Endpoint   string
	HTTP       *http.Client
	MaxRetries int           // default: 4
	BaseDelay  time.Duration // default: 500ms
}

// NewCodex builds a subscription client backed by src.
func NewCodex(src CredentialSource) *CodexClient {
	return &CodexClient{
		Source:     src,
		Endpoint:   codexEndpoint,
		HTTP:       retry.NewHTTPClient(),
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}
}

func (c *CodexClient) Name() string { return "openai-codex" }

func (c *CodexClient) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultCodexModel
	}
	apiReq := buildAPIRequest(req, model, true, "auto", true)
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request",
		"provider", c.Name(),
		"model", model,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"payload", logx.PayloadValue(string(body)),
	)

	result, err := retry.Do(ctx, retry.Options{Provider: c.Name(), ErrorLabel: "openai-codex", MaxRetries: c.MaxRetries, BaseDelay: c.BaseDelay}, func(ctx context.Context) (retry.AttemptResult, error) {
		raw, status, retryAfter, err := c.doRequest(ctx, body)
		return retry.AttemptResult{Body: raw, Status: status, RetryAfter: retryAfter}, err
	})
	if err != nil {
		return nil, err
	}
	raw, status := result.Body, result.Status
	if status >= 400 {
		logx.Debug("provider client error",
			"provider", c.Name(),
			"status", status,
			"body", logx.PayloadValue(string(raw)),
		)
		return nil, llm.NewHTTPError("openai-codex", status, raw)
	}

	logx.Debug("provider response",
		"provider", c.Name(),
		"status", status,
		"response", logx.PayloadValue(string(raw)),
	)
	return parseCodexStream(raw)
}

// doRequest fetches a fresh token (refreshing if needed), issues one POST, and
// returns the buffered body, status, and any Retry-After hint. The SSE body is
// small enough to buffer fully — neo presents blocking results, so there is no
// need to consume deltas incrementally.
func (c *CodexClient) doRequest(ctx context.Context, body []byte) ([]byte, int, retry.RetryAfter, error) {
	access, accountID, err := c.Source.Token(ctx)
	if err != nil {
		// The source decides whether this is permanent (see retry.Permanent):
		// a missing or rejected login is, a transient refresh failure is not.
		return nil, 0, retry.Absent(), err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("chatgpt-account-id", accountID)
	httpReq.Header.Set("originator", "neo")
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	// Propagate the read error. Discarding it turns a cancelled request into an
	// apparently successful empty body, which then surfaces as a confusing
	// decode failure instead of the cancellation that actually happened.
	raw, err := retry.ReadAllIdle(resp.Body, cancel)
	return raw, resp.StatusCode, retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()), err
}

// parseCodexStream extracts the final result from a Responses SSE stream.
//
// The output items are assembled from the per-item "response.output_item.done"
// events, each of which carries one completed output item (a message, a
// function_call, etc.). The terminal "response.completed" event is used for
// status and usage — the ChatGPT/Codex backend omits the `output` array from
// it, so we must not rely on it for content. As a fallback, a "response.completed"
// that does include output (the public API shape) is honored, and a non-SSE
// body is parsed directly.
func parseCodexStream(raw []byte) (*llm.Response, error) {
	if !bytes.Contains(raw, []byte("data:")) {
		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		return toResponse(out)
	}

	var st streamParse
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if err := st.consumeLine(sc.Text()); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read codex stream: %w", err)
	}
	if st.completed == nil {
		return nil, fmt.Errorf("openai-codex: stream ended without a completed response")
	}
	// The Codex backend leaves `output` empty on response.completed; fill it
	// from the per-item done events. If the server did include output (public
	// API shape), prefer it.
	if len(st.completed.Output) == 0 {
		st.completed.Output = st.items
	}
	return toResponse(*st.completed)
}

// streamParse collects the pieces of a streamed Codex response: the finished
// output items and the terminal response envelope.
type streamParse struct {
	completed *apiResponse
	items     []outputItem
}

// consumeLine folds one `data:` line into the response. Lines that carry no
// payload are skipped, as are malformed ones: a single bad event should not
// discard a response the server already assembled.
func (st *streamParse) consumeLine(line string) error {
	line = strings.TrimSpace(line)
	data, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return nil
	}
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return nil
	}
	var ev struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
		Item     json.RawMessage `json:"item"`
		Error    *responseError  `json:"error"`
		Message  string          `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil // ignore malformed event lines
	}
	switch ev.Type {
	case "response.output_item.done":
		// Each done event carries one fully-assembled output item; this is
		// the authoritative source of content on the Codex backend.
		var item outputItem
		if err := json.Unmarshal(ev.Item, &item); err == nil {
			item.Raw = append(item.Raw[:0], ev.Item...)
			st.items = append(st.items, item)
		}
	case "response.completed", "response.incomplete":
		var out apiResponse
		if err := json.Unmarshal(ev.Response, &out); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		st.completed = &out
	case "response.failed", "error":
		return eventError(ev.Error, ev.Message, ev.Response)
	}
	return nil
}

// eventError reports why the server ended the stream with a failure. The
// response.failed shape may nest the message inside the response object.
func eventError(evErr *responseError, message string, response json.RawMessage) error {
	if evErr != nil && evErr.Message != "" {
		return fmt.Errorf("openai-codex: %s", evErr.Message)
	}
	if message != "" {
		return fmt.Errorf("openai-codex: %s", message)
	}
	if len(response) > 0 {
		var out apiResponse
		if err := json.Unmarshal(response, &out); err == nil && out.Error != nil {
			return fmt.Errorf("openai-codex: %s", out.Error.Message)
		}
	}
	return fmt.Errorf("openai-codex: request failed")
}
