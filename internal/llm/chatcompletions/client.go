// Package chatcompletions implements llm.Provider for OpenAI-compatible
// Chat Completions APIs.
package chatcompletions

import (
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

// Client talks to an OpenAI-compatible Chat Completions endpoint.
type Client struct {
	ProviderName string
	// APIKey is sent as a Bearer token. Empty means the endpoint needs no
	// authentication (a local runtime such as Ollama or vLLM), so the
	// Authorization header is omitted entirely rather than sent blank.
	APIKey       string
	Endpoint     string
	DefaultModel string
	HTTP         *http.Client
	MaxRetries   int
	BaseDelay    time.Duration
	// CredentialHint is attached to 401/403 failures so the advice names the
	// variable this endpoint actually reads. Empty falls back to the built-in
	// per-provider hints.
	CredentialHint string
}

func (c *Client) Name() string { return c.ProviderName }

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = c.DefaultModel
	}
	apiReq := BuildRequest(req, model)
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}
	// Log the body before sending. Every other adapter already does, and it is
	// the only way to answer "did this backend actually receive reasoning_effort
	// and what did it do with it" — an unknown value is passed through by
	// design, so the server's silence looks the same as acceptance. The payload
	// is reduced to a sha256 unless NEO_LOG_VERBOSE is set.
	logx.Debug("provider request",
		"provider", c.Name(),
		"model", model,
		"messages", len(apiReq.Messages),
		"tools", len(apiReq.Tools),
		"reasoning_effort", apiReq.ReasoningEffort,
		"payload", logx.PayloadValue(string(body)),
	)

	result, err := retry.Do(ctx, retry.Options{
		Provider:       c.Name(),
		ErrorLabel:     c.ProviderName,
		MaxRetries:     c.MaxRetries,
		BaseDelay:      c.BaseDelay,
		CredentialHint: c.CredentialHint,
	}, func(ctx context.Context) (retry.AttemptResult, error) {
		raw, status, retryAfter, err := c.doRequest(ctx, body)
		return retry.AttemptResult{Body: raw, Status: status, RetryAfter: retryAfter}, err
	})
	if err != nil {
		return nil, err
	}
	raw, status := result.Body, result.Status
	if status >= 400 {
		httpErr := llm.NewHTTPError(c.ProviderName, status, raw)
		httpErr.CredentialHint = c.CredentialHint
		return nil, httpErr
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}
	return ToLLMResponse(out)
}

func (c *Client) doRequest(ctx context.Context, body []byte) ([]byte, int, retry.RetryAfter, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = retry.NewHTTPClient()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	raw, err := retry.ReadAllIdle(resp.Body, cancel)
	if err != nil {
		return nil, resp.StatusCode, retry.Absent(), fmt.Errorf("read response body: %w", err)
	}
	return raw, resp.StatusCode, retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()), nil
}

// Request is the Chat Completions request body.
type Request struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
	// ReasoningEffort is passed through as given (lowercased). Servers disagree
	// on the accepted set — some take low/medium/high, others add minimal or
	// reject the field entirely — so an unknown value is sent anyway and the
	// server's error is what surfaces to the user.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func reasoningEffort(effort string) string {
	return strings.ToLower(strings.TrimSpace(effort))
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Response is the Chat Completions response body.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// BuildRequest converts Neo's provider-neutral request to Chat Completions.
func BuildRequest(req llm.Request, model string) Request {
	messages := make([]Message, 0, len(req.Messages)+1)
	if system := systemText(req); system != "" {
		messages = append(messages, Message{Role: "system", Content: system})
	}
	for _, m := range req.Messages {
		messages = append(messages, toMessages(m)...)
	}
	out := Request{Model: model, Messages: messages, Tools: toTools(req.Tools), MaxTokens: req.MaxTokens,
		ReasoningEffort: reasoningEffort(req.Effort)}
	if len(out.Tools) > 0 {
		out.ToolChoice = "auto"
	}
	return out
}

func systemText(req llm.Request) string {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	var b strings.Builder
	for _, blk := range req.SystemBlocks {
		if blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

func messageContentText(v any) string {
	s, _ := v.(string)
	return s
}

func toMessages(m llm.Message) []Message {
	switch m.Role {
	case llm.RoleAssistant:
		var content strings.Builder
		var calls []ToolCall
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				content.WriteString(b.Text)
			case "tool_use":
				calls = append(calls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{Name: b.Name, Arguments: string(llm.MarshalToolInput(b.Input))}})
			}
		}
		if content.Len() == 0 && len(calls) == 0 {
			return nil
		}
		return []Message{{Role: "assistant", Content: content.String(), ToolCalls: calls}}
	case llm.RoleUser, llm.RoleTool:
		var out []Message
		var text strings.Builder
		var parts []ContentPart
		flushUser := func() {
			if text.Len() == 0 && len(parts) == 0 {
				return
			}
			if len(parts) == 0 {
				out = append(out, Message{Role: "user", Content: text.String()})
				text.Reset()
				return
			}
			if text.Len() > 0 {
				parts = append(parts, ContentPart{Type: "text", Text: text.String()})
			}
			out = append(out, Message{Role: "user", Content: parts})
			text.Reset()
			parts = nil
		}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "image":
				if b.Source != nil && b.Source.Data != "" {
					mediaType := b.Source.MediaType
					if mediaType == "" {
						mediaType = "application/octet-stream"
					}
					parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "data:" + mediaType + ";base64," + b.Source.Data}})
				}
			case "tool_result":
				flushUser()
				out = append(out, Message{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content})
			}
		}
		flushUser()
		return out
	default:
		return nil
	}
}

func toTools(tools []llm.ToolSpec) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, Tool{Type: "function", Function: FunctionSpec{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	return out
}

// ToLLMResponse converts Chat Completions output into Neo's neutral response.
func ToLLMResponse(out Response) (*llm.Response, error) {
	if out.Error != nil {
		return nil, fmt.Errorf("chat completions: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("chat completions: no choices returned")
	}
	choice := out.Choices[0]
	var content []llm.ContentBlock
	if text := messageContentText(choice.Message.Content); text != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
	}
	for _, tc := range choice.Message.ToolCalls {
		input := map[string]any{}
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				return nil, fmt.Errorf("chat completions: decode tool arguments for %s: %w", tc.Function.Name, err)
			}
		}
		content = append(content, llm.ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	return &llm.Response{Content: content, StopReason: stopReason(choice), Usage: toUsage(out.Usage)}, nil
}

func stopReason(c Choice) string {
	if len(c.Message.ToolCalls) > 0 || c.FinishReason == "tool_calls" {
		return "tool_use"
	}
	if c.FinishReason == "length" {
		return "max_tokens"
	}
	return "end_turn"
}

func toUsage(u *Usage) llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	return llm.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
}
