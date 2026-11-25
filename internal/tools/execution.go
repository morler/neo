package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/owainlewis/neo/internal/logx"
)

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
	// Logged before the sink lookup, because headless runs have no sink and are
	// exactly where "did this script read and write in one call?" has to be
	// answerable from NEO_LOG alone. Arguments are left out: they carry file
	// contents and paths, and only the shape of the call is being recorded.
	logx.Debug("script sub-call", "name", call.Name, "done", call.Done, "error", call.IsError)
	sink, ok := ctx.Value(subCallSinkKey{}).(func(SubCall))
	if !ok || sink == nil {
		return
	}
	sink(call)
}

// ApprovalRequest describes one tool call a tool wants to make on the model's
// behalf. Parent names the tool asking, so a prompt can say "script wants to
// run write_file" rather than implying the model called it directly.
type ApprovalRequest struct {
	ToolName string
	Args     map[string]any
	Parent   string
}

// ApprovalGate is the match-and-approve pair the agent already applies to a
// top-level call. A tool that runs other tools applies the same gate to each of
// them, which is the only way a rule like `tool_approvals: [write_file]` keeps
// its meaning once a script can call write_file.
//
// Both functions are the agent's own, not copies: a second matcher would be a
// second policy, and the two would drift.
type ApprovalGate struct {
	Requires func(tool string, args map[string]any) bool
	Approve  func(ctx context.Context, req ApprovalRequest) (bool, error)
}

type approvalGateKey struct{}

// WithApprovalGate attaches the gate a nested tool must consult. A nil gate is
// valid and means nobody configured approvals, which is the normal case.
func WithApprovalGate(ctx context.Context, gate *ApprovalGate) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalGateKey{}, gate)
}

// ApprovalGateFrom returns the gate carried by ctx, or nil.
func ApprovalGateFrom(ctx context.Context) *ApprovalGate {
	gate, _ := ctx.Value(approvalGateKey{}).(*ApprovalGate)
	return gate
}

// requireApproval applies the gate to one nested call. It returns nil when the
// call may proceed, and a tool-shaped error when it may not.
//
// The wording matches the agent's top-level path on purpose: a model should not
// be able to tell whether it was denied at the top or one level down, because
// the remedy is the same.
func requireApproval(ctx context.Context, toolName string, args map[string]any, parent string) error {
	gate := ApprovalGateFrom(ctx)
	if gate == nil || gate.Requires == nil || !gate.Requires(toolName, args) {
		return nil
	}
	if gate.Approve == nil {
		return errors.New("tool approval required but no approver is configured")
	}
	approved, err := gate.Approve(ctx, ApprovalRequest{ToolName: toolName, Args: args, Parent: parent})
	if err != nil {
		return fmt.Errorf("approval error: %w", err)
	}
	if !approved {
		return errors.New("user denied this tool call")
	}
	return nil
}
