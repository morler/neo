package tools

import "context"

type executionContextKey struct{}
type conversationGenerationKey struct{}

// CallMetadata identifies one model-requested tool execution. It is carried
// through context so tools that supervise nested work can attribute their
// events without exposing scheduler fields in model-facing schemas.
type CallMetadata struct {
	ToolUseID string
	GroupID   string
	GroupSize int
	GroupPos  int
}

func WithCallMetadata(ctx context.Context, metadata CallMetadata) context.Context {
	return context.WithValue(ctx, executionContextKey{}, metadata)
}

func CallMetadataFrom(ctx context.Context) (CallMetadata, bool) {
	metadata, ok := ctx.Value(executionContextKey{}).(CallMetadata)
	return metadata, ok
}

// WithConversationGeneration tags work started by one TUI conversation. Tools
// copy the value onto buffered UI events at production time, before those
// events can be delayed by an intermediate channel.
func WithConversationGeneration(ctx context.Context, generation uint64) context.Context {
	return context.WithValue(ctx, conversationGenerationKey{}, generation)
}

// ConversationGenerationFrom returns the TUI conversation generation carried
// by ctx. Zero is the initial generation and the default outside the TUI.
func ConversationGenerationFrom(ctx context.Context) uint64 {
	generation, _ := ctx.Value(conversationGenerationKey{}).(uint64)
	return generation
}

// SubCall describes one tool call a tool makes on the model's behalf. It exists
// so a tool that runs other tools — the script tool today — can report what it
// did without importing the agent package and without exposing scheduler fields
// in a model-facing schema.
//
// ID is unique within one parent call; the agent builds it from the parent's
// tool-use ID and a per-script counter. Done distinguishes the start of a call
// from its end.
type SubCall struct {
	ID      string
	Name    string
	Args    map[string]any
	Text    string
	IsError bool
	Done    bool
}

type subCallSinkKey struct{}

// WithSubCallSink attaches the receiver for sub-calls made inside this tool.
// A nil sink is valid and means "nobody is watching", which is the default for
// headless runs and for tests that only care about the result.
func WithSubCallSink(ctx context.Context, sink func(SubCall)) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, subCallSinkKey{}, sink)
}

// ReportSubCall forwards one sub-call to the sink, if any.
//
// The sink is called inline, so it must not block: a slow consumer is dropped
// rather than allowed to stall the work it is observing. Callers that have
// nothing to report simply do not call this.
func ReportSubCall(ctx context.Context, call SubCall) {
	sink, ok := ctx.Value(subCallSinkKey{}).(func(SubCall))
	if !ok || sink == nil {
		return
	}
	sink(call)
}
