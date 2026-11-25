package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"

	"github.com/owainlewis/neo/internal/llm"
)

// scriptSourceKey is the JS binding the model's source is loaded into, so the
// wrapper can evaluate it without the model's text becoming part of the
// wrapper's own body.
const scriptSourceKey = "__neo_source"

// Defaults for one script. Both are sized so an ordinary script never notices
// them and a runaway one ends quickly: 5 minutes is longer than any read-only
// batch on a large repository, and 64 MiB is four times what a script can put
// into the transcript (MaxOutputBytes).
//
// These are not config knobs. Nothing has measured a case that needs them
// changed, and docs/design/token-budget.md sets the precedent: an unmeasured
// switch is a second behaviour nobody tests.
const (
	DefaultScriptTimeout     = 5 * time.Minute
	DefaultScriptMemoryLimit = 64 << 20
)

// scriptWrapper evaluates the model's source and normalizes its result.
//
// `eval` rather than `new Function` because the two disagree on what the value
// is: a classic script's value is its last expression, while a function body
// returns undefined unless it has an explicit `return`. Verified against the
// engine — `eval("1+41")` is 42 and `(new Function("1+41"))()` is undefined.
//
// The Promise check exists because a classic script may not use top-level
// `await` but can still produce a Promise, and stringifying one yields `{}`.
// Silent empty results are worse than a refusal, so this throws instead.
const scriptWrapper = `(function (source) {
	var value = eval(source);
	if (value !== null && typeof value === "object" && typeof value.then === "function") {
		throw new Error("script returned a Promise; write a synchronous script, async and top-level await are not supported");
	}
	if (value === undefined) {
		return null;
	}
	if (typeof value === "string") {
		return value;
	}
	return JSON.stringify(value);
})(` + scriptSourceKey + `)`

// Script runs model-written JavaScript with a closed set of tools.
//
// The whitelist is built here as a list of values, never as a filtered registry:
// adding a tool has to be an edit to this file. A tool that appears by accident
// is a tool whose approvals and output limits nobody reviewed.
type Script struct {
	// Read, Grep, Glob, Write, and Edit are the whole surface a script has.
	// Each one is an explicit line in globals and every one passes through the
	// approval gate, so a capability cannot appear here by accident.
	//
	// bash is absent on purpose: Neo does not parse shell commands, so it
	// cannot tell a read from a write, and a tool it cannot classify makes the
	// approvals above decorative.
	Read  ReadFile
	Grep  Grep
	Glob  Glob
	Write WriteFile
	Edit  EditFile

	// Timeout bounds one script. Zero means DefaultScriptTimeout. A script that
	// has not finished by then is interrupted, which is the only way to stop a
	// loop the engine cannot otherwise see the end of.
	Timeout time.Duration
	// MemoryLimit bounds what the script's heap may retain, in bytes. Zero means
	// DefaultScriptMemoryLimit. It is the engine's own limit, so a script that
	// allocates past it stops with a typed error rather than being killed by the
	// host after the machine is already under pressure.
	MemoryLimit int64

	// CWD is where run() starts programs. Empty means the process's own
	// working directory, which is what a session usually wants.
	CWD string

	// CodeMode makes this the session's way of acting: globals() also exposes
	// run(), the session keeps one interpreter, and a bare string argument binds
	// to the tool's one required field. It is set once at startup, because
	// changing a session's tool list mid-turn breaks the cached prefix.
	CodeMode bool

	// Session keeps one runtime alive across calls, so a declaration a script
	// made is still there for the next one. Nil means every call starts fresh,
	// which is the default. Only a session that wants state sets it, because a
	// kept runtime also keeps its heap: the memory limit then bounds the sum of
	// everything the session's scripts retained, not one script.
	Session *ScriptSession
}

// ScriptSession holds the interpreter state one session's scripts share. It is
// not persisted: internal/session stores only messages, so resuming a session
// or restarting the process starts from an empty interpreter and the prompt
// says so.
//
// moejs requires one goroutine per runtime, so every use goes through the mutex
// and a script is never concurrent with another.
type ScriptSession struct {
	mu   sync.Mutex
	rt   *moejs.Runtime
	used bool
}

func (Script) Name() string { return "script" }

// ParallelSafe reports false, always. A script runs arbitrary logic over
// whatever the whitelist allows, so running two together would be a concurrency
// decision nobody made. Overriding this needs an isolation argument, not a
// preference.
func (Script) ParallelSafe(map[string]any) bool { return false }

// Spec is the model's only description of what a script can reach, so it lists
// the whitelist that globals actually builds. It said "no write" for as long as
// it took someone to notice a script had been able to write since the approval
// gate landed; the model read the conservative text and routed around the tool.
// TestScriptSpecMatchesGlobals now fails if the two drift apart again.
func (s Script) Spec() llm.ToolSpec {
	if s.CodeMode {
		return s.codeModeSpec()
	}
	return llm.ToolSpec{
		Name: "script",
		Description: `Run JavaScript that calls tools and returns its last expression's value.
Use it when a later step depends on an earlier result: filter, count, or branch over tool output inside the script instead of sending it back and forth.
Inside the script: tools.read_file, tools.grep, tools.glob, tools.write_file, tools.edit_file, with the same arguments as at top level. All return strings; grep returns "path:line:text" lines, glob one path per line.
No bash, no import, no network. Writes pass the same approval a top-level call does.
Synchronous only; top-level await is an error. The last expression is the result: strings as-is, other values JSON.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "JavaScript to run. The last expression is the result.",
				},
			},
			"required": []string{"source"},
		},
	}
}

func (s Script) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultScriptTimeout
}

func (s Script) memoryLimit() int64 {
	if s.MemoryLimit > 0 {
		return s.MemoryLimit
	}
	return DefaultScriptMemoryLimit
}

func (s Script) Run(ctx context.Context, input map[string]any) (string, error) {
	source, err := mustString(input, "source")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("input source must not be empty")
	}
	// A cancelled turn reports cancellation rather than a confusing engine error.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Solo mode runs the model's source as a real script rather than inside
	// eval. Only that makes its declarations global bindings that the next call
	// still sees; inside eval they belong to the eval scope and vanish, which
	// would make the promised persistence a lie. RunScript already returns the
	// script's completion value, so the last expression is still the result.
	if s.CodeMode {
		return s.runCodeMode(ctx, source)
	}

	wrapper, err := moejs.CompileScript("script", scriptWrapper)
	if err != nil {
		// The wrapper is a constant in this file, so this means the engine
		// changed under us, not that the model wrote bad code.
		return "", fmt.Errorf("script wrapper does not compile: %w", err)
	}

	if s.Session != nil {
		return s.runInSession(ctx, wrapper, source)
	}

	rt := moejs.NewRuntime(moejs.Options{MemoryLimit: s.memoryLimit()})
	console := &consoleLog{}
	if err := s.bindGlobals(rt, ctx, console); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	if err := rt.SetGlobal(scriptSourceKey, source); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}

	// One deadline covers both the script's own budget and the caller's
	// cancellation, so an interrupted turn reports its real cause instead of
	// waiting out the script's timeout. AfterFunc is what lets the engine stop:
	// RunScript is a single blocking call and cannot select on ctx itself.
	runCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { rt.Interrupt(context.Cause(runCtx)) })
	defer stop()

	result, err := rt.RunScript(wrapper)
	if err != nil {
		return "", console.describeFailure(err, source)
	}
	out, convErr := scriptResult(rt, result)
	if convErr != nil {
		return "", convErr
	}
	return s.limitOutput(console.join(out))
}

// limitOutput caps what one script contributes to the transcript. The agent
// applies the same cap as a backstop for every tool, but capping here keeps a
// large script result from being built into a string first.
//
// The marker matches the agent's wording so a reader cannot tell which layer
// truncated, and the suggestion is the same one that layer gives.
func (s Script) limitOutput(result string) (string, error) {
	if len(result) <= MaxOutputBytes {
		return result, nil
	}
	originalBytes := len(result)
	originalLines := strings.Count(result, "\n") + 1
	marker := fmt.Sprintf("\n\n[tool output truncated: original %d bytes across %d lines; showing first %d bytes. Re-run the tool with narrower output to inspect the rest.]",
		originalBytes, originalLines, MaxOutputBytes)
	keep := MaxOutputBytes - len(marker)
	if keep <= 0 {
		return trimToBytes(marker, MaxOutputBytes), nil
	}
	return trimToBytes(result, keep) + marker, nil
}

// trimToBytes cuts at a rune boundary so the result stays valid UTF-8.
func trimToBytes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// globals builds the tool namespace. Every entry is an explicit edit here.
func (s Script) globals(ctx context.Context) map[string]any {
	// The counter numbers sub-calls within this one script execution, so the
	// agent can tell two read_file calls apart even when the arguments match.
	calls := &subCallCounter{}
	globals := map[string]any{
		"read_file":  s.hostTool(ctx, s.Read, calls, hostCall{required: "path", path: "path"}),
		"grep":       s.hostTool(ctx, s.Grep, calls, hostCall{required: "pattern"}),
		"glob":       s.hostTool(ctx, s.Glob, calls, hostCall{required: "pattern"}),
		"write_file": s.hostTool(ctx, s.Write, calls, hostCall{required: "path", path: "path"}),
		"edit_file":  s.hostTool(ctx, s.Edit, calls, hostCall{required: "path", path: "path"}),
	}
	if s.CodeMode {
		globals["run"] = s.runTool(ctx, calls)
	}
	return globals
}

// consoleGlobals is the console every script gets, in both modes. The engine has
// none, and every model writing JavaScript reaches for console.log first; its
// absence reads as a broken tool rather than a missing one, and whatever was
// printed is lost. They go on the global object because that is where the model
// writes them, and eval in the default path can only see globals.
func (s Script) consoleGlobals(console *consoleLog) map[string]any {
	if console == nil {
		return nil
	}
	object := console.object()
	return map[string]any{
		"console": object,
		// print is the first verb many models reach for and costs one line; a
		// ReferenceError here is a turn spent on the interpreter rather than the
		// task.
		"print": object["log"],
	}
}

// codeModeExtras are the globals a code-mode session has beyond the tools. The
// console is not here: it belongs to every script, so splitting it out is what
// keeps the two modes from drifting.
func (s Script) codeModeExtras(ctx context.Context) map[string]any {
	globals := s.globals(ctx)
	return map[string]any{"run": globals["run"]}
}

// subCallCounter numbers the calls one script makes. It is not shared and not
// concurrent: a script runs on one goroutine, which is why the tool is serial.
type subCallCounter struct{ n int }

func (c *subCallCounter) next(parent string) string {
	c.n++
	return fmt.Sprintf("%s#%d", parent, c.n)
}

// hostTool adapts one Go tool into a synchronous JS function.
//
// The arguments are converted to Go before the tool runs, so nothing the script
// does to its own object afterwards can change what executes. An error thrown
// here surfaces as a JS exception, which is how a script sees a failed tool.
func (s Script) hostTool(ctx context.Context, t Tool, calls *subCallCounter, call hostCall) moejs.NativeFunc {
	// The parent's tool-use ID is what the agent already knows: reusing
	// CallMetadata means this file needs no second context key and no import of
	// the agent package.
	parent, _ := CallMetadataFrom(ctx)
	report := func(id, name string, input map[string]any, text string, isError, done bool) {
		ReportSubCall(ctx, SubCall{
			ID: id, Name: name, Args: input, Text: text, IsError: isError, Done: done,
		})
	}
	return func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
		input := map[string]any{}
		if len(args) > 0 {
			converted := r.ToGo(args[0])
			switch value := converted.(type) {
			case map[string]any:
				input = value
			case string:
				// A script mixes three call shapes: these tools take an object,
				// run() takes an array, console.log takes positionals. A bare
				// string is the obvious mistake, and measured runs made it in five
				// of five sessions. Binding it to the one required string field is
				// unambiguous: it is what the model meant.
				input[call.required] = value
			default:
				return engine.Undefined(), fmt.Errorf("%s takes an object of named arguments, not %T; write %s({%s: ...})", t.Name(), converted, t.Name(), call.required)
			}
		}
		// Relative paths resolve against the script's working directory, which is
		// where run() starts programs. Without this, "./a.txt" in a tool call meant
		// the process directory while run(["cat", "a.txt"]) meant CWD, so a script
		// had two answers for the same path. Resolved before approval so a rule and
		// a prompt both see the real path, and before the tool runs so the read and
		// the write that follow use one key.
		resolvePath(input, call.path, s.CWD)

		id := calls.next(parent.ToolUseID)
		name := t.Name()
		report(id, name, input, "", false, false)

		// Approval is enforced here, at the tool boundary, not at the script
		// boundary. The outer `script` call is one rule away from every tool the
		// whitelist exposes, so a matcher that only sees "script" cannot express
		// "ask before write_file". The argument is the Go map built above, not
		// the JS object, so a script cannot change what was approved afterwards.
		if err := requireApproval(ctx, name, input, "script"); err != nil {
			report(id, name, input, err.Error(), true, true)
			return engine.Undefined(), err
		}

		out, err := t.Run(ctx, input)
		if err != nil {
			report(id, name, input, err.Error(), true, true)
			return engine.Undefined(), err
		}
		report(id, name, input, out, false, true)
		return engine.StringValue(engine.FromGoString(out)), nil
	}
}

// scriptResult turns the wrapper's value into the tool result. The wrapper
// already stringifies everything except strings and null, so the common case is
// a Go string.
func scriptResult(rt *moejs.Runtime, result engine.Value) (string, error) {
	if result.IsUndefined() || result.IsNullish() {
		return "", nil
	}
	converted, err := rt.ToGo(result)
	if err != nil {
		return "", describeScriptError(err, "")
	}
	if text, ok := converted.(string); ok {
		return text, nil
	}
	if converted == nil {
		return "", nil
	}
	return fmt.Sprintf("%v", converted), nil
}

// describeScriptError names the failure class, because the model reacts
// differently to "your source did not compile" and "your source threw".
//
// A syntax error inside `eval` arrives as a thrown SyntaxError rather than the
// compile-time *SyntaxError, since the source is only parsed when the script
// runs. Both are reported as syntax errors so the model does not go looking for
// a runtime bug it cannot find.
//
// Each class is followed by one sentence saying how to fix it, because naming a
// class is not the same as telling the model what to write next: a run that
// tried to use this tool spent three failed calls discovering the convention
// (docs/performance.md). The hint costs bytes only on the failure path, so the
// fixed floor is untouched. It is added only when the source confirms the cause,
// since a wrong hint sends the model after a bug that is not there.
func describeScriptError(err error, source string) error {
	// A memory limit and a deadline both surface as an interrupt. Telling them
	// apart matters: one means "allocate less", the other "finish sooner", and a
	// model given the wrong one retries the wrong thing.
	var limit *moejs.MemoryLimitError
	if errors.As(err, &limit) {
		return fmt.Errorf("script exceeded its memory limit: %w", err)
	}
	var interrupted *moejs.InterruptedError
	if errors.As(err, &interrupted) {
		// The interrupt's value is already in the message, including whether it
		// was a deadline or a cancellation, so no second wording is needed.
		return fmt.Errorf("script stopped: %w", err)
	}
	var syntax *moejs.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("script has a syntax error: %w%s", err, awaitHint(source))
	}
	var exception *moejs.Exception
	if errors.As(err, &exception) {
		if exception.Name() == "SyntaxError" {
			return fmt.Errorf("script has a syntax error: %w%s", err, awaitHint(source))
		}
		return fmt.Errorf("script threw: %w%s", err, editFileHint(err))
	}
	return fmt.Errorf("script failed: %w", err)
}

// awaitHint recognizes the one syntax failure the model makes for a convention
// reason rather than a typo. The engine reports `await 1` as "Unexpected
// number", which says nothing about the script being synchronous; the source is
// the signal that makes the correct reading available, so it is checked rather
// than guessed from the message.
func awaitHint(source string) string {
	if !strings.Contains(source, "await") {
		return ""
	}
	return "; scripts are synchronous and there is no top-level await, so call the tools directly and use their return value, e.g. `const n = tools.grep({pattern: \"func \"}).split(\"\\n\").filter(Boolean).length; n + \" matches\"`"
}

// editFileHint covers the other convention trap: read_file prefixes every line
// with its number and a tab, while edit_file matches raw text, so feeding one
// result into the other fails with "old_string not found" and reads like a bug
// in the file rather than in the call.
func editFileHint(err error) string {
	if !strings.Contains(err.Error(), "old_string not found") {
		return ""
	}
	return "; if old_string came from read_file, strip the leading line number and tab it adds, since edit_file matches the file's raw text"
}

// NewScript builds the script tool over the file tools the coordinator already
// holds, so a file a script reads is a file the next edit knows about. Passing
// the same values rather than new ones is what keeps the stale-edit guard
// meaningful across the script boundary.
//
// Only the tools named here are reachable from JavaScript. Adding one means
// adding a struct field and a line in globals; there is no registry lookup that
// could grow the surface without an edit.
func NewScript(files []Tool) Script {
	s := Script{}
	for _, t := range files {
		switch v := t.(type) {
		case ReadFile:
			s.Read = v
		case Grep:
			s.Grep = v
		case Glob:
			s.Glob = v
		case WriteFile:
			s.Write = v
		case EditFile:
			s.Edit = v
		}
	}
	return s
}

// runInSession runs one script in the session's kept runtime. The globals are
// rebound on every call because they close over this call's context, and a
// script must read files through the approvals of the turn it is in.
//
// The runtime is created on first use, so a session that never scripts pays
// nothing for the interpreter.
func (s Script) runInSession(ctx context.Context, wrapper *moejs.Script, source string) (string, error) {
	sess := s.Session
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.rt == nil {
		sess.rt = moejs.NewRuntime(moejs.Options{MemoryLimit: s.memoryLimit()})
	}
	rt := sess.rt
	if err := s.bindGlobals(rt, ctx, nil); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	if err := rt.SetGlobal(scriptSourceKey, source); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	// An interrupt from the previous call would stop this one before it runs.
	rt.ClearInterrupt()

	runCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { rt.Interrupt(context.Cause(runCtx)) })
	defer stop()

	result, err := rt.RunScript(wrapper)
	if err != nil {
		return "", describeScriptError(err, source)
	}
	out, convErr := scriptResult(rt, result)
	if convErr != nil {
		return "", convErr
	}
	sess.used = true
	return s.limitOutput(out)
}

// codeModeSpec describes the tool when it is how the session acts, so the text
// carries both what a script may call and what run() is for. It lists the
// whitelist for the same reason the default description does: this text is the
// model's only account of what exists.
func (s Script) codeModeSpec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "script",
		Description: `Do the work by writing JavaScript; the last expression is the result.
Inside the script: tools.read_file, tools.grep, tools.glob, tools.write_file, tools.edit_file, all returning strings (grep returns "path:line:text" lines, glob one path per line).
Start programs with run(["prog", "arg"], timeoutSeconds), which returns {stdout, stderr, code}; a non-zero code is data, not a failure. Use it for git, tests and builds.
No import. Writes pass the same approval a top-level call does. Synchronous only; top-level await is an error.
Assign to globalThis (globalThis.cwd = ...) for state a later call needs; each call has its own scope otherwise.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "JavaScript to run. The last expression is the result.",
				},
			},
			"required": []string{"source"},
		},
	}
}

// bindGlobals installs this call's namespace. The console is bound in both
// modes; run() only in solo, because the default surface has bash for programs.
func (s Script) bindGlobals(rt *moejs.Runtime, ctx context.Context, console *consoleLog) error {
	if err := rt.SetGlobal("tools", s.globals(ctx)); err != nil {
		return err
	}
	for name, value := range s.consoleGlobals(console) {
		if err := rt.SetGlobal(name, value); err != nil {
			return err
		}
	}
	if !s.CodeMode {
		return nil
	}
	for name, value := range s.codeModeExtras(ctx) {
		if err := rt.SetGlobal(name, value); err != nil {
			return err
		}
	}
	return nil
}

// runCodeModeSnippet is what a code-mode call runs. The model's source keeps its last
// expression as the call's value: eval returns it, which is the rule the default
// mode already documents.
//
// A block would scope redeclarations but loses the completion value, and a kept
// runtime makes a repeated `const cwd = ...` a SyntaxError before anything runs.
// The block plus an explicit return would need the model to write `return`, which
// no other mode asks for. So the snippet runs inside eval, declarations stay in
// this call, and state a model wants to keep is assigned to globalThis, which the
// prompt says.
const runCodeModeSnippet = `(function () {
	if (typeof globalThis === "undefined") throw new Error("no globalThis");
	return eval(%q);
})()`

// runCodeMode runs one script in the session's kept runtime as a real script.
func (s Script) runCodeMode(ctx context.Context, source string) (string, error) {
	// Compiled per call on purpose: a cache keyed by model-written source has no
	// natural bound, and a session writes different code every turn, so it would
	// grow for the life of the process. Compilation was never the bottleneck.
	script, err := moejs.CompileScript("script", fmt.Sprintf(runCodeModeSnippet, source))
	if err != nil {
		return "", describeScriptError(err, source)
	}

	sess := s.Session
	if sess == nil {
		sess = &ScriptSession{}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.rt == nil {
		sess.rt = moejs.NewRuntime(moejs.Options{MemoryLimit: s.memoryLimit()})
	}
	rt := sess.rt
	console := s.takeConsole()
	if err := s.bindGlobals(rt, ctx, console); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	rt.ClearInterrupt()

	runCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { rt.Interrupt(context.Cause(runCtx)) })
	defer stop()

	result, err := rt.RunScript(script)
	if err != nil {
		return "", console.describeFailure(err, source)
	}
	sess.used = true
	out, convErr := scriptResult(rt, result)
	if convErr != nil {
		return "", convErr
	}
	return s.limitOutput(console.join(out))
}

// Output is collected per call and prepended to the result, because the tool's
// value is the only channel a script has back to the model.
type consoleLog struct {
	mu    sync.Mutex
	lines []string
}

func (c *consoleLog) add(args []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, fmt.Sprint(arg))
	}
	c.lines = append(c.lines, strings.Join(parts, " "))
}

func (c *consoleLog) drain() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lines) == 0 {
		return ""
	}
	out := strings.Join(c.lines, "\n")
	c.lines = nil
	return out
}

// takeConsole builds the console a solo script writes to. The engine has none,
// and a script written the way every other JavaScript script is written calls
// console.log, so its absence reads as a broken tool rather than a missing one.
func (s Script) takeConsole() *consoleLog { return &consoleLog{} }

// object is the value a script sees. Every level writes to the same buffer, so a
// call that logs twice keeps both lines in order.
func (c *consoleLog) object() map[string]any {
	write := func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
		values := make([]any, 0, len(args))
		for _, arg := range args {
			values = append(values, r.ToGo(arg))
		}
		c.add(values)
		return engine.Undefined(), nil
	}
	console := map[string]any{}
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		console[name] = moejs.NativeFunc(write)
	}
	return console
}

// join puts what the script printed in front of its value. The console is the
// only channel besides the value itself, so dropping it loses work the model did
// on purpose.
func (c *consoleLog) join(value string) string {
	printed := c.drain()
	switch {
	case printed == "":
		return value
	case value == "":
		return printed
	default:
		return printed + "\n" + value
	}
}

// describeFailure names the error and keeps what was printed before it, which is
// usually why the model printed anything.
func (c *consoleLog) describeFailure(err error, source string) error {
	described := describeScriptError(err, source)
	if printed := c.drain(); printed != "" {
		return fmt.Errorf("%w\n(printed before the error)\n%s", described, printed)
	}
	return described
}

// resolvePath makes a tool call's path absolute against the script's working
// directory. It touches nothing when the path is already absolute or when there
// is no working directory to resolve against, which keeps a session that never
// set one behaving as it did before.
func resolvePath(input map[string]any, key, dir string) {
	if dir == "" || key == "" {
		return
	}
	path, ok := input[key].(string)
	if !ok || path == "" || filepath.IsAbs(path) {
		return
	}
	input[key] = filepath.Join(dir, path)
}

// hostCall says how one tool's arguments are read. required is the field a bare
// string argument binds to; path is the field holding a filesystem path, which is
// resolved against the script's directory. They are separate because grep and
// glob take a pattern, and a pattern is not a path.
type hostCall struct {
	required string
	path     string
}
