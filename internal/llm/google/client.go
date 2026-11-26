// Package google implements the llm.Provider interface against the Google
// Gemini GenerateContent API.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

const (
	DefaultEndpoint = "https://generativelanguage.googleapis.com/v1beta/models"
	DefaultModel    = "gemini-3.5-flash"
	apiKeyURL       = "https://aistudio.google.com/app/apikey"
)

// Client talks to Google's Gemini GenerateContent API.
type Client struct {
	APIKey     string
	Endpoint   string
	HTTP       *http.Client
	MaxRetries int
	BaseDelay  time.Duration
}

// New constructs a Gemini provider from GOOGLE_API_KEY.
func New() (*Client, error) {
	key, err := llm.APIKeyFromEnv("GOOGLE_API_KEY", apiKeyURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		APIKey:     key,
		Endpoint:   DefaultEndpoint,
		HTTP:       retry.NewHTTPClient(),
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}, nil
}

func (c *Client) Name() string { return "google" }

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	apiReq := buildRequest(req)
	if apiReq.buildErr != nil {
		return nil, apiReq.buildErr
	}
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request", "provider", c.Name(), "model", model, "messages", len(req.Messages), "tools", len(req.Tools), "payload", logx.PayloadValue(string(body)))

	result, err := retry.Do(ctx, retry.Options{
		Provider:   c.Name(),
		ErrorLabel: "google",
		MaxRetries: c.MaxRetries,
		BaseDelay:  c.BaseDelay,
		Retryable: func(status int) bool {
			return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
		},
	}, func(ctx context.Context) (retry.AttemptResult, error) {
		raw, status, retryAfter, err := c.doRequest(ctx, model, body)
		return retry.AttemptResult{Body: raw, Status: status, RetryAfter: retryAfter}, err
	})
	if err != nil {
		return nil, err
	}
	raw, status := result.Body, result.Status
	if status >= 400 {
		return nil, llm.NewHTTPError("google", status, raw)
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
	}
	logx.Debug("provider response", "provider", c.Name(), "status", status, "response", logx.PayloadValue(string(raw)), "candidates", len(out.Candidates))
	return toLLMResponse(out)
}

func validateRequest(req llm.Request) error {
	supportedImages := map[string]bool{
		"image/png":  true,
		"image/jpeg": true,
		"image/webp": true,
		"image/heic": true,
		"image/heif": true,
	}
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if block.Type != "image" || block.Source == nil || block.Source.Data == "" {
				continue
			}
			if block.Source.Type != "" && block.Source.Type != "base64" {
				return fmt.Errorf("google: unsupported image source type %q", block.Source.Type)
			}
			if !supportedImages[block.Source.MediaType] {
				return fmt.Errorf("google: unsupported image media type %q", block.Source.MediaType)
			}
		}
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, model string, body []byte) ([]byte, int, retry.RetryAfter, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = retry.NewHTTPClient()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	endpoint := strings.TrimRight(c.Endpoint, "/") + "/" + url.PathEscape(model) + ":generateContent"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.APIKey)
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

type request struct {
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Contents          []content         `json:"contents"`
	Tools             []tool            `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`

	// buildErr carries a build-time problem (an effort this backend cannot
	// express) to Complete, where it becomes the caller's error. It is not part
	// of the wire shape.
	buildErr error
}

type generationConfig struct {
	MaxOutputTokens int           `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *thinkingConf `json:"thinkingConfig,omitempty"`
}

// thinkingConf is Gemini's thinking control. Gemini takes a token budget, not
// an effort word, so Neo's names map onto budgets and a bare integer is taken
// as the budget itself. A value Gemini cannot express is an error rather than
// a silently dropped setting: the word would never reach the API, so the API
// could never complain, and the user would believe it had taken effect.
//
// includeThoughts stays off: it would add thought summaries to the streamed
// parts, and Neo has no rendering for them past the existing thinking block.
type thinkingConf struct {
	// ThinkingBudget has no omitempty on purpose: 0 is a meaningful value (turn
	// thinking off) and omitting it would fall back to the model's dynamic
	// default, silently undoing "none".
	ThinkingBudget  int  `json:"thinkingBudget"`
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
}

// EffortBudgets maps Neo's effort names onto Gemini thinking budgets. Unlike
// the Messages API, Gemini has a real "off" value — a budget of 0 — so "none"
// belongs in the map here rather than being special-cased. Only a negative
// budget is rejected.
var EffortBudgets = map[string]int{
	"none":   0,
	"low":    1024,
	"medium": 8192,
	"high":   24576,
	"xhigh":  32768,
	"max":    65536,
}

// EffortNames is the vocabulary in increasing order, shared by error messages
// and help text so the two cannot disagree about what exists. A test keeps it
// in step with EffortBudgets.
const EffortNames = "none, low, medium, high, xhigh, max"

func thinkingFor(effort string) (*thinkingConf, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return nil, nil
	}
	budget, ok := EffortBudgets[effort]
	if !ok {
		n, err := strconv.Atoi(effort)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("google cannot express reasoning effort %q: use %s, or a thinking budget of 0 or more",
				effort, EffortNames)
		}
		budget = n
	}
	return &thinkingConf{ThinkingBudget: budget}, nil
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	Raw              json.RawMessage   `json:"-"`
}

// UnmarshalJSON keeps the original Gemini part so opaque thought metadata can
// be replayed byte-for-byte on the next request. The API requires this for
// thinking-model function-call continuations.
func (p *part) UnmarshalJSON(data []byte) error {
	type wirePart part
	var decoded wirePart
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = part(decoded)
	p.Raw = append(p.Raw[:0], data...)
	return nil
}

// MarshalJSON emits a preserved response part unchanged when it is replayed.
func (p part) MarshalJSON() ([]byte, error) {
	if len(p.Raw) > 0 {
		return p.Raw, nil
	}
	type wirePart part
	return json.Marshal(wirePart(p))
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type tool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type response struct {
	Candidates     []candidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
	Usage *usageMetadata `json:"usageMetadata,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// usageMetadata is Gemini's token accounting for one response.
type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	CachedTokenCount     int `json:"cachedContentTokenCount"`
}

type candidate struct {
	Content       content `json:"content"`
	FinishReason  string  `json:"finishReason"`
	FinishMessage string  `json:"finishMessage"`
}

func buildRequest(req llm.Request) request {
	out := request{Contents: toContents(req.Messages), Tools: toTools(req.Tools)}
	if system := systemText(req); system != "" {
		out.SystemInstruction = &content{Parts: []part{{Text: system}}}
	}
	thinking, err := thinkingFor(req.Effort)
	if err != nil {
		out.buildErr = err
		return out
	}
	if req.MaxTokens > 0 || thinking != nil {
		out.GenerationConfig = &generationConfig{MaxOutputTokens: req.MaxTokens, ThinkingConfig: thinking}
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

func toContents(messages []llm.Message) []content {
	out := make([]content, 0, len(messages))
	toolRefs := map[string]toolRef{}
	for _, m := range messages {
		parts := toParts(m.Content, toolRefs)
		if len(parts) == 0 {
			continue
		}
		role := "user"
		if m.Role == llm.RoleAssistant {
			role = "model"
		}
		out = append(out, content{Role: role, Parts: parts})
	}
	return out
}

type toolRef struct {
	name   string
	wireID string
}

func toParts(blocks []llm.ContentBlock, toolRefs map[string]toolRef) []part {
	parts := make([]part, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				if p, ok := replayPart(b); ok && p.Text == b.Text {
					parts = append(parts, p)
				} else {
					parts = append(parts, part{Text: b.Text})
				}
			}
		case "image":
			if b.Source != nil && b.Source.Data != "" {
				parts = append(parts, part{InlineData: &inlineData{MimeType: b.Source.MediaType, Data: b.Source.Data}})
			}
		case "tool_use":
			wireID := b.ID
			if p, ok := replayPart(b); ok && p.FunctionCall != nil && p.FunctionCall.Name == b.Name {
				if p.FunctionCall.Args == nil {
					// Gemini omits args on a zero-argument call; args is
					// required on replay, so send {} rather than nothing.
					p = withEmptyArgs(p)
				}
				parts = append(parts, p)
				wireID = p.FunctionCall.ID
			} else {
				args := b.Input
				if args == nil {
					args = map[string]any{}
				}
				parts = append(parts, part{FunctionCall: &functionCall{ID: b.ID, Name: b.Name, Args: args}})
			}
			toolRefs[b.ID] = toolRef{name: b.Name, wireID: wireID}
		case "tool_result":
			name := b.Name
			wireID := b.ToolUseID
			if ref, ok := toolRefs[b.ToolUseID]; ok {
				if name == "" {
					name = ref.name
				}
				wireID = ref.wireID
			}
			if name == "" {
				name = b.ToolUseID
			}
			response := map[string]any{"output": b.Content}
			if b.IsError {
				response = map[string]any{"error": b.Content}
			}
			parts = append(parts, part{FunctionResponse: &functionResponse{ID: wireID, Name: name, Response: response}})
		case "raw":
			if p, ok := replayPart(b); ok {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

// withEmptyArgs sets functionCall.args to {} on a preserved part. The raw
// bytes are patched rather than dropped so any opaque fields Gemini attached
// to the call are still replayed verbatim.
func withEmptyArgs(p part) part {
	p.FunctionCall.Args = map[string]any{}
	if len(p.Raw) == 0 {
		return p
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(p.Raw, &raw); err != nil {
		p.Raw = nil
		return p
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(raw["functionCall"], &call); err != nil {
		p.Raw = nil
		return p
	}
	call["args"] = json.RawMessage("{}")
	callBytes, err := json.Marshal(call)
	if err != nil {
		p.Raw = nil
		return p
	}
	raw["functionCall"] = callBytes
	patched, err := json.Marshal(raw)
	if err != nil {
		p.Raw = nil
		return p
	}
	p.Raw = patched
	return p
}

// replayPart accepts Gemini thought metadata and raw function-call parts. The
// latter preserves unsigned calls in a parallel response without turning Neo's
// synthetic internal IDs into Gemini wire IDs.
func replayPart(b llm.ContentBlock) (part, bool) {
	if len(b.Raw) == 0 {
		return part{}, false
	}
	var p part
	if err := json.Unmarshal(b.Raw, &p); err != nil {
		return part{}, false
	}
	if b.Type == "tool_use" && p.FunctionCall != nil {
		return p, true
	}
	if !p.Thought && p.ThoughtSignature == "" {
		return part{}, false
	}
	return p, true
}

func toTools(specs []llm.ToolSpec) []tool {
	if len(specs) == 0 {
		return nil
	}
	decls := make([]functionDeclaration, 0, len(specs))
	for _, s := range specs {
		decls = append(decls, functionDeclaration{Name: s.Name, Description: s.Description, Parameters: s.InputSchema})
	}
	return []tool{{FunctionDeclarations: decls}}
}

func toLLMResponse(out response) (*llm.Response, error) {
	cand, err := validateResponse(out)
	if err != nil {
		return nil, err
	}
	blocks := make([]llm.ContentBlock, 0, len(cand.Content.Parts))
	missingIDs := map[string]int{}
	for _, p := range cand.Content.Parts {
		blocks = append(blocks, partsToBlocks(p, missingIDs)...)
	}
	resp := &llm.Response{Content: blocks, StopReason: stopReason(cand)}
	if out.Usage != nil {
		resp.Usage = usageFrom(out.Usage)
	}
	return resp, nil
}

// validateResponse rejects the envelope-level failures before any part is
// read, and returns the candidate to convert.
func validateResponse(out response) (candidate, error) {
	if out.Error != nil {
		return candidate{}, fmt.Errorf("google: %s", out.Error.Message)
	}
	if len(out.Candidates) == 0 {
		if out.PromptFeedback != nil && out.PromptFeedback.BlockReason != "" {
			return candidate{}, fmt.Errorf("google: prompt blocked with %s", out.PromptFeedback.BlockReason)
		}
		return candidate{}, fmt.Errorf("google: no candidates returned")
	}
	cand := out.Candidates[0]
	if cand.FinishReason != "" && cand.FinishReason != "STOP" && cand.FinishReason != "MAX_TOKENS" {
		if cand.FinishMessage != "" {
			return candidate{}, fmt.Errorf("google: generation stopped with %s: %s", cand.FinishReason, cand.FinishMessage)
		}
		return candidate{}, fmt.Errorf("google: generation stopped with %s", cand.FinishReason)
	}
	return cand, nil
}

// partsToBlocks converts one response part. Thought parts carry no text of
// their own, so they and anything unrecognized are preserved as raw blocks for
// the next request rather than dropped.
func partsToBlocks(p part, missingIDs map[string]int) []llm.ContentBlock {
	raw := preservedPart(p)
	if p.Thought && p.FunctionCall == nil {
		return []llm.ContentBlock{{Type: "raw", Raw: raw}}
	}
	var blocks []llm.ContentBlock
	if p.Text != "" {
		blocks = append(blocks, llm.ContentBlock{Type: "text", Text: p.Text, Raw: raw})
	}
	if p.FunctionCall != nil {
		blocks = append(blocks, toolUseBlock(p.FunctionCall, missingIDs, raw))
	}
	if p.Text == "" && p.FunctionCall == nil && len(raw) > 0 {
		blocks = append(blocks, llm.ContentBlock{Type: "raw", Raw: raw})
	}
	return blocks
}

// toolUseBlock names a call, synthesizing an id from the function name when
// Gemini supplied none and disambiguating repeats within one response.
func toolUseBlock(call *functionCall, missingIDs map[string]int, raw json.RawMessage) llm.ContentBlock {
	id := call.ID
	if id == "" {
		missingIDs[call.Name]++
		id = call.Name
		if n := missingIDs[call.Name]; n > 1 {
			id = fmt.Sprintf("%s_%d", id, n)
		}
	}
	input := call.Args
	if input == nil {
		input = map[string]any{}
	}
	return llm.ContentBlock{Type: "tool_use", ID: id, Name: call.Name, Input: input, Raw: raw}
}

// usageFrom partitions Gemini's counts the way llm.Usage expects: the prompt
// count includes the cached count, and thoughts bill as output.
func usageFrom(usage *usageMetadata) llm.Usage {
	return llm.Usage{
		InputTokens:     max(0, usage.PromptTokenCount-usage.CachedTokenCount),
		OutputTokens:    usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
		CacheReadTokens: usage.CachedTokenCount,
	}
}

func preservedPart(p part) json.RawMessage {
	if p.FunctionCall == nil && !p.Thought && p.ThoughtSignature == "" {
		return nil
	}
	return append(json.RawMessage(nil), p.Raw...)
}

func stopReason(c candidate) string {
	for _, p := range c.Content.Parts {
		if p.FunctionCall != nil {
			return "tool_use"
		}
	}
	if c.FinishReason == "MAX_TOKENS" {
		return "max_tokens"
	}
	return "end_turn"
}
