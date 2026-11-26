package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/tools"
)

type EventKind string

const (
	EventAssistantText       EventKind = "assistant_text"
	EventAssistantCommentary EventKind = "assistant_commentary"
	EventParallelStart       EventKind = "parallel_start"
	EventToolCall            EventKind = "tool_call"
	EventToolResult          EventKind = "tool_result"
	EventSteeringApplied     EventKind = "steering_applied"
	EventDone                EventKind = "done"
	EventError               EventKind = "error"
	EventMaxTurnsReached     EventKind = "max_turns_reached"
)

var ErrMaxTurns = errors.New("max turns reached")

// ErrUnexpectedStopReason is returned when a provider reports a stop reason
// the agent does not know how to handle safely. Failing the turn prevents an
// unrecognized response from silently re-calling the provider until MaxTurns.
var ErrUnexpectedStopReason = errors.New("unexpected provider stop reason")

// ErrContextWindowExceeded is returned when the provider stops generation
// because the model's context window is full. The partial response remains
// available to the caller and in the transcript.
var ErrContextWindowExceeded = errors.New("response truncated: model context window exceeded")

// ErrMaxOutputTokens is returned when the model stops because it hit its
// output-token limit with no tool calls to continue on. Ending the turn (with
// the partial text) beats silently re-calling the provider until MaxTurns.
var ErrMaxOutputTokens = errors.New("response truncated: model hit its max output tokens limit")

// ErrMissingToolCall is returned when a provider reports that it stopped to
// call a tool but sends no tool call. There is nothing to run and nothing to
// reply with, so the loop would otherwise re-ask the provider until MaxTurns.
var ErrMissingToolCall = errors.New("provider stopped for a tool call but sent none")

type Event struct {
	Kind      EventKind
	Text      string
	Name      string
	Args      map[string]any
	ToolUseID string
	GroupID   string
	GroupSize int
	GroupPos  int
	Calls     []ToolCallRef
	MaxTurns  int
	IsError   bool // set on EventToolResult when the tool returned an error
	Err       error
}

// ToolCallRef is the ordered, immutable summary announced before a parallel
// group starts. IDs are provider tool-use IDs and are opaque to consumers.
type ToolCallRef struct {
	ID   string
	Name string
	Args map[string]any
}

type ApprovalRequest struct {
	ToolName string
	Args     map[string]any
	// Parent names the tool that asked for this call, when it was not the model
	// calling directly. A script asking for write_file is a different decision
	// for the user than the model asking for it, and the prompt should say so.
	Parent string
}

type Config struct {
	Model string
	// System is the flattened system prompt. SystemBlocks, when set, carries the
	// same prompt as ordered segments so the provider can place cache breakpoints;
	// the loop passes both so providers can use whichever they support.
	System       string
	SystemBlocks []llm.SystemBlock
	Provider     llm.Provider
	Tools        *tools.Registry
	Compactor    compact.Compactor
	// Effort is the reasoning effort sent with every request; empty means no
	// reasoning field is sent at all.
	Effort           string
	RequiresApproval func(string, map[string]any) bool
	Approve          func(context.Context, ApprovalRequest) (bool, error)
	MaxTurns         int
	MaxParallelTools int
	OnEvent          func(Event)
	Messages         []llm.Message
	Usage            llm.Usage
}

type Agent struct {
	cfg      Config
	messages []llm.Message
	usage    llm.Usage
	// promptTokens is the full prompt size the provider reported for the last
	// request, used to decide when to compact.
	promptTokens int

	backendMu sync.RWMutex

	steerMu      sync.Mutex
	steerActive  bool
	steerPending []string

	groupSeq uint64

	// repeats counts identical tool calls within the current user turn.
	repeats *repeatGuard
	// repeatReminders holds notes produced during this turn, appended after the
	// tool results so they land at the end of the conversation. Appending is the
	// only way to say something new without disturbing the cached prefix.
	repeatReminders []string
	// repeatStopped is set when a call was refused for repeating too often. The
	// turn ends after its tool results are committed, so the refusal reaches the
	// transcript as a failed tool_result and the user sees why.
	repeatStopped bool
	// repeatStopText is the refusal message for the call that tripped the guard.
	repeatStopText string
}

// DefaultMaxTurns is a high safety fuse for runaway tool loops, not a normal
// work budget. Coding tasks can legitimately need many model/tool cycles; the
// model should usually stop because it is done, not because Neo reached this.
const DefaultMaxTurns = 500

const DefaultMaxParallelTools = 8

func New(cfg Config) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}
	if cfg.MaxParallelTools <= 0 {
		cfg.MaxParallelTools = DefaultMaxParallelTools
	}
	if cfg.Tools == nil {
		cfg.Tools = tools.NewRegistry()
	}
	if cfg.Compactor == nil {
		cfg.Compactor = compact.NoCompaction{}
	}
	return &Agent{cfg: cfg, messages: cloneMessages(cfg.Messages), usage: cfg.Usage}
}

func (a *Agent) emit(e Event) {
	if a.cfg.OnEvent != nil {
		a.cfg.OnEvent(e)
	}
}

// SetEventHandler replaces the event callback. Useful when the consumer (e.g.
// a Bubble Tea program) isn't constructed until after the agent.
func (a *Agent) SetEventHandler(fn func(Event)) {
	a.cfg.OnEvent = fn
}

func (a *Agent) SetApprover(fn func(context.Context, ApprovalRequest) (bool, error)) {
	a.cfg.Approve = fn
}

func (a *Agent) Transcript() []llm.Message { return cloneMessages(a.messages) }

func (a *Agent) Model() string {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Model
}

// Compactor returns the summarizer currently in use. It exists so a caller can
// check that a /model switch really rebuilt the window rather than reusing the
// previous model's setting.
func (a *Agent) Compactor() compact.Compactor {
	_, _, compactor := a.backend()
	return compactor
}

// Backend returns a consistent provider/model pair for display and session
// persistence.
func (a *Agent) Backend() (string, string) {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Provider.Name(), a.cfg.Model
}

func (a *Agent) SetModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	a.backendMu.Lock()
	a.cfg.Model = model
	a.backendMu.Unlock()
}

// SetBackend changes the provider, model, and compactor as one unit. Callers
// switch only between turns, but the lock also keeps diagnostic readers and
// race-enabled tests from observing a half-switched backend.
func (a *Agent) SetBackend(provider llm.Provider, model string, compactor compact.Compactor) error {
	model = strings.TrimSpace(model)
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	if compactor == nil {
		compactor = compact.NoCompaction{}
	}
	a.backendMu.Lock()
	a.cfg.Provider = provider
	a.cfg.Model = model
	a.cfg.Compactor = compactor
	a.backendMu.Unlock()
	return nil
}

// SetTools replaces the tool registry between turns. It exists for switches
// that change what the model can call — the script tool is one — where
// rebuilding the whole agent would discard the transcript.
//
// Like SetBackend it is a between-turns operation; the lock keeps a concurrent
// reader from seeing a half-switched registry.
func (a *Agent) SetTools(reg *tools.Registry) error {
	if reg == nil {
		return fmt.Errorf("tool registry is required")
	}
	a.backendMu.Lock()
	a.cfg.Tools = reg
	a.backendMu.Unlock()
	return nil
}

// SetPrompt replaces the system prompt and its cached blocks, which must move
// together: a capability section is part of the prompt, so switching tools
// without switching the prompt would leave instructions for a tool that is gone.
func (a *Agent) SetPrompt(system string, blocks []llm.SystemBlock) {
	a.backendMu.Lock()
	a.cfg.System = system
	a.cfg.SystemBlocks = blocks
	a.backendMu.Unlock()
}

// tools returns the active registry under the same lock SetTools takes.
func (a *Agent) tools() *tools.Registry {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Tools
}

// prompt returns the active system prompt under the same lock SetPrompt takes.
func (a *Agent) prompt() (string, []llm.SystemBlock) {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.System, a.cfg.SystemBlocks
}

func (a *Agent) backend() (llm.Provider, string, compact.Compactor) {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Provider, a.cfg.Model, a.cfg.Compactor
}

// Effort returns the reasoning effort sent with each request.
func (a *Agent) Effort() string {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Effort
}

// SetEffort changes the reasoning effort for subsequent turns. Like SetModel it
// is a between-turns operation; a turn already in flight keeps the value it
// started with.
func (a *Agent) SetEffort(effort string) {
	a.backendMu.Lock()
	a.cfg.Effort = strings.TrimSpace(effort)
	a.backendMu.Unlock()
}

func (a *Agent) Clear() {
	a.messages = nil
	a.usage = llm.Usage{}
	a.promptTokens = 0
}

func (a *Agent) Usage() llm.Usage {
	return a.usage
}

// RunTool runs a built-in tool directly through the same approval path used for
// model-requested tool calls. It emits the normal tool call/result events, but
// does not mutate the LLM transcript.
func (a *Agent) RunTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	a.emit(Event{Kind: EventToolCall, Name: name, Args: cloneInput(input)})
	out, isErr := a.runTool(ctx, name, input)
	out = capToolResultContent(out)
	a.emit(Event{Kind: EventToolResult, Name: name, Text: out, IsError: isErr})
	return out, isErr
}

func (a *Agent) Send(ctx context.Context, userText string) (string, error) {
	return a.SendWith(ctx, userText, nil)
}

// SendWith sends a user turn that may carry image attachments alongside the
// text. Images are placed before the text block, which is how vision models
// expect a "here's an image, now my question" message to be ordered. Paths
// that can't be read are skipped with an inline note rather than aborting the
// turn — attachments are best-effort.
func (a *Agent) SendWith(ctx context.Context, userText string, imagePaths []string) (string, error) {
	return a.SendWithDisplay(ctx, userText, "", imagePaths)
}

// SendWithDisplay sends promptText to the provider while preserving a shorter
// user-visible value for transcript replay, session titles, and local search.
// An empty displayText keeps the normal prompt text visible.
func (a *Agent) SendWithDisplay(ctx context.Context, promptText, displayText string, imagePaths []string) (string, error) {
	var content []llm.ContentBlock
	var skipped []string
	for _, p := range imagePaths {
		blk, err := imageBlock(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", p, err))
			continue
		}
		content = append(content, blk)
	}
	text := promptText
	if len(skipped) > 0 {
		text = strings.TrimSpace(text + "\n\n[could not attach: " + strings.Join(skipped, "; ") + "]")
	}
	if text != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
	}
	if len(content) == 0 {
		return "", nil
	}
	// One user turn is one protection window: the same call ten minutes later in
	// a new turn is not a loop.
	a.repeats = newRepeatGuard()
	a.repeatReminders = nil
	a.repeatStopped = false
	a.repeatStopText = ""
	a.beginSteering()
	defer a.endSteering()
	_, model, _ := a.backend()
	logx.Debug("agent turn queued",
		"model", model,
		"messages_before", len(a.messages),
		"images", len(imagePaths),
		"text", logx.SafeString(text, 240),
	)
	visibleText := strings.TrimSpace(displayText)
	if visibleText == strings.TrimSpace(promptText) {
		visibleText = ""
	}
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content, DisplayText: visibleText})
	return a.run(ctx)
}

// Steer adds a user instruction to the active turn. The loop consumes it only
// after the current provider response and any requested tools have completed,
// preserving tool_use/tool_result pairing in the transcript.
func (a *Agent) Steer(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if !a.steerActive {
		return false
	}
	a.steerPending = append(a.steerPending, text)
	return true
}

func (a *Agent) beginSteering() {
	a.steerMu.Lock()
	a.steerActive = true
	a.steerPending = nil
	a.steerMu.Unlock()
}

func (a *Agent) endSteering() {
	a.steerMu.Lock()
	a.steerActive = false
	a.steerPending = nil
	a.steerMu.Unlock()
}

// takeSteering drains pending instructions. When closeIfEmpty is true, an
// empty inbox is atomically closed so a late instruction cannot be accepted
// after the loop has decided to finish the turn.
func (a *Agent) takeSteering(closeIfEmpty bool) (pending []string, closed bool) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerPending) == 0 {
		if closeIfEmpty {
			a.steerActive = false
			return nil, true
		}
		return nil, false
	}
	pending = append([]string(nil), a.steerPending...)
	a.steerPending = nil
	return pending, false
}

func (a *Agent) run(ctx context.Context) (string, error) {
	var finalText strings.Builder
	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		done, err := a.runTurn(ctx, turn, &finalText)
		if err != nil {
			return strings.TrimSpace(finalText.String()), err
		}
		if done {
			return strings.TrimSpace(finalText.String()), nil
		}
	}
	logx.Debug("agent max turns reached", "max_turns", a.cfg.MaxTurns)
	a.emit(Event{Kind: EventMaxTurnsReached, MaxTurns: a.cfg.MaxTurns, Err: ErrMaxTurns})
	return strings.TrimSpace(finalText.String()), ErrMaxTurns
}

// runTurn makes one provider request and applies its outcome. It reports
// whether the conversation is over; finalText accumulates across turns, so the
// caller owns it.
func (a *Agent) runTurn(ctx context.Context, turn int, finalText *strings.Builder) (bool, error) {
	resp, provider, err := a.requestTurn(ctx, turn)
	if err != nil {
		return true, err
	}
	if done, err := a.rejectStop(resp, finalText, turn, provider); done {
		return true, err
	}
	if resp.StopReason == "refusal" {
		return a.finishRefusal(ctx, resp, finalText, turn)
	}
	toolResults, steering := a.processResponseContent(ctx, resp.Content, finalText)
	a.messages = append(a.messages, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})
	if len(toolResults) > 0 {
		return a.commitToolResults(ctx, turn, toolResults, steering)
	}
	return a.finishStop(ctx, resp, finalText, turn)
}

// requestTurn compacts, sends the request, and records usage. It returns the
// response together with the provider that produced it, since later branches
// name it in their errors.
func (a *Agent) requestTurn(ctx context.Context, turn int) (*llm.Response, llm.Provider, error) {
	provider, model, compactor := a.backend()
	system, blocks := a.prompt()
	logx.Debug("agent turn start",
		"turn", turn+1,
		"max_turns", a.cfg.MaxTurns,
		"messages", len(a.messages),
		"provider", provider.Name(),
		"model", model,
	)
	compaction, err := compactor.Compact(ctx, a.messages, a.promptTokens)
	a.usage = addUsage(a.usage, compaction.Usage)
	if err != nil {
		logx.Debug("agent compaction error", "turn", turn+1, "error", err.Error())
		a.emit(Event{Kind: EventError, Err: err})
		return nil, provider, err
	}
	a.messages = compaction.Messages
	resp, err := provider.Complete(ctx, llm.Request{
		Model:        model,
		System:       system,
		SystemBlocks: blocks,
		Messages:     a.messages,
		Tools:        a.tools().Specs(),
		Effort:       a.Effort(),
	})
	if err != nil {
		logx.Debug("agent provider error", "turn", turn+1, "error", err.Error())
		a.emit(Event{Kind: EventError, Err: err})
		return nil, provider, err
	}
	a.usage = addUsage(a.usage, resp.Usage)
	a.promptTokens = resp.Usage.PromptTokens()
	logx.Debug("agent provider response",
		"turn", turn+1,
		"stop_reason", resp.StopReason,
		"content_blocks", len(resp.Content),
		"usage", resp.Usage,
	)
	return resp, provider, nil
}

// failTurn records the response's non-tool content and reports err. The
// branches that call it never run the tool calls, and persisting an unmatched
// tool_use would make the next request invalid.
func (a *Agent) failTurn(resp *llm.Response, finalText *strings.Builder, err error) error {
	a.appendSafeAssistantMessage(resp.Content)
	a.processResponseText(resp.Content, finalText)
	a.emit(Event{Kind: EventError, Err: err})
	return err
}

// rejectStop handles the stop reasons that end the conversation with an error.
// It reports whether the turn is over and, if so, with what.
func (a *Agent) rejectStop(resp *llm.Response, finalText *strings.Builder, turn int, provider llm.Provider) (bool, error) {
	switch {
	case !knownStopReason(resp.StopReason):
		err := fmt.Errorf("%w %q from %s", ErrUnexpectedStopReason, resp.StopReason, provider.Name())
		logx.Debug("agent unexpected stop reason", "turn", turn+1, "stop_reason", resp.StopReason)
		return true, a.failTurn(resp, finalText, err)
	case resp.StopReason == "tool_use" && !hasToolUse(resp.Content):
		// Nothing to execute means nothing to reply with, so the next request
		// would be identical and the loop would spin to MaxTurns. An adapter
		// bug or an unfamiliar provider dialect should fail closed after one
		// call, not burn 500.
		err := fmt.Errorf("%w (from %s)", ErrMissingToolCall, provider.Name())
		logx.Debug("agent tool_use stop with no tool call", "turn", turn+1)
		return true, a.failTurn(resp, finalText, err)
	case resp.StopReason == "model_context_window_exceeded":
		logx.Debug("agent model context window exceeded", "turn", turn+1)
		return true, a.failTurn(resp, finalText, ErrContextWindowExceeded)
	}
	return false, nil
}

// finishRefusal ends a refused turn, or continues it when steering input is
// waiting.
func (a *Agent) finishRefusal(ctx context.Context, resp *llm.Response, finalText *strings.Builder, turn int) (bool, error) {
	a.appendSafeAssistantMessage(resp.Content)
	a.processResponseText(resp.Content, finalText)
	if err := ctx.Err(); err != nil {
		logx.Debug("agent turn canceled after provider refusal", "turn", turn+1, "error", err.Error())
		a.emit(Event{Kind: EventError, Err: err})
		return true, err
	}
	if a.openSteering() {
		return false, nil
	}
	logx.Debug("agent turn refused", "turn", turn+1)
	a.emit(Event{Kind: EventDone})
	return true, nil
}

// commitToolResults records the tool results and any steering for the next
// request. The assistant message is already committed, so the transcript never
// holds a tool_use without its tool_result.
func (a *Agent) commitToolResults(ctx context.Context, turn int, results []llm.ContentBlock, steering []string) (bool, error) {
	if err := ctx.Err(); err != nil {
		a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: results})
		logx.Debug("agent turn canceled after tool results", "turn", turn+1, "error", err.Error())
		a.emit(Event{Kind: EventError, Err: err})
		return true, err
	}
	if len(steering) == 0 {
		steering, _ = a.takeSteering(false)
	}
	results = a.appendSteering(results, steering)
	results = a.appendRepeatReminders(results)
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: results})
	if a.repeatStopped {
		err := fmt.Errorf("%w: %s", ErrRepeatedToolCall, a.repeatStopMessage())
		logx.Debug("agent stopped a repeated tool call", "turn", turn+1)
		a.emit(Event{Kind: EventError, Err: err})
		return true, err
	}
	return false, nil
}

// appendRepeatReminders adds this turn's notes as trailing text blocks after the
// tool results. They are new content at the end of the conversation, so the
// cached prefix and every earlier message keep their exact bytes.
func (a *Agent) appendRepeatReminders(results []llm.ContentBlock) []llm.ContentBlock {
	reminders := a.repeatReminders
	a.repeatReminders = nil
	for _, text := range reminders {
		results = append(results, llm.ContentBlock{Type: "text", Text: text})
	}
	return results
}

// repeatStopMessage names the call that tripped the guard, so the turn error
// reads as "bash was called 12 times", not as a bare failure.
func (a *Agent) repeatStopMessage() string {
	if a.repeatStopText != "" {
		return a.repeatStopText
	}
	return "a tool call repeated too many times"
}

// finishStop handles the stop reasons that arrive after the assistant message
// is part of the transcript.
func (a *Agent) finishStop(ctx context.Context, resp *llm.Response, finalText *strings.Builder, turn int) (bool, error) {
	switch resp.StopReason {
	case "end_turn", "stop_sequence", "":
		if err := ctx.Err(); err != nil {
			logx.Debug("agent turn canceled after provider response", "turn", turn+1, "error", err.Error())
			a.emit(Event{Kind: EventError, Err: err})
			return true, err
		}
		if a.openSteering() {
			return false, nil
		}
		logx.Debug("agent turn complete", "turn", turn+1, "stop_reason", resp.StopReason)
		a.emit(Event{Kind: EventDone})
		return true, nil
	case "max_tokens":
		logx.Debug("agent max output tokens", "turn", turn+1)
		a.emit(Event{Kind: EventError, Err: ErrMaxOutputTokens})
		return true, ErrMaxOutputTokens
	case "pause_turn":
		if err := ctx.Err(); err != nil {
			logx.Debug("agent turn canceled while paused", "turn", turn+1, "error", err.Error())
			a.emit(Event{Kind: EventError, Err: err})
			return true, err
		}
		logx.Debug("agent provider requested continuation", "turn", turn+1)
	}
	return false, nil
}

// openSteering reports whether the conversation may take another turn,
// recording the buffered steering message when there is one to deliver.
func (a *Agent) openSteering() bool {
	steering, closed := a.takeSteering(true)
	if closed {
		return false
	}
	content := a.appendSteering(nil, steering)
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content})
	return true
}

func knownStopReason(reason string) bool {
	switch reason {
	case "", "end_turn", "stop_sequence", "tool_use", "max_tokens", "refusal", "pause_turn", "model_context_window_exceeded":
		return true
	default:
		return false
	}
}

// appendSafeAssistantMessage records the non-tool content of a terminal or
// rejected response. Tool calls are excluded because these branches do not run
// them, and persisting an unmatched tool_use would make the next request invalid.
func (a *Agent) appendSafeAssistantMessage(content []llm.ContentBlock) {
	safe := make([]llm.ContentBlock, 0, len(content))
	for _, block := range content {
		if block.Type != "tool_use" {
			safe = append(safe, block)
		}
	}
	if len(safe) == 0 {
		return
	}
	a.messages = append(a.messages, llm.Message{Role: llm.RoleAssistant, Content: safe})
}

func skippedToolResults(content []llm.ContentBlock, reason string) []llm.ContentBlock {
	var results []llm.ContentBlock
	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		results = append(results, llm.ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   reason,
			IsError:   true,
		})
	}
	return results
}

type preparedToolCall struct {
	block            llm.ContentBlock
	tool             tools.Tool
	approvalRequired bool
	lookupErr        string
	groupID          string
	groupSize        int
	groupPos         int
	// refused explains why this call must not run at all, and is set when the
	// repeat guard reached its stop count. An empty value means "run it".
	refused string
}

type toolOutcome struct {
	text    string
	isError bool
}

func (a *Agent) processResponseContent(ctx context.Context, content []llm.ContentBlock, finalText *strings.Builder) ([]llm.ContentBlock, []string) {
	var results []llm.ContentBlock
	hasTools := hasToolUse(content)
	for i := 0; i < len(content); {
		block := content[i]
		if block.Type == "text" {
			a.appendResponseText(block.Text, hasTools, finalText)
			i++
			continue
		}
		if block.Type != "tool_use" {
			i++
			continue
		}

		if reason, steering := a.executionStop(ctx); reason != "" {
			results = append(results, skippedToolResults(content[i:], reason)...)
			return results, steering
		}

		if !a.tools().ParallelSafe(block.Name, block.Input) {
			result := a.executeSerialBlock(ctx, block)
			results = append(results, result)
			i++
			if reason, steering := a.executionStop(ctx); reason != "" {
				results = append(results, skippedToolResults(content[i:], reason)...)
				return results, steering
			}
			continue
		}

		j := i
		for j < len(content) && content[j].Type == "tool_use" && a.tools().ParallelSafe(content[j].Name, content[j].Input) {
			j++
		}
		prepared := make([]preparedToolCall, 0, j-i)
		for _, candidate := range content[i:j] {
			prepared = append(prepared, a.prepareTool(candidate))
		}
		processed, batchResults, steering := a.executePreparedCalls(ctx, prepared)
		results = append(results, batchResults...)
		if processed < len(prepared) || len(steering) > 0 || ctx.Err() != nil {
			reason := "skipped because the user steered the active turn"
			if ctx.Err() != nil {
				reason = "skipped because the active turn was canceled"
			}
			results = append(results, skippedToolResults(content[i+processed:], reason)...)
			return results, steering
		}
		i = j
	}
	return results, nil
}

// processResponseText preserves useful text from a response whose tool calls
// must not run, such as a refusal, context limit, or unknown stop reason.
func (a *Agent) processResponseText(content []llm.ContentBlock, finalText *strings.Builder) {
	hasTools := hasToolUse(content)
	for _, block := range content {
		if block.Type == "text" {
			a.appendResponseText(block.Text, hasTools, finalText)
		}
	}
}

func (a *Agent) appendResponseText(text string, commentary bool, finalText *strings.Builder) {
	if text == "" {
		return
	}
	kind := EventAssistantText
	if commentary {
		kind = EventAssistantCommentary
	}
	a.emit(Event{Kind: kind, Text: text})
	finalText.WriteString(text)
	finalText.WriteString("\n")
}

// executePreparedCalls splits a run of parallel-safe calls around approval
// barriers. Approval matching was performed serially by prepareTool.
func (a *Agent) executePreparedCalls(ctx context.Context, calls []preparedToolCall) (int, []llm.ContentBlock, []string) {
	var results []llm.ContentBlock
	processed := 0
	for processed < len(calls) {
		if reason, steering := a.executionStop(ctx); reason != "" {
			return processed, results, steering
		}
		barrier := processed
		for barrier < len(calls) && !calls[barrier].approvalRequired {
			barrier++
		}
		if barrier > processed {
			segment := calls[processed:barrier]
			segmentResults := a.executePreparedGroup(ctx, segment)
			results = append(results, segmentResults...)
			processed = barrier
			if reason, steering := a.executionStop(ctx); reason != "" {
				return processed, results, steering
			}
		}
		if processed < len(calls) {
			call := calls[processed]
			results = append(results, a.executePreparedSerial(ctx, call))
			processed++
			if reason, steering := a.executionStop(ctx); reason != "" {
				return processed, results, steering
			}
		}
	}
	return processed, results, nil
}

// skippedOutcome is what a call records when the turn ended before it ran.
var skippedOutcome = toolOutcome{text: "skipped because the active turn was canceled", isError: true}

func (a *Agent) executePreparedGroup(ctx context.Context, calls []preparedToolCall) []llm.ContentBlock {
	// Parallelism only pays off when more than one call can actually run, so a
	// lone runnable call inside a larger group stays serial.
	if len(calls) < 2 || countRunnable(calls) < 2 {
		results := make([]llm.ContentBlock, 0, len(calls))
		for _, call := range calls {
			results = append(results, a.executePreparedSerial(ctx, call))
		}
		return results
	}

	a.groupSeq++
	groupID := fmt.Sprintf("parallel-%d", a.groupSeq)
	a.announceGroup(calls, groupID)
	outcomes := a.runGroup(ctx, calls, groupID)
	return a.collectGroup(calls, outcomes, groupID)
}

// countRunnable reports how many calls can run without an approval barrier,
// a missing tool, or a lookup failure.
func countRunnable(calls []preparedToolCall) int {
	n := 0
	for _, call := range calls {
		if call.tool != nil && call.lookupErr == "" && !call.approvalRequired {
			n++
		}
	}
	return n
}

// announceGroup emits the start event and one tool-call event per member, so
// the transcript shows the group before any result arrives.
func (a *Agent) announceGroup(calls []preparedToolCall, groupID string) {
	refs := make([]ToolCallRef, len(calls))
	for i, call := range calls {
		refs[i] = ToolCallRef{ID: call.block.ID, Name: call.block.Name, Args: cloneInput(call.block.Input)}
	}
	a.emit(Event{Kind: EventParallelStart, GroupID: groupID, GroupSize: len(calls), Calls: refs})
	for i, call := range calls {
		a.emit(toolEvent(EventToolCall, call.block, groupID, len(calls), i, "", false))
	}
}

// runGroup executes every call, in parallel where allowed, and returns the
// outcome per position. A canceled turn fills the remaining slots with a
// "skipped" outcome instead of running them.
func (a *Agent) runGroup(ctx context.Context, calls []preparedToolCall, groupID string) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))
	sem := make(chan struct{}, a.cfg.MaxParallelTools)
	var wg sync.WaitGroup
	for i, call := range calls {
		call.groupID = groupID
		call.groupSize = len(calls)
		call.groupPos = i
		if call.tool == nil || call.lookupErr != "" || call.approvalRequired {
			outcomes[i] = a.runPreparedTool(ctx, call)
			continue
		}
		wg.Add(1)
		go func(i int, call preparedToolCall) {
			defer wg.Done()
			outcomes[i] = a.runConcurrently(ctx, sem, call)
		}(i, call)
	}
	wg.Wait()
	return outcomes
}

// runConcurrently waits for a parallel slot and then runs one tool, reporting a
// skip rather than starting work once the turn is canceled.
func (a *Agent) runConcurrently(ctx context.Context, sem chan struct{}, call preparedToolCall) toolOutcome {
	if ctx.Err() != nil {
		return skippedOutcome
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
		if ctx.Err() != nil {
			return skippedOutcome
		}
		return a.runPreparedTool(ctx, call)
	case <-ctx.Done():
		return skippedOutcome
	}
}

// collectGroup caps, emits, and converts the outcomes into tool_result blocks
// in call order.
func (a *Agent) collectGroup(calls []preparedToolCall, outcomes []toolOutcome, groupID string) []llm.ContentBlock {
	results := make([]llm.ContentBlock, len(calls))
	for i, call := range calls {
		outcome := outcomes[i]
		outcome.text = capToolResultContent(outcome.text)
		a.emit(toolEvent(EventToolResult, call.block, groupID, len(calls), i, outcome.text, outcome.isError))
		results[i] = toolResult(call.block.ID, outcome)
	}
	return results
}

func (a *Agent) executeSerialBlock(ctx context.Context, block llm.ContentBlock) llm.ContentBlock {
	return a.executePreparedSerial(ctx, a.prepareTool(block))
}

func (a *Agent) executePreparedSerial(ctx context.Context, call preparedToolCall) llm.ContentBlock {
	call.groupSize = 1
	a.emit(toolEvent(EventToolCall, call.block, "", 1, 0, "", false))
	outcome := a.runPreparedTool(ctx, call)
	outcome.text = capToolResultContent(outcome.text)
	a.emit(toolEvent(EventToolResult, call.block, "", 1, 0, outcome.text, outcome.isError))
	return toolResult(call.block.ID, outcome)
}

func toolEvent(kind EventKind, block llm.ContentBlock, groupID string, groupSize, groupPos int, text string, isError bool) Event {
	return Event{Kind: kind, Name: block.Name, Args: cloneInput(block.Input), ToolUseID: block.ID,
		GroupID: groupID, GroupSize: groupSize, GroupPos: groupPos, Text: text, IsError: isError}
}

func toolResult(id string, outcome toolOutcome) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool_result", ToolUseID: id, Content: outcome.text, IsError: outcome.isError}
}

func (a *Agent) executionStop(ctx context.Context) (string, []string) {
	if ctx.Err() != nil {
		return "skipped because the active turn was canceled", nil
	}
	if pending, _ := a.takeSteering(false); len(pending) > 0 {
		return "skipped because the user steered the active turn", pending
	}
	return "", nil
}

func (a *Agent) appendSteering(content []llm.ContentBlock, steering []string) []llm.ContentBlock {
	for _, text := range steering {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
		a.emit(Event{Kind: EventSteeringApplied, Text: text})
	}
	return content
}

func hasToolUse(content []llm.ContentBlock) bool {
	for _, block := range content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func (a *Agent) runTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	// A direct call is not a model loop, so it bypasses the repeat guard. The
	// guard is turn-scoped state and a RunTool call is outside any turn.
	call := a.prepareToolWithoutRepeatGuard(llm.ContentBlock{Type: "tool_use", Name: name, Input: input})
	outcome := a.runPreparedTool(ctx, call)
	return outcome.text, outcome.isError
}

// prepareTool performs lookup, approval matching, and repeat counting for a
// model-requested call. Approval itself remains deferred so it can act as a
// serial execution barrier.
func (a *Agent) prepareTool(block llm.ContentBlock) preparedToolCall {
	return a.prepareToolWithRepeatGuard(block, a.repeats)
}

// prepareToolWithoutRepeatGuard prepares a call the user made directly, which
// the repeat guard must not count.
func (a *Agent) prepareToolWithoutRepeatGuard(block llm.ContentBlock) preparedToolCall {
	return a.prepareToolWithRepeatGuard(block, nil)
}

// prepareToolWithRepeatGuard is the shared body of prepareTool and
// prepareToolWithoutRepeatGuard; a nil guard skips repeat counting.
func (a *Agent) prepareToolWithRepeatGuard(block llm.ContentBlock, guard *repeatGuard) preparedToolCall {
	call := preparedToolCall{block: block}
	t, ok := a.tools().Get(block.Name)
	if !ok {
		call.lookupErr = fmt.Sprintf("unknown tool: %s", block.Name)
		return call
	}
	call.tool = t
	if a.cfg.RequiresApproval != nil {
		call.approvalRequired = a.cfg.RequiresApproval(block.Name, block.Input)
	}
	// Counting happens here because prepareTool runs serially, before any
	// parallel execution, so the result is deterministic even when one response
	// contains several calls to the same tool.
	if guard == nil {
		return call
	}
	if count, remind, stop := guard.observe(block.Name, block.Input); stop {
		call.refused = repeatStopReason(block.Name, count)
		a.repeatStopped = true
		a.repeatStopText = call.refused
	} else if remind {
		a.repeatReminders = append(a.repeatReminders, repeatReminder(block.Name, count))
	}
	return call
}

func (a *Agent) runPreparedTool(ctx context.Context, call preparedToolCall) toolOutcome {
	// The tool gets its own copy. call.block.Input belongs to the response that
	// becomes the assistant message in the transcript, and the Tool interface
	// does not promise not to mutate what it is given.
	name, input := call.block.Name, cloneInput(call.block.Input)
	ctx = tools.WithCallMetadata(ctx, tools.CallMetadata{
		ToolUseID: call.block.ID,
		GroupID:   call.groupID,
		GroupSize: call.groupSize,
		GroupPos:  call.groupPos,
	})
	// A tool that runs other tools reports them here. The events carry the
	// parent's group identity, so a UI can nest them without a second field:
	// GroupID is the parent's tool-use ID and GroupPos orders the sub-calls.
	// The same matcher and approver the top-level path uses. A tool that runs
	// other tools must consult this one, because the outer call's name says
	// nothing about what runs inside it.
	if a.cfg.RequiresApproval != nil || a.cfg.Approve != nil {
		ctx = tools.WithApprovalGate(ctx, &tools.ApprovalGate{
			Requires: a.cfg.RequiresApproval,
			Approve: func(ctx context.Context, req tools.ApprovalRequest) (bool, error) {
				if a.cfg.Approve == nil {
					return false, nil
				}
				return a.cfg.Approve(ctx, ApprovalRequest{ToolName: req.ToolName, Args: req.Args, Parent: req.Parent})
			},
		})
	}
	ctx = tools.WithSubCallSink(ctx, func(sub tools.SubCall) {
		kind := EventToolCall
		if sub.Done {
			kind = EventToolResult
		}
		a.emit(Event{
			Kind:      kind,
			Name:      sub.Name,
			Args:      cloneInput(sub.Args),
			ToolUseID: sub.ID,
			GroupID:   call.block.ID,
			GroupSize: 0, // unknown while the script runs; the UI appends rows
			GroupPos:  subPosition(sub.ID),
			Text:      sub.Text,
			IsError:   sub.IsError,
		})
	})
	logx.Debug("tool call", "name", name, "args", logx.SafeAny(input))
	if call.refused != "" {
		logx.Debug("tool call refused as a repeat", "name", name)
		return toolOutcome{text: call.refused, isError: true}
	}
	if call.lookupErr != "" {
		logx.Debug("tool lookup failed", "name", name)
		return toolOutcome{text: call.lookupErr, isError: true}
	}
	if call.approvalRequired {
		if a.cfg.Approve == nil {
			logx.Debug("tool approval missing approver", "name", name)
			return toolOutcome{text: "tool approval required but no approver is configured", isError: true}
		}
		approved, err := a.cfg.Approve(ctx, ApprovalRequest{
			ToolName: name,
			Args:     cloneInput(input),
		})
		if err != nil {
			logx.Debug("tool approval error", "name", name, "error", err.Error())
			return toolOutcome{text: fmt.Sprintf("approval error: %v", err), isError: true}
		}
		if !approved {
			logx.Debug("tool denied by user", "name", name)
			return toolOutcome{text: "user denied this tool call", isError: true}
		}
		logx.Debug("tool approved", "name", name)
	}
	out, err := call.tool.Run(ctx, input)
	if err != nil {
		logx.Debug("tool error",
			"name", name,
			"error", err.Error(),
			"output", logx.PayloadValue(out),
		)
		return toolOutcome{text: fmt.Sprintf("error: %v\n%s", err, out), isError: true}
	}
	logx.Debug("tool result", "name", name, "output", logx.PayloadValue(out))
	return toolOutcome{text: out}
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:         a.InputTokens + b.InputTokens,
		OutputTokens:        a.OutputTokens + b.OutputTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
	}
}

func cloneMessages(in []llm.Message) []llm.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.Message, len(in))
	for i, msg := range in {
		out[i].Role = msg.Role
		out[i].Content = cloneContentBlocks(msg.Content)
		out[i].DisplayText = msg.DisplayText
	}
	return out
}

func cloneContentBlocks(in []llm.ContentBlock) []llm.ContentBlock {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.ContentBlock, len(in))
	for i, block := range in {
		out[i] = cloneContentBlock(block)
	}
	return out
}

// cloneContentBlock isolates every mutable field. Raw is a byte slice and
// Source is a pointer, so copying the struct alone would leave both shared.
func cloneContentBlock(block llm.ContentBlock) llm.ContentBlock {
	out := block
	out.Input = cloneInput(block.Input)
	if block.Raw != nil {
		out.Raw = append(json.RawMessage(nil), block.Raw...)
	}
	if block.Source != nil {
		source := *block.Source
		out.Source = &source
	}
	return out
}

// cloneInput deep-copies a tool input so the copy shares nothing mutable with
// the original. Ownership of JSON-like values is the rule the transcript
// depends on: a provider response's input map ends up in the assistant message
// committed to the transcript, so anything handed the same map could corrupt a
// turn that has already happened.
func cloneInput(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies the JSON value types a decoded tool input can contain.
// Everything else is immutable (strings, numbers, bools) or something a tool
// schema cannot produce, so it is returned as-is rather than reflected over.
// A recursive copy beats a JSON round trip here: it allocates only what the
// payload actually contains and cannot fail.
func cloneValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, elem := range value {
			out[k] = cloneValue(elem)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, elem := range value {
			out[i] = cloneValue(elem)
		}
		return out
	default:
		return v
	}
}

// subPosition reads the ordinal a sub-call's ID carries. The tools package
// builds it as "<parent>#<n>", so the number after the last '#' is the position
// within the parent. An ID that does not follow the shape sorts last rather
// than failing: a UI row in the wrong place is better than a dropped event.
func subPosition(id string) int {
	i := strings.LastIndex(id, "#")
	if i < 0 || i == len(id)-1 {
		return 0
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n < 0 {
		return 0
	}
	return n - 1
}
