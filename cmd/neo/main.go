package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/approval"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
	"github.com/owainlewis/neo/internal/workflow"
	"github.com/owainlewis/neo/internal/workspace"
)

// Version is overridable at build time via -ldflags "-X main.Version=...".
// Default "dev" makes local builds obvious in the splash screen.
var Version = "dev"

// chatSystemPrompt is always loaded, so it stays short and assumes nothing
// about which tools are registered. Behaviour that depends on a specific tool
// lives in the capability blocks below and is appended only when that tool is
// present — `neo run` has no workflow or agent tool, and instructing a model to
// use tools it was not given wastes tokens and invites invented calls.
const chatSystemPrompt = `You are neo, a coding agent working in the user's current working directory.

Inspect the project before changing it, and prefer the smallest change that does
the job. After changing code, run the checks that cover it and say what you ran;
if something could not be verified, say that instead of implying it passed.

Answer the request that was made. An inspection, explanation, review, or design
request is answered, not implemented, unless changes were asked for. Leave
unrelated work alone, and do not commit, push, or deploy unless asked.

Before a tool call, write one short sentence explaining what you are checking or
changing and why. Do not narrate obvious individual calls or expose private
reasoning. ` + chatPromptTail

// chatPromptTail closes chatSystemPrompt and carries the batching rule, which
// script can contradict. A script is a serial barrier: its host calls are
// synchronous, and the agent flushes a parallel batch around it. Left
// unqualified, "put independent calls in one response" makes the cheapest way
// to obey the rule a split into two responses, which is exactly the round trip
// a script exists to remove. Kept as its own constant so only a session that
// can script pays for the clarification.
const chatPromptTail = `Issue independent reads, searches, or inspections together in one
response. When you finish a task, briefly summarize what changed.`

// chatPromptTailCodeMode closes the prompt in single-tool mode. The batching rule is
// dropped rather than qualified: with one tool there is no second top-level call
// to batch, so keeping the instruction would only tell the model to do something
// it cannot.
const chatPromptTailCodeMode = `Work inside one script call: read, decide, and write together when the decision
depends on what you read. When you finish a task, briefly summarize what changed.`

// chatPromptTailScripted is chatPromptTail plus the tie-break. It is used only
// when the script tool is registered, so the default prompt stays byte-identical.
const chatPromptTailScripted = `Issue independent reads, searches, or inspections together in one
response. That batching is for calls that do not depend on each other; when one
depends on another's output, that is one script call, not two responses. When
you finish a task, briefly summarize what changed.`

// workflowCapability is appended when the workflow tool is registered.
const workflowCapability = `

# Workflow checklist

For multi-step tasks, or when workflow instructions are provided, create a
visible checklist with the workflow tool before doing the work. Instructions may
come from the user's request, AGENTS.md, an invoked skill, or your own plan;
render them through the workflow tool either way, preserving the wording and
order of user-provided numbered steps. Do not invent a workflow for a simple
single-step request. Mark each high-level item running before working on it, and
done, failed, or skipped based on the outcome.`

// subagentCapability is appended when the agent tool is registered.
const subagentCapability = `

# Delegation

When the user asks for a coordinator-worker or orchestrated-agent flow, act as
the coordinator: plan first, delegate suitable self-contained tasks to subagents
with the agent tool, inspect their results, and base any workflow status on
evidence. Do not mirror every tool call manually; Neo attaches tool and subagent
activity to the active workflow item automatically. Write subagent prompts from
the user's goal and current context, and use the normal tools directly when
delegation is unnecessary.`

// goalCapability is appended when the goal tool is registered.
const goalCapability = `

# Standing goal

When the user sets a goal, keep working toward it across turns. Neo sends a
continuation message while the goal is active and the user is idle; treat each
one as "the goal is still open" rather than a new request.

Never redefine the goal as a smaller task. Before calling the goal tool with
complete, verify the objective with a command, test, or inspection and cite that

evidence in the reason. Call it with blocked only when the same blocker has
recurred after real attempts; one transient failure is not a blocker. Pausing,
resuming, clearing, and the attempt budget belong to the user, so the goal tool
offers no such action.`

// scriptCapability is appended when the script tool is registered.
//
// It states when a script is worth writing and shows one runnable example; the
// tool's own schema lists the calls and their return shapes, so the prompt does
// not repeat that list. Repeating it is how the two drifted apart before.
// cmd/neo/script_example_test.go runs the example and fails if it stops working.
//
// The judgement is written around dependency, not volume. A script's host calls
// are synchronous, so looping over many files is slower than one `grep -r`,
// which is what the model reaches for anyway: issue #83 measured nine runs on a
// 2,001-line file and the model never called read_file, it narrowed the window
// with `sed`. Naming "many files" as the reason to script therefore advertised
// the one shape where the tool loses. What a script actually saves is a round
// trip, and one file is enough to need one.
const scriptCapability = `

# Scripts

Write a script when the next call depends on what this one returns: whether a
pattern matched, how many times, which line, or what that line says. The saving
is the round trip, not the number of files, so read, branch, and write inside
one script whenever the branch decides the write. The description lists what a
script may call.

` + "```js" + `
const hits = tools.grep({pattern: "TODO"}).split("\n").filter(Boolean);
hits.length + " matches: " + hits.join(", ")
` + "```" + `

` + "`grep`" + ` says "no matches" instead of returning an empty string. When the next
call does not need this result, call the tool instead of wrapping it.`

func main() {
	os.Exit(execute(os.Args[1:], stdio{
		in:  os.Stdin,
		out: os.Stdout,
		err: os.Stderr,
	}, lifecycle{
		init:  initLogging,
		close: logx.Close,
	}))
}

type stdio struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

type lifecycle struct {
	init  func() error
	close func() error
}

// initLogging redacts the built-in provider keys plus whatever api_key_env
// names the configuration declares for custom providers. A broken config is
// not fatal here: the command reports it with the usual guidance, and logging
// still falls back to the built-in secret list.
func initLogging() error {
	var customEnv []string
	if cfg, err := config.Load(); err == nil {
		customEnv = cfg.CustomAPIKeyEnvNames()
	}
	return logx.InitFromEnvWithSecrets(customEnv)
}

// execute owns process-lifetime setup and cleanup. It returns before main
// terminates the process, so deferred cleanup is never skipped on failures.
func execute(args []string, streams stdio, life lifecycle) int {
	if err := life.init(); err != nil {
		fmt.Fprintf(streams.err, "warning: NEO_LOG: %v\n", err)
	}
	defer func() { _ = life.close() }()
	return run(args, streams)
}

// run is the testable command boundary. It owns signal handling, dispatches one
// command, and returns the process exit code without terminating the process.
func run(args []string, streams stdio) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logx.Debug("neo start", "args", logx.SafeAny(args))

	// --agent is global: it selects the system prompt, not a subcommand, so it
	// is pulled out before dispatch and works with both chat and run.
	args, agentName, err := takeAgentFlag(args)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		return 2
	}
	args, scriptFlag, err := takeScriptFlag(args)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		return 2
	}
	scriptOverride = scriptFlag
	args, err = takeCodeModeFlag(args)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		return 2
	}

	// `neo` with no subcommand defaults to chat — the common case.
	if len(args) == 0 {
		return runChat(ctx, agentName, streams)
	}

	switch args[0] {
	case "chat":
		return runChat(ctx, agentName, streams)
	case "run":
		return runHeadless(ctx, args[1:], agentName, streams)
	case "agents":
		return runAgents(streams)
	case "sessions":
		return runSessions(ctx, args[1:], streams)
	case "doctor":
		return runDoctor(ctx, streams.out)
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(streams.err, "usage: neo resume <session-id>")
			return 2
		}
		return resumeSession(ctx, args[1], agentName, streams)
	case "login":
		return runLogin(ctx, streams)
	case "logout":
		return runLogout(streams)
	case "-v", "--version", "version":
		printVersion(streams.out)
		return 0
	case "-h", "--help", "help":
		printUsage(streams.out)
		return 0
	default:
		logx.Debug("neo unknown command", "command", args[0])
		fmt.Fprintf(streams.err, "unknown command: %s\n", args[0])
		printUsage(streams.out)
		return 2
	}
}

var errAgentNeedsName = fmt.Errorf("--agent needs a name, for example --agent=reviewer")

// takeAgentFlag removes --agent from anywhere in args and returns its value.
// It is handled here rather than by a FlagSet because it has to be readable
// before we know which subcommand is running.
func takeAgentFlag(args []string) ([]string, string, error) {
	var out []string
	name := ""
	seen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(arg, "=")
		if flag != "--agent" && flag != "-agent" {
			out = append(out, arg)
			continue
		}
		seen = true
		if hasValue {
			name = value
			continue
		}
		if i+1 >= len(args) {
			return nil, "", errAgentNeedsName
		}
		i++
		name = args[i]
	}
	if seen && strings.TrimSpace(name) == "" {
		return nil, "", errAgentNeedsName
	}
	return out, strings.TrimSpace(name), nil
}

// takeScriptFlag extracts the global --script switch. The tool is off by
// default because it costs prompt bytes on every request, so the flag is the
// per-invocation way to turn it on without editing the config.
func takeScriptFlag(args []string) ([]string, *bool, error) {
	var out []string
	var on *bool
	for _, arg := range args {
		switch arg {
		case "--script":
			v := true
			on = &v
			continue
		case "--no-script":
			v := false
			on = &v
			continue
		}
		out = append(out, arg)
	}
	return out, on, nil
}

// codeModeOverride carries --codemode=<mode> into loadConfig, the same way
// scriptOverride carries --script. It is written once at startup.
var codeModeOverride string

// takeCodeModeFlag extracts --codemode=<mode>. The mode decides the session's
// whole tool surface, so an unknown value is an error rather than a silent fall
// back to the default: a typo would otherwise look like a working session.
func takeCodeModeFlag(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		value, ok := strings.CutPrefix(arg, "--codemode=")
		if !ok {
			out = append(out, arg)
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(value))
		if mode != config.CodeModeCodemode && mode != config.CodeModeClassic {
			return nil, fmt.Errorf("--codemode must be %q or %q, got %q", config.CodeModeCodemode, config.CodeModeClassic, value)
		}
		codeModeOverride = mode
	}
	return out, nil
}

// loadProfile resolves --agent. An unknown name is fatal: continuing with the
// built-in coding prompt would make a typo look like a working session.
func loadProfile(cwd, name string, errOut io.Writer) (profile.Profile, bool) {
	if name == "" {
		return profile.Profile{}, true
	}
	p, err := profile.Load(cwd, name)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return profile.Profile{}, false
	}
	return p, true
}

func runAgents(streams stdio) int {
	cwd, _ := os.Getwd()
	found, err := profile.List(cwd)
	if err != nil {
		fmt.Fprintf(streams.err, "agents: %v\n", err)
		return 1
	}
	if len(found) == 0 {
		fmt.Fprintln(streams.out, "No agents defined. Create one at ~/.neo/agents/<name>.md or .neo/agents/<name>.md")
		return 0
	}
	w := tabwriter.NewWriter(streams.out, 0, 0, 2, ' ', 0)
	for _, p := range found {
		fmt.Fprintf(w, "%s\t%s\n", p.Name, p.Path)
	}
	return flushTabwriter(w, streams.err)
}

func flushTabwriter(w *tabwriter.Writer, errOut io.Writer) int {
	if err := w.Flush(); err != nil {
		fmt.Fprintf(errOut, "write: %v\n", err)
		return 1
	}
	return 0
}

const usageText = `neo — a Go coding agent

USAGE:
  neo                Interactive chat mode (default)
  neo chat           Interactive chat mode (explicit)
  neo run [options] <prompt>
                     Run one headless prompt and exit
  neo agents         List available agent prompts
  neo sessions       List saved chat sessions
  neo sessions search <query>
                     Search saved session transcripts
  neo doctor         Check local config and environment
  neo resume <id>    Resume a saved chat session
  neo login          Log in to an OpenAI ChatGPT/Codex subscription (device code)
  neo logout         Remove stored subscription credentials
  neo version        Show the Neo version (also -v, --version)
  neo help           Show this help

CONFIG:
  Reads neo.yaml (cwd) → ~/.neo/config.yaml → embedded defaults.
  Select a backend with the "provider" key. Built in: "anthropic" (default),
  "openai", "openrouter", "google". Any other name must be declared under a
  top-level "providers:" map in neo.yaml.

  Inside chat, /provider switches backends and writes "provider:" and "model:"
  back to the config file in use (~/.neo/config.yaml when running on embedded
  defaults). /model switches the model and writes only "model:", so the choice
  survives a restart.

  /goal <objective> sets a standing goal for the session and starts the first
  turn; Neo keeps sending continuation turns while it is active and you are
  idle. /goal status reports it, /goal pause and /goal resume stop and restart
  continuation, and /goal clear removes it. The goal is saved with the session.

  ANTHROPIC_API_KEY    required when provider is "anthropic"
  OPENAI_API_KEY       required when provider is "openai" with api_key auth
  OPENROUTER_API_KEY   required when provider is "openrouter"
  GOOGLE_API_KEY       required when provider is "google"

  To use a ChatGPT subscription instead of an API key, set in neo.yaml:
    provider: openai
    openai_auth: subscription
  then run "neo login".

  Custom OpenAI-compatible endpoints (Ollama, vLLM, LiteLLM, internal
  gateways) are declared once and then selected by name:
    provider: local
    providers:
      local:
        endpoint: http://127.0.0.1:8000/v1
        api_key_env: LOCAL_LLM_API_KEY   # omit if the endpoint needs no key
        default_model: qwen3-coder
  endpoint takes a base URL such as http://127.0.0.1:8000/v1; Neo appends
  /chat/completions, and a URL that already ends with it is used as-is.
  Only the OpenAI Chat Completions protocol is supported; there is no
  Anthropic-compatible custom backend.

AGENT PROMPTS:
  --agent <name>     Replace the built-in system prompt with a markdown file
                     from ~/.neo/agents/<name>.md or .neo/agents/<name>.md.
                     Works with chat, run, and resume. Use "neo agents" to list.

    neo --agent=assistant
    neo run --agent=reviewer "review the current diff"

OPT-IN TOOLS:
  --codemode=codemode   Default. Act by writing code: one script tool with
                        run(), a console, and one interpreter per session.
                        agent, goal and workflow stay available.
  --codemode=classic    Keep the separate bash, read_file, write_file,
                        edit_file, grep and glob tools. Read once at startup.

  --script           Register the JavaScript script tool (default: on).
  --no-script        Leave it unregistered, overriding features.script.
                     In chat, /codemode on|off|status changes it and writes the
                     choice to features.script.

HEADLESS RUN:
  neo run --json --timeout 10m "Review this repo without changing files"
  cat prompt.md | neo run --json
  git diff | neo run "Review this diff" -

  Options: --timeout <duration>, --json
  stdin is the prompt when no prompt argument is given; pass "-" to prepend
  stdin to a prompt argument.`

func printUsage(out io.Writer) {
	fmt.Fprintln(out, usageText)
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "neo version %s\n", Version)
}

func newRegistry(cwd, root string, extra ...tools.Tool) *tools.Registry {
	base := []tools.Tool{
		tools.Bash{Timeout: tools.DefaultBashTimeout, CWD: cwd},
		tools.Grep{Root: root},
		tools.Glob{Root: root},
	}
	return tools.NewRegistry(append(append(base, tools.NewFileTools()...), extra...)...)
}

// registryFor is the composition root's single decision about the tool surface.
//
// Code mode is the default: the model gets one code tool and writes what it
// would otherwise express as a sequence of separate calls, with run() for
// programs, a console, and one interpreter per session. The coordinating tools
// (agent, goal, workflow) are passed in by the caller and stay either way: a
// session that writes code still delegates, tracks a goal, and shows a checklist.
//
// Classic mode keeps the per-action tools for a session that wants them. The
// script tool is still there when features.script is on, but nothing depends on
// it, and bash names it from the place the model reads before choosing a tool.
func registryFor(cfg *config.Config, cwd, root string, extra ...tools.Tool) *tools.Registry {
	scripted := cfg == nil || cfg.ScriptEnabled()

	if cfg == nil || !cfg.ClassicCodeMode() {
		// Absolute paths are what make grep's output usable: inside one code call
		// there is no cd, so a relative path from grep would not resolve.
		codeFiles := append([]tools.Tool{
			tools.Grep{Root: root, Absolute: true},
			tools.Glob{Root: root, Absolute: true},
		}, tools.NewFileTools()...)
		code := tools.NewScript(codeFiles)
		code.CodeMode = true
		code.CWD = cwd
		code.Session = &tools.ScriptSession{}
		base := []tools.Tool{code}
		if !scripted {
			// features.script: false in code mode leaves only the coordinating
			// tools: a session that delegates but acts on nothing itself. The
			// result is a usable session only when extra is non-empty, and two
			// callers depend on that differently:
			//
			//   - chatExtraTools always returns workflow and goal, so the
			//     interactive surface is never empty. TestInteractiveSurfaceIs
			//     NeverEmpty pins that across both surfaces and both script
			//     settings.
			//   - headless passes no extra tools, so headlessAgent refuses the
			//     combination rather than let the run degrade into prose advice.
			base = nil
		}
		return tools.NewRegistry(append(base, extra...)...)
	}

	files := append([]tools.Tool{
		tools.Grep{Root: root},
		tools.Glob{Root: root},
	}, tools.NewFileTools()...)
	base := append([]tools.Tool{
		tools.Bash{Timeout: tools.DefaultBashTimeout, CWD: cwd, ScriptAvailable: scripted},
	}, files...)
	if scripted {
		base = append(base, tools.NewScript(files))
	}
	return tools.NewRegistry(append(base, extra...)...)
}

// chatSystem builds the chat agent's system prompt as ordered blocks: a stable,
// cacheable base (the static instructions plus the skill catalog) followed by
// uncached dynamic session context blocks. Splitting it this way lets prompt
// caching reuse the base across turns and sessions while the project tail
// varies. Discovery errors are non-fatal, warning and falling back to the blocks
// built so far rather than failing to start.
func chatSystem(cfg *config.Config, cwd string, sk []skills.Skill, agentProfile profile.Profile, reg *tools.Registry, errOut io.Writer) (string, []llm.SystemBlock) {
	// Base block: static instructions plus the skill catalog. Stable within a session
	// and largely reused across them, so it's the cache breakpoint.
	//
	// An agent profile replaces the instructions outright rather than appending
	// to them: a personal assistant should not be carrying "run tests after you
	// change code". Everything after this block composes unchanged.
	instructions := chatSystemPrompt
	if codeModeScript(reg) {
		instructions = strings.TrimSuffix(chatSystemPrompt, chatPromptTail) + chatPromptTailCodeMode
	} else if hasScript(reg) {
		instructions = strings.TrimSuffix(chatSystemPrompt, chatPromptTail) + chatPromptTailScripted
	}
	if agentProfile.Body != "" {
		instructions = agentProfile.Body
	}
	instructions += capabilitySections(reg)
	base := skills.Augment(instructions, sk)
	cache := cfg.PromptCachingEnabled()
	blocks := []llm.SystemBlock{{Text: base, Cache: cache}}
	// Dynamic tail: everything below is kept uncached and after the breakpoint
	// so it never evicts the cached base.
	if section := environmentSection(cwd, time.Now()); section != "" {
		blocks = append(blocks, llm.SystemBlock{Text: section})
	}
	if cfg.AgentsFileEnabled() && cwd != "" {
		docs, err := projectctx.Load(cwd)
		if err != nil {
			fmt.Fprintf(errOut, "warning: AGENTS.md: %v\n", err)
		}
		if section := projectctx.Augment("", docs); section != "" {
			blocks = append(blocks, llm.SystemBlock{Text: section})
		}
	}
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String(), blocks
}

// environmentSection states where the agent is running. Without it the model
// has to spend a bash call on pwd and has no idea what today's date is. These
// facts are fixed for the session, so a branch name is deliberately absent: it
// changes underneath us and the model can ask git when it matters.
//
// The date is captured once, when the session starts, so a chat left open
// across midnight reports the previous day. Refreshing it would mean rebuilding
// the system blocks every turn, which is more machinery than the failure is
// worth; resuming a session rebuilds this section.
func environmentSection(cwd string, now time.Time) string {
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Environment\n\n")
	fmt.Fprintf(&b, "Working directory: %s\n", promptValue(cwd))
	if root := workspace.Root(cwd); root != cwd {
		fmt.Fprintf(&b, "Repository root: %s\n", promptValue(root))
	}
	fmt.Fprintf(&b, "Platform: %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "Today's date: %s\n", now.Format("2006-01-02"))
	return b.String()
}

// promptValue keeps a discovered value on one line. A path may legally contain
// a newline, and pasting one verbatim would let a directory name introduce its
// own headings into the system prompt.
func promptValue(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// capabilitySections returns the instructions for tools that are actually
// registered. A nil registry contributes nothing, which keeps the prompt honest
// for callers that build one later.
func capabilitySections(reg *tools.Registry) string {
	if reg == nil {
		return ""
	}
	var b strings.Builder
	if _, ok := reg.Get("workflow"); ok {
		b.WriteString(workflowCapability)
	}
	if _, ok := reg.Get("agent"); ok {
		b.WriteString(subagentCapability)
	}
	if _, ok := reg.Get("goal"); ok {
		b.WriteString(goalCapability)
	}
	if codeModeScript(reg) {
		b.WriteString(codeModeCapability)
	} else if hasScript(reg) {
		b.WriteString(scriptCapability)
	}
	return b.String()
}

// codeModeScript reports whether the session acts by writing code. It is a
// different question from hasScript: both shapes register a script tool, and the
// prompt sections cannot be shared because each names tools the other lacks.
func codeModeScript(reg *tools.Registry) bool {
	if reg == nil {
		return false
	}
	t, ok := reg.Get("script")
	if !ok {
		return false
	}
	s, ok := t.(tools.Script)
	return ok && s.CodeMode
}

// codeModeCapability replaces scriptCapability when code is how the session acts. The two cannot
// share text: this one must not offer a top-level tool as the alternative to
// wrapping work in a script, because there is none, and it must say that run()
// and persistent variables exist, which the default section has no reason to.
const codeModeCapability = `

# Acting by code

Every file and command action goes through script: reading a file, searching,
editing, and starting a program with run(). There is no top-level bash,
read_file, or edit_file to fall back on. The other tools here — agent, goal,
workflow — coordinate the work rather than perform it.

Read, decide, and write inside the same call whenever the decision comes from
what you read. One call that greps, branches on the match, and edits the file
costs one round trip; splitting it across calls costs one turn per step.

Each call has its own scope, so a const you reuse across calls is fine. Assign
to globalThis (globalThis.cwd = ...) for state a later call needs, and note that
it does not survive /clear, resuming, or a restart: keep what matters in files.

` + "```js" + `
const hits = tools.grep({pattern: "placeholder"}).split("\n").filter(Boolean);
if (hits.length === 1) {
  const [path, line] = hits[0].split(":");
  const text = tools.read_file({path});
  tools.edit_file({path, old_string: "placeholder", new_string: "final"});
  "edited " + path + " line " + line + " of " + text.split("\n").length;
} else {
  hits.length + " matches, not editing";
}
` + "```" + `

When edit_file reports that old_string matched more than once, cut a unique window
from what read_file already returned instead of reading the file again:
` + "`const raw = tools.read_file({path}).split(\"\\n\").find((l) => l.includes(\"marker\")).replace(/^\\s*\\d+\\t/, \"\");`" + `
then pass that raw line as old_string.
` + "`grep`" + ` says "no matches" instead of returning an empty string. run(["go", "test", "./..."]) returns {stdout, stderr, code}, and a non-zero code is data you can branch on.`

// hasScript answers whether the session can write scripts. Both the capability
// section and the tie-break in chatPromptTail hang off it, so they cannot
// disagree about whether a script is available.
func hasScript(reg *tools.Registry) bool {
	if reg == nil {
		return false
	}
	_, ok := reg.Get("script")
	return ok
}

// scriptOverride carries --script / --no-script from argument parsing into
// loadConfig. A package-level value keeps the flag out of five call signatures
// that have no other reason to know about it; it is written once at startup.
var scriptOverride *bool

func loadConfig(errOut io.Writer) (*config.Config, bool) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(errOut, "config: %v\n", err)
		return nil, false
	}
	if scriptOverride != nil {
		cfg.SetScript(*scriptOverride)
	}
	if codeModeOverride != "" {
		cfg.Features.CodeMode = codeModeOverride
	}
	return cfg, true
}

func runChat(ctx context.Context, agentName string, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	return runChatSession(ctx, store, nil, agentName, streams)
}

type headlessOptions struct {
	timeout time.Duration
	json    bool
}

type headlessResult struct {
	OK         bool      `json:"ok"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Requests   int       `json:"requests"`
	Usage      llm.Usage `json:"usage"`
	ToolCalls  int       `json:"tool_calls"`
	ToolErrors int       `json:"tool_errors"`
	Final      string    `json:"final,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func runHeadless(ctx context.Context, args []string, agentName string, streams stdio) int {
	opts, prompt, err := parseHeadlessArgs(args, streams.in)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		fmt.Fprintln(streams.err, "usage: neo run [--json] [--timeout 10m] <prompt> [-]")
		return 2
	}
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}
	started := time.Now()
	cfg, ok := loadConfig(streams.err)
	if !ok {
		return 1
	}
	providerName, model := cfg.Provider, cfg.Model
	prov, err := checkedProvider(ctx, cfg, providerName)
	if err != nil {
		finishHeadless(opts, headlessResult{OK: false, ElapsedMS: time.Since(started).Milliseconds(), Provider: providerName, Model: model, Error: err.Error()}, streams)
		return 1
	}
	// Count requests at the provider boundary, so both agent turns and the
	// compactor's summary call are included. The agent's own events cannot
	// answer this: EventDone fires once per finished turn, not per request.
	counted := &countingProvider{inner: prov}
	// A custom backend may report the model's context window in its catalogue.
	// A headless run is a single batch, so waiting for the answer here is what
	// makes a discovered window useful at all; the wait is bounded by
	// discoveryTimeout and ignored if the session's own deadline is shorter.
	waitContextWindowDiscovery(requestContextWindowDiscovery(cfg, providerName, model, nil), ctx)
	ag, model, ok := headlessAgent(ctx, cfg, counted, model, agentName, streams)
	if !ok {
		return 1
	}
	stats := &headlessStats{}
	ag.SetEventHandler(stats.observe)
	out, err := ag.Send(ctx, prompt)
	result := headlessResult{
		OK:        err == nil,
		ElapsedMS: time.Since(started).Milliseconds(),
		Provider:  prov.Name(),
		Model:     model,
		Requests:  counted.calls,
		// Usage() sums every provider response, including the compaction call,
		// which never surfaces as an event. Counting events instead would
		// under-report a run that compacted.
		Usage:      ag.Usage(),
		ToolCalls:  stats.calls,
		ToolErrors: stats.errors,
		Final:      out,
	}
	if err != nil {
		result.Error = err.Error()
	}
	finishHeadless(opts, result, streams)
	if err != nil {
		return 1
	}
	return 0
}

// headlessStats counts the tool activity a run reports in its summary.
type headlessStats struct {
	calls  int
	errors int
}

// observe is the agent's event hook during a headless run.
func (s *headlessStats) observe(e agent.Event) {
	switch e.Kind {
	case agent.EventToolCall:
		s.calls++
	case agent.EventToolResult:
		if e.IsError {
			s.errors++
		}
	}
}

// countingProvider counts the calls that reach a provider, so a headless run
// can report how many requests it cost. The agent's events cannot answer that:
// EventDone fires once per finished turn, not once per request, and a call the
// compactor makes never emits an event at all. Wrapping the provider at the CLI
// boundary keeps the count accurate without teaching the agent loop to count.
type countingProvider struct {
	inner llm.Provider
	calls int
}

func (p *countingProvider) Name() string { return p.inner.Name() }

func (p *countingProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	p.calls++
	return p.inner.Complete(ctx, req)
}

// headlessAgent builds the agent for one prompt. It shares the chat registry
// but advertises no skills: /name and $name invocation are interactive, so the
// catalog would only spend prompt tokens.
//
// It refuses to start a session with no tools. In code mode with
// features.script off, registryFor leaves only the coordinating tools, and
// headless passes none, so the request would advertise nothing: the run would
// answer normally and change no file, with no signal to the caller. Failing
// here names both ways out instead.
func headlessAgent(ctx context.Context, cfg *config.Config, prov llm.Provider, model, agentName string, streams stdio) (*agent.Agent, string, bool) {
	cwd, _ := os.Getwd()
	root := workspace.Root(cwd)
	agentProfile, ok := loadProfile(cwd, agentName, streams.err)
	if !ok {
		return nil, "", false
	}
	reg := registryFor(cfg, cwd, root)
	if len(reg.Names()) == 0 {
		fmt.Fprintf(streams.err, "neo run: features.script is false in %s mode, which leaves this session with no tools: set features.script: true, or use features.codemode: classic for bash and the file tools\n", surfaceName(cfg))
		return nil, "", false
	}
	system, systemBlocks := chatSystem(cfg, cwd, nil, agentProfile, reg, streams.err)
	return agent.New(agent.Config{
		Model:        model,
		System:       system,
		SystemBlocks: systemBlocks,
		Provider:     prov,
		Tools:        reg,
		Compactor:    chatCompactor(prov, model, cfg),
	}), model, true
}

// surfaceName names the surface in a message the user has to act on. It reuses
// the config value rather than inventing a second vocabulary for it.
func surfaceName(cfg *config.Config) string {
	if cfg.ClassicCodeMode() {
		return config.CodeModeClassic
	}
	return config.CodeModeCodemode
}

func parseHeadlessArgs(args []string, stdin io.Reader) (headlessOptions, string, error) {
	opts := headlessOptions{timeout: 10 * time.Minute}
	for _, arg := range args {
		if arg == "--permission" || arg == "-permission" ||
			strings.HasPrefix(arg, "--permission=") || strings.HasPrefix(arg, "-permission=") {
			return opts, "", fmt.Errorf("--permission has been removed; run Neo inside a sandbox and use tool_approvals for optional interactive confirmations")
		}
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "maximum wall-clock duration")
	fs.BoolVar(&opts.json, "json", false, "print a JSON summary instead of plain text")
	if err := fs.Parse(args); err != nil {
		return opts, "", err
	}
	// stdin is the prompt when no prompt argument is given, and is prepended to
	// the arguments when one of them is "-". It is never read otherwise: a
	// harness that keeps an idle pipe open on stdin would block io.ReadAll
	// forever, and nothing about `neo run "prompt"` asks for stdin.
	parts, wantStdin := takeStdinMarker(fs.Args())
	if (wantStdin || len(parts) == 0) && stdin != nil && !isCharacterDevice(stdin) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return opts, "", fmt.Errorf("read stdin: %w", err)
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			parts = append([]string{s}, parts...)
		}
	}
	prompt := strings.TrimSpace(strings.Join(parts, " "))
	if prompt == "" {
		return opts, "", fmt.Errorf("neo run: missing prompt")
	}
	return opts, prompt, nil
}

// takeStdinMarker removes a literal "-" argument and reports whether one was
// present, so `cat diff | neo run "review this" -` combines both.
func takeStdinMarker(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == "-" {
			found = true
			continue
		}
		out = append(out, arg)
	}
	return out, found
}

func isCharacterDevice(in io.Reader) bool {
	input, ok := in.(interface {
		Stat() (os.FileInfo, error)
	})
	if !ok {
		return false
	}
	info, err := input.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func finishHeadless(opts headlessOptions, result headlessResult, streams stdio) {
	if opts.json {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(streams.err, "encode result: %v\n", err)
			return
		}
		fmt.Fprintln(streams.out, string(b))
		return
	}
	if result.Final != "" {
		fmt.Fprintln(streams.out, result.Final)
	}
	if result.Error != "" {
		fmt.Fprintln(streams.err, result.Error)
	}
}

func resumeSession(ctx context.Context, id string, agentName string, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	sess, err := store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			fmt.Fprintf(streams.err, "session not found: %s\n", id)
		} else {
			fmt.Fprintf(streams.err, "load session: %v\n", err)
		}
		return 1
	}
	restoreSessionCWD(sess.Metadata.CWD, streams.err)
	// A resumed session keeps the agent it was started with unless the user
	// names a different one; reverting to the coding prompt mid-conversation
	// would change who the agent is.
	if agentName == "" {
		agentName = sess.Metadata.Agent
	}
	return runChatSession(ctx, store, sess, agentName, streams)
}

func restoreSessionCWD(cwd string, errOut io.Writer) {
	if cwd == "" {
		return
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(errOut, "warning: session cwd %s is unavailable; using current directory\n", cwd)
		return
	}
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(errOut, "warning: session cwd %s: %v; using current directory\n", cwd, err)
	}
}

// runChatSession runs the interactive TUI, re-entering it when the user
// switches sessions with /sessions. agentName is the explicit --agent flag (it
// may be empty); a resumed session falls back to its recorded agent, so
// switching sessions keeps the same rule `neo resume` uses.
func runChatSession(ctx context.Context, store *session.Store, sess *session.Session, agentName string, streams stdio) int {
	for {
		sessionAgent := agentName
		if sessionAgent == "" && sess != nil {
			sessionAgent = sess.Metadata.Agent
		}
		next, ok := runOneChatSession(ctx, store, sess, sessionAgent, streams)
		if !ok {
			return 1
		}
		if next == nil {
			return 0
		}
		// Switching sessions reuses the resume path: the target cwd applies
		// before its tools are built, and an unavailable one only warns.
		restoreSessionCWD(next.Metadata.CWD, streams.err)
		sess = next
	}
}

// runOneChatSession prepares and runs one TUI pass. It returns the session the
// user asked to switch to, or nil when the program ended normally. The target
// is loaded while the TUI is still open, so an unreadable session is reported
// in the browser and the running conversation is left untouched.
func runOneChatSession(ctx context.Context, store *session.Store, sess *session.Session, agentName string, streams stdio) (*session.Session, bool) {
	setup, ok := prepareChat(ctx, store, sess, agentName, streams)
	if !ok {
		return nil, false
	}
	rt := setup.runtime
	saveSession := func() error {
		return saveChatSession(ctx, store, setup.sess, rt.ag, rt.goal, setup.cwd, setup.profile.Name)
	}
	var next *session.Session
	target, err := tui.Run(ctx, rt.ag, rt.model, Version, setup.cwd, setup.sk,
		tui.WithAfterSend(saveSession),
		tui.WithGoalManager(rt.goal),
		tui.WithGoalPersist(saveSession),
		tui.WithModelSwitcher(rt.providerName, modelChoices(setup.cfg, rt.providerName), rt.switchModel),
		tui.WithModelChoicesLoader(modelChoicesLoader(setup.cfg, rt.providerName)),
		tui.WithProviderSwitcher(providerChoices(setup.cfg), func(name string) (tui.ProviderState, error) {
			return rt.switchProvider(ctx, name)
		}),
		tui.WithSessions(setup.sess.Metadata.ID,
			func(ctx context.Context) ([]tui.SessionChoice, error) {
				items, err := store.List(ctx)
				if err != nil {
					return nil, err
				}
				return sessionChoices(items), nil
			},
			func(id string) error {
				loaded, err := store.Load(ctx, id)
				if err != nil {
					return describeSessionLoadError(id, err)
				}
				next = loaded
				return nil
			},
		),
		tui.WithScriptSwitcher(rt.switchCodeMode, setup.cfg.ScriptEnabled),
		tui.WithWorkflowEvents(setup.workflowEvents),
		tui.WithVerbose(setup.cfg.VerboseEnabled()),
		tui.WithIO(streams.in, streams.out),
	)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		return nil, false
	}
	if target == "" {
		return nil, true
	}
	if next == nil {
		fmt.Fprintf(streams.err, "session not found: %s\n", target)
		return nil, true
	}
	return next, true
}

// sessionChoices maps saved session metadata into the /sessions browser rows.
// The columns mirror `neo sessions`: id, then the title with its model, cwd,
// and update time in the description.
func sessionChoices(items []session.Metadata) []tui.SessionChoice {
	choices := make([]tui.SessionChoice, 0, len(items))
	for _, meta := range items {
		var detail []string
		if meta.Model != "" {
			detail = append(detail, meta.Model)
		}
		if meta.CWD != "" {
			detail = append(detail, shortPath(meta.CWD))
		}
		if !meta.UpdatedAt.IsZero() {
			detail = append(detail, meta.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		choices = append(choices, tui.SessionChoice{
			ID:          meta.ID,
			Title:       meta.Title,
			Description: strings.Join(detail, " · "),
		})
	}
	return choices
}

// describeSessionLoadError keeps the wording of `neo resume` for a missing
// session while adding the context that the switch did not happen.
func describeSessionLoadError(id string, err error) error {
	if errors.Is(err, session.ErrNotFound) {
		return fmt.Errorf("session not found: %s", id)
	}
	return fmt.Errorf("load session %s: %w", id, err)
}

// chatSetup is everything an interactive session resolved before the TUI
// starts. Each field is produced by one step of prepareChat, so a failure there
// names the step that failed.
type chatSetup struct {
	cfg            *config.Config
	sess           *session.Session
	cwd            string
	profile        profile.Profile
	sk             []skills.Skill
	runtime        *chatRuntime
	workflowEvents <-chan workflow.Event
}

// chatRuntime is the state that keeps changing while a session runs: the active
// backend, the model remembered per provider, and the agent those backends are
// swapped into.
type chatRuntime struct {
	cfg  *config.Config
	ag   *agent.Agent
	goal *goal.Manager
	// runner is nil when the working directory could not be read, which also
	// disables the agent tool.
	runner                   *subagent.AgentRunner
	runnerFollowsCoordinator bool

	provider       llm.Provider
	providerName   string
	model          string
	providerModels map[string]string

	// The pieces /codemode needs to rebuild the tool list and prompt without
	// discarding the transcript: the registry is composed from cwd, root, and
	// the extra tools the session added.
	cwd     string
	root    string
	extra   []tools.Tool
	profile profile.Profile
	sk      []skills.Skill
}

// prepareChat resolves config, backend, session, tools, and system prompt in the
// order each depends on the last.
func prepareChat(ctx context.Context, store *session.Store, sess *session.Session, agentName string, streams stdio) (*chatSetup, bool) {
	cfg, ok := loadConfig(streams.err)
	if !ok {
		return nil, false
	}
	providerName, model, prov, ok := resolveChatProvider(ctx, cfg, sess, streams)
	if !ok {
		return nil, false
	}
	cwd, _ := os.Getwd() // "" on failure → cwd-dependent capabilities are skipped
	root := workspace.Root(cwd)
	agentProfile, ok := loadProfile(cwd, agentName, streams.err)
	if !ok {
		return nil, false
	}
	sess, ok = ensureChatSession(ctx, store, sess, chatSessionMeta{
		cwd:        cwd,
		agent:      agentProfile.Name,
		model:      model,
		providerID: sessionProviderID(prov.Name()),
		openAIAuth: adapterOpenAIAuth(prov.Name()),
	}, streams)
	if !ok {
		return nil, false
	}

	rt, workflowEvents := buildChatRuntime(ctx, cfg, prov, providerName, model, sess, agentProfile, cwd, root, streams)
	return &chatSetup{
		cfg:            cfg,
		sess:           sess,
		cwd:            cwd,
		profile:        agentProfile,
		sk:             rt.sk,
		runtime:        rt.runtime,
		workflowEvents: workflowEvents,
	}, true
}

// buildChatRuntime assembles the tools, the system prompt, and the agent they
// run in, plus the event channel the TUI reads workflow progress from.
func buildChatRuntime(ctx context.Context, cfg *config.Config, prov llm.Provider, providerName, model string, sess *session.Session, agentProfile profile.Profile, cwd, root string, streams stdio) (chatRuntimeSetup, <-chan workflow.Event) {
	// The goal tool is always registered, even with no goal in the session, so
	// the model's tool list never changes mid-conversation. Its manager owns the
	// session goal; the tool cannot pause, resume, clear, or spend the budget.
	goalManager, goalWarning := chatGoalManager(sess.Goal, cfg)
	if goalWarning != "" {
		fmt.Fprintln(streams.err, goalWarning)
	}
	workflowEvents, extra, agentRunner, followsCoordinator := chatExtraTools(ctx, cfg, prov, model, cwd, root, goalManager)

	reg := registryFor(cfg, cwd, root, extra...)
	// Skills are loaded once: the catalog is advertised in the system prompt
	// (via chatSystem), and the same slice drives $name and /name expansion in
	// the TUI.
	sk := loadSkills(cfg, cwd, streams.err)
	system, systemBlocks := chatSystem(cfg, cwd, sk, agentProfile, reg, streams.err)

	var requiresApproval func(string, map[string]any) bool
	if len(cfg.ToolApprovals) > 0 {
		requiresApproval = approval.New(cfg.ToolApprovals).Requires
	}
	ag := agent.New(agent.Config{
		Model:            model,
		System:           system,
		SystemBlocks:     systemBlocks,
		Provider:         prov,
		Tools:            reg,
		Compactor:        chatCompactor(prov, model, cfg),
		RequiresApproval: requiresApproval,
		Messages:         sess.Messages,
		Usage:            sess.Usage,
	})
	// A custom backend can report the window in its catalogue. The fetch runs in
	// the background so startup never waits on the network; when it answers, the
	// summarizer is rebuilt so the trigger follows the window the server holds
	// rather than the one the config guessed. The refresh re-reads the agent's
	// model, so a /model switch that landed meanwhile is not overwritten.
	requestContextWindowDiscovery(cfg, providerName, model, func() {
		// Only the backend discovery ran for: if the session moved on, that
		// switch already resolved its own window and this must not undo it.
		name, current := ag.Backend()
		if name != providerName || current == "" {
			return
		}
		_ = ag.SetBackend(prov, current, chatCompactor(prov, current, cfg))
	})
	return chatRuntimeSetup{
		sk: sk,
		runtime: &chatRuntime{
			cfg:                      cfg,
			ag:                       ag,
			goal:                     goalManager,
			runner:                   agentRunner,
			runnerFollowsCoordinator: followsCoordinator,
			provider:                 prov,
			providerName:             providerName,
			model:                    model,
			providerModels:           map[string]string{providerName: model},
			cwd:                      cwd,
			root:                     root,
			extra:                    extra,
			profile:                  agentProfile,
			sk:                       sk,
		},
	}, workflowEvents
}

// chatRuntimeSetup pairs the runtime with the skills its system prompt
// advertised, since the TUI expands the same slice.
type chatRuntimeSetup struct {
	sk      []skills.Skill
	runtime *chatRuntime
}

// resolveChatProvider builds the backend a session was recorded with, falling
// back to the configured default when only that one can be built.
func resolveChatProvider(ctx context.Context, cfg *config.Config, sess *session.Session, streams stdio) (string, string, llm.Provider, bool) {
	providerName, model := sessionBackend(cfg, sessionMetadata(sess), streams.err)
	prov, err := chatSessionProvider(ctx, cfg, sess, providerName)
	if err == nil {
		return providerName, model, prov, true
	}
	if providerName == cfg.Provider {
		fmt.Fprintln(streams.err, err)
		return "", "", nil, false
	}
	fmt.Fprintf(streams.err, "warning: cannot resume with %s (%v); continuing with %s model %s\n", providerName, err, cfg.Provider, cfg.Model)
	prov, err = newProvider(cfg, cfg.Provider)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		return "", "", nil, false
	}
	return cfg.Provider, cfg.Model, prov, true
}

// chatSessionMeta is the metadata a fresh session is created with.
type chatSessionMeta struct {
	cwd        string
	agent      string
	model      string
	providerID string
	openAIAuth string
}

// ensureChatSession creates the session when the caller has none, and returns
// the one to use either way. The session exists before the goal manager is
// built: the goal belongs to the session, so resuming restores it from the
// record just loaded.
func ensureChatSession(ctx context.Context, store *session.Store, sess *session.Session, meta chatSessionMeta, streams stdio) (*session.Session, bool) {
	if sess != nil {
		return sess, true
	}
	created, err := store.Create(ctx, session.Metadata{
		CWD:        meta.cwd,
		Agent:      meta.agent,
		Model:      meta.model,
		Provider:   meta.providerID,
		OpenAIAuth: meta.openAIAuth,
	})
	if err != nil {
		fmt.Fprintf(streams.err, "create session: %v\n", err)
		return nil, false
	}
	return created, true
}

// chatExtraTools registers the tools that are not part of the built-in
// registry: workflow events, the goal tool, and — when the working directory
// is readable — the agent tool.
//
// The chat agent is the primary coordinator. It gets the agent tool (as caller
// node 0) so it can spawn amnesiac subagents with self-contained prompts
// directly from the conversation. Sequencing is the agent's judgment, not a
// stored workflow artifact.
func chatExtraTools(ctx context.Context, cfg *config.Config, prov llm.Provider, model, cwd, root string, goalManager *goal.Manager) (<-chan workflow.Event, []tools.Tool, *subagent.AgentRunner, bool) {
	wf := make(chan workflow.Event, 128)
	extra := []tools.Tool{workflow.Tool{Events: wf}, goal.Tool{Manager: goalManager}}
	var agentRunner *subagent.AgentRunner
	followsCoordinator := false
	if cwd != "" {
		workerProvider, workerModel, follows := subagentBackend(ctx, cfg, prov, model)
		followsCoordinator = follows
		var at subagent.AgentTool
		at, _, agentRunner = chatAgentTool(workerProvider, workerModel, cwd, root, cfg)
		extra = append(extra, at)
	}
	return wf, extra, agentRunner, followsCoordinator
}

// switchModel changes the model and persists the choice beside the backend it
// was made against, so the active provider cannot drift from the written model.

// switchCodeMode turns the script tool on or off for the running chat. It
// rebuilds the registry and the system prompt from the same inputs the session
// was created with, swaps both into the agent, and only then persists the
// choice — so a failed write leaves the session as it was.
//
// The transcript is untouched: switching tools must not look like starting over.
func (r *chatRuntime) switchCodeMode(on bool) error {
	if r.ag == nil {
		return fmt.Errorf("codemode is unavailable without an agent")
	}
	previous := r.cfg.ScriptEnabled()
	if previous == on {
		return nil
	}

	r.cfg.SetScript(on)
	reg := registryFor(r.cfg, r.cwd, r.root, r.extra...)
	system, blocks := chatSystem(r.cfg, r.cwd, r.sk, r.profile, reg, io.Discard)
	if err := r.ag.SetTools(reg); err != nil {
		r.cfg.SetScript(previous)
		return err
	}
	r.ag.SetPrompt(system, blocks)
	// Children build their tool list per run from the runner, so the switch has
	// to reach it too; otherwise a session that says "codemode on" would spawn
	// subagents that cannot use the tool.
	r.setRunnerScript(on)

	if _, err := r.cfg.WriteFeature("script", on); err != nil {
		// Put the old capability back before reporting, or the session would
		// claim a setting the next start would not reproduce.
		r.cfg.SetScript(previous)
		reg = registryFor(r.cfg, r.cwd, r.root, r.extra...)
		system, blocks = chatSystem(r.cfg, r.cwd, r.sk, r.profile, reg, io.Discard)
		_ = r.ag.SetTools(reg)
		r.ag.SetPrompt(system, blocks)
		r.setRunnerScript(previous)
		return err
	}
	return nil
}

// setRunnerScript keeps the subagent runner's copy of the setting in step with
// the coordinator's. A nil runner means the session has no agent tool, which is
// already the case in which no child exists to disagree.
func (r *chatRuntime) setRunnerScript(on bool) {
	if r.runner != nil {
		r.runner.Script = on
	}
}

func (r *chatRuntime) switchModel(nextModel string) error {
	prevModel := r.providerModels[r.providerName]
	if prevModel == "" {
		_, prevModel = r.ag.Backend()
	}
	if err := setChatModel(r.provider, r.cfg, r.ag, r.runner, r.runnerFollowsCoordinator, nextModel); err != nil {
		return err
	}
	if _, err := r.cfg.WriteBackend(r.providerName, nextModel); err != nil {
		restoreChatModel(r.provider, r.cfg, r.ag, r.runner, r.runnerFollowsCoordinator, prevModel)
		return err
	}
	r.providerModels[r.providerName] = nextModel
	r.cfg.Model = nextModel
	r.model = nextModel
	return nil
}

// switchProvider builds the named backend, swaps it in, and persists it. Every
// failure leaves the running session and the config file untouched.
func (r *chatRuntime) switchProvider(ctx context.Context, name string) (tui.ProviderState, error) {
	if !r.cfg.HasProvider(name) {
		return tui.ProviderState{}, fmt.Errorf("unknown provider %q; available: %s",
			name, strings.Join(r.cfg.ProviderNames(), ", "))
	}
	// Build the backend first: a missing key or subscription login fails here,
	// before the running chat or the config file change.
	next, err := checkedProvider(ctx, r.cfg, name)
	if err != nil {
		return tui.ProviderState{}, err
	}
	nextModel := r.providerModels[name]
	if nextModel == "" {
		nextModel = r.cfg.DefaultModelFor(name)
	}
	if nextModel == "" {
		return tui.ProviderState{}, fmt.Errorf("provider %s has no default model; set default_model for it in neo.yaml", name)
	}
	// Apply the backend, then persist. A failed write restores the previous
	// backend so a failed switch leaves nothing half-changed.
	prevProvider, prevModel := r.provider, r.providerModels[r.providerName]
	if prevModel == "" {
		prevModel = r.model
	}
	if err := switchChatBackend(r.cfg, r.ag, r.runner, r.runnerFollowsCoordinator, next, name, nextModel, prevProvider, prevModel); err != nil {
		return tui.ProviderState{}, err
	}
	r.provider, r.providerName = next, name
	r.model = nextModel
	r.providerModels[name] = nextModel
	r.cfg.Provider, r.cfg.Model = name, nextModel
	return tui.ProviderState{
		Provider: name,
		Model:    nextModel,
		Choices:  modelChoices(r.cfg, name),
		Loader:   modelChoicesLoader(r.cfg, name),
	}, nil
}

func saveChatSession(ctx context.Context, store *session.Store, sess *session.Session, ag *agent.Agent, goals *goal.Manager, cwd, agentName string) error {
	activeProvider, activeModel := ag.Backend()
	sess.Messages = ag.Transcript()
	sess.Usage = ag.Usage()
	// Only a caller holding a manager may change the saved goal. A caller with
	// none (a test double, or a future non-goal path) leaves whatever the
	// session already recorded rather than erasing it.
	if goals != nil {
		sess.Goal = goals.Snapshot()
	}
	sess.Metadata.CWD = cwd
	// Resuming with a different --agent switches the session's agent, so record
	// the one actually in use rather than the one it was created with.
	sess.Metadata.Agent = agentName
	sess.Metadata.Model = activeModel
	sess.Metadata.Provider = sessionProviderID(activeProvider)
	sess.Metadata.OpenAIAuth = adapterOpenAIAuth(activeProvider)
	return store.Save(ctx, sess)
}

func sessionMetadata(sess *session.Session) session.Metadata {
	if sess == nil {
		return session.Metadata{}
	}
	return sess.Metadata
}

// sessionBackend restores a saved backend when its local credential source is
// still configured. Otherwise resume is explicit about falling back to the
// current config rather than applying a model id to the wrong provider.
// loadSkills returns the built-in skills plus any discovered ones when the
// feature is enabled. A discovery error is non-fatal — it warns and falls
// back to the built-ins rather than failing to start.
func loadSkills(cfg *config.Config, cwd string, errOut io.Writer) []skills.Skill {
	if !cfg.SkillsEnabled() || cwd == "" {
		return skills.Defaults()
	}
	sk, err := skills.Load(cwd)
	if err != nil {
		fmt.Fprintf(errOut, "warning: skills: %v\n", err)
		return skills.Defaults()
	}
	return sk
}
