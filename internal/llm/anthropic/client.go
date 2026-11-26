package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

const defaultEndpoint = "https://api.anthropic.com/v1/messages"
const defaultVersion = "2023-06-01"
const apiKeyURL = "https://console.anthropic.com/settings/keys"

// defaultMaxTokens caps output for a single completion. Responses stream and
// the transport has no total deadline, so this is a cost and runaway guard
// rather than a timeout budget.
const defaultMaxTokens = 16384

type Client struct {
	APIKey     string
	Endpoint   string
	Version    string
	HTTP       *http.Client
	MaxRetries int           // default: 4
	BaseDelay  time.Duration // default: 500ms
}

func New() (*Client, error) {
	key, err := llm.APIKeyFromEnv("ANTHROPIC_API_KEY", apiKeyURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		APIKey:   key,
		Endpoint: defaultEndpoint,
		Version:  defaultVersion,
		// No total deadline: a fixed timeout caps how long a generation may
		// take. The connection is bounded instead by header and idle deadlines
		// (see retry.NewHTTPClient) and by the caller's context.
		HTTP:       retry.NewHTTPClient(),
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}, nil
}

func (c *Client) Name() string { return "anthropic" }

type apiRequest struct {
	Model     string         `json:"model"`
	System    any            `json:"system,omitempty"` // string or []systemBlock
	Messages  []wireMessage  `json:"messages"`
	Tools     []llm.ToolSpec `json:"tools,omitempty"`
	MaxTokens int            `json:"max_tokens"`
	Stream    bool           `json:"stream,omitempty"`
	Thinking  *thinking      `json:"thinking,omitempty"`
}

// thinking is the Messages API's extended-thinking switch. The API has no
// effort scale: it takes a token budget, and every budget below the model's
// minimum (1024) or at/above max_tokens is rejected. It also refuses thinking
// for models without support and for requests that force a tool choice.
//
// Effort values map to budgets rather than being sent verbatim because this
// API cannot accept a word. A value that is not one of EffortBudgets therefore
// falls through to the default budget; the user's own words cannot be honoured
// here, and silently sending nothing would look like the setting was ignored.
type thinking struct {
	Type       string `json:"type"` // always "enabled"
	BudgetToks int    `json:"budget_tokens"`
}

// wireMessage and wireBlock are the Messages API shapes. They exist so a
// content block can carry a cache_control breakpoint, which the neutral
// llm.ContentBlock has no reason to know about. The embedded block's fields are
// promoted, so a wireBlock marshals exactly like a ContentBlock plus the
// optional breakpoint.
type wireMessage struct {
	Role    llm.Role    `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireBlock struct {
	llm.ContentBlock
	// Input shadows ContentBlock.Input, whose omitempty tag drops an empty map.
	// The Messages API requires input on every tool_use, so a zero-argument
	// call must still send {} or the request is rejected on replay.
	Input        json.RawMessage `json:"input,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

// newWireBlock converts a neutral block for the wire, materializing tool_use
// input as JSON so an empty object survives.
func newWireBlock(b llm.ContentBlock) wireBlock {
	b.Raw = nil
	out := wireBlock{ContentBlock: b}
	if b.Type == "tool_use" {
		out.Input = llm.MarshalToolInput(b.Input)
	}
	return out
}

// systemBlock is an Anthropic system content block. A non-nil CacheControl marks
// a prompt-cache breakpoint: this block and everything before it are cached.
type systemBlock struct {
	Type         string        `json:"type"` // always "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// apiUsage is the wire shape of Anthropic's usage object. input_tokens already
// excludes the cached counts, which is the partition llm.Usage documents, so
// the fields map across directly.
type apiUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u apiUsage) toLLM() llm.Usage {
	return llm.Usage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
	}
}

// systemPayload renders the request's system prompt for the API. When the
// request carries SystemBlocks it emits a content-block array, attaching a
// cache_control breakpoint to each block flagged for caching; otherwise it
// falls back to the plain System string.
func systemPayload(req llm.Request) any {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	blocks := make([]systemBlock, 0, len(req.SystemBlocks))
	for _, b := range req.SystemBlocks {
		if b.Text == "" {
			continue
		}
		blk := systemBlock{Type: "text", Text: b.Text}
		if b.Cache {
			blk.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		blocks = append(blocks, blk)
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

// wireMessages strips content blocks the Messages API does not accept, removes
// provider-specific replay data from the neutral blocks it does accept, and
// places the conversation cache breakpoint. Messages left with no content are
// dropped entirely.
//
// The breakpoint goes on the very last block, so the cached prefix is tools +
// system + the whole conversation. Next turn the server matches that prefix and
// only the new messages are written. One rolling breakpoint is enough: a cache
// entry outlives the request that created it, so the earlier entry is still
// matched after the breakpoint has moved on.
func wireMessages(in []llm.Message, cache bool) []wireMessage {
	out := make([]wireMessage, 0, len(in))
	for _, m := range in {
		blocks := make([]wireBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "text", "tool_use", "tool_result", "image":
				blocks = append(blocks, newWireBlock(b))
			}
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, wireMessage{Role: m.Role, Content: blocks})
	}
	if cache && len(out) > 0 {
		last := out[len(out)-1].Content
		last[len(last)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	}
	return out
}

// cacheRequested reports whether the caller asked for prompt caching. It is
// derived from the system blocks rather than carried as its own request field:
// the same features.prompt_caching flag governs both, so one signal keeps the
// two breakpoints from drifting apart.
func cacheRequested(req llm.Request) bool {
	for _, b := range req.SystemBlocks {
		if b.Cache {
			return true
		}
	}
	return false
}

// EffortBudgets maps Neo's effort names onto the Messages API's token budgets.
// The numbers are suggestions, not API constants: any budget the model accepts
// works, which is why a bare integer is accepted as well. They rise with the
// name so an unfamiliar scale still behaves monotonically.
//
// "none" is deliberately absent: the Messages API has no "off" value, only the
// absence of the field, so "none" is handled before this map is consulted.
var EffortBudgets = map[string]int{
	"low":    2048,
	"medium": 8192,
	"high":   16384,
	"xhigh":  24576,
	"max":    32000,
}

// EffortNone turns extended thinking off. It is the only name that removes the
// field rather than filling it in.
const EffortNone = "none"

// EffortNames is the vocabulary in increasing order, shared by error messages
// and help text so the two cannot disagree about what exists. A test keeps it
// in step with EffortBudgets.
const EffortNames = "none, low, medium, high, xhigh, max"

// minThinkingBudget is the smallest budget the API accepts for any model.
const minThinkingBudget = 1024

// thinkingFor turns the request's effort into a thinking switch, raising
// maxTokens when the budget would otherwise not leave room for an answer.
//
// This API has no effort scale — it takes a token budget — so an effort name
// cannot be forwarded as written. A name Neo knows is mapped; a bare integer is
// taken as the budget itself; anything else is an error naming what this
// backend can express. Returning an error rather than silently dropping the
// setting is the point: the API could never reject the word, because the word
// is never sent, so this is the only place the user can learn it did nothing.
//
// The API also requires budget_tokens < max_tokens. Simply honouring that would
// leave a thinking request with no room to reply, so the budget sets the floor
// instead: max_tokens grows to budget + defaultMaxTokens. An unset effort
// returns nil, and the request stays exactly as it was before this feature.
// "none" also returns nil: this API disables thinking by omitting the field, so
// the request it produces is identical to an unset one — which is the honest
// representation of "off" here.
func thinkingFor(effort string, maxTokens *int) (*thinking, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || effort == EffortNone {
		return nil, nil
	}
	budget, ok := EffortBudgets[effort]
	if !ok {
		n, err := strconv.Atoi(effort)
		if err != nil || n < minThinkingBudget {
			return nil, fmt.Errorf("anthropic cannot express reasoning effort %q: use %s, or a token budget of at least %d",
				effort, effortNames(), minThinkingBudget)
		}
		budget = n
	}
	if *maxTokens < budget+defaultMaxTokens {
		*maxTokens = budget + defaultMaxTokens
	}
	return &thinking{Type: "enabled", BudgetToks: budget}, nil
}

// effortNames renders the vocabulary for error messages.
func effortNames() string {
	return EffortNames
}

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = defaultMaxTokens
	}
	// Resolve thinking before the literal: thinkingFor may raise req.MaxTokens,
	// and the field's value must be read after that, not before.
	thinking, err := thinkingFor(req.Effort, &req.MaxTokens)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(apiRequest{
		Model:     req.Model,
		System:    systemPayload(req),
		Messages:  wireMessages(req.Messages, cacheRequested(req)),
		Tools:     req.Tools,
		MaxTokens: req.MaxTokens,
		Stream:    true,
		Thinking:  thinking,
	})
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request",
		"provider", c.Name(),
		"model", req.Model,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"payload", logx.PayloadValue(string(body)),
	)
	// Streaming is the transport, not a UI feature: nothing is emitted as it
	// arrives. It keeps a long generation from being one long request, and the
	// Messages API requires it above a max_tokens threshold, so raising
	// defaultMaxTokens later does not need another transport change.
	streamed, result, err := c.sendWithRetry(ctx, body)
	if err != nil {
		return nil, err
	}
	if result.Status >= 400 {
		logx.Debug("provider client error",
			"provider", c.Name(),
			"status", result.Status,
			"body", logx.PayloadValue(string(result.Body)),
		)
		return nil, llm.NewHTTPError("anthropic", result.Status, result.Body)
	}
	logx.Debug("provider response",
		"provider", c.Name(),
		"status", result.Status,
		"items", len(streamed.Content),
		"stop_reason", streamed.StopReason,
		"usage", streamed.Usage,
	)
	return streamed, nil
}

// sendWithRetry runs one streaming attempt under the shared retry policy and
// returns both the assembled response and the last attempt's status.
//
// streamed is assigned by the attempt and read after it succeeds. The retry
// helper deals in buffered bodies, which a stream has no equivalent of, so the
// assembled response comes back this way rather than through it.
func (c *Client) sendWithRetry(ctx context.Context, body []byte) (*llm.Response, retry.AttemptResult, error) {
	var streamed *llm.Response
	result, err := retry.Do(ctx, retry.Options{
		Provider:       c.Name(),
		ErrorLabel:     "anthropic",
		MaxRetries:     c.MaxRetries,
		BaseDelay:      c.BaseDelay,
		RetryAfterBody: parseRetryAfterBody,
	}, c.streamAttempt(body, &streamed))
	return streamed, result, err
}

// streamAttempt returns the function one retry attempt runs. cancel is scoped
// to the attempt: the idle guard cancels that context when the stream stalls,
// while the outer ctx stays clean for the retry decision.
func (c *Client) streamAttempt(body []byte, streamed **llm.Response) retry.AttemptFunc {
	return func(ctx context.Context) (retry.AttemptResult, error) {
		*streamed = nil
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		resp, err := c.send(ctx, body)
		if err != nil {
			return retry.AttemptResult{}, err
		}
		body := retry.IdleBody(resp.Body, retry.IdleTimeout, cancel)
		defer body.Close()
		retryAfter := retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now())

		if resp.StatusCode >= 400 {
			raw, readErr := io.ReadAll(body)
			return retry.AttemptResult{Body: raw, Status: resp.StatusCode, RetryAfter: retryAfter}, readErr
		}
		parsed, err := parseStream(body)
		if err != nil {
			return retry.AttemptResult{Status: resp.StatusCode, RetryAfter: retryAfter}, err
		}
		*streamed = parsed
		return retry.AttemptResult{Status: resp.StatusCode, RetryAfter: retryAfter}, nil
	}
}

// send issues one POST and returns the live response with its body unread, so
// the caller can consume the event stream incrementally.
func (c *Client) send(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", c.Version)
	return c.HTTP.Do(httpReq)
}

func parseRetryAfterBody(body []byte) retry.RetryAfter {
	var probe struct {
		RetryAfter any `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return retry.Absent()
	}
	switch v := probe.RetryAfter.(type) {
	case float64:
		if v < 0 {
			return retry.Absent()
		}
		return retry.RetryAfter{Delay: time.Duration(v * float64(time.Second)), Present: true}
	case string:
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return retry.Absent()
		}
		return retry.RetryAfter{Delay: time.Duration(n) * time.Second, Present: true}
	}
	return retry.Absent()
}
