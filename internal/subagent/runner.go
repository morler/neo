package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

const dynamicAgentSystemPrompt = `You are a focused subagent spawned by Neo's chat coordinator.

You have no memory of the parent conversation except the prompt you receive. Follow that prompt exactly, use tools as needed, and return a concise report with evidence. Do not commit changes unless explicitly asked.`

// AgentRunner runs chat-spawned subagents on Neo's core agent loop. Each call
// gets a fresh agent with the standard coding tool set and no nested agent tool.
type AgentRunner struct {
	backendMu    sync.RWMutex
	Provider     llm.Provider
	DefaultModel string
	// ContextWindowTokens applies the configured compaction window to each
	// fresh worker. Zero preserves compact.NewSummarizer's default behavior.
	// It is consulted only when WindowFor does not resolve a value, which is
	// how a worker on a different model gets that model's window rather than
	// the coordinator's.
	ContextWindowTokens int
	// WindowFor resolves the window for the provider and model a worker will
	// actually run on. The coordinator's compactor uses the same config method,
	// so a child and its parent cannot disagree about when to compact.
	WindowFor   func(provider, model string) int
	Root        string
	BashTimeout time.Duration
	// Script registers the JavaScript script tool for work-mode children when
	// the session has it enabled. It is a field rather than a lookup because the
	// contract is over a capability the runner shares with the coordinator.
	Script bool
	// CodeMode gives children the same code-first surface the coordinator has:
	// one code tool with run() instead of bash and the per-action file tools.
	CodeMode bool
}

// SetBackend updates the default provider and model used by future workers.
// Existing workers keep the backend snapshot they started with.
func (r *AgentRunner) SetBackend(provider llm.Provider, model string) error {
	model = strings.TrimSpace(model)
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	r.backendMu.Lock()
	r.Provider = provider
	r.DefaultModel = model
	r.backendMu.Unlock()
	return nil
}

// ScriptEnabled answers the supervisor's question about the script tool.
func (r *AgentRunner) ScriptEnabled() bool { return r.Script }

// CodeModeEnabled answers the supervisor's question about the surface shape.
func (r *AgentRunner) CodeModeEnabled() bool { return r.CodeMode }

func (r *AgentRunner) backend() (llm.Provider, string) {
	r.backendMu.RLock()
	defer r.backendMu.RUnlock()
	return r.Provider, r.DefaultModel
}

// RunAgentWithOptions applies immutable per-run capabilities without mutating
// the shared runner used by concurrent inspect children.
func (r *AgentRunner) RunAgentWithOptions(ctx context.Context, dir, input string, events chan<- AgentEvent, opts RunOptions) (string, error) {
	provider, model := r.backend()

	ag := agent.New(agent.Config{
		Model:     model,
		System:    dynamicAgentSystemPrompt,
		Provider:  provider,
		Tools:     r.registryWithOptions(dir, opts),
		Compactor: r.compactor(provider, model),
		MaxTurns:  agent.DefaultMaxTurns,
		OnEvent: func(e agent.Event) {
			if ev, ok := translate(e); ok {
				select {
				case events <- ev:
				case <-ctx.Done():
				}
			}
		},
	})

	out, err := ag.Send(ctx, input)
	u := ag.Usage()
	select {
	case events <- AgentEvent{
		Kind: "usage",
		Body: fmt.Sprintf("tokens in=%d out=%d cached=%d", u.InputTokens, u.OutputTokens, u.CacheReadTokens),
	}:
	case <-ctx.Done():
	}
	return out, err
}

func (r *AgentRunner) compactor(provider llm.Provider, model string) compact.Compactor {
	window := r.ContextWindowTokens
	if r.WindowFor != nil && provider != nil {
		if resolved := r.WindowFor(provider.Name(), model); resolved > 0 {
			window = resolved
		}
	}
	return compact.NewSummarizerForContextWindow(provider, model, window)
}

func (r *AgentRunner) registryWithOptions(dir string, opts RunOptions) *tools.Registry {
	bashTimeout := r.BashTimeout
	if bashTimeout <= 0 {
		bashTimeout = tools.DefaultBashTimeout
	}
	// The file tools are built once and handed to the script tool as well, so a
	// read inside a child's script is the same read its later edit consults.
	files := append([]tools.Tool{tools.Grep{Root: r.Root}, tools.Glob{Root: r.Root}},
		tools.NewFileTools()...)

	var base []tools.Tool
	if r.CodeMode {
		// A child in code mode acts the way the coordinator does. Absolute paths
		// are what make grep's output usable inside one call, where there is no cd.
		codeFiles := append([]tools.Tool{
			tools.Grep{Root: r.Root, Absolute: true},
			tools.Glob{Root: r.Root, Absolute: true},
		}, tools.NewFileTools()...)
		code := tools.NewScript(codeFiles)
		code.CodeMode = true
		code.CWD = dir
		code.Session = &tools.ScriptSession{}
		base = []tools.Tool{code}
	} else {
		base = append([]tools.Tool{tools.Bash{Timeout: bashTimeout, CWD: dir, ScriptAvailable: r.Script}}, files...)
		if r.Script {
			base = append(base, tools.NewScript(files))
		}
	}

	all := tools.NewRegistry(base...)
	if len(opts.Tools) == 0 {
		return all
	}
	return all.Filter(opts.Tools)
}

// translate maps core agent loop events onto the subagent event stream.
func translate(e agent.Event) (AgentEvent, bool) {
	switch e.Kind {
	case agent.EventAssistantText, agent.EventAssistantCommentary:
		return AgentEvent{Kind: "text", Body: e.Text}, true
	case agent.EventToolCall:
		if e.Name == "agent" {
			return AgentEvent{}, false
		}
		// A sub-call inside a tool carries the parent's group identity; keep it
		// so the UI can nest the row instead of showing a flat list.
		ev := AgentEvent{Kind: "tool", Body: summarize(e.Name, e.Args)}
		if e.GroupSize == 0 && e.GroupID != "" {
			ev.CallID = e.ToolUseID
			ev.GroupPos = e.GroupPos
		}
		return ev, true
	case agent.EventToolResult:
		if e.IsError {
			return AgentEvent{Kind: "tool", Body: e.Name + " error: " + e.Text}, true
		}
		return AgentEvent{}, false
	case agent.EventError, agent.EventMaxTurnsReached:
		msg := ""
		if e.Err != nil {
			msg = e.Err.Error()
		}
		return AgentEvent{Kind: "error", Body: msg}, true
	default:
		return AgentEvent{}, false
	}
}

func summarize(name string, args map[string]any) string {
	arg := func(key string) string { s, _ := args[key].(string); return s }
	switch name {
	case "bash":
		return "$ " + arg("command")
	case "read_file":
		return "read " + arg("path")
	case "write_file":
		return "write " + arg("path")
	case "edit_file":
		return "edit " + arg("path")
	case "grep":
		return "grep " + arg("pattern")
	case "glob":
		return "glob " + arg("pattern")
	}
	return name
}
